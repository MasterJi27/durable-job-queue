# Verification record

Performed on **18 October 2026**. Local environment: macOS/Apple Silicon,
Go 1.27.2, Docker Engine 29.8.2, PostgreSQL 17.11 in the `postgres:17-alpine`
container. The Docker build also compiled the application using `golang:1.25-alpine`.

| Check | Result |
|---|---|
| `go test -race -count=1 -coverprofile=... ./...` with a PostgreSQL test URL | Passed; integration tests enabled |
| `go vet ./...` | Passed |
| `gofmt` | Source formatted |
| Shared contract on memory and PostgreSQL | Passed |
| 200 seeds × 1,000 simulated events | Passed; 200,000 events plus final drain checks |
| Same-seed trace/report replay | Passed byte-for-byte comparison |
| Atomic business-write rollback and duplicate acknowledgement | Passed |
| 32 competing producers and 32 claimers for one key | One job, one live grant |
| 16 consumers processing 100 distinct jobs | Exactly 100 committed results |
| Long handler with heartbeat, panic handling, cancellation | Passed |
| Python smoke test with actual worker `SIGKILL` | Recovered job with higher lease version and one result |
| Docker image build and complete Compose startup | Passed |
| HTTP submission, duplicate replay, conflicting payload, retry to success | Passed against the container stack |
| PostgreSQL container restart | All columns of three jobs and two results retained unchanged |

The GitHub Actions workflow is supplied but has not been run on GitHub. No remote
repository has been created. These results demonstrate correctness in the tested
cases; no throughput benchmark, production deployment, or formal proof is claimed.

## Evidence

- [Go test output](validation/test-results.txt)
- [Coverage profile](validation/coverage.out)
- [10,000-event seed 42 report](validation/seed-42.json)
- [Replay summary](validation/replay-summary.json) and [event trace](validation/replay.log)
- [Real-process smoke results](validation/process-smoke.json)
- [Docker/API results](validation/docker-api.json)
- [Database restart results](validation/restart.json)

The Go coverage profile covers instrumented package tests. Separate CLI,
process-kill, and Docker/API smoke tests are not included in those percentages.

## Reproduce

```sh
docker compose up --build -d
export DATABASE_URL='postgres://queue:queue@127.0.0.1:55432/queue?sslmode=disable'
export TEST_DATABASE_URL="$DATABASE_URL"
go test -race -count=1 ./...
go vet ./...
make build
python3 scripts/smoke.py
bin/dq simulate -seed 42 -steps 10000 -jobs 100 -workers 8
```

Run the commands above to start your own local demo. Integration tests isolate
their tables in generated schemas; the smoke script retains its uniquely named
demo queue so you can inspect the recovery afterward.

## GUI verification

The dashboard was added on 18 October 2026. The complete race-detector suite,
including PostgreSQL integration tests, passed after the GUI changes; see
[GUI test output](validation/gui-tests.txt). `go vet ./...` and JavaScript syntax
checking also passed. The Docker stack successfully built and served the GUI.

Browser checks verified the rendered dashboard, six sample-job submissions,
invalid-JSON validation, a new job submitted through the form, result inspection,
a seeded simulation run, and JSON download. No browser errors or warnings were
reported during these checks. Dedicated mobile viewport verification was not
completed; responsive styles are included.

New API/backend tests cover full-queue statistics independent of page size,
queue isolation, literal search, non-overlapping pages, result lookup, asset
serving, bounded simulation requests, deterministic replay, and simulation
isolation from live queue data. The previous coverage profile predates the GUI;
`gui-tests.txt` records the current full test run.

## Developer handbook verification

The English handbook was added on 18 October 2026 as
[`DEVELOPER_GUIDE.pdf`](DEVELOPER_GUIDE.pdf), with editable Markdown and a
ReportLab renderer. The 20-page PDF was rendered and visually reviewed. Its
page numbers, 20 bookmarks, 18 contents links, text bounds, 13 shell examples
(syntax only), and one standalone JSON example were checked. The new guide was
cross-checked against source files and the existing verification record.

This documentation update did not change application behavior or rerun the
application test suite. Recorded runtime results remain the earlier checks above.

## Final public portfolio review

After copying the project to Projects, a fresh full race-detector run with
PostgreSQL and coverage, Go vet/format/build/module-integrity checks, JavaScript
syntax checking, an actual worker-kill recovery check, and live Docker API
checks all passed. The review also expanded `.gitignore` for local environment
files and binaries. See [the release review](RELEASE_REVIEW.md) and its current
`release-*` evidence files. `release-coverage.out` includes the GUI packages;
the earlier `coverage.out` remains a historical pre-GUI record.
