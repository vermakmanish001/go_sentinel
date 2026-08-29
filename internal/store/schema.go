package store

// schema is applied on every open; each statement is idempotent.
//
// A run stores a snapshot of the plan it executed rather than a reference to a
// saved plan. Editing a saved plan must not retroactively rewrite what past
// runs claim to have executed.
const schema = `
PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;
PRAGMA busy_timeout = 5000;

CREATE TABLE IF NOT EXISTS plans (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT    NOT NULL,
    spec       TEXT    NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS runs (
    id             TEXT    PRIMARY KEY,
    name           TEXT    NOT NULL,
    plan_spec      TEXT    NOT NULL,
    status         TEXT    NOT NULL,
    started_at     INTEGER NOT NULL,
    finished_at    INTEGER,
    workers        INTEGER NOT NULL DEFAULT 0,
    peak_vus       INTEGER NOT NULL DEFAULT 0,
    total_requests INTEGER NOT NULL DEFAULT 0,
    total_errors   INTEGER NOT NULL DEFAULT 0,
    error_pct      REAL    NOT NULL DEFAULT 0,
    peak_rps       REAL    NOT NULL DEFAULT 0,
    avg_rps        REAL    NOT NULL DEFAULT 0,
    p95_ms         INTEGER NOT NULL DEFAULT 0,
    p99_ms         INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_runs_started ON runs(started_at DESC);

CREATE TABLE IF NOT EXISTS samples (
    run_id   TEXT    NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    ts_ms    INTEGER NOT NULL,
    rps      REAL    NOT NULL,
    err_rate REAL    NOT NULL,
    err_pct  REAL    NOT NULL,
    p50_ms   INTEGER NOT NULL DEFAULT 0,
    p95_ms   INTEGER NOT NULL DEFAULT 0,
    p99_ms   INTEGER NOT NULL DEFAULT 0,
    requests INTEGER NOT NULL DEFAULT 0,
    errors   INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (run_id, ts_ms)
);
`
