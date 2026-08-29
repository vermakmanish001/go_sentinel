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
    started_by     TEXT,
    finished_at    INTEGER,
    dispatched_at  INTEGER,
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
CREATE INDEX IF NOT EXISTS idx_runs_status  ON runs(status, started_at);

CREATE TABLE IF NOT EXISTS users (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    username      TEXT    NOT NULL UNIQUE COLLATE NOCASE,
    password_hash TEXT    NOT NULL,
    created_at    INTEGER NOT NULL
);

-- Sessions store a hash of the token, never the token itself: a leaked database
-- must not hand out usable cookies.
CREATE TABLE IF NOT EXISTS sessions (
    token_hash TEXT    PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_sessions_expiry ON sessions(expires_at);

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

// migrations run after schema, bringing databases created by earlier versions
// up to date. Each is written to be harmless if already applied; SQLite has no
// "ADD COLUMN IF NOT EXISTS", so a duplicate-column error is expected and
// ignored by applyMigrations.
var migrations = []string{
	`ALTER TABLE runs ADD COLUMN dispatched_at INTEGER`,
	`ALTER TABLE runs ADD COLUMN started_by TEXT`,
}
