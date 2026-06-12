package web

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

func (s *Server) handleInviteAction(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/invites/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	token := parts[0]

	if len(parts) == 1 {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		preview, err := s.Manager.InvitePreview(token)
		if err != nil {
			writeInviteError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, preview)
		return
	}
	if len(parts) != 2 || r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}

	switch parts[1] {
	case "register":
		s.handleInviteRegister(w, r, token)
	case "join":
		s.handleInviteJoin(w, r, token)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleInviteRegister(w http.ResponseWriter, r *http.Request, token string) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
	}
	if !s.limiterGet().allow("invite-reg:" + clientIP(r)) {
		writeError(w, http.StatusTooManyRequests, "too many attempts, slow down")
		return
	}
	user, sessionToken, workspaces, err := s.Manager.RegisterWithInvite(token, body.Username, body.Password)
	if err != nil {
		writeInviteError(w, err)
		return
	}
	s.setSessionCookie(w, sessionToken)
	writeJSON(w, http.StatusCreated, map[string]any{
		"mode":       "user",
		"admin":      false,
		"user":       user,
		"devices":    []any{},
		"workspaces": workspaces,
	})
}

func (s *Server) handleInviteJoin(w http.ResponseWriter, r *http.Request, token string) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		writeError(w, http.StatusUnauthorized, "login required")
		return
	}
	user, _, ok := s.Manager.ResolveSession(cookie.Value)
	if !ok {
		writeError(w, http.StatusUnauthorized, "login required")
		return
	}
	workspaces, err := s.Manager.JoinWithInvite(token, user.ID)
	if err != nil {
		writeInviteError(w, err)
		return
	}
	devices, _ := s.Manager.ListDevices(user.ID)
	writeJSON(w, http.StatusOK, map[string]any{
		"mode":       "user",
		"admin":      false,
		"user":       user,
		"devices":    devices,
		"workspaces": workspaces,
	})
}

func writeInviteError(w http.ResponseWriter, err error) {
	message := err.Error()
	status := http.StatusBadRequest
	switch {
	case strings.Contains(message, "revoked"), strings.Contains(message, "expired"):
		status = http.StatusGone
	case strings.Contains(message, "not found"):
		status = http.StatusNotFound
	}
	writeError(w, status, message)
}
