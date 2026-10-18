# Design decisions

## Storage and transactions

`dq_jobs` stores the durable job and lease. `dq_results` has one row per job,
enforced by its primary key and foreign key. Partial indexes cover pending jobs
and running leases. The queue never deletes a job during processing.

Claims hold a row lock only while issuing a lease. Processing happens outside
the transaction. `FOR UPDATE SKIP LOCKED` lets competing workers skip a locked
job and continue. It does not provide strict FIFO ordering or fairness under
unbounded contention.

Completion takes the job lock again and reads database time after acquiring
the lock. Checking time before waiting for a lock could accept a lease that
expired during the wait. Ownership, version, running status, and deadline are
all validated. The lock serializes completion against reclaiming the same job.

A `CompleteTx` callback may run SQL inside that transaction. If the deadline
passes while the callback holds the lock, another worker cannot reclaim the job
until the transaction ends. Keep callbacks short and context-aware. They must
use the supplied transaction, must not commit/roll it back, and must not send
external requests. The ordinary worker computes a result and calls `Complete`;
an application that needs business writes should integrate `CompleteTx` in its
own processing path.

```go
err := store.CompleteTx(ctx, job.Lease(), json.RawMessage(`{"credited":10}`),
    func(ctx context.Context, tx pgx.Tx) error {
        _, err := tx.Exec(ctx,
            "UPDATE accounts SET credits = credits + $1 WHERE id = $2",
            10, accountID)
        return err
    })
```

The application owns the `accounts` schema. The integration test uses a
`balances` table to demonstrate rollback and replay of this pattern.

## Crash boundaries

| Crash point | Recovery |
|---|---|
| Before enqueue commits | Producer retries with the same key |
| After enqueue commits, before response | Unique key returns the original job |
| After claim, before processing | Lease expires and another worker reclaims |
| During processing | Handler may run again; no completion was committed |
| Inside completion transaction | Uncommitted result/business writes/ack roll back together |
| After completion commits, before response | Retry of the winning token returns success with the original result |
| Old worker resumes after reclaim | Its lower lease version is rejected |
| Crash on final attempt | Next claim sweep retains a dead-letter record |

An ambiguous database response is not proof that a write failed. Reuse the
original key or lease token when retrying; do not manufacture a new request ID.

## Idempotency semantics

Keys are scoped to a queue and retained for the lifetime of the job. The
fingerprint includes the kind and canonical JSON payload. Object key ordering
and insignificant whitespace are normalized. Numeric spellings such as `1`
and `1.0` remain different for fingerprinting. The first successful enqueue
sets the retry budget and schedule; later duplicates do not modify those options.

PostgreSQL stores payloads as JSONB, so applications must not rely on original
JSON formatting or object key order. Its JSONB encoding restrictions still apply.

The enqueue conflict path uses a no-op update to return the winning row under
concurrent insertion. This avoids a single-statement snapshot race in an
`INSERT ... DO NOTHING` plus `SELECT` CTE, at the cost of writing a new row
version for a duplicate request. High-volume deployments should measure this
trade-off and tune vacuum/retention accordingly.

## Leases and clocks

Each claim increments a persistent fencing version. A lease consists of job ID,
owner, and version; worker wall-clock time is not trusted. Heartbeats extend a
valid lease and never shorten it. A stale owner cannot acknowledge, fail, or
extend the current grant.

PostgreSQL uses `clock_timestamp()` rather than a timestamp captured at
transaction start. Database wall-clock jumps still affect deadlines. A forward
jump can cause overlapping *handler execution*, but only the current locked
grant may commit. A backward jump can postpone retry eligibility and can make
an unreassigned lease appear valid again. A superseded fencing version stays
invalid regardless of clock direction.

Liveness assumes eventually advancing database time, available storage, active
workers, and a finite failure period. Dead-lettering is a terminal accounting
outcome, not successful execution.

## Retry policy

Attempts count lease acquisitions, including acquisitions followed by crashes.
The default budget is five. Handler failure waits roughly 1, 2, 4, 8, … seconds,
with the exponential base capped at 60 seconds and deterministic jitter up to
about 25%. Explicit failures back off; a crashed lease can be reclaimed as soon
as it expires. This intentionally favors crash recovery over extra crash backoff.

All claims participate in a bounded sweep of final-attempt expirations. Terminal
jobs and results are immutable through the queue API. There is no operator replay
endpoint; a future replay feature must define new idempotency and attempt semantics.

## What simulation proves—and does not

The seeded scheduler operates on the memory store and the shared `Acquire`,
`Reject`, and lease-validation rules. It controls the reference database clock,
orders events deterministically, and replays old delivery tokens. Assertions
run after each event; after fault injection stops, it advances time and drains.

This catches protocol errors and verifies finite explored schedules. It is not
a formal proof, does not model every possible interleaving, and does not emulate
disk/network/PostgreSQL internals. Real PostgreSQL tests independently cover
unique constraints, transactional rollback, concurrency, and row locking. Worker
goroutine behavior is covered by worker tests and Go's race detector.

Useful next steps: transactional outbox dispatch, explicit dead-letter replay,
cursor-based pagination, queue-depth/lease metrics, load testing, retention, and a fault
harness that kills database connections or the database process mid-transaction.
