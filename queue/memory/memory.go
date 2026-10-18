// Package memory implements a deterministic, in-memory reference store.
// It is a simulator backend, not a durable production queue.
package memory

import (
	"context"
	"durablequeue/queue"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"
)

type Store struct {
	mu      sync.Mutex
	now     func() time.Time
	jobs    map[string]queue.Job
	keys    map[string]string
	results map[string]queue.Result
	next    int
}

func New(now func() time.Time) *Store {
	return &Store{now: now, jobs: map[string]queue.Job{}, keys: map[string]string{}, results: map[string]queue.Result{}}
}

var _ queue.Store = (*Store)(nil)

func (s *Store) Enqueue(ctx context.Context, r queue.Request) (queue.Job, error) {
	if err := ctx.Err(); err != nil {
		return queue.Job{}, err
	}
	r, hash, err := r.Normalize()
	if err != nil {
		return queue.Job{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := r.Queue + "\x00" + r.Key
	if id, ok := s.keys[k]; ok {
		j := s.jobs[id]
		if j.Fingerprint != hash {
			return queue.Job{}, queue.ErrConflict
		}
		return j.Clone(), nil
	}
	s.next++
	now := s.now()
	j := queue.Job{ID: fmt.Sprintf("job-%06d", s.next), Queue: r.Queue, Kind: r.Kind, Key: r.Key, Payload: r.Payload, Fingerprint: hash, Status: queue.Pending, MaxAttempts: r.MaxAttempts, AvailableAt: now.Add(r.Delay), CreatedAt: now, UpdatedAt: now}
	s.jobs[j.ID] = j
	s.keys[k] = j.ID
	return j.Clone(), nil
}
func (s *Store) Claim(ctx context.Context, q, owner string, d time.Duration) (queue.Job, error) {
	if err := ctx.Err(); err != nil {
		return queue.Job{}, err
	}
	if err := queue.ValidateClaim(q, owner, d); err != nil {
		return queue.Job{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	var eligible []queue.Job
	for id, j := range s.jobs {
		if j.Queue != q {
			continue
		}
		if j.Status == queue.Running && !now.Before(j.LeaseUntil) && j.Attempts >= j.MaxAttempts {
			j = queue.Reject(j, now, "lease expired on final attempt")
			s.jobs[id] = j
		}
		if (j.Status == queue.Pending && !now.Before(j.AvailableAt)) || (j.Status == queue.Running && !now.Before(j.LeaseUntil)) {
			eligible = append(eligible, j)
		}
	}
	if len(eligible) == 0 {
		return queue.Job{}, queue.ErrEmpty
	}
	sort.Slice(eligible, func(a, b int) bool {
		if eligible[a].AvailableAt.Equal(eligible[b].AvailableAt) {
			return eligible[a].ID < eligible[b].ID
		}
		return eligible[a].AvailableAt.Before(eligible[b].AvailableAt)
	})
	j := queue.Acquire(eligible[0], owner, now, d)
	s.jobs[j.ID] = j
	return j.Clone(), nil
}
func (s *Store) Extend(ctx context.Context, l queue.Lease, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := queue.ValidateDuration(d); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	j := s.jobs[l.JobID]
	now := s.now()
	if err := queue.CheckLease(j, l, now); err != nil {
		return err
	}
	until := now.Add(d.Truncate(time.Microsecond))
	if until.After(j.LeaseUntil) {
		j.LeaseUntil = until
	}
	j.UpdatedAt = now
	s.jobs[j.ID] = j
	return nil
}
func (s *Store) Complete(ctx context.Context, l queue.Lease, value json.RawMessage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !json.Valid(value) || len(value) > 1<<20 {
		return fmt.Errorf("result must be valid JSON, at most 1 MiB")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	j := s.jobs[l.JobID]
	now := s.now()
	if j.Status == queue.Succeeded && queue.Owns(j, l) {
		return nil
	}
	if err := queue.CheckLease(j, l, now); err != nil {
		return err
	}
	s.results[j.ID] = queue.Result{JobID: j.ID, Value: append(json.RawMessage(nil), value...), CommittedAt: now}
	j.Status = queue.Succeeded
	j.UpdatedAt = now
	j.LeaseUntil = time.Time{}
	s.jobs[j.ID] = j
	return nil
}
func (s *Store) Fail(ctx context.Context, l queue.Lease, reason string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	j := s.jobs[l.JobID]
	now := s.now()
	if err := queue.CheckLease(j, l, now); err != nil {
		return err
	}
	s.jobs[j.ID] = queue.Reject(j, now, reason)
	return nil
}
func (s *Store) Get(ctx context.Context, id string) (queue.Job, error) {
	if err := ctx.Err(); err != nil {
		return queue.Job{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	j, ok := s.jobs[id]
	if !ok {
		return j, queue.ErrNotFound
	}
	return j.Clone(), nil
}
func (s *Store) List(ctx context.Context, q string, limit int) ([]queue.Job, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []queue.Job{}
	for _, j := range s.jobs {
		if j.Queue == q {
			out = append(out, j.Clone())
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.Before(out[j].CreatedAt) || out[i].CreatedAt.Equal(out[j].CreatedAt) && out[i].ID < out[j].ID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
func (s *Store) Results(ctx context.Context) ([]queue.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []queue.Result{}
	for _, r := range s.results {
		r.Value = append(json.RawMessage(nil), r.Value...)
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].JobID < out[j].JobID })
	return out, nil
}
