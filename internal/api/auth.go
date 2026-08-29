package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/vermakmanish001/go_sentinel/internal/store"
)

const sessionCookie = "gosentinel_session"

// Argon2id parameters. Deliberately above the RFC 9106 second recommended
// option so a stolen database is expensive to attack offline.
const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // KiB
	argonThreads = 4
	argonKeyLen  = 32
	argonSaltLen = 16
)

type ctxKey int

const userKey ctxKey = iota

// UserFrom returns the authenticated user, if the request carried one.
func UserFrom(ctx context.Context) (store.User, bool) {
	u, ok := ctx.Value(userKey).(store.User)
	return u, ok
}

// HashPassword returns an encoded Argon2id hash, salt included.
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword reports whether password matches the encoded hash. The
// comparison is constant-time so a wrong password cannot be narrowed by timing.
func VerifyPassword(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}

	var memory, iterations uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &iterations, &threads); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}

	got := argon2.IDKey([]byte(password), salt, iterations, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// hashToken hashes a session token for storage. Sessions are looked up by this
// hash so the database never holds a usable cookie value.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func newToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// requireAuth wraps a handler so it only runs for an authenticated request.
// When auth is disabled the wrapper is transparent.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.auth.Enabled {
			next(w, r)
			return
		}

		cookie, err := r.Cookie(sessionCookie)
		if err != nil || cookie.Value == "" {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}

		user, err := s.store.UserForSession(r.Context(), hashToken(cookie.Value), time.Now().UnixMilli())
		if err != nil {
			// Clear the cookie so a stale session does not loop the dashboard.
			http.SetCookie(w, s.sessionCookieValue("", -1))
			writeError(w, http.StatusUnauthorized, "session expired")
			return
		}

		next(w, r.WithContext(context.WithValue(r.Context(), userKey, user)))
	}
}

func (s *Server) sessionCookieValue(token string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		// Secure is set only behind TLS: a Secure cookie is silently dropped
		// over plain HTTP, which would make local development fail confusingly.
		Secure: s.auth.SecureCookies,
		MaxAge: maxAge,
	}
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	user, err := s.store.GetUserByName(r.Context(), strings.TrimSpace(req.Username))
	if err != nil {
		// Hash anyway so a missing user and a wrong password take similar time.
		_ = VerifyPassword(req.Password, "$argon2id$v=19$m=65536,t=3,p=4$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	if !VerifyPassword(req.Password, user.PasswordHash) {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	token, err := newToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create session")
		return
	}

	expires := time.Now().Add(s.auth.SessionTTL)
	if err := s.store.CreateSession(r.Context(), hashToken(token), user.ID, expires.UnixMilli()); err != nil {
		writeError(w, http.StatusInternalServerError, "could not create session")
		return
	}

	http.SetCookie(w, s.sessionCookieValue(token, int(s.auth.SessionTTL.Seconds())))
	writeJSON(w, http.StatusOK, map[string]any{"username": user.Username})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookie); err == nil && cookie.Value != "" {
		_ = s.store.DeleteSession(r.Context(), hashToken(cookie.Value))
	}
	http.SetCookie(w, s.sessionCookieValue("", -1))
	w.WriteHeader(http.StatusNoContent)
}

// handleMe lets the dashboard decide whether to show the login screen.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	if !s.auth.Enabled {
		writeJSON(w, http.StatusOK, map[string]any{"auth_required": false})
		return
	}

	cookie, err := r.Cookie(sessionCookie)
	if err != nil || cookie.Value == "" {
		writeJSON(w, http.StatusOK, map[string]any{"auth_required": true})
		return
	}

	user, err := s.store.UserForSession(r.Context(), hashToken(cookie.Value), time.Now().UnixMilli())
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"auth_required": true})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"auth_required": true, "username": user.Username})
}

// BootstrapUser creates the first account when the database has none, so a
// fresh deployment is reachable without a manual step.
func BootstrapUser(ctx context.Context, st store.Store, username, password string) (bool, error) {
	n, err := st.CountUsers(ctx)
	if err != nil {
		return false, err
	}
	if n > 0 {
		return false, nil
	}
	if username == "" || password == "" {
		return false, errors.New("no users exist and no bootstrap credentials were provided")
	}

	hash, err := HashPassword(password)
	if err != nil {
		return false, err
	}
	if _, err := st.CreateUser(ctx, username, hash); err != nil {
		return false, err
	}
	return true, nil
}
