#!/usr/bin/env python3
"""Exercise real processes against DATABASE_URL; retain uniquely scoped demo jobs.

Run `make build` first. Uses only the Python standard library. Starts and stops
only worker processes created by this script; it does not stop your database.
"""
import json
import os
from pathlib import Path
import subprocess
import time
import uuid

ROOT = Path(__file__).resolve().parents[1]
BIN = ROOT / "bin" / "dq"
NAME = "smoke-" + uuid.uuid4().hex[:12]


def command(*args):
    result = subprocess.run([str(BIN), *args], check=True, capture_output=True, text=True)
    return json.loads(result.stdout) if result.stdout.strip() else None


def enqueue(key, payload, kind="echo", attempts=5):
    return command("enqueue", "-queue", NAME, "-key", key, "-kind", kind,
                   "-payload", json.dumps(payload), "-attempts", str(attempts))


def wait_for(job_id, predicate, timeout=20):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        job = command("get", "-id", job_id)
        if predicate(job):
            return job
        time.sleep(0.05)
    raise AssertionError(f"job {job_id} did not reach expected state: {job}")


def worker(log):
    return subprocess.Popen([str(BIN), "worker", "-queue", NAME,
                             "-concurrency", "1", "-lease", "1s"],
                            stdout=log, stderr=log)


def main():
    if not os.environ.get("DATABASE_URL"):
        raise SystemExit("Set DATABASE_URL to a development database")
    command("migrate")
    active = None
    with open(os.devnull, "w") as log:
        try:
            retry = enqueue("retry", {"demo_fail_until": 1})
            duplicate = enqueue("retry", {"demo_fail_until": 1})
            assert retry["id"] == duplicate["id"], "duplicate enqueue inserted another row"
            active = worker(log)
            completed = wait_for(retry["id"], lambda j: j["status"] == "succeeded")
            assert completed["attempts"] == 2, completed
            dead = enqueue("dead", {}, kind="unknown", attempts=1)
            wait_for(dead["id"], lambda j: j["status"] == "dead")
            slow = enqueue("crash", {"demo_sleep_ms": 2000})
            running = wait_for(slow["id"], lambda j: j["status"] == "running")
            active.kill()  # Actual abrupt process death, before computation finishes.
            active.wait(timeout=5)
            active = worker(log)
            recovered = wait_for(slow["id"], lambda j: j["status"] == "succeeded")
            assert recovered["attempts"] == 2, recovered
            assert recovered["lease_version"] > running["lease_version"], recovered
            results = command("results")
            for job_id in [retry["id"], slow["id"]]:
                assert sum(r["job_id"] == job_id for r in results) == 1
            assert all(r["job_id"] != dead["id"] for r in results)
            print(json.dumps({"status": "passed", "queue": NAME,
                              "checks": ["producer deduplication", "retry then success",
                                         "dead-letter retention", "SIGKILL recovery",
                                         "lease fencing version advances", "one result per success"]}, indent=2))
        finally:
            if active is not None and active.poll() is None:
                active.terminate()
                try:
                    active.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    active.kill()
                    active.wait(timeout=5)


if __name__ == "__main__":
    main()
