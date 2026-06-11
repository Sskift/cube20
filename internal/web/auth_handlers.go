package web

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

// sessionCookieName is the website session cookie. The cube CLI never uses it
// (it sends a bearer token); the cookie path is browser-only, which keeps the
// machine-to-machine sync API entirely off the cookie/CSRF surface.
const sessionCookieName = "cube_session"

// withSessionAuth guards website routes with the session cookie. The admin
// cloud-token still works (so an admin can hit these from a token too), then the
// cookie is resolved to a user. On failure it writes 401.
func (s *Server) withSessionAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if auth, ok := s.adminAuthorized(r); ok {
			next(w, r.WithContext(context.WithValue(r.Context(), authContextKey{}, auth)))
			return
		}
		cookie, err := r.Cookie(sessionCookieName)
		if err == nil && cookie.Value != "" {
			if user, sessionID, ok := s.Manager.ResolveSession(cookie.Value); ok {
				auth := requestAuth{UserID: user.ID, SessionID: sessionID}
				next(w, r.WithContext(context.WithValue(r.Context(), authContextKey{}, auth)))
				return
			}
		}
		writeError(w, http.StatusUnauthorized, "login required")
	}
}

// loginLimiter is a tiny per-key token bucket guarding register/login from
// brute force. Fail-closed: if the bucket is empty the attempt is rejected
// before the expensive password hash runs.
type loginLimiter struct {
	mu     sync.Mutex
	hits   map[string][]time.Time
	max    int
	window time.Duration
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{hits: map[string][]time.Time{}, max: 8, window: time.Minute}
}

func (l *loginLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-l.window)
	kept := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.max {
		l.hits[key] = kept
		return false
	}
	l.hits[key] = append(kept, now)
	return true
}

func clientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func (s *Server) setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		// Secure is intentionally NOT set: the live host serves plain HTTP for a
		// small internal audience. Tighten to Secure once TLS is in front.
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int((30 * 24 * time.Hour).Seconds()),
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func (s *Server) handleAuthRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
	}
	if !s.limiterGet().allow("reg:" + clientIP(r)) {
		writeError(w, http.StatusTooManyRequests, "too many attempts, slow down")
		return
	}
	user, err := s.Manager.CreateUser(body.Username, body.Password)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	token, err := s.Manager.CreateSession(user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.setSessionCookie(w, token)
	writeJSON(w, http.StatusCreated, map[string]any{"user": user})
}

func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
	}
	if !s.limiterGet().allow("login:" + clientIP(r)) {
		writeError(w, http.StatusTooManyRequests, "too many attempts, slow down")
		return
	}
	user, ok := s.Manager.AuthenticateUser(body.Username, body.Password)
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	token, err := s.Manager.CreateSession(user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.setSessionCookie(w, token)
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	auth := authFromRequest(r)
	if auth.SessionID != "" {
		_ = s.Manager.DeleteSession(auth.SessionID)
	}
	s.clearSessionCookie(w)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleAuthMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	auth := authFromRequest(r)
	if auth.UserID == "" {
		// Admin-token holder has no user identity; report admin shape.
		if auth.Admin {
			writeJSON(w, http.StatusOK, map[string]any{"admin": true})
			return
		}
		writeError(w, http.StatusUnauthorized, "login required")
		return
	}
	user, ok := s.Manager.GetUser(auth.UserID)
	if !ok {
		writeError(w, http.StatusUnauthorized, "user not found")
		return
	}
	devices, _ := s.Manager.ListDevices(auth.UserID)
	workspaces, _ := s.Manager.ListWorkspacesForClient(auth.UserID)
	writeJSON(w, http.StatusOK, map[string]any{
		"user":       user,
		"devices":    devices,
		"workspaces": workspaces,
		"admin":      s.userIsPlatformAdmin(auth),
	})
}

// userIsPlatformAdmin reports whether the caller may use platform-wide admin
// surfaces (all users, all devices, full cross-tenant audit). This is the
// cloud-token holder ONLY. A workspace-admin role is deliberately NOT platform
// admin — that would let an admin of one workspace read every other tenant's
// users/devices/audit. Workspace-scoped management stays gated per-workspace by
// requireWorkspaceAdmin in workspace_handlers.go.
func (s *Server) userIsPlatformAdmin(auth requestAuth) bool {
	return auth.Admin
}
