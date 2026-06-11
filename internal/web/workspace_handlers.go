package web

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

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
	if _, ok := s.Manager.MembershipRole(workspaceID, auth.ClientID); !ok {
		writeError(w, http.StatusForbidden, "account belongs to a workspace you are not a member of")
		return false
	}
	return true
}

// requireWorkspaceAdmin authorizes a workspace-management action: the platform
// admin token passes unconditionally; a client PAT passes only if it holds an
// admin-role membership in the target workspace. Returns true when authorized;
// on failure it writes the response and returns false.
func (s *Server) requireWorkspaceAdmin(w http.ResponseWriter, auth requestAuth, workspaceID string) bool {
	if auth.Admin {
		return true
	}
	role, ok := s.Manager.MembershipRole(workspaceID, auth.ClientID)
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
// workspace — platform admin only).
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
		views, err := s.Manager.ListWorkspacesForClient(auth.ClientID)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"workspaces": views})
	case http.MethodPost:
		if !auth.Admin {
			writeError(w, http.StatusForbidden, "creating a workspace requires the platform admin token")
			return
		}
		var body struct {
			Name string `json:"name"`
		}
		if r.Body != nil {
			_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
		}
		ws, err := s.Manager.CreateWorkspace(body.Name, "admin")
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, ws)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleWorkspaceAction routes /api/workspaces/{id}/members and
// /api/workspaces/{id}/members/{clientId}.
func (s *Server) handleWorkspaceAction(w http.ResponseWriter, r *http.Request) {
	auth := authFromRequest(r)
	path := strings.TrimPrefix(r.URL.Path, "/api/workspaces/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	// Expected shapes:
	//   {id}/members             -> GET list, POST add/set role
	//   {id}/members/{clientId}  -> DELETE remove
	if len(parts) < 2 || parts[0] == "" || parts[1] != "members" {
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
				ClientID string `json:"clientId"`
				Role     string `json:"role"`
			}
			if r.Body != nil {
				_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
			}
			role := manager.WorkspaceRole(strings.TrimSpace(body.Role))
			if role == "" {
				role = manager.RoleMember
			}
			if err := s.Manager.SetMembership(workspaceID, strings.TrimSpace(body.ClientID), role); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"workspaceId": workspaceID,
				"clientId":    strings.TrimSpace(body.ClientID),
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
