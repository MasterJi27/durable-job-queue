# CV wording and interview preparation

## CV entry

**Durable Job Queue with Simulation Testing** — Go, PostgreSQL, Docker

- Built a PostgreSQL-backed job queue in Go with expiring leases, fencing tokens,
  bounded retries, idempotency keys, and transactional result commits.
- Implemented a deterministic simulator that injects worker crashes, duplicate
  delivery, lost acknowledgements, and clock jumps to verify job accounting and
  prevent duplicate committed results.

After reproducing the checked-in tests, an optional quantified version of the
second bullet is:

- Validated queue invariants across 200 seeded schedules and 200,000 simulated
  events, with PostgreSQL integration tests for concurrent claims and
  atomic completion.

Use the entry once you have run the project and can explain its implementation.
Do not describe it as production-deployed or claim throughput measurements that
have not been performed. See `VALIDATION.md` for the delivered verification record.

## Optional GUI bullet

- Added a web dashboard for job submission, live queue statistics, paginated
  inspection, and reproducible failure simulations with JSON export.

## A concise project explanation

“I built a durable background-job queue using Go and PostgreSQL. Producers use
idempotency keys, workers claim jobs with expiring leases, and a fencing version
prevents an old worker from committing after reassignment. Completion writes the
result and acknowledgement in a single transaction. A deterministic simulator
lets me replay crash and duplicate-delivery schedules using a seed. The key
distinction is that processing attempts can repeat, while committed database
results do not.”

## Questions to be ready for

**Why PostgreSQL instead of Redis or Kafka?**

It makes durable queue state, business writes, and acknowledgement available in
one relational transaction. This is useful when the application already uses
PostgreSQL. The trade-offs include row contention, polling, write amplification,
vacuum overhead, and less specialized scheduling/distribution machinery.

**Why isn't a lease enough?**

A paused process can resume after its lease expired and another worker took over.
A monotonically increasing version lets the database reject its old grant.

**Is this exactly-once execution?**

No. A handler can execute repeatedly. The guarantee covers a single committed
result and business writes inside the same PostgreSQL completion transaction.
External effects require a separate idempotency protocol.

**What if the database commits but the client times out?**

The caller cannot infer whether the transaction committed. Enqueue retries reuse
the key; completion retries reuse the lease. A successful completion replay is
a no-op and does not execute its callback again.

**Why deterministic simulation?**

Seeded event ordering, deterministic IDs, an injected clock, and no sleeps make
failure schedules replayable. It is fast enough to explore many schedules. It
complements integration tests; it cannot certify database or network behavior.

**Can a job be lost after too many failures?**

It remains in `dead` with its attempt count and last error. That is explicit
terminal failure, not successful processing and not silent disappearance.

**What would you improve next?**

Add observability and load tests, cursor-based pagination, retention and dead-letter
replay, an outbox for external effects, and tests that kill database connections
at transaction boundaries. Each feature needs explicit semantics and tests.
