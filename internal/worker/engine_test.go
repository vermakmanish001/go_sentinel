package worker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/vermakmanish001/go_sentinel/pkg/models"
)

// A worker allocated no VUs for a stage must still hold that stage for its full
// duration. Returning as soon as its (empty) VU set finishes would let it run
// ahead into the next stage while the rest of the fleet is still on this one.
func TestStageHoldsFullDurationWithZeroVUs(t *testing.T) {
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	const stageDur = 400 * time.Millisecond
	plan := &models.TestPlan{
		ID:   "stage-timing",
		Name: "stage timing",
		Stages: []models.Stage{
			{Duration: stageDur, TargetVUs: 0}, // this worker sits this stage out
			{Duration: stageDur, TargetVUs: 2},
			{Duration: stageDur, TargetVUs: 0},
		},
		HTTP: models.HTTPConfig{
			BaseURL:  srv.URL,
			Requests: []models.Request{{Method: http.MethodGet, Path: "/"}},
			Timeout:  5 * time.Second,
		},
	}

	engine := NewEngine("test-worker", 4, nil, zap.NewNop())
	defer engine.Shutdown()

	start := time.Now()
	if err := engine.RunTest(context.Background(), "stage-timing", plan, 2); err != nil {
		t.Fatalf("RunTest: %v", err)
	}
	elapsed := time.Since(start)

	if want := 3 * stageDur; elapsed < want {
		t.Errorf("test ran for %v, want at least %v: a zero-VU stage returned early", elapsed, want)
	}
	if atomic.LoadInt64(&hits) == 0 {
		t.Error("no requests reached the target during the non-empty stage")
	}
}

// Virtual users must survive being stopped at a stage boundary. They are built
// once per test and reused every stage, so a VU whose context stayed cancelled
// after its first Stop would return instantly on reuse and the fleet would
// silently under-generate load in every stage after the first.
func TestVirtualUsersAreReusedAcrossStages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	const stageDur = 600 * time.Millisecond
	plan := &models.TestPlan{
		ID: "reuse", Name: "reuse",
		Stages: []models.Stage{
			{Duration: stageDur, TargetVUs: 1}, // uses VU 0, then stops it
			{Duration: stageDur, TargetVUs: 3}, // must reuse VU 0 to reach 3
			{Duration: stageDur, TargetVUs: 2},
		},
		HTTP: models.HTTPConfig{
			BaseURL:  srv.URL,
			Requests: []models.Request{{Method: http.MethodGet, Path: "/"}},
			Timeout:  5 * time.Second,
		},
	}

	engine := NewEngine("reuse-worker", 4, nil, zap.NewNop())
	defer engine.Shutdown()

	done := make(chan error, 1)
	go func() { done <- engine.RunTest(context.Background(), "reuse", plan, 3) }()

	var peak int32
	deadline := time.Now().Add(3*stageDur + time.Second)
	for time.Now().Before(deadline) {
		if v := engine.GetActiveVUs(); v > peak {
			peak = v
		}
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("RunTest: %v", err)
			}
			deadline = time.Now()
		case <-time.After(20 * time.Millisecond):
		}
	}

	if peak < 3 {
		t.Errorf("peak active VUs = %d, want 3: virtual users stopped in stage 0 did not run again", peak)
	}
}

// StopTest must abort the whole run, not just the stage in flight.
func TestStopTestAbortsRemainingStages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	plan := &models.TestPlan{
		ID: "stop", Name: "stop",
		Stages: []models.Stage{
			{Duration: 2 * time.Second, TargetVUs: 2},
			{Duration: 2 * time.Second, TargetVUs: 2},
			{Duration: 2 * time.Second, TargetVUs: 2},
		},
		HTTP: models.HTTPConfig{
			BaseURL:  srv.URL,
			Requests: []models.Request{{Method: http.MethodGet, Path: "/"}},
			Timeout:  5 * time.Second,
		},
	}

	engine := NewEngine("stop-worker", 4, nil, zap.NewNop())
	defer engine.Shutdown()

	done := make(chan error, 1)
	start := time.Now()
	go func() { done <- engine.RunTest(context.Background(), "stop", plan, 2) }()

	time.Sleep(300 * time.Millisecond)
	if err := engine.StopTest(); err != nil {
		t.Fatalf("StopTest: %v", err)
	}

	select {
	case <-done:
		if elapsed := time.Since(start); elapsed > 3*time.Second {
			t.Errorf("run took %v after stop; remaining stages were not aborted", elapsed)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("RunTest did not return after StopTest: the run continued through later stages")
	}
}
