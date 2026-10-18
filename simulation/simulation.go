// Package simulation explores reproducible failure schedules against the
// reference store and shared queue transitions. It is not a database emulator.
package simulation

import (
	"context"
	"durablequeue/queue"
	"durablequeue/queue/memory"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"time"
)

type Config struct {
	Seed                 int64
	Steps, Jobs, Workers int
	Trace                io.Writer
}
type Report struct {
	Seed                 int64 `json:"seed"`
	Steps                int   `json:"steps"`
	Accepted             int   `json:"accepted"`
	Succeeded            int   `json:"succeeded"`
	Dead                 int   `json:"dead"`
	Claims               int   `json:"claims"`
	Crashes              int   `json:"crashes"`
	ClockJumps           int   `json:"clock_jumps"`
	DuplicateEnqueues    int   `json:"duplicate_enqueues"`
	DuplicateDeliveries  int   `json:"duplicate_deliveries"`
	LostAcknowledgements int   `json:"lost_acknowledgements"`
	StaleRejections      int   `json:"stale_rejections"`
	InvariantChecks      int   `json:"invariant_checks"`
}

// Run has no sleeps, goroutines, system clocks, random IDs, or network calls.
// Identical config yields an identical report and event trace.
func Run(c Config) (report Report, err error) {
	if c.Steps < 1 || c.Jobs < 1 || c.Jobs > 10000 || c.Workers < 1 || c.Workers > 256 {
		return report, errors.New("steps/jobs/workers must be positive; jobs <=10000, workers <=256")
	}
	report.Seed = c.Seed
	report.Steps = c.Steps
	rng := rand.New(rand.NewSource(c.Seed))
	ctx := context.Background()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s := memory.New(func() time.Time { return now })
	requests := make([]queue.Request, c.Jobs)
	accepted := map[string]bool{}
	for i := range requests {
		requests[i] = queue.Request{Kind: "echo", Key: fmt.Sprintf("request-%d", i), Payload: json.RawMessage(fmt.Sprintf(`{"number":%d}`, i)), MaxAttempts: 5}
		j, e := s.Enqueue(ctx, requests[i])
		if e != nil {
			return report, e
		}
		accepted[j.ID] = true
	}
	report.Accepted = len(accepted)
	active := make([]*queue.Job, c.Workers)
	var deliveries []queue.Lease
	previous := map[string]queue.Job{}
	committed := map[string]string{}
	trace := func(step int, format string, args ...any) {
		if c.Trace != nil {
			_, _ = fmt.Fprintf(c.Trace, "step=%d db_time=%s "+format+"\n", append([]any{step, now.Format(time.RFC3339Nano)}, args...)...)
		}
	}
	check := func() error {
		jobs, _ := s.List(ctx, "default", 0)
		results, _ := s.Results(ctx)
		if len(jobs) != len(accepted) {
			return errors.New("accepted job disappeared or duplicate job inserted")
		}
		seen := map[string]string{}
		for _, r := range results {
			if _, exists := seen[r.JobID]; exists {
				return errors.New("duplicate committed result")
			}
			if !accepted[r.JobID] {
				return errors.New("result for unaccepted job")
			}
			seen[r.JobID] = string(r.Value)
			if v, ok := committed[r.JobID]; ok && v != string(r.Value) {
				return errors.New("committed result overwritten")
			}
			committed[r.JobID] = string(r.Value)
		}
		for _, j := range jobs {
			_, has := seen[j.ID]
			if !accepted[j.ID] || ((j.Status == queue.Succeeded) != has) {
				return fmt.Errorf("job/result consistency violated for %s", j.ID)
			}
			if j.Attempts > j.MaxAttempts || j.Attempts < 0 {
				return errors.New("retry budget violated")
			}
			if (j.Status == queue.Running) == j.LeaseUntil.IsZero() {
				return errors.New("lease/status consistency violated")
			}
			if p, ok := previous[j.ID]; ok {
				if j.Version < p.Version || j.Attempts < p.Attempts {
					return errors.New("lease version or attempts decreased")
				}
				if p.Terminal() && p.Status != j.Status {
					return errors.New("terminal state changed")
				}
			}
			previous[j.ID] = j
		}
		report.InvariantChecks++
		return nil
	}
	complete := func(l queue.Lease, step int) error {
		before, e := s.Get(ctx, l.JobID)
		if e != nil {
			return e
		}
		// Independent oracle: terminal winner may replay; a live matching grant
		// may commit. Expired and superseded grants must be rejected.
		matching := before.Owner == l.Owner && before.Version == l.Version
		allowed := matching && (before.Status == queue.Succeeded || (before.Status == queue.Running && now.Before(before.LeaseUntil)))
		value := json.RawMessage(fmt.Sprintf(`{"job_id":%q,"delivered_at_step":%d}`, l.JobID, step))
		e = s.Complete(ctx, l, value)
		if allowed && e != nil {
			return fmt.Errorf("valid completion rejected: %w", e)
		}
		if !allowed {
			if !errors.Is(e, queue.ErrLeaseLost) {
				return fmt.Errorf("stale lease accepted: %+v", l)
			}
			report.StaleRejections++
		}
		return nil
	}
	for step := 0; step < c.Steps; step++ {
		now = now.Add(time.Duration(rng.Intn(500)) * time.Millisecond)
		w := rng.Intn(c.Workers)
		event := rng.Intn(10)
		trace(step, "event=%d worker=%d", event, w)
		switch event {
		case 0, 1:
			if active[w] == nil {
				j, e := s.Claim(ctx, "default", fmt.Sprintf("worker-%d", w), 3*time.Second)
				if e == nil {
					active[w] = &j
					deliveries = append(deliveries, j.Lease())
					report.Claims++
					trace(step, "claim=%s version=%d", j.ID, j.Version)
				} else if !errors.Is(e, queue.ErrEmpty) {
					err = e
				}
			}
		case 2:
			if j := active[w]; j != nil {
				err = complete(j.Lease(), step)
				active[w] = nil
			}
		case 3:
			// Process death: its volatile assignment is gone, durable lease remains.
			if active[w] != nil {
				active[w] = nil
				report.Crashes++
			}
		case 4:
			if j := active[w]; j != nil {
				e := s.Fail(ctx, j.Lease(), "injected transient failure")
				if e != nil && !errors.Is(e, queue.ErrLeaseLost) {
					err = e
				}
				active[w] = nil
			}
		case 5:
			if len(deliveries) > 0 {
				l := deliveries[rng.Intn(len(deliveries))]
				report.DuplicateDeliveries++
				err = complete(l, step)
			}
		case 6:
			jump := time.Duration(rng.Intn(121)-60) * time.Second
			now = now.Add(jump)
			report.ClockJumps++
			trace(step, "database_clock_jump=%s", jump)
		case 7:
			if j := active[w]; j != nil {
				e := s.Extend(ctx, j.Lease(), 3*time.Second)
				if e != nil && !errors.Is(e, queue.ErrLeaseLost) {
					err = e
				}
				if e != nil {
					active[w] = nil
				}
			}
		case 8:
			j, e := s.Enqueue(ctx, requests[rng.Intn(len(requests))])
			report.DuplicateEnqueues++
			if e != nil {
				err = e
			} else if !accepted[j.ID] {
				err = errors.New("duplicate enqueue created new job")
			}
		case 9:
			// Commit succeeded but acknowledgement was lost. Replay deliberately
			// changes the proposed result to prove that a commit cannot be rewritten.
			if j := active[w]; j != nil {
				before, _ := s.Get(ctx, j.ID)
				err = complete(j.Lease(), step)
				if err == nil {
					err = complete(j.Lease(), step+1)
				}
				after, _ := s.Get(ctx, j.ID)
				if before.Status == queue.Running && after.Status == queue.Succeeded && after.Version == j.Version {
					report.LostAcknowledgements++
				}
				active[w] = nil
			}
		}
		if err != nil {
			return report, fmt.Errorf("seed=%d step=%d: %w", c.Seed, step, err)
		}
		if err = check(); err != nil {
			return report, fmt.Errorf("seed=%d step=%d: %w", c.Seed, step, err)
		}
	}
	// Liveness requires a fair, fault-free suffix and an advancing DB clock.
	// Advance past every outstanding lease/retry; drain until all are terminal.
	now = now.Add(48 * time.Hour)
	for i := 0; i < c.Jobs+1; i++ {
		j, e := s.Claim(ctx, "default", "drain", time.Minute)
		if errors.Is(e, queue.ErrEmpty) {
			break
		}
		if e != nil {
			return report, e
		}
		report.Claims++
		if err = complete(j.Lease(), c.Steps+i); err != nil {
			return report, err
		}
	}
	if err = check(); err != nil {
		return report, err
	}
	jobs, _ := s.List(ctx, "default", 0)
	for _, j := range jobs {
		switch j.Status {
		case queue.Succeeded:
			report.Succeeded++
		case queue.Dead:
			report.Dead++
		default:
			return report, fmt.Errorf("job %s never reached a terminal state", j.ID)
		}
	}
	return report, nil
}
