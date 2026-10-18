package queue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

type Handler func(context.Context, Job) (json.RawMessage, error)
type Worker struct {
	Store         Store
	Queue         string
	Owner         string
	Concurrency   int
	LeaseDuration time.Duration
	PollInterval  time.Duration
	Handle        Handler
	Logger        *slog.Logger
}

// Run polls until cancellation and waits for cooperative handlers to return.
// Handlers must honor context cancellation; an uncooperative handler cannot be
// forcibly stopped in Go. A lost lease still prevents its database completion.
func (w Worker) Run(ctx context.Context) error {
	if w.Store == nil || w.Handle == nil {
		return errors.New("store and handler are required")
	}
	if len(w.Owner) > 250 {
		return errors.New("worker owner must be at most 250 bytes (space reserved for slot suffix)")
	}
	if w.Queue == "" {
		w.Queue = "default"
	}
	if w.Concurrency == 0 {
		w.Concurrency = 4
	}
	if w.Concurrency < 1 || w.Concurrency > 256 {
		return errors.New("concurrency must be 1..256")
	}
	if w.LeaseDuration == 0 {
		w.LeaseDuration = 15 * time.Second
	}
	if err := ValidateClaim(w.Queue, w.Owner, w.LeaseDuration); err != nil {
		return err
	}
	if w.PollInterval == 0 {
		w.PollInterval = 200 * time.Millisecond
	}
	if w.PollInterval < time.Millisecond {
		return errors.New("poll interval must be at least 1ms")
	}
	if w.Logger == nil {
		w.Logger = slog.Default()
	}
	var wg sync.WaitGroup
	for i := 0; i < w.Concurrency; i++ {
		wg.Add(1)
		go func(slot int) {
			defer wg.Done()
			owner := fmt.Sprintf("%s/%d", w.Owner, slot)
			for ctx.Err() == nil {
				j, err := w.Store.Claim(ctx, w.Queue, owner, w.LeaseDuration)
				if err != nil {
					if !errors.Is(err, ErrEmpty) && ctx.Err() == nil {
						w.Logger.Error("claim failed", "error", err)
					}
					if !pause(ctx, w.PollInterval) {
						return
					}
					continue
				}
				w.process(ctx, j)
			}
		}(i)
	}
	wg.Wait()
	return nil
}
func pause(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
func invoke(ctx context.Context, h Handler, j Job) (value json.RawMessage, err error) {
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("handler panic: %v", v)
		}
	}()
	return h(ctx, j)
}
func (w Worker) process(ctx context.Context, j Job) {
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		tick := time.NewTicker(w.LeaseDuration / 3)
		defer tick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-workCtx.Done():
				return
			case <-tick.C:
				hbCtx, hbCancel := context.WithTimeout(workCtx, w.LeaseDuration/3)
				err := w.Store.Extend(hbCtx, j.Lease(), w.LeaseDuration)
				hbCancel()
				if err != nil {
					w.Logger.Warn("heartbeat failed", "job_id", j.ID, "error", err)
					cancel()
					return
				}
			}
		}
	}()
	value, err := invoke(workCtx, w.Handle, j)
	close(stop)
	<-done
	if workCtx.Err() != nil {
		return
	} // Leave the lease for recovery, never ack cancelled work.
	if err == nil && !json.Valid(value) {
		err = errors.New("handler returned invalid JSON")
	}
	if err != nil {
		if failErr := w.Store.Fail(ctx, j.Lease(), err.Error()); failErr != nil {
			w.Logger.Warn("retry scheduling failed", "job_id", j.ID, "error", failErr)
		}
		w.Logger.Warn("job attempt failed", "job_id", j.ID, "attempt", j.Attempts, "error", err)
		return
	}
	if err = w.Store.Complete(ctx, j.Lease(), value); err != nil {
		w.Logger.Warn("completion failed; lease will recover", "job_id", j.ID, "error", err)
		return
	}
	w.Logger.Info("job completed", "job_id", j.ID, "attempt", j.Attempts)
}
