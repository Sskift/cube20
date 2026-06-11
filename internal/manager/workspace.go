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
		if ms.ClientID == clientID {
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

// WorkspaceMembershipView pairs a workspace with the requesting client's role in
// it — what /api/workspaces and /api/me need to surface.
type WorkspaceMembershipView struct {
	Workspace
	Role WorkspaceRole `json:"role"`
}

// SetMembership adds or updates a client's role in a workspace (upsert).
func (m *Manager) SetMembership(workspaceID, clientID string, role WorkspaceRole) error {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()

	workspaceID = strings.TrimSpace(workspaceID)
	clientID = strings.TrimSpace(clientID)
	if workspaceID == "" || clientID == "" {
		return fmt.Errorf("workspace id and client id are required")
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
	if !clientExists(state, clientID) {
		return fmt.Errorf("client %q not found", clientID)
	}
	for i := range state.Memberships {
		if state.Memberships[i].WorkspaceID == workspaceID && state.Memberships[i].ClientID == clientID {
			state.Memberships[i].Role = role
			return m.Save(state)
		}
	}
	state.Memberships = append(state.Memberships, Membership{
		WorkspaceID: workspaceID,
		ClientID:    clientID,
		Role:        role,
		CreatedAt:   time.Now(),
	})
	return m.Save(state)
}

// RemoveMembership revokes a client's access to a workspace.
func (m *Manager) RemoveMembership(workspaceID, clientID string) error {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()

	state, err := m.Load()
	if err != nil {
		return err
	}
	filtered := state.Memberships[:0]
	removed := false
	for _, ms := range state.Memberships {
		if ms.WorkspaceID == workspaceID && ms.ClientID == clientID {
			removed = true
			continue
		}
		filtered = append(filtered, ms)
	}
	if !removed {
		return fmt.Errorf("client %q is not a member of workspace %q", clientID, workspaceID)
	}
	state.Memberships = filtered
	// On Postgres the generic Save is upsert-only and never deletes rows, so the
	// revoked membership must be removed with a targeted delete (mirroring
	// DeleteAccount). On the file backend, Save rewrites the whole document, so
	// writing the filtered slice is sufficient.
	if strings.TrimSpace(m.DatabaseURL) != "" {
		if err := m.deletePostgresMembership(workspaceID, clientID); err != nil {
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

// MembershipRole returns a client's role in a workspace and whether a membership
// exists. This is the primitive the web layer's authorization uses.
func (m *Manager) MembershipRole(workspaceID, clientID string) (WorkspaceRole, bool) {
	state, err := m.Load()
	if err != nil {
		return "", false
	}
	for _, ms := range state.Memberships {
		if ms.WorkspaceID == workspaceID && ms.ClientID == clientID {
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
