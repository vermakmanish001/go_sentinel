// Package store persists run history and saved plans in SQLite.
//
// It deliberately lives in the API server rather than the orchestrator: the
// orchestrator's job is executing load, and keeping history out of it means a
// restart there loses nothing durable.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver: the images build with CGO_ENABLED=0
)

// ErrNotFound is returned when a lookup matches no row.
var ErrNotFound = errors.New("not found")

// Run is one execution of a plan.
type Run struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	PlanSpec      string  `json:"plan_spec,omitempty"`
	Status        string  `json:"status"`
	StartedAt     int64   `json:"started_at"`
	FinishedAt    *int64  `json:"finished_at,omitempty"`
	Workers       int     `json:"workers"`
	PeakVUs       int     `json:"peak_vus"`
	TotalRequests int64   `json:"total_requests"`
	TotalErrors   int64   `json:"total_errors"`
	ErrorPct      float64 `json:"error_pct"`
	PeakRPS       float64 `json:"peak_rps"`
	AvgRPS        float64 `json:"avg_rps"`
	P95Ms         int64   `json:"p95_ms"`
	P99Ms         int64   `json:"p99_ms"`
}

// Sample is one second of a run's metrics.
type Sample struct {
	TsMs     int64   `json:"ts_ms"`
	RPS      float64 `json:"rps"`
	ErrRate  float64 `json:"err_rate"`
	ErrPct   float64 `json:"err_pct"`
	P50Ms    int64   `json:"p50_ms"`
	P95Ms    int64   `json:"p95_ms"`
	P99Ms    int64   `json:"p99_ms"`
	Requests int64   `json:"requests"`
	Errors   int64   `json:"errors"`
}

// Plan is a reusable saved plan.
type Plan struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Spec      string `json:"spec"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

// Store is the persistence surface. Keeping it an interface means swapping
// SQLite for Postgres later is a constructor change, not a rewrite.
type Store interface {
	CreateRun(ctx context.Context, r Run) error
	FinishRun(ctx context.Context, id, status string, finishedAt int64, summary Run) error
	GetRun(ctx context.Context, id string) (Run, error)
	ListRuns(ctx context.Context, limit, offset int) ([]Run, error)
	DeleteRun(ctx context.Context, id string) error

	AddSample(ctx context.Context, runID string, s Sample) error
	ListSamples(ctx context.Context, runID string) ([]Sample, error)

	CreatePlan(ctx context.Context, name, spec string) (Plan, error)
	UpdatePlan(ctx context.Context, id int64, name, spec string) (Plan, error)
	GetPlan(ctx context.Context, id int64) (Plan, error)
	ListPlans(ctx context.Context) ([]Plan, error)
	DeletePlan(ctx context.Context, id int64) error

	Close() error
}

type sqliteStore struct{ db *sql.DB }

// Open opens (creating if needed) the SQLite database at path and applies the
// schema.
func Open(path string) (Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}

	// SQLite tolerates one writer. Serialising here avoids SQLITE_BUSY under
	// concurrent sample writes and API reads.
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &sqliteStore{db: db}, nil
}

func (s *sqliteStore) Close() error { return s.db.Close() }

func nowMs() int64 { return time.Now().UnixMilli() }

// ---------- runs ----------

func (s *sqliteStore) CreateRun(ctx context.Context, r Run) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO runs (id, name, plan_spec, status, started_at, workers, peak_vus)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.Name, r.PlanSpec, r.Status, r.StartedAt, r.Workers, r.PeakVUs)
	return err
}

func (s *sqliteStore) FinishRun(ctx context.Context, id, status string, finishedAt int64, sum Run) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE runs SET status = ?, finished_at = ?,
		    total_requests = ?, total_errors = ?, error_pct = ?,
		    peak_rps = ?, avg_rps = ?, p95_ms = ?, p99_ms = ?
		WHERE id = ?`,
		status, finishedAt,
		sum.TotalRequests, sum.TotalErrors, sum.ErrorPct,
		sum.PeakRPS, sum.AvgRPS, sum.P95Ms, sum.P99Ms, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

const runColumns = `id, name, plan_spec, status, started_at, finished_at, workers,
	peak_vus, total_requests, total_errors, error_pct, peak_rps, avg_rps, p95_ms, p99_ms`

func scanRun(sc interface{ Scan(...any) error }) (Run, error) {
	var r Run
	err := sc.Scan(&r.ID, &r.Name, &r.PlanSpec, &r.Status, &r.StartedAt, &r.FinishedAt,
		&r.Workers, &r.PeakVUs, &r.TotalRequests, &r.TotalErrors, &r.ErrorPct,
		&r.PeakRPS, &r.AvgRPS, &r.P95Ms, &r.P99Ms)
	return r, err
}

func (s *sqliteStore) GetRun(ctx context.Context, id string) (Run, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+runColumns+` FROM runs WHERE id = ?`, id)
	r, err := scanRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	return r, err
}

func (s *sqliteStore) ListRuns(ctx context.Context, limit, offset int) ([]Run, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+runColumns+` FROM runs ORDER BY started_at DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	runs := make([]Run, 0, limit)
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		// The plan snapshot can be large; list views do not need it.
		r.PlanSpec = ""
		runs = append(runs, r)
	}
	return runs, rows.Err()
}

func (s *sqliteStore) DeleteRun(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM runs WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------- samples ----------

func (s *sqliteStore) AddSample(ctx context.Context, runID string, sm Sample) error {
	// A run's metrics are polled once a second; an occasional duplicate
	// timestamp is expected and is simply the newer reading.
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO samples (run_id, ts_ms, rps, err_rate, err_pct, p50_ms, p95_ms, p99_ms, requests, errors)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(run_id, ts_ms) DO UPDATE SET
			rps = excluded.rps, err_rate = excluded.err_rate, err_pct = excluded.err_pct,
			p50_ms = excluded.p50_ms, p95_ms = excluded.p95_ms, p99_ms = excluded.p99_ms,
			requests = excluded.requests, errors = excluded.errors`,
		runID, sm.TsMs, sm.RPS, sm.ErrRate, sm.ErrPct, sm.P50Ms, sm.P95Ms, sm.P99Ms, sm.Requests, sm.Errors)
	return err
}

func (s *sqliteStore) ListSamples(ctx context.Context, runID string) ([]Sample, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT ts_ms, rps, err_rate, err_pct, p50_ms, p95_ms, p99_ms, requests, errors
		FROM samples WHERE run_id = ? ORDER BY ts_ms`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Sample
	for rows.Next() {
		var sm Sample
		if err := rows.Scan(&sm.TsMs, &sm.RPS, &sm.ErrRate, &sm.ErrPct,
			&sm.P50Ms, &sm.P95Ms, &sm.P99Ms, &sm.Requests, &sm.Errors); err != nil {
			return nil, err
		}
		out = append(out, sm)
	}
	return out, rows.Err()
}

// ---------- plans ----------

func (s *sqliteStore) CreatePlan(ctx context.Context, name, spec string) (Plan, error) {
	ts := nowMs()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO plans (name, spec, created_at, updated_at) VALUES (?, ?, ?, ?)`,
		name, spec, ts, ts)
	if err != nil {
		return Plan{}, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Plan{}, err
	}
	return Plan{ID: id, Name: name, Spec: spec, CreatedAt: ts, UpdatedAt: ts}, nil
}

func (s *sqliteStore) UpdatePlan(ctx context.Context, id int64, name, spec string) (Plan, error) {
	ts := nowMs()
	res, err := s.db.ExecContext(ctx,
		`UPDATE plans SET name = ?, spec = ?, updated_at = ? WHERE id = ?`, name, spec, ts, id)
	if err != nil {
		return Plan{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Plan{}, ErrNotFound
	}
	return s.GetPlan(ctx, id)
}

func (s *sqliteStore) GetPlan(ctx context.Context, id int64) (Plan, error) {
	var p Plan
	err := s.db.QueryRowContext(ctx,
		`SELECT id, name, spec, created_at, updated_at FROM plans WHERE id = ?`, id).
		Scan(&p.ID, &p.Name, &p.Spec, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Plan{}, ErrNotFound
	}
	return p, err
}

func (s *sqliteStore) ListPlans(ctx context.Context) ([]Plan, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, spec, created_at, updated_at FROM plans ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	plans := make([]Plan, 0)
	for rows.Next() {
		var p Plan
		if err := rows.Scan(&p.ID, &p.Name, &p.Spec, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		plans = append(plans, p)
	}
	return plans, rows.Err()
}

func (s *sqliteStore) DeletePlan(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM plans WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
