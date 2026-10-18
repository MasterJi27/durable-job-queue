# Five-minute demonstration

Start with `docker compose up --build -d`. In another terminal run
`docker compose logs -f worker` to watch structured events.

1. Submit an echo job using the README curl example. It moves to `succeeded`.
2. Submit the identical request again. It returns the same job ID.
3. Change the payload but keep its key. Explain the `409` response.
4. Submit a retrying job with `"demo_fail_until":2` in its payload. Watch two
   failures followed by one success; inspect `attempts: 3`.
5. Run `go run ./cmd/dq simulate -seed 42 -steps 2000 -jobs 30 -trace`.
   Explain the seed, fault events, invariant checks, and terminal accounting.

## Show crash recovery

Use a local worker so it is easy to kill a single process. Stop the Compose
worker first; keep PostgreSQL running. In a POSIX shell from the project root:

```sh
docker compose stop worker
export DATABASE_URL='postgres://queue:queue@127.0.0.1:55432/queue?sslmode=disable'
make build
bin/dq migrate
bin/dq enqueue -key crash-demo-001 -payload '{"demo_sleep_ms":10000}'
bin/dq worker -concurrency 1 -lease 3s &
worker_pid=$!
sleep 1
kill -KILL "$worker_pid"
bin/dq list
sleep 3
bin/dq worker -concurrency 1 -lease 3s
```

In another terminal, load `DATABASE_URL` and inspect `bin/dq list` and
`bin/dq results` after about ten seconds. The attempt count and lease version
increase. One result exists. Stop the demonstration worker with Ctrl-C.
Use a new idempotency key for another run.

## Explain the failure window

Draw: compute → business write → acknowledgement. If business writes and
acknowledgement are separate, a crash between them repeats the effect.
Then show `CompleteTx`, which puts database business writes, the result, and the
acknowledgement in one transaction. Point to
`TestAtomicBusinessEffectAndLostAck` for a runnable example.

For an external payment/email/API call, explain that the transaction cannot
include another service. A destination-side idempotency key or outbox/inbox
design is necessary; handler execution itself may repeat.

## Before publishing

- Run the tests and demo yourself, and inspect the SQL and worker code.
- Replace the module path `durablequeue` and internal imports with your intended
  repository path if you want others to import it as a Go library. The supplied
  module path already builds and runs locally.
- Add your chosen license and repository URL. No license has been selected on
  your behalf, and no repository has been published.
- Use measured correctness results. Add throughput/latency figures only after
  running a reproducible load test with documented hardware and configuration.
