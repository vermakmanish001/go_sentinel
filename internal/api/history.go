package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/vermakmanish001/go_sentinel/internal/store"
)

// ---------- run history ----------

func (s *Server) handleListRuns(w http.ResponseWriter, r *http.Request) {
	limit := intParam(r, "limit", 50)
	offset := intParam(r, "offset", 0)

	runs, err := s.store.ListRuns(r.Context(), limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

func (s *Server) handleDeleteRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	s.mu.Lock()
	active := s.activeRunID == id
	s.mu.Unlock()
	if active {
		writeError(w, http.StatusConflict, "run is still in flight; stop it first")
		return
	}

	if err := s.store.DeleteRun(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "run not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRunSeries returns a run's stored per-second samples, for charting.
func (s *Server) handleRunSeries(w http.ResponseWriter, r *http.Request) {
	samples, err := s.store.ListSamples(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if samples == nil {
		samples = []store.Sample{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"samples": samples})
}

// ---------- saved plans ----------

type planPayload struct {
	Name string          `json:"name"`
	Spec json.RawMessage `json:"spec"`
}

func (s *Server) handleListPlans(w http.ResponseWriter, r *http.Request) {
	plans, err := s.store.ListPlans(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"plans": plans})
}

func (s *Server) handleCreatePlan(w http.ResponseWriter, r *http.Request) {
	payload, ok := decodePlan(w, r)
	if !ok {
		return
	}

	plan, err := s.store.CreatePlan(r.Context(), payload.Name, string(payload.Spec))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, plan)
}

func (s *Server) handleUpdatePlan(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid plan id")
		return
	}
	payload, ok := decodePlan(w, r)
	if !ok {
		return
	}

	plan, err := s.store.UpdatePlan(r.Context(), id, payload.Name, string(payload.Spec))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "plan not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

func (s *Server) handleDeletePlan(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid plan id")
		return
	}
	if err := s.store.DeletePlan(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "plan not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// decodePlan reads and sanity-checks a saved-plan payload. The spec is stored
// verbatim so the form can round-trip fields the runner does not care about.
func decodePlan(w http.ResponseWriter, r *http.Request) (planPayload, bool) {
	var p planPayload
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return p, false
	}
	if p.Name == "" {
		writeError(w, http.StatusUnprocessableEntity, "name is required")
		return p, false
	}
	if len(p.Spec) == 0 {
		writeError(w, http.StatusUnprocessableEntity, "spec is required")
		return p, false
	}
	return p, true
}

func intParam(r *http.Request, key string, fallback int) int {
	if raw := r.URL.Query().Get(key); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			return n
		}
	}
	return fallback
}
