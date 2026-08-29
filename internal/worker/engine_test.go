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
