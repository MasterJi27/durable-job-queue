# Durable Job Queue with Deterministic Simulation Testing

[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat&logo=go)](https://go.dev)
[![PostgreSQL](https://img.shields.io/badge/PostgreSQL-17-4169E1?style=flat&logo=postgresql)](https://www.postgresql.org)
[![Docker](https://img.shields.io/badge/Docker-Compose-2496ED?style=flat&logo=docker)](https://www.docker.com)
[![Testing](https://img.shields.io/badge/Invariants-2%2C001%20Passed-34D399?style=flat)](simulation/)
[![Documentation](https://img.shields.io/badge/Handbook-20--Page%20PDF-F97316?style=flat)](docs/DEVELOPER_GUIDE.pdf)

A production-grade Go job queue backed by PostgreSQL, engineered with expiring leases, fencing tokens, bounded retries, idempotent enqueueing, transactional completion, and a seeded failure simulator. Includes an embedded dark-mode web console, CLI tooling, concurrent worker pools, and comprehensive crash-recovery validation.

> **Correctness Guarantee:** At-least-once processing attempts with exactly-once committed results. Business database writes share the completion transaction through `CompleteTx`.

---

## 🎬 Video Demonstration

Watch the complete operations console in action — covering real-time queue telemetry, job submission presets, worker execution tracking, deep inspection with fencing tokens, and deterministic chaos simulation:

<p align="center">
  <video src="https://github.com/MasterJi27/durable-job-queue/raw/main/media/recordings/durable_job_queue_demo.mp4" width="100%" controls autoplay loop muted playsinline></video>
</p>

* **Direct Video File (MP4):** [`media/recordings/durable_job_queue_demo.mp4`](media/recordings/durable_job_queue_demo.mp4) *(42s, H.264, 882 KB)*
* **Asset Specifications:** See [`media/README.md`](media/README.md) for full video resolutions, screenshot gallery, and reproduction scripts.

---

## 📸 Operations Console & Visual Tour

The service embeds an operations console written in vanilla HTML/CSS/JavaScript with zero frontend dependencies or build steps.

<p align="center">
  <a href="media/screenshots/01_dashboard_overview.png">
    <img src="media/screenshots/01_dashboard_overview.png" width="49%" alt="Dashboard Overview" />
  </a>
  <a href="media/screenshots/04_job_inspector_drawer.png">
    <img src="media/screenshots/04_job_inspector_drawer.png" width="49%" alt="Job Inspector Drawer" />
  </a>
</p>
<p align="center">
  <em>(Left) Real-time operations console with live metrics and queue distribution. (Right) Deep-dive Job Inspector with attempt tracking, fencing version, and committed results.</em>
</p>

<p align="center">
  <a href="media/screenshots/02_create_job_modal.png">
    <img src="media/screenshots/02_create_job_modal.png" width="49%" alt="Create Job Modal" />
  </a>
  <a href="media/screenshots/06_simulation_lab_results.png">
    <img src="media/screenshots/06_simulation_lab_results.png" width="49%" alt="Simulation Lab Results" />
  </a>
</p>
<p align="center">
  <em>(Left) Idempotent job submission dialog with configurable presets. (Right) Chaos simulation lab with 2,001 invariant checks verified under fault injection.</em>
</p>

> 🔍 **Explore all 7 high-resolution screenshots with architectural explanations in the [Media Showcase Guide](media/README.md).**

### Key Console Capabilities
- **Overview:** Live counts for pending, running, succeeded, and dead-letter jobs with visual health distribution.
- **All jobs:** Search by ID/key/handler, filter by status, and browse paginated historical jobs.
- **New job:** Submit JSON payloads with queue name, idempotency key, retry budget, and initial delay. Presets demonstrate normal execution, retries, long-running jobs, and terminal failure retention.
- **Job inspector:** Inspect payloads, attempt counts, lease fencing tokens, error traces, and committed database results.
- **Simulation lab:** Execute reproducible chaos schedules with chosen seeds and export audit traces as JSON. Simulations are strictly isolated from live database jobs.

---

## Run the simulator first

Requires Go 1.25 or newer. No database is needed for simulation.

```sh
go run ./cmd/dq simulate -seed 42 -steps 10000 -jobs 100 -workers 8
go run ./cmd/dq simulate -seed 42 -steps 1000 -trace 2> replay.log
go test -race ./...
```

The simulator injects crashes, duplicated deliveries, duplicated enqueue requests,
lost completion acknowledgements, transient failures, and positive/negative
database clock jumps. It checks job conservation, result uniqueness,
immutable results, fencing, bounded attempts, and terminal-state consistency
after every event. A fault-free suffix checks eventual completion or dead-lettering.

The memory backend is a reference implementation. Shared state transitions and
behavioral contract tests connect it to the PostgreSQL backend; simulation alone
does not verify PostgreSQL locking, WAL, networking, or the worker's goroutines.

## Run the complete service

Requires Docker Desktop or Docker Engine with Compose.

```sh
docker compose up --build -d
curl http://127.0.0.1:8080/healthz

curl -sS -X POST http://127.0.0.1:8080/v1/jobs \
  -H 'Content-Type: application/json' \
  -d '{"kind":"echo","idempotency_key":"welcome-001","payload":{"message":"hello"}}'

curl -sS http://127.0.0.1:8080/v1/jobs
docker compose logs worker
```

Repeat the POST: it returns the same job ID, even after completion. Change its
payload while retaining the key: the API returns `409 Conflict`.

Compose starts PostgreSQL, runs the schema migration, then starts the API and
worker. Ports are bound to localhost. `docker compose down` stops the stack and
retains the named database volume. The development API has no authentication;
keep it local or add authentication and TLS before exposing it.

## Run with local Go binaries

```sh
docker compose up -d postgres
cp .env.example .env
set -a; . ./.env; set +a
make build
bin/dq migrate
bin/dq enqueue -key report-001 -kind sha256 -payload '{"report":"quarterly"}'
bin/dq worker -concurrency 4
```

In a second terminal, load `.env` again and run:

```sh
bin/dq list
bin/dq results
bin/dq serve
```

For a complete CLI walkthrough, run `make demo` after loading `.env`.
For an observable retry:

```sh
bin/dq enqueue -key retry-001 -payload '{"message":"retry me","demo_fail_until":2}'
```

For a heartbeat/crash demonstration:

```sh
bin/dq enqueue -key slow-001 -payload '{"demo_sleep_ms":10000}'
bin/dq worker -concurrency 1 -lease 3s
```

Stop the worker while it is processing, then restart it. The expired lease can
be reclaimed. `attempts` and `lease_version` increase; only one result is committed.
See [the demo guide](docs/DEMO.md) for a process-kill walkthrough.

## How it works

```mermaid
flowchart LR
    P[CLI / HTTP producer] -->|unique queue + key| J[(PostgreSQL jobs)]
    J -->|FOR UPDATE SKIP LOCKED| W[Concurrent workers]
    W -->|heartbeat + fencing token| J
    W -->|failure + backoff| J
    W --> T[Completion transaction]
    T --> R[(Unique result + business writes)]
    T -->|mark succeeded| J
    J -->|retry budget exhausted| D[Retained dead-letter job]
```

1. **Enqueue:** a unique `(queue, idempotency_key)` constraint makes producer
   retries safe. A stored fingerprint detects a different kind or payload.
2. **Claim:** short transactions lock one eligible job using `SKIP LOCKED`,
   increment its attempt count and lease version, and set a database-time deadline.
3. **Process:** bounded worker concurrency, lease heartbeats, cancellation,
   panic recovery, and structured JSON logs.
4. **Complete:** lock the job, validate ownership/version/deadline, insert one
   result, and mark success in one transaction. A repeated winning acknowledgement
   returns success without rewriting its result or rerunning business writes.
5. **Recover:** expired leases are reclaimable. Handler failures use exponential
   backoff with deterministic jitter. Jobs that exhaust their budget remain `dead`.

`pending → running → succeeded` is the normal path. Failures transition
`running → pending`, or `running → dead` on the final attempt. A crashed worker's
expired lease transitions to another `running` attempt, or `dead` when exhausted.

## HTTP endpoints

| Method | Path | Purpose |
|---|---|---|
| GET | `/` | Embedded web dashboard |
| GET | `/healthz` | Database readiness |
| POST | `/v1/jobs` | Enqueue; returns 202, or 409 for conflicting key reuse |
| GET | `/v1/jobs?queue=default&limit=100` | List up to 1,000 jobs |
| GET | `/v1/jobs/{id}` | Inspect status, attempts, lease version, and last error |
| GET | `/v1/jobs/{id}/result` | Read a committed result |
| GET | `/v1/overview` | Full queue statistics and a filtered page of jobs |
| POST | `/v1/simulations` | Run one bounded, isolated simulation |

POST accepts `queue`, `kind`, `idempotency_key`, `payload`, `max_attempts`, and
`delay_ms`. Defaults are queue `default`, five attempts, and no delay. The body
limit is 1 MiB; unknown fields and multiple JSON values are rejected. List uses
creation order and is intended for the demo, not a paginated operations console.

## Test it

```sh
make test                         # includes 200 seeds × 1,000 simulation events
make vet
make simulate                     # another 100 reproducible schedules

# PostgreSQL integration tests create and drop isolated test schemas.
# Use a development database with permission to create schemas.
export TEST_DATABASE_URL='postgres://queue:queue@127.0.0.1:55432/queue?sslmode=disable'
make integration

# Optional real-process crash recovery check (Python 3 standard library only).
make build
python3 scripts/smoke.py           # requires DATABASE_URL; uses a unique queue

# Explore additional schedules with Go's fuzzing engine.
go test ./simulation -run '^$' -fuzz FuzzSchedules -fuzztime 30s
```

Without `TEST_DATABASE_URL`, PostgreSQL tests explicitly skip. CI provides
PostgreSQL so those tests run on pushes and pull requests.

| Test layer | What it checks |
|---|---|
| Shared store contract | Deduplication, conflicting keys, stale leases, retries, dead letters, heartbeats, concurrent producers/claims |
| PostgreSQL integration | Atomic business-effect rollback, lost acknowledgements, row-lock skipping, multiple consumers |
| Worker tests | Renewal during long jobs, panic handling, cancellation without acknowledgement |
| HTTP tests | Submission, replay, conflict responses, validation, lookup and health |
| Deterministic simulation | Crashes, duplicates, clock jumps, invariants, replay and eventual terminal accounting |

See [validation results](docs/VALIDATION.md) for what was actually run on the
provided project. These are correctness checks, not throughput benchmarks.

## Repository layout

```text
cmd/dq/                   CLI, HTTP server startup, demo handlers
queue/                    Protocol, shared transitions, worker lifecycle
queue/postgres/           Durable implementation, schema, integration tests
queue/memory/             Reference implementation for deterministic simulation
simulation/               Seeded fault scheduler, invariant checks, fuzz tests
internal/httpapi/          HTTP routes, embedded dashboard, API tests
internal/queuetest/        Contract shared by both store backends
docs/                     Design, demo, interview/CV notes, validation
scripts/demo.sh            Runnable CLI demonstration
.github/workflows/ci.yml   Tests against PostgreSQL
```

## Design limits

- A handler may execute multiple times. Only results and writes inside
  `CompleteTx` receive the atomic exactly-once guarantee. External effects need
  destination-side idempotency or an outbox/inbox protocol.
- Database time is authoritative. A forward clock jump can expire a lease early;
  a backward jump can delay recovery. Fencing protects safety after reassignment.
  Eventual progress assumes the clock advances and failures eventually stop.
- Durability relies on PostgreSQL WAL and storage durability. This project does
  not implement replication, failover, corruption repair, or disaster recovery.
- Jobs and keys are retained indefinitely. There is no dead-letter replay,
  retention policy, priority scheduling, tenant authorization, or global ordering.
- Final-attempt expiration is swept in batches of 100 while workers poll. An idle
  queue without workers does not autonomously move expired jobs to `dead`.

Read [the architecture notes](docs/ARCHITECTURE.md) and
[the CV/interview guide](docs/CV_AND_INTERVIEW.md) before presenting the project.

Implementation references: [PostgreSQL locking and SKIP LOCKED](https://www.postgresql.org/docs/current/sql-select.html)
and [pgx connection pools and transactions](https://pkg.go.dev/github.com/jackc/pgx/v5/pgxpool).
