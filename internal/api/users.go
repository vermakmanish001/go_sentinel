package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"go.uber.org/zap"

	"github.com/vermakmanish001/go_sentinel/internal/store"
)

// minPasswordLength matches the --create-user check, so an account cannot be
// weaker for having been made in the browser.
const minPasswordLength = 8

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.store.ListUsers(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

// handleCreateUser adds an account. There is no open signup: only someone who
// already has access can grant it, because an account here can point the whole
// fleet at any allowed target.
func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	username := strings.TrimSpace(req.Username)
	if username == "" {
		writeError(w, http.StatusUnprocessableEntity, "username is required")
		return
	}
	if len(req.Password) < minPasswordLength {
		writeError(w, http.StatusUnprocessableEntity,
			"password must be at least "+strconv.Itoa(minPasswordLength)+" characters")
		return
	}

	if _, err := s.store.GetUserByName(r.Context(), username); err == nil {
		writeError(w, http.StatusConflict, "that username is taken")
		return
	}

	hash, err := HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not hash password")
		return
	}

	user, err := s.store.CreateUser(r.Context(), username, hash)
	if err != nil {
		// The unique index is the real arbiter; the check above only makes the
		// common case a friendlier message.
		writeError(w, http.StatusConflict, "could not create user: "+err.Error())
		return
	}

	s.logger.Info("user created", zap.String("username", user.Username))
	writeJSON(w, http.StatusCreated, user)
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return
	}

	// Deleting yourself would sign you out mid-action; deleting the last account
	// would leave the instance unreachable, and the server refuses to start with
	// auth enabled and no users. Both are worth blocking outright.
	if current, ok := UserFrom(r.Context()); ok && current.ID == id {
		writeError(w, http.StatusConflict, "you cannot delete your own account")
		return
	}
	if n, err := s.store.CountUsers(r.Context()); err == nil && n <= 1 {
		writeError(w, http.StatusConflict,
			"cannot delete the last account: the server will not start without one")
		return
	}

	if err := s.store.DeleteUser(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
