package queue_test

import (
	"context"
	"durablequeue/queue"
	"durablequeue/queue/memory"
	"encoding/json"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

type countedStore struct {
	queue.Store
	heartbeats atomic.Int64
}

func (s *countedStore) Extend(ctx context.Context, l queue.Lease, d time.Duration) error {
	s.heartbeats.Add(1)
	return s.Store.Extend(ctx, l, d)
}
func waitFor(t *testing.T, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition did not become true")
}
func TestWorkerHeartbeatsLongJob(t *testing.T) {
	s := &countedStore{Store: memory.New(time.Now)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	j, err := s.Enqueue(ctx, queue.Request{Kind: "echo", Key: "long", Payload: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	w := queue.Worker{Store: s, Owner: "worker", Concurrency: 1, LeaseDuration: 600 * time.Millisecond, PollInterval: time.Millisecond, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Handle: func(ctx context.Context, j queue.Job) (json.RawMessage, error) {
		timer := time.NewTimer(1400 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			return json.RawMessage(`true`), nil
		}
	}}
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	waitFor(t, func() bool { got, _ := s.Get(ctx, j.ID); return got.Status == queue.Succeeded })
	cancel()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if s.heartbeats.Load() < 2 {
		t.Fatal("long job was not renewed")
	}
}
func TestWorkerPanicBecomesDeadLetter(t *testing.T) {
	s := memory.New(time.Now)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	j, _ := s.Enqueue(ctx, queue.Request{Kind: "panic", Key: "panic", Payload: json.RawMessage(`{}`), MaxAttempts: 1})
	w := queue.Worker{Store: s, Owner: "worker", Concurrency: 1, PollInterval: time.Millisecond, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Handle: func(context.Context, queue.Job) (json.RawMessage, error) { panic("injected") }}
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	waitFor(t, func() bool { got, _ := s.Get(ctx, j.ID); return got.Status == queue.Dead })
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	r, _ := s.Results(context.Background())
	if len(r) != 0 {
		t.Fatal("failed handler committed a result")
	}
}
func TestCancelledWorkerLeavesRecoverableLease(t *testing.T) {
	s := memory.New(time.Now)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	j, _ := s.Enqueue(ctx, queue.Request{Kind: "echo", Key: "cancel", Payload: json.RawMessage(`{}`)})
	started := make(chan struct{})
	w := queue.Worker{Store: s, Owner: "worker", Concurrency: 1, Handle: func(ctx context.Context, j queue.Job) (json.RawMessage, error) {
		close(started)
		<-ctx.Done()
		return json.RawMessage(`true`), nil
	}}
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("worker never started")
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(context.Background(), j.ID)
	if got.Status != queue.Running {
		t.Fatalf("cancelled work was acknowledged: %s", got.Status)
	}
}
