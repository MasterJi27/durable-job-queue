// Package postgres is the durable PostgreSQL queue implementation.
package postgres

import (
	"context"
	"durablequeue/queue"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"time"
)

//go:embed schema.sql
var schema string

type Store struct{ pool *pgxpool.Pool }

var _ queue.Store = (*Store)(nil)

func Open(ctx context.Context, url string) (*Store, error) {
	p, err := pgxpool.New(ctx, url)
	if err != nil {
		return nil, err
	}
	if err = p.Ping(ctx); err != nil {
		p.Close()
		return nil, err
	}
	return New(p), nil
}
func New(p *pgxpool.Pool) *Store                { return &Store{pool: p} }
func (s *Store) Close()                         { s.pool.Close() }
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

// Migrate applies version 1 atomically. The lock serializes concurrent startup.
func (s *Store) Migrate(ctx context.Context) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(tx)
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(718240931)"); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, schema); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func rollback(tx pgx.Tx) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = tx.Rollback(ctx)
}

const columns = `id,queue,kind,idempotency_key,payload,fingerprint,status,attempts,max_attempts,lease_version,owner,available_at,lease_until,created_at,updated_at,last_error`

func scan(row pgx.Row) (queue.Job, error) {
	var j queue.Job
	var until *time.Time
	err := row.Scan(&j.ID, &j.Queue, &j.Kind, &j.Key, &j.Payload, &j.Fingerprint, &j.Status, &j.Attempts, &j.MaxAttempts, &j.Version, &j.Owner, &j.AvailableAt, &until, &j.CreatedAt, &j.UpdatedAt, &j.LastError)
	if until != nil {
		j.LeaseUntil = *until
	}
	if errors.Is(err, pgx.ErrNoRows) {
		err = queue.ErrNotFound
	}
	return j, err
}
func dbNow(ctx context.Context, tx pgx.Tx) (time.Time, error) {
	var now time.Time
	err := tx.QueryRow(ctx, "SELECT clock_timestamp()").Scan(&now)
	return now, err
}

func (s *Store) Enqueue(ctx context.Context, r queue.Request) (queue.Job, error) {
	r, hash, err := r.Normalize()
	if err != nil {
		return queue.Job{}, err
	}
	id, err := queue.NewID()
	if err != nil {
		return queue.Job{}, err
	}
	// The no-op conflict update returns the winning row even under concurrent
	// producers; it never changes its payload, status, schedule, or retry budget.
	j, err := scan(s.pool.QueryRow(ctx, `WITH t AS MATERIALIZED (SELECT clock_timestamp() AS now)
	INSERT INTO dq_jobs(id,queue,kind,idempotency_key,payload,fingerprint,status,max_attempts,available_at,created_at,updated_at)
	SELECT $1,$2,$3,$4,$5::jsonb,$6,'pending',$7,t.now + $8 * interval '1 microsecond',t.now,t.now FROM t
	ON CONFLICT(queue,idempotency_key) DO UPDATE SET idempotency_key=EXCLUDED.idempotency_key
	RETURNING `+columns, id, r.Queue, r.Kind, r.Key, []byte(r.Payload), hash, r.MaxAttempts, r.Delay.Microseconds()))
	if err != nil {
		return queue.Job{}, err
	}
	if j.Fingerprint != hash {
		return queue.Job{}, queue.ErrConflict
	}
	return j, nil
}

func (s *Store) Claim(ctx context.Context, q, owner string, d time.Duration) (queue.Job, error) {
	if err := queue.ValidateClaim(q, owner, d); err != nil {
		return queue.Job{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return queue.Job{}, err
	}
	defer rollback(tx)
	// Final-attempt crashes must become visible terminal failures, not remain
	// running forever. A bounded sweep avoids holding thousands of locks.
	_, err = tx.Exec(ctx, `WITH expired AS (
	 SELECT id FROM dq_jobs WHERE queue=$1 AND status='running' AND lease_until<=clock_timestamp() AND attempts>=max_attempts
	 ORDER BY lease_until,id LIMIT 100 FOR UPDATE SKIP LOCKED
	) UPDATE dq_jobs SET status='dead',lease_until=NULL,last_error='lease expired on final attempt',updated_at=clock_timestamp()
	 WHERE id IN (SELECT id FROM expired)`, q)
	if err != nil {
		return queue.Job{}, err
	}
	j, err := scan(tx.QueryRow(ctx, `SELECT `+columns+` FROM dq_jobs WHERE queue=$1 AND attempts<max_attempts AND
	 ((status='pending' AND available_at<=clock_timestamp()) OR (status='running' AND lease_until<=clock_timestamp()))
	 ORDER BY available_at,id LIMIT 1 FOR UPDATE SKIP LOCKED`, q))
	if errors.Is(err, queue.ErrNotFound) {
		if err = tx.Commit(ctx); err != nil {
			return queue.Job{}, err
		}
		return queue.Job{}, queue.ErrEmpty
	}
	if err != nil {
		return queue.Job{}, err
	}
	now, err := dbNow(ctx, tx)
	if err != nil {
		return queue.Job{}, err
	}
	// Recheck against authoritative time after taking the row lock.
	if (j.Status == queue.Pending && now.Before(j.AvailableAt)) || (j.Status == queue.Running && now.Before(j.LeaseUntil)) {
		if err = tx.Commit(ctx); err != nil {
			return queue.Job{}, err
		}
		return queue.Job{}, queue.ErrEmpty
	}
	j = queue.Acquire(j, owner, now, d)
	_, err = tx.Exec(ctx, `UPDATE dq_jobs SET status='running',attempts=$2,lease_version=$3,owner=$4,lease_until=$5,updated_at=$6 WHERE id=$1`, j.ID, j.Attempts, j.Version, j.Owner, j.LeaseUntil, j.UpdatedAt)
	if err != nil {
		return queue.Job{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return queue.Job{}, err
	}
	return j, nil
}

func locked(ctx context.Context, tx pgx.Tx, l queue.Lease) (queue.Job, time.Time, error) {
	j, err := scan(tx.QueryRow(ctx, "SELECT "+columns+" FROM dq_jobs WHERE id=$1 FOR UPDATE", l.JobID))
	if errors.Is(err, queue.ErrNotFound) {
		return j, time.Time{}, queue.ErrLeaseLost
	}
	if err != nil {
		return j, time.Time{}, err
	}
	now, err := dbNow(ctx, tx)
	return j, now, err
}
func (s *Store) Extend(ctx context.Context, l queue.Lease, d time.Duration) error {
	if err := queue.ValidateDuration(d); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(tx)
	j, now, err := locked(ctx, tx, l)
	if err != nil {
		return err
	}
	if err = queue.CheckLease(j, l, now); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE dq_jobs SET lease_until=GREATEST(lease_until,$2::timestamptz),updated_at=$3 WHERE id=$1`, j.ID, now.Add(d.Truncate(time.Microsecond)), now)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) Complete(ctx context.Context, l queue.Lease, value json.RawMessage) error {
	return s.CompleteTx(ctx, l, value, nil)
}

// CompleteTx commits a result, optional business writes, and acknowledgement in
// ONE database transaction. fn must only perform SQL through tx, must not commit
// or roll back tx, and must not perform external side effects. Keep it short.
// A repeated acknowledgement from the winning lease is a no-op; fn is not rerun.
func (s *Store) CompleteTx(ctx context.Context, l queue.Lease, value json.RawMessage, fn func(context.Context, pgx.Tx) error) error {
	if !json.Valid(value) || len(value) > 1<<20 {
		return fmt.Errorf("result must be valid JSON, at most 1 MiB")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(tx)
	j, now, err := locked(ctx, tx, l)
	if err != nil {
		return err
	}
	if j.Status == queue.Succeeded && queue.Owns(j, l) {
		return nil
	}
	if err = queue.CheckLease(j, l, now); err != nil {
		return err
	}
	if fn != nil {
		if err = fn(ctx, tx); err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, "INSERT INTO dq_results(job_id,value,committed_at) VALUES($1,$2::jsonb,clock_timestamp())", j.ID, []byte(value))
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, "UPDATE dq_jobs SET status='succeeded',lease_until=NULL,updated_at=clock_timestamp() WHERE id=$1", j.ID)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) Fail(ctx context.Context, l queue.Lease, reason string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(tx)
	j, now, err := locked(ctx, tx, l)
	if err != nil {
		return err
	}
	if err = queue.CheckLease(j, l, now); err != nil {
		return err
	}
	j = queue.Reject(j, now, reason)
	_, err = tx.Exec(ctx, `UPDATE dq_jobs SET status=$2,available_at=$3,lease_until=NULL,updated_at=$4,last_error=$5 WHERE id=$1`, j.ID, j.Status, j.AvailableAt, j.UpdatedAt, j.LastError)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (s *Store) Get(ctx context.Context, id string) (queue.Job, error) {
	return scan(s.pool.QueryRow(ctx, "SELECT "+columns+" FROM dq_jobs WHERE id=$1", id))
}
func (s *Store) List(ctx context.Context, q string, limit int) ([]queue.Job, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	rows, err := s.pool.Query(ctx, "SELECT "+columns+" FROM dq_jobs WHERE queue=$1 ORDER BY created_at,id LIMIT $2", q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []queue.Job{}
	for rows.Next() {
		j, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}
func (s *Store) Results(ctx context.Context) ([]queue.Result, error) {
	rows, err := s.pool.Query(ctx, "SELECT job_id,value,committed_at FROM dq_results ORDER BY job_id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []queue.Result{}
	for rows.Next() {
		var r queue.Result
		if err := rows.Scan(&r.JobID, &r.Value, &r.CommittedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
