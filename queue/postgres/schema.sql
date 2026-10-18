CREATE TABLE IF NOT EXISTS dq_jobs (
    id text PRIMARY KEY,
    queue text NOT NULL,
    kind text NOT NULL,
    idempotency_key text NOT NULL,
    payload jsonb NOT NULL,
    fingerprint text NOT NULL,
    status text NOT NULL CHECK (status IN ('pending','running','succeeded','dead')),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    max_attempts integer NOT NULL CHECK (max_attempts BETWEEN 1 AND 100),
    lease_version bigint NOT NULL DEFAULT 0,
    owner text NOT NULL DEFAULT '',
    available_at timestamptz NOT NULL,
    lease_until timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    last_error text NOT NULL DEFAULT '',
    UNIQUE (queue, idempotency_key),
    CHECK (attempts <= max_attempts),
    CHECK ((status = 'running') = (lease_until IS NOT NULL))
);
CREATE INDEX IF NOT EXISTS dq_pending ON dq_jobs(queue, available_at, id) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS dq_expired ON dq_jobs(queue, lease_until) WHERE status = 'running';
CREATE TABLE IF NOT EXISTS dq_results (
    job_id text PRIMARY KEY REFERENCES dq_jobs(id),
    value jsonb NOT NULL,
    committed_at timestamptz NOT NULL
);
