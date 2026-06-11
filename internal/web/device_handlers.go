package web

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// handleDevices serves the session-authed device surface: GET lists the caller's
// devices (admin may pass ?userId=), POST mints a new device token.
func (s *Server) handleDevices(w http.ResponseWriter, r *http.Request) {
	auth := authFromRequest(r)
	switch r.Method {
	case http.MethodGet:
		userID := auth.UserID
		if q := strings.TrimSpace(r.URL.Query().Get("userId")); q != "" {
			if !s.userIsPlatformAdmin(auth) && q != auth.UserID {
				writeError(w, http.StatusForbidden, "cannot list another user's devices")
				return
			}
			userID = q
		}
		if userID == "" && s.userIsPlatformAdmin(auth) {
			userID = "" // admin with no filter: all devices
		} else if userID == "" {
			writeError(w, http.StatusUnauthorized, "login required")
			return
		}
		devices, err := s.Manager.ListDevices(userID)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"devices": devices})
	case http.MethodPost:
		if auth.UserID == "" {
			writeError(w, http.StatusUnauthorized, "login required to mint a device")
			return
		}
		var body struct {
			Label string `json:"label"`
		}
		if r.Body != nil {
			_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
		}
		device, token, err := s.Manager.CreateDevice(auth.UserID, body.Label)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"device": device, "token": token})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleDeviceAction serves DELETE /api/devices/{id} (revoke). A user may revoke
// their own device; a platform admin may revoke any.
func (s *Server) handleDeviceAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/devices/"), "/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing device id")
		return
	}
	auth := authFromRequest(r)
	owner, ok := s.Manager.DeviceOwner(id)
	if !ok {
		writeError(w, http.StatusNotFound, "device not found")
		return
	}
	if !s.userIsPlatformAdmin(auth) && owner != auth.UserID {
		writeError(w, http.StatusForbidden, "cannot revoke another user's device")
		return
	}
	if err := s.Manager.RevokeDevice(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"revoked": true})
}

// handleUsers serves GET /api/users (admin roster).
func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	auth := authFromRequest(r)
	if !s.userIsPlatformAdmin(auth) {
		writeError(w, http.StatusForbidden, "admin only")
		return
	}
	users, err := s.Manager.ListUsers()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

// handleUserAction serves PATCH /api/users/{id}/status (admin enable/disable).
func (s *Server) handleUserAction(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/users/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] != "status" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPatch {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	auth := authFromRequest(r)
	if !s.userIsPlatformAdmin(auth) {
		writeError(w, http.StatusForbidden, "admin only")
		return
	}
	var body struct {
		Disabled bool `json:"disabled"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
	}
	if err := s.Manager.SetUserDisabled(parts[0], body.Disabled); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"disabled": body.Disabled})
}
