package manager

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

const defaultInviteTTL = 7 * 24 * time.Hour

func (m *Manager) CreateWorkspaceInvite(workspaceID, createdBy string, role WorkspaceRole, ttl time.Duration) (WorkspaceInviteCreated, error) {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()

	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		return WorkspaceInviteCreated{}, fmt.Errorf("workspace id is required")
	}
	if role == "" {
		role = RoleMember
	}
	if !validWorkspaceRole(role) {
		return WorkspaceInviteCreated{}, fmt.Errorf("role must be admin or member")
	}
	if ttl == 0 {
		ttl = defaultInviteTTL
	}
	token, err := generateInviteToken()
	if err != nil {
		return WorkspaceInviteCreated{}, err
	}

	state, err := m.Load()
	if err != nil {
		return WorkspaceInviteCreated{}, err
	}
	if !workspaceExists(state, workspaceID) {
		return WorkspaceInviteCreated{}, fmt.Errorf("workspace %q not found", workspaceID)
	}
	used := map[string]bool{}
	for _, invite := range state.Invites {
		used[invite.ID] = true
	}
	now := time.Now()
	invite := WorkspaceInvite{
		ID:          uniqueFromUsed("invite-"+workspaceID, used),
		WorkspaceID: workspaceID,
		Role:        role,
		TokenHash:   hashToken(token),
		CreatedBy:   strings.TrimSpace(createdBy),
		CreatedAt:   now,
		ExpiresAt:   now.Add(ttl),
	}
	state.Invites = append(state.Invites, invite)
	if err := m.Save(state); err != nil {
		return WorkspaceInviteCreated{}, err
	}
	return WorkspaceInviteCreated{Invite: workspaceInviteView(invite, state, now), Token: token}, nil
}

func (m *Manager) ListWorkspaceInvites(workspaceID string) ([]WorkspaceInviteView, error) {
	state, err := m.Load()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	out := []WorkspaceInviteView{}
	for _, invite := range state.Invites {
		if invite.WorkspaceID != workspaceID {
			continue
		}
		out = append(out, workspaceInviteView(invite, state, now))
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

func (m *Manager) RevokeWorkspaceInvite(workspaceID, inviteID string) error {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()

	state, err := m.Load()
	if err != nil {
		return err
	}
	now := time.Now()
	for i := range state.Invites {
		if state.Invites[i].WorkspaceID == workspaceID && state.Invites[i].ID == inviteID {
			state.Invites[i].RevokedAt = &now
			return m.Save(state)
		}
	}
	return fmt.Errorf("invite %q not found in workspace %q", inviteID, workspaceID)
}

func (m *Manager) InvitePreview(token string) (InvitePreview, error) {
	state, err := m.Load()
	if err != nil {
		return InvitePreview{}, err
	}
	now := time.Now()
	invite, _, err := findUsableInvite(state, token, now)
	if err != nil {
		return InvitePreview{}, err
	}
	ws, ok := workspaceByID(state, invite.WorkspaceID)
	if !ok {
		return InvitePreview{}, fmt.Errorf("workspace %q not found", invite.WorkspaceID)
	}
	return InvitePreview{
		Valid:         true,
		WorkspaceID:   ws.ID,
		WorkspaceName: ws.Name,
		Role:          invite.Role,
		ExpiresAt:     invite.ExpiresAt,
	}, nil
}

func (m *Manager) RegisterWithInvite(token, username, password string) (UserView, string, []WorkspaceMembershipView, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	if username == "" {
		return UserView{}, "", nil, fmt.Errorf("username is required")
	}
	if len(password) < 6 {
		return UserView{}, "", nil, fmt.Errorf("password must be at least 6 characters")
	}
	passwordHash, err := hashPassword(password)
	if err != nil {
		return UserView{}, "", nil, err
	}
	sessionToken, err := generateSessionToken()
	if err != nil {
		return UserView{}, "", nil, err
	}

	m.stateMu.Lock()
	defer m.stateMu.Unlock()

	state, err := m.Load()
	if err != nil {
		return UserView{}, "", nil, err
	}
	now := time.Now()
	invite, inviteIndex, err := findUsableInvite(state, token, now)
	if err != nil {
		return UserView{}, "", nil, err
	}
	for _, user := range state.Users {
		if strings.EqualFold(user.Username, username) {
			return UserView{}, "", nil, fmt.Errorf("username %q is taken", username)
		}
	}
	usedIDs := map[string]bool{}
	for _, user := range state.Users {
		usedIDs[user.ID] = true
	}
	user := User{
		ID:           uniqueFromUsed("user-"+username, usedIDs),
		Username:     username,
		PasswordHash: passwordHash,
		CreatedAt:    now,
		UpdatedAt:    now,
		LastLoginAt:  now,
	}
	state.Users = append(state.Users, user)
	added := addMembershipIfMissing(&state, invite.WorkspaceID, user.ID, invite.Role, now)
	if added {
		state.Invites[inviteIndex].UsedCount++
		state.Invites[inviteIndex].LastUsedAt = now
	}
	session := Session{
		ID:         uniqueSessionID(state, "session-"+user.ID),
		TokenHash:  hashToken(sessionToken),
		UserID:     user.ID,
		CreatedAt:  now,
		ExpiresAt:  now.Add(30 * 24 * time.Hour),
		LastSeenAt: now,
	}
	state.Sessions = append(state.Sessions, session)
	if err := m.Save(state); err != nil {
		return UserView{}, "", nil, err
	}
	if strings.TrimSpace(m.DatabaseURL) != "" {
		if err := m.insertPostgresSession(session); err != nil {
			return UserView{}, "", nil, err
		}
	}
	return userView(user, state), sessionToken, workspacesForPrincipalFromState(state, user.ID), nil
}

func (m *Manager) JoinWithInvite(token, userID string) ([]WorkspaceMembershipView, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return nil, fmt.Errorf("user id is required")
	}

	m.stateMu.Lock()
	defer m.stateMu.Unlock()

	state, err := m.Load()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	invite, inviteIndex, err := findUsableInvite(state, token, now)
	if err != nil {
		return nil, err
	}
	if !activeUserExists(state, userID) {
		return nil, fmt.Errorf("user %q not found", userID)
	}
	added := addMembershipIfMissing(&state, invite.WorkspaceID, userID, invite.Role, now)
	if added {
		state.Invites[inviteIndex].UsedCount++
		state.Invites[inviteIndex].LastUsedAt = now
		if err := m.Save(state); err != nil {
			return nil, err
		}
	}
	return workspacesForPrincipalFromState(state, userID), nil
}

func findUsableInvite(state State, token string, now time.Time) (WorkspaceInvite, int, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return WorkspaceInvite{}, -1, fmt.Errorf("invite token is required")
	}
	hash := hashToken(token)
	for i, invite := range state.Invites {
		if invite.TokenHash == "" || !subtleStringEqual(invite.TokenHash, hash) {
			continue
		}
		if invite.RevokedAt != nil {
			return WorkspaceInvite{}, -1, fmt.Errorf("invite has been revoked")
		}
		if !invite.ExpiresAt.IsZero() && !now.Before(invite.ExpiresAt) {
			return WorkspaceInvite{}, -1, fmt.Errorf("invite has expired")
		}
		if !workspaceExists(state, invite.WorkspaceID) {
			return WorkspaceInvite{}, -1, fmt.Errorf("workspace %q not found", invite.WorkspaceID)
		}
		return invite, i, nil
	}
	return WorkspaceInvite{}, -1, fmt.Errorf("invite not found")
}

func workspaceInviteView(invite WorkspaceInvite, state State, now time.Time) WorkspaceInviteView {
	ws, _ := workspaceByID(state, invite.WorkspaceID)
	return WorkspaceInviteView{
		ID:            invite.ID,
		WorkspaceID:   invite.WorkspaceID,
		WorkspaceName: ws.Name,
		Role:          invite.Role,
		CreatedBy:     invite.CreatedBy,
		CreatedAt:     invite.CreatedAt,
		ExpiresAt:     invite.ExpiresAt,
		RevokedAt:     invite.RevokedAt,
		UsedCount:     invite.UsedCount,
		LastUsedAt:    invite.LastUsedAt,
		Valid:         invite.RevokedAt == nil && (invite.ExpiresAt.IsZero() || now.Before(invite.ExpiresAt)),
	}
}

func workspaceByID(state State, id string) (Workspace, bool) {
	for _, ws := range state.Workspaces {
		if ws.ID == id {
			return ws, true
		}
	}
	return Workspace{}, false
}

func activeUserExists(state State, id string) bool {
	for _, user := range state.Users {
		if user.ID == id && user.DisabledAt == nil {
			return true
		}
	}
	return false
}

func addMembershipIfMissing(state *State, workspaceID, userID string, role WorkspaceRole, now time.Time) bool {
	for i := range state.Memberships {
		if state.Memberships[i].WorkspaceID == workspaceID && membershipMatches(state.Memberships[i], userID) {
			if state.Memberships[i].Role == "" {
				state.Memberships[i].Role = role
			}
			if state.Memberships[i].UserID == "" {
				state.Memberships[i].UserID = userID
			}
			return false
		}
	}
	state.Memberships = append(state.Memberships, Membership{
		WorkspaceID: workspaceID,
		UserID:      userID,
		Role:        role,
		CreatedAt:   now,
	})
	return true
}

func workspacesForPrincipalFromState(state State, principal string) []WorkspaceMembershipView {
	roleByWS := map[string]WorkspaceRole{}
	for _, ms := range state.Memberships {
		if membershipMatches(ms, principal) {
			roleByWS[ms.WorkspaceID] = ms.Role
		}
	}
	out := []WorkspaceMembershipView{}
	for _, ws := range state.Workspaces {
		if role, ok := roleByWS[ws.ID]; ok {
			out = append(out, WorkspaceMembershipView{Workspace: ws, Role: role})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func uniqueSessionID(state State, base string) string {
	used := map[string]bool{}
	for _, session := range state.Sessions {
		used[session.ID] = true
	}
	return uniqueFromUsed(base, used)
}
