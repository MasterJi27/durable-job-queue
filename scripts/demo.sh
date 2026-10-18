#!/usr/bin/env sh
set -eu
: "${DATABASE_URL:?Set DATABASE_URL first; see .env.example}"
mkdir -p bin
go build -o bin/dq ./cmd/dq
bin/dq migrate
demo_key="demo-$(date +%s)-$$"
bin/dq enqueue -key "$demo_key" -kind echo -payload '{"message":"hello durable queue","demo_fail_until":1}'
# Repeating the request must print the same job ID.
bin/dq enqueue -key "$demo_key" -kind echo -payload '{"message":"hello durable queue","demo_fail_until":1}'
bin/dq worker -concurrency 2 &
worker_pid=$!
trap 'kill "$worker_pid" 2>/dev/null || true; wait "$worker_pid" 2>/dev/null || true' EXIT INT TERM
sleep 4
bin/dq list
bin/dq results
bin/dq simulate -seed 42 -steps 2000 -jobs 30
