package postgres_test

import (
	"context"
	"durablequeue/internal/queuetest"
	"durablequeue/queue"
	"durablequeue/queue/postgres"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"os"
	"sync"
	"testing"
	"time"
)

func fixture(t *testing.T) (*postgres.Store, *pgxpool.Pool) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	ctx := context.Background()
	admin, e := pgxpool.New(ctx, url)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(admin.Close)
	id, e := queue.NewID()
	if e != nil {
		t.Fatal(e)
	}
	schema := "test_dq_" + id
	if _, e = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize()); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if _, e := admin.Exec(context.Background(), "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE"); e != nil {
			t.Error(e)
		}
	})
	config, e := pgxpool.ParseConfig(url)
	if e != nil {
		t.Fatal(e)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	config.MaxConns = 16
	p, e := pgxpool.NewWithConfig(ctx, config)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(p.Close)
	s := postgres.New(p)
	if e = s.Migrate(ctx); e != nil {
		t.Fatal(e)
	}
	return s, p
}
func TestContract(t *testing.T) {
	queuetest.Contract(t, func(t *testing.T) queuetest.Fixture {
		s, p := fixture(t)
		return queuetest.Fixture{Store: s, Advance: func(d time.Duration) {
			// Move stored deadlines backwards to test expiry without sleeps or changing
			// the host clock. Database clock-jump schedules live in the simulator.
			_, e := p.Exec(context.Background(), `UPDATE dq_jobs SET available_at=available_at-$1*interval '1 microsecond',lease_until=lease_until-$1*interval '1 microsecond'`, d.Microseconds())
			if e != nil {
				t.Fatal(e)
			}
		}}
	})
}

func TestAtomicBusinessEffectAndLostAck(t *testing.T) {
	s, p := fixture(t)
	ctx := context.Background()
	if _, e := p.Exec(ctx, "CREATE TABLE balances(id int PRIMARY KEY, amount int NOT NULL); INSERT INTO balances VALUES(1,0)"); e != nil {
		t.Fatal(e)
	}
	j, e := s.Enqueue(ctx, queue.Request{Kind: "credit", Key: "credit-1", Payload: json.RawMessage(`{"amount":10}`)})
	if e != nil {
		t.Fatal(e)
	}
	j, e = s.Claim(ctx, "default", "worker", time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	failure := errors.New("crash before transaction commit")
	e = s.CompleteTx(ctx, j.Lease(), json.RawMessage(`10`), func(ctx context.Context, tx pgx.Tx) error {
		if _, e := tx.Exec(ctx, "UPDATE balances SET amount=amount+10 WHERE id=1"); e != nil {
			return e
		}
		return failure
	})
	if !errors.Is(e, failure) {
		t.Fatal(e)
	}
	var balance int
	if e = p.QueryRow(ctx, "SELECT amount FROM balances WHERE id=1").Scan(&balance); e != nil || balance != 0 {
		t.Fatalf("business update escaped rollback: %d %v", balance, e)
	}
	results, e := s.Results(ctx)
	if e != nil || len(results) != 0 {
		t.Fatal("result escaped rollback")
	}
	got, e := s.Get(ctx, j.ID)
	if e != nil || got.Status != queue.Running {
		t.Fatal("ack escaped rollback")
	}
	calls := 0
	effect := func(ctx context.Context, tx pgx.Tx) error {
		calls++
		_, e := tx.Exec(ctx, "UPDATE balances SET amount=amount+10 WHERE id=1")
		return e
	}
	for i := 0; i < 3; i++ {
		if e = s.CompleteTx(ctx, j.Lease(), json.RawMessage(`10`), effect); e != nil {
			t.Fatal(e)
		}
	}
	if e = p.QueryRow(ctx, "SELECT amount FROM balances WHERE id=1").Scan(&balance); e != nil || balance != 10 || calls != 1 {
		t.Fatalf("duplicate business effect: balance=%d callbacks=%d error=%v", balance, calls, e)
	}
}

func TestLockedJobDoesNotBlockOtherWork(t *testing.T) {
	s, p := fixture(t)
	ctx := context.Background()
	j, e := s.Enqueue(ctx, queue.Request{Kind: "echo", Key: "locked", Payload: json.RawMessage(`1`)})
	if e != nil {
		t.Fatal(e)
	}
	tx, e := p.Begin(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, "SELECT id FROM dq_jobs WHERE id=$1 FOR UPDATE", j.ID); e != nil {
		t.Fatal(e)
	}
	other, e := s.Enqueue(ctx, queue.Request{Kind: "echo", Key: "unlocked", Payload: json.RawMessage(`2`)})
	if e != nil {
		t.Fatal(e)
	}
	deadline, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	got, e := s.Claim(deadline, "default", "worker", time.Minute)
	if e != nil || got.ID != other.ID {
		t.Fatalf("locked row blocked queue: %+v %v", got, e)
	}
}

func TestManyConsumersNoDuplicateResults(t *testing.T) {
	s, _ := fixture(t)
	ctx := context.Background()
	const total = 100
	for i := 0; i < total; i++ {
		_, e := s.Enqueue(ctx, queue.Request{Kind: "echo", Key: fmt.Sprint(i), Payload: json.RawMessage(`{}`)})
		if e != nil {
			t.Fatal(e)
		}
	}
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(owner int) {
			defer wg.Done()
			for {
				j, e := s.Claim(ctx, "default", fmt.Sprint(owner), time.Minute)
				if errors.Is(e, queue.ErrEmpty) {
					return
				}
				if e != nil {
					errs <- e
					return
				}
				if e = s.Complete(ctx, j.Lease(), j.Payload); e != nil {
					errs <- e
					return
				}
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
	results, e := s.Results(ctx)
	if e != nil || len(results) != total {
		t.Fatalf("expected %d results, got %d: %v", total, len(results), e)
	}
}
