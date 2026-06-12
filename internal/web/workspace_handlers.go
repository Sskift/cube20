package web

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"cube20/internal/manager"
)

// requireAccountReportAccess authorizes a client PAT to write a quota/usage
// report for an account: the platform admin token passes unconditionally; a
// client PAT passes only if it is a member of the account's workspace. Unknown
// accounts are allowed through so the underlying manager keeps its own
// not-found/no-op behavior unchanged. Returns true when authorized; on failure
// it writes the response and returns false.
func (s *Server) requireAccountReportAccess(w http.ResponseWriter, auth requestAuth, accountID string) bool {
	if auth.Admin {
		return true
	}
	workspaceID, ok := s.Manager.AccountWorkspace(accountID)
	if !ok {
		// Unknown account: defer to the manager (no-op / not-found) rather than
		// leaking account existence through a distinct error here.
		return true
	}
	if _, ok := s.Manager.MembershipRole(workspaceID, auth.principal()); !ok {
		writeError(w, http.StatusForbidden, "account belongs to a workspace you are not a member of")
		return false
	}
	return true
}

// requireWorkspaceAdmin authorizes a workspace-management action: the platform
// admin token passes unconditionally; otherwise the caller (session user or
// bearer device) must hold an admin-role membership in the target workspace.
// Returns true when authorized; on failure it writes the response and returns
// false.
func (s *Server) requireWorkspaceAdmin(w http.ResponseWriter, auth requestAuth, workspaceID string) bool {
	if auth.Admin {
		return true
	}
	role, ok := s.Manager.MembershipRole(workspaceID, auth.principal())
	if !ok {
		writeError(w, http.StatusForbidden, "not a member of this workspace")
		return false
	}
	if role != manager.RoleAdmin {
		writeError(w, http.StatusForbidden, "workspace admin role required")
		return false
	}
	return true
}

// handleWorkspaces serves GET (list the caller's workspaces) and POST (create a
// workspace). Any logged-in user may create a workspace and becomes its admin.
func (s *Server) handleWorkspaces(w http.ResponseWriter, r *http.Request) {
	auth := authFromRequest(r)
	switch r.Method {
	case http.MethodGet:
		if auth.Admin {
			list, err := s.Manager.ListWorkspaces()
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"workspaces": list})
			return
		}
		views, err := s.Manager.ListWorkspacesForClient(auth.principal())
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"workspaces": views})
	case http.MethodPost:
		// Any logged-in user (session) or the platform admin may create a
		// workspace. A bearer-device caller without a user identity cannot.
		if !auth.Admin && auth.UserID == "" {
			writeError(w, http.StatusForbidden, "log in to create a workspace")
			return
		}
		var body struct {
			Name string `json:"name"`
		}
		if r.Body != nil {
			_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
		}
		creator := auth.UserID
		if creator == "" {
			creator = "admin"
		}
		ws, err := s.Manager.CreateWorkspace(body.Name, creator)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		// The creator becomes the workspace's first admin (skip for the
		// cloud-token admin, which has no user identity).
		if auth.UserID != "" {
			if err := s.Manager.SetMembership(ws.ID, auth.UserID, manager.RoleAdmin); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
		}
		writeJSON(w, http.StatusCreated, ws)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleWorkspaceAction routes /api/workspaces/{id}/members,
// /api/workspaces/{id}/invites, and their child resources.
func (s *Server) handleWorkspaceAction(w http.ResponseWriter, r *http.Request) {
	auth := authFromRequest(r)
	path := strings.TrimPrefix(r.URL.Path, "/api/workspaces/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	// Expected shapes:
	//   {id}/members             -> GET list, POST add/set role
	//   {id}/members/{clientId}  -> DELETE remove
	//   {id}/invites             -> GET list, POST create invite
	//   {id}/invites/{inviteId}  -> DELETE revoke invite
	if len(parts) < 2 || parts[0] == "" || parts[1] != "members" {
		if len(parts) >= 2 && parts[0] != "" && parts[1] == "invites" {
			s.handleWorkspaceInvites(w, r, auth, parts)
			return
		}
		http.NotFound(w, r)
		return
	}
	workspaceID := parts[0]

	if len(parts) == 2 {
		switch r.Method {
		case http.MethodGet:
			if !s.requireWorkspaceAdmin(w, auth, workspaceID) {
				return
			}
			members, err := s.Manager.ListMemberships(workspaceID)
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"members": members})
		case http.MethodPost:
			if !s.requireWorkspaceAdmin(w, auth, workspaceID) {
				return
			}
			var body struct {
				UserID   string `json:"userId"`
				ClientID string `json:"clientId"`
				Role     string `json:"role"`
			}
			if r.Body != nil {
				_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
			}
			// Prefer the user id (current model); fall back to clientId for
			// legacy callers. SetMembership resolves either to a principal.
			principal := strings.TrimSpace(body.UserID)
			if principal == "" {
				principal = strings.TrimSpace(body.ClientID)
			}
			role := manager.WorkspaceRole(strings.TrimSpace(body.Role))
			if role == "" {
				role = manager.RoleMember
			}
			if err := s.Manager.SetMembership(workspaceID, principal, role); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"workspaceId": workspaceID,
				"userId":      principal,
				"role":        string(role),
			})
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}

	if len(parts) == 3 && r.Method == http.MethodDelete {
		if !s.requireWorkspaceAdmin(w, auth, workspaceID) {
			return
		}
		clientID := parts[2]
		if err := s.Manager.RemoveMembership(workspaceID, clientID); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"removed": true, "clientId": clientID})
		return
	}

	http.NotFound(w, r)
}

func (s *Server) handleWorkspaceInvites(w http.ResponseWriter, r *http.Request, auth requestAuth, parts []string) {
	workspaceID := parts[0]
	if len(parts) == 2 {
		switch r.Method {
		case http.MethodGet:
			if !s.requireWorkspaceAdmin(w, auth, workspaceID) {
				return
			}
			invites, err := s.Manager.ListWorkspaceInvites(workspaceID)
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"invites": invites})
		case http.MethodPost:
			if !s.requireWorkspaceAdmin(w, auth, workspaceID) {
				return
			}
			var body struct {
				Role           string `json:"role"`
				ExpiresInHours int    `json:"expiresInHours"`
			}
			if r.Body != nil {
				_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
			}
			role := manager.WorkspaceRole(strings.TrimSpace(body.Role))
			if role == "" {
				role = manager.RoleMember
			}
			ttl := time.Duration(body.ExpiresInHours) * time.Hour
			createdBy := auth.UserID
			if createdBy == "" {
				createdBy = "admin"
			}
			created, err := s.Manager.CreateWorkspaceInvite(workspaceID, createdBy, role, ttl)
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			created.URL = inviteURL(r, created.Token)
			writeJSON(w, http.StatusCreated, created)
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}
	if len(parts) == 3 && r.Method == http.MethodDelete {
		if !s.requireWorkspaceAdmin(w, auth, workspaceID) {
			return
		}
		inviteID := strings.TrimSpace(parts[2])
		if err := s.Manager.RevokeWorkspaceInvite(workspaceID, inviteID); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"revoked": true, "id": inviteID})
		return
	}
	http.NotFound(w, r)
}

func inviteURL(r *http.Request, token string) string {
	proto := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))
	if proto == "" {
		if r.TLS != nil {
			proto = "https"
		} else {
			proto = "http"
		}
	}
	host := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = r.Host
	}
	if host == "" {
		return "/invite/" + token
	}
	// Guard against invalid forwarded proto values producing surprising links.
	if _, err := strconv.Atoi(proto); err == nil {
		proto = "http"
	}
	return proto + "://" + host + "/invite/" + token
}
