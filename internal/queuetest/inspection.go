package queuetest

import (
	"context"
	"durablequeue/queue"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func InspectionContract(t *testing.T, newFixture Factory) {
	f := newFixture(t)
	s := f.Store
	ctx := context.Background()
	for _, key := range []string{"alpha", "beta", "literal%_key", "delta"} {
		if _, err := s.Enqueue(ctx, queue.Request{Kind: "echo", Key: key, Payload: json.RawMessage(`{}`), MaxAttempts: 1}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Enqueue(ctx, queue.Request{Queue: "another", Kind: "echo", Key: "outside", Payload: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	winner, err := s.Claim(ctx, "default", "winner", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Result(ctx, winner.ID); !errors.Is(err, queue.ErrNotFound) {
		t.Fatalf("unfinished job has a result: %v", err)
	}
	if err = s.Complete(ctx, winner.Lease(), json.RawMessage(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	failed, err := s.Claim(ctx, "default", "failure", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Fail(ctx, failed.Lease(), "failure"); err != nil {
		t.Fatal(err)
	}
	a, err := s.Inspect(ctx, "default", "", "", 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if a.Stats != (queue.Stats{Total: 4, Pending: 2, Succeeded: 1, Dead: 1}) || a.Filtered != 4 || len(a.Jobs) != 2 {
		t.Fatalf("incorrect full counts or page: %+v", a)
	}
	b, err := s.Inspect(ctx, "default", "", "", 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, j := range append(a.Jobs, b.Jobs...) {
		if seen[j.ID] {
			t.Fatal("pages overlap")
		}
		seen[j.ID] = true
	}
	if len(seen) != 4 {
		t.Fatal("pagination lost jobs")
	}
	c, err := s.Inspect(ctx, "default", queue.Succeeded, "", 0, 20)
	if err != nil || c.Filtered != 1 || len(c.Jobs) != 1 || c.Jobs[0].ID != winner.ID || c.Stats.Total != 4 {
		t.Fatalf("status filter altered full counts: %+v %v", c, err)
	}
	c, err = s.Inspect(ctx, "default", "", "LITERAL%_", 0, 20)
	if err != nil || c.Filtered != 1 || c.Jobs[0].Key != "literal%_key" {
		t.Fatalf("literal search incorrect: %+v %v", c, err)
	}
	c, err = s.Inspect(ctx, "default", "", "", 100, 20)
	if err != nil || len(c.Jobs) != 0 || c.Filtered != 4 {
		t.Fatalf("empty page: %+v %v", c, err)
	}
	r, err := s.Result(ctx, winner.ID)
	if err != nil || r.JobID != winner.ID || !json.Valid(r.Value) {
		t.Fatalf("result lookup: %+v %v", r, err)
	}
}
