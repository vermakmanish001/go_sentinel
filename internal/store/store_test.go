package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestRunLifecycle(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	if err := st.CreateRun(ctx, Run{
		ID: "run-1", Name: "smoke", PlanSpec: `{"name":"smoke"}`,
		Status: "RUNNING", StartedAt: 1000, Workers: 3, PeakVUs: 10,
	}); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	got, err := st.GetRun(ctx, "run-1")
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if got.Status != "RUNNING" || got.PeakVUs != 10 || got.FinishedAt != nil {
		t.Errorf("unexpected run after create: %+v", got)
	}

	if err := st.FinishRun(ctx, "run-1", "COMPLETED", 5000, Run{
		TotalRequests: 2670, TotalErrors: 0, PeakRPS: 120, AvgRPS: 89, P95Ms: 2, P99Ms: 9,
	}); err != nil {
		t.Fatalf("FinishRun: %v", err)
	}

	got, _ = st.GetRun(ctx, "run-1")
	if got.Status != "COMPLETED" || got.TotalRequests != 2670 || got.PeakRPS != 120 {
		t.Errorf("summary not persisted: %+v", got)
	}
	if got.FinishedAt == nil || *got.FinishedAt != 5000 {
		t.Errorf("finished_at = %v, want 5000", got.FinishedAt)
	}
}

func TestGetRunMissing(t *testing.T) {
	if _, err := newTestStore(t).GetRun(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

// A run stores a snapshot of its plan. Editing the saved plan it came from must
// not rewrite what history says the run executed.
func TestRunPlanSnapshotIsIndependentOfSavedPlan(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	plan, err := st.CreatePlan(ctx, "nightly", `{"stages":[{"target_vus":10}]}`)
	if err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}
	if err := st.CreateRun(ctx, Run{
		ID: "run-1", Name: "nightly", PlanSpec: plan.Spec, Status: "RUNNING", StartedAt: 1,
	}); err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	if _, err := st.UpdatePlan(ctx, plan.ID, "nightly", `{"stages":[{"target_vus":500}]}`); err != nil {
		t.Fatalf("UpdatePlan: %v", err)
	}

	got, _ := st.GetRun(ctx, "run-1")
	if got.PlanSpec != `{"stages":[{"target_vus":10}]}` {
		t.Errorf("run's plan changed when the saved plan was edited: %s", got.PlanSpec)
	}
}

func TestSamplesRoundTripAndCascade(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	_ = st.CreateRun(ctx, Run{ID: "run-1", Name: "s", PlanSpec: "{}", Status: "RUNNING", StartedAt: 1})

	for i := int64(0); i < 3; i++ {
		if err := st.AddSample(ctx, "run-1", Sample{TsMs: 1000 + i, RPS: float64(i * 10), P95Ms: i}); err != nil {
			t.Fatalf("AddSample: %v", err)
		}
	}
	// A repeated timestamp is the newer reading, not a duplicate row.
	if err := st.AddSample(ctx, "run-1", Sample{TsMs: 1000, RPS: 99}); err != nil {
		t.Fatalf("AddSample (upsert): %v", err)
	}

	samples, err := st.ListSamples(ctx, "run-1")
	if err != nil {
		t.Fatalf("ListSamples: %v", err)
	}
	if len(samples) != 3 {
		t.Fatalf("got %d samples, want 3 (upsert should not add a row)", len(samples))
	}
	if samples[0].RPS != 99 {
		t.Errorf("sample not overwritten: rps = %v, want 99", samples[0].RPS)
	}

	if err := st.DeleteRun(ctx, "run-1"); err != nil {
		t.Fatalf("DeleteRun: %v", err)
	}
	if after, _ := st.ListSamples(ctx, "run-1"); len(after) != 0 {
		t.Errorf("%d samples survived the run being deleted; cascade is not working", len(after))
	}
}

func TestListRunsIsNewestFirstAndOmitsPlanSpec(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	for i, id := range []string{"old", "mid", "new"} {
		_ = st.CreateRun(ctx, Run{
			ID: id, Name: id, PlanSpec: `{"big":"payload"}`,
			Status: "COMPLETED", StartedAt: int64(i * 1000),
		})
	}

	runs, err := st.ListRuns(ctx, 10, 0)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != 3 || runs[0].ID != "new" || runs[2].ID != "old" {
		t.Fatalf("wrong order: %v", ids(runs))
	}
	if runs[0].PlanSpec != "" {
		t.Error("list view returned the full plan snapshot; it should be omitted")
	}

	page, _ := st.ListRuns(ctx, 1, 1)
	if len(page) != 1 || page[0].ID != "mid" {
		t.Errorf("pagination broken: %v", ids(page))
	}
}

func ids(runs []Run) []string {
	out := make([]string, len(runs))
	for i, r := range runs {
		out[i] = r.ID
	}
	return out
}

func TestPlanCRUD(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	p, err := st.CreatePlan(ctx, "nightly", `{"a":1}`)
	if err != nil {
		t.Fatalf("CreatePlan: %v", err)
	}

	if _, err := st.UpdatePlan(ctx, p.ID, "nightly v2", `{"a":2}`); err != nil {
		t.Fatalf("UpdatePlan: %v", err)
	}
	got, _ := st.GetPlan(ctx, p.ID)
	if got.Name != "nightly v2" || got.Spec != `{"a":2}` {
		t.Errorf("update did not stick: %+v", got)
	}

	plans, _ := st.ListPlans(ctx)
	if len(plans) != 1 {
		t.Errorf("got %d plans, want 1", len(plans))
	}

	if err := st.DeletePlan(ctx, p.ID); err != nil {
		t.Fatalf("DeletePlan: %v", err)
	}
	if err := st.DeletePlan(ctx, p.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete err = %v, want ErrNotFound", err)
	}
}
