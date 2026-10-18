// Package queuetest supplies a behavioral contract for both store backends.
package queuetest

import (
	"context"
	"durablequeue/queue"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

type Fixture struct {
	Store   queue.Store
	Advance func(time.Duration)
}
type Factory func(*testing.T) Fixture

func Contract(t *testing.T, newFixture Factory) {
	t.Run("dashboard_inspection", func(t *testing.T) { InspectionContract(t, newFixture) })
	ctx := context.Background()
	request := queue.Request{Kind: "echo", Key: "same-key", Payload: json.RawMessage(`{"b":2,"a":1}`), MaxAttempts: 2}
	enqueue := func(t *testing.T, s queue.Store, r queue.Request) queue.Job {
		t.Helper()
		j, e := s.Enqueue(ctx, r)
		if e != nil {
			t.Fatal(e)
		}
		return j
	}
	claim := func(t *testing.T, s queue.Store, owner string) queue.Job {
		t.Helper()
		j, e := s.Claim(ctx, "default", owner, time.Minute)
		if e != nil {
			t.Fatal(e)
		}
		return j
	}
	t.Run("idempotency_and_payload_conflict", func(t *testing.T) {
		f := newFixture(t)
		j := enqueue(t, f.Store, request)
		r := request
		r.Payload = json.RawMessage(`{ "a":1, "b":2 }`)
		if j2 := enqueue(t, f.Store, r); j2.ID != j.ID {
			t.Fatal("duplicate enqueue got another ID")
		}
		r.Payload = json.RawMessage(`{"a":3}`)
		if _, e := f.Store.Enqueue(ctx, r); !errors.Is(e, queue.ErrConflict) {
			t.Fatalf("conflict: %v", e)
		}
	})
	t.Run("duplicate_ack_and_stale_fencing", func(t *testing.T) {
		f := newFixture(t)
		enqueue(t, f.Store, request)
		a := claim(t, f.Store, "a")
		f.Advance(2 * time.Minute)
		if e := f.Store.Extend(ctx, a.Lease(), time.Minute); !errors.Is(e, queue.ErrLeaseLost) {
			t.Fatalf("expired heartbeat accepted: %v", e)
		}
		b := claim(t, f.Store, "b")
		if b.Version <= a.Version {
			t.Fatal("fencing token did not advance")
		}
		if e := f.Store.Complete(ctx, a.Lease(), json.RawMessage(`1`)); !errors.Is(e, queue.ErrLeaseLost) {
			t.Fatalf("stale completion: %v", e)
		}
		if e := f.Store.Fail(ctx, a.Lease(), "stale"); !errors.Is(e, queue.ErrLeaseLost) {
			t.Fatalf("stale failure: %v", e)
		}
		if e := f.Store.Complete(ctx, b.Lease(), json.RawMessage(`2`)); e != nil {
			t.Fatal(e)
		}
		if e := f.Store.Complete(ctx, b.Lease(), json.RawMessage(`3`)); e != nil {
			t.Fatal(e)
		}
		r, e := f.Store.Results(ctx)
		if e != nil || len(r) != 1 || string(r[0].Value) != "2" {
			t.Fatalf("result overwritten or duplicated: %+v %v", r, e)
		}
		if _, e = f.Store.Claim(ctx, "default", "c", time.Minute); !errors.Is(e, queue.ErrEmpty) {
			t.Fatalf("terminal job reclaimed: %v", e)
		}
	})
	t.Run("retry_dead_letter", func(t *testing.T) {
		f := newFixture(t)
		j := enqueue(t, f.Store, request)
		a := claim(t, f.Store, "a")
		if e := f.Store.Fail(ctx, a.Lease(), "temporary"); e != nil {
			t.Fatal(e)
		}
		if _, e := f.Store.Claim(ctx, "default", "b", time.Minute); !errors.Is(e, queue.ErrEmpty) {
			t.Fatalf("backoff bypassed: %v", e)
		}
		f.Advance(2 * time.Minute)
		b := claim(t, f.Store, "b")
		if b.Attempts != 2 {
			t.Fatal("incorrect attempt count")
		}
		if e := f.Store.Fail(ctx, b.Lease(), "permanent"); e != nil {
			t.Fatal(e)
		}
		got, e := f.Store.Get(ctx, j.ID)
		if e != nil || got.Status != queue.Dead || got.LastError != "permanent" {
			t.Fatalf("job not retained as dead: %+v %v", got, e)
		}
	})
	t.Run("crash_on_final_attempt", func(t *testing.T) {
		f := newFixture(t)
		r := request
		r.MaxAttempts = 1
		j := enqueue(t, f.Store, r)
		claim(t, f.Store, "crashed")
		f.Advance(2 * time.Minute)
		if _, e := f.Store.Claim(ctx, "default", "new", time.Minute); !errors.Is(e, queue.ErrEmpty) {
			t.Fatalf("final attempt reclaimed: %v", e)
		}
		got, e := f.Store.Get(ctx, j.ID)
		if e != nil || got.Status != queue.Dead {
			t.Fatalf("final crash stuck running: %+v %v", got, e)
		}
	})
	t.Run("delay_queue_isolation_and_heartbeat", func(t *testing.T) {
		f := newFixture(t)
		r := request
		r.Delay = time.Minute
		enqueue(t, f.Store, r)
		if _, e := f.Store.Claim(ctx, "default", "a", time.Minute); !errors.Is(e, queue.ErrEmpty) {
			t.Fatal("delay ignored")
		}
		f.Advance(2 * time.Minute)
		if _, e := f.Store.Claim(ctx, "another", "a", time.Minute); !errors.Is(e, queue.ErrEmpty) {
			t.Fatal("queue isolation failed")
		}
		j := claim(t, f.Store, "a")
		if e := f.Store.Extend(ctx, j.Lease(), 5*time.Minute); e != nil {
			t.Fatal(e)
		}
		f.Advance(2 * time.Minute)
		if _, e := f.Store.Claim(ctx, "default", "b", time.Minute); !errors.Is(e, queue.ErrEmpty) {
			t.Fatal("heartbeat did not extend")
		}
		if e := f.Store.Extend(ctx, j.Lease(), time.Millisecond); e != nil {
			t.Fatal(e)
		}
		f.Advance(time.Minute)
		if e := f.Store.Complete(ctx, j.Lease(), json.RawMessage(`true`)); e != nil {
			t.Fatalf("short heartbeat reduced lease: %v", e)
		}
	})
	t.Run("concurrent_producers_and_claims", func(t *testing.T) {
		f := newFixture(t)
		var wg sync.WaitGroup
		ids := make(chan string, 32)
		errs := make(chan error, 64)
		for i := 0; i < 32; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				j, e := f.Store.Enqueue(ctx, request)
				if e != nil {
					errs <- e
					return
				}
				ids <- j.ID
			}()
		}
		wg.Wait()
		close(ids)
		id := ""
		for got := range ids {
			if id != "" && id != got {
				t.Fatal("concurrent duplicate inserted")
			}
			id = got
		}
		claimed := make(chan queue.Job, 32)
		for i := 0; i < 32; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				j, e := f.Store.Claim(ctx, "default", "worker", time.Minute)
				if e == nil {
					claimed <- j
				} else if !errors.Is(e, queue.ErrEmpty) {
					errs <- e
				}
			}()
		}
		wg.Wait()
		close(claimed)
		close(errs)
		for e := range errs {
			t.Error(e)
		}
		if len(claimed) != 1 {
			t.Fatalf("got %d simultaneous grants for one job", len(claimed))
		}
	})
}
