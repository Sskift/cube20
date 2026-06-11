package manager

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// CreateWorkspace adds a new isolated account pool. The id is derived from the
// name; createdBy records the platform operator who provisioned it.
func (m *Manager) CreateWorkspace(name, createdBy string) (Workspace, error) {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()

	name = strings.TrimSpace(name)
	if name == "" {
		return Workspace{}, fmt.Errorf("workspace name is required")
	}
	state, err := m.Load()
	if err != nil {
		return Workspace{}, err
	}
	used := map[string]bool{}
	for _, ws := range state.Workspaces {
		used[ws.ID] = true
	}
	id := uniqueFromUsed(name, used)
	if !strings.HasPrefix(id, "ws-") {
		id = uniqueFromUsed("ws-"+id, used)
	}
	now := time.Now()
	ws := Workspace{
		ID:        id,
		Name:      name,
		CreatedBy: strings.TrimSpace(createdBy),
		CreatedAt: now,
		UpdatedAt: now,
	}
	state.Workspaces = append(state.Workspaces, ws)
	if err := m.Save(state); err != nil {
		return Workspace{}, err
	}
	return ws, nil
}

// ListWorkspaces returns all workspaces, sorted by id.
func (m *Manager) ListWorkspaces() ([]Workspace, error) {
	state, err := m.Load()
	if err != nil {
		return nil, err
	}
	out := make([]Workspace, len(state.Workspaces))
	copy(out, state.Workspaces)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// ListWorkspacesForClient returns only the workspaces a given client belongs to,
// each annotated with that client's role.
func (m *Manager) ListWorkspacesForClient(clientID string) ([]WorkspaceMembershipView, error) {
	state, err := m.Load()
	if err != nil {
		return nil, err
	}
	roleByWS := map[string]WorkspaceRole{}
	for _, ms := range state.Memberships {
		if membershipMatches(ms, clientID) {
			roleByWS[ms.WorkspaceID] = ms.Role
		}
	}
	var out []WorkspaceMembershipView
	for _, ws := range state.Workspaces {
		if role, ok := roleByWS[ws.ID]; ok {
			out = append(out, WorkspaceMembershipView{Workspace: ws, Role: role})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// membershipMatches reports whether a membership belongs to the given principal,
// which may be a user id (new model) or a device/client id (legacy). Memberships
// carry both fields during the transition, so we match either.
func membershipMatches(ms Membership, principal string) bool {
	if principal == "" {
		return false
	}
	return ms.UserID == principal || ms.ClientID == principal
}

// WorkspaceMembershipView pairs a workspace with the requesting client's role in
// it — what /api/workspaces and /api/me need to surface.
type WorkspaceMembershipView struct {
	Workspace
	Role WorkspaceRole `json:"role"`
}

// SetMembership adds or updates a principal's role in a workspace (upsert). The
// principal may be a user id (preferred) or a legacy device/client id; the right
// field is populated based on which entity it names.
func (m *Manager) SetMembership(workspaceID, principal string, role WorkspaceRole) error {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()

	workspaceID = strings.TrimSpace(workspaceID)
	principal = strings.TrimSpace(principal)
	if workspaceID == "" || principal == "" {
		return fmt.Errorf("workspace id and principal id are required")
	}
	if !validWorkspaceRole(role) {
		return fmt.Errorf("role must be admin or member")
	}
	state, err := m.Load()
	if err != nil {
		return err
	}
	if !workspaceExists(state, workspaceID) {
		return fmt.Errorf("workspace %q not found", workspaceID)
	}
	isUser := userExists(state, principal)
	if !isUser && !clientExists(state, principal) {
		return fmt.Errorf("principal %q not found", principal)
	}
	for i := range state.Memberships {
		if state.Memberships[i].WorkspaceID == workspaceID && membershipMatches(state.Memberships[i], principal) {
			state.Memberships[i].Role = role
			if isUser && state.Memberships[i].UserID == "" {
				state.Memberships[i].UserID = principal
			}
			return m.Save(state)
		}
	}
	ms := Membership{
		WorkspaceID: workspaceID,
		Role:        role,
		CreatedAt:   time.Now(),
	}
	if isUser {
		ms.UserID = principal
	} else {
		ms.ClientID = principal
	}
	state.Memberships = append(state.Memberships, ms)
	return m.Save(state)
}

// RemoveMembership revokes a principal's access to a workspace.
func (m *Manager) RemoveMembership(workspaceID, principal string) error {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()

	state, err := m.Load()
	if err != nil {
		return err
	}
	filtered := state.Memberships[:0]
	removed := false
	var removedKey string
	for _, ms := range state.Memberships {
		if ms.WorkspaceID == workspaceID && membershipMatches(ms, principal) {
			removed = true
			// The PG PK is (workspace_id, client_id) where client_id holds the
			// principal key (user id when no legacy client). Capture it for the
			// targeted delete below.
			removedKey = ms.ClientID
			if removedKey == "" {
				removedKey = ms.UserID
			}
			continue
		}
		filtered = append(filtered, ms)
	}
	if !removed {
		return fmt.Errorf("%q is not a member of workspace %q", principal, workspaceID)
	}
	state.Memberships = filtered
	// On Postgres the generic Save is upsert-only and never deletes rows, so the
	// revoked membership must be removed with a targeted delete (mirroring
	// DeleteAccount). On the file backend, Save rewrites the whole document, so
	// writing the filtered slice is sufficient.
	if strings.TrimSpace(m.DatabaseURL) != "" {
		if err := m.deletePostgresMembership(workspaceID, removedKey); err != nil {
			return err
		}
		return nil
	}
	return m.Save(state)
}

// ListMemberships returns the members of a workspace with their roles.
func (m *Manager) ListMemberships(workspaceID string) ([]Membership, error) {
	state, err := m.Load()
	if err != nil {
		return nil, err
	}
	var out []Membership
	for _, ms := range state.Memberships {
		if ms.WorkspaceID == workspaceID {
			out = append(out, ms)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ClientID < out[j].ClientID })
	return out, nil
}

// MembershipRole returns a principal's role in a workspace and whether a
// membership exists. This is the primitive the web layer's authorization uses.
func (m *Manager) MembershipRole(workspaceID, principal string) (WorkspaceRole, bool) {
	state, err := m.Load()
	if err != nil {
		return "", false
	}
	for _, ms := range state.Memberships {
		if ms.WorkspaceID == workspaceID && membershipMatches(ms, principal) {
			return ms.Role, true
		}
	}
	return "", false
}

func workspaceExists(state State, id string) bool {
	for _, ws := range state.Workspaces {
		if ws.ID == id {
			return true
		}
	}
	return false
}

// AccountWorkspace returns the workspace id an account belongs to, and whether
// the account exists. Used by report endpoints to enforce that a client only
// writes quota/usage for accounts in a workspace it belongs to.
func (m *Manager) AccountWorkspace(accountID string) (string, bool) {
	state, err := m.Load()
	if err != nil {
		return "", false
	}
	for _, a := range state.Accounts {
		if a.ID == accountID {
			return workspaceOrDefault(a.WorkspaceID), true
		}
	}
	return "", false
}

// ResolveWorkspaceForClient determines which workspace a client should act in.
// Resolution order: an explicit requested id (validated for membership) wins;
// otherwise, if the client belongs to exactly one workspace that one is used; if
// it belongs to several and none was requested it is ambiguous and an error is
// returned so the caller must specify. This always requires a membership row —
// the platform admin token (clientID empty) is not a member of anything and is
// not expected to claim leases, so callers that need an admin bypass must handle
// it before calling this.
func (m *Manager) ResolveWorkspaceForClient(clientID, requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	state, err := m.Load()
	if err != nil {
		return "", err
	}
	var memberOf []string
	for _, ms := range state.Memberships {
		if ms.ClientID == clientID {
			memberOf = append(memberOf, ms.WorkspaceID)
		}
	}
	if requested != "" {
		if !workspaceExists(state, requested) {
			return "", fmt.Errorf("workspace %q not found", requested)
		}
		for _, ws := range memberOf {
			if ws == requested {
				return requested, nil
			}
		}
		return "", fmt.Errorf("not a member of workspace %q", requested)
	}
	switch len(memberOf) {
	case 0:
		return "", fmt.Errorf("client belongs to no workspace; specify a workspace")
	case 1:
		return memberOf[0], nil
	default:
		return "", fmt.Errorf("client belongs to multiple workspaces; specify a workspace")
	}
}

func clientExists(state State, id string) bool {
	for _, c := range state.Clients {
		if c.ID == id {
			return true
		}
	}
	return false
}
