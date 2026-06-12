package manager

import (
	"strings"
	"testing"
	"time"
)

func TestWorkspaceInviteCreatePreviewAndList(t *testing.T) {
	m := newTestManager(t)
	ws, err := m.CreateWorkspace("Team Invite", "owner")
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}

	created, err := m.CreateWorkspaceInvite(ws.ID, "owner", RoleMember, 7*24*time.Hour)
	if err != nil {
		t.Fatalf("CreateWorkspaceInvite: %v", err)
	}
	if created.Token == "" || !strings.HasPrefix(created.Token, "cube_inv_") {
		t.Fatalf("invite token = %q, want cube_inv_ prefix", created.Token)
	}
	if created.Invite.TokenHash != "" {
		t.Fatalf("created invite leaked token hash: %+v", created.Invite)
	}
	if created.Invite.WorkspaceID != ws.ID || created.Invite.Role != RoleMember {
		t.Fatalf("created invite = %+v, want workspace %s member", created.Invite, ws.ID)
	}

	preview, err := m.InvitePreview(created.Token)
	if err != nil {
		t.Fatalf("InvitePreview: %v", err)
	}
	if !preview.Valid || preview.WorkspaceID != ws.ID || preview.WorkspaceName != "Team Invite" || preview.Role != RoleMember {
		t.Fatalf("preview = %+v, want valid Team Invite member", preview)
	}

	list, err := m.ListWorkspaceInvites(ws.ID)
	if err != nil {
		t.Fatalf("ListWorkspaceInvites: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("invite list length = %d, want 1: %+v", len(list), list)
	}
	if list[0].ID != created.Invite.ID || list[0].TokenHash != "" {
		t.Fatalf("listed invite = %+v, want same id and no token hash", list[0])
	}
}

func TestWorkspaceInviteRegisterCreatesUserSessionAndMembership(t *testing.T) {
	m := newTestManager(t)
	ws, err := m.CreateWorkspace("Team Register", "owner")
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	created, err := m.CreateWorkspaceInvite(ws.ID, "owner", RoleMember, time.Hour)
	if err != nil {
		t.Fatalf("CreateWorkspaceInvite: %v", err)
	}

	user, sessionToken, workspaces, err := m.RegisterWithInvite(created.Token, "InvitedUser", "secret1")
	if err != nil {
		t.Fatalf("RegisterWithInvite: %v", err)
	}
	if user.Username != "inviteduser" || user.ID == "" {
		t.Fatalf("user = %+v, want normalized inviteduser", user)
	}
	if sessionToken == "" || !strings.HasPrefix(sessionToken, "cube_ses_") {
		t.Fatalf("session token = %q, want cube_ses_ prefix", sessionToken)
	}
	if len(workspaces) != 1 || workspaces[0].ID != ws.ID || workspaces[0].Role != RoleMember {
		t.Fatalf("workspaces = %+v, want joined member workspace", workspaces)
	}
	if role, ok := m.MembershipRole(ws.ID, user.ID); !ok || role != RoleMember {
		t.Fatalf("MembershipRole = %q ok=%v, want member true", role, ok)
	}
	if _, _, ok := m.ResolveSession(sessionToken); !ok {
		t.Fatalf("created session token did not resolve")
	}

	list, err := m.ListWorkspaceInvites(ws.ID)
	if err != nil {
		t.Fatalf("ListWorkspaceInvites: %v", err)
	}
	if len(list) != 1 || list[0].UsedCount != 1 || list[0].LastUsedAt.IsZero() {
		t.Fatalf("invite usage = %+v, want one use with last used", list)
	}
}

func TestWorkspaceInviteJoinExistingUserIsIdempotent(t *testing.T) {
	m := newTestManager(t)
	ws, err := m.CreateWorkspace("Team Join", "owner")
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	created, err := m.CreateWorkspaceInvite(ws.ID, "owner", RoleMember, time.Hour)
	if err != nil {
		t.Fatalf("CreateWorkspaceInvite: %v", err)
	}
	user, err := m.CreateUser("joiner", "secret1")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	first, err := m.JoinWithInvite(created.Token, user.ID)
	if err != nil {
		t.Fatalf("first JoinWithInvite: %v", err)
	}
	second, err := m.JoinWithInvite(created.Token, user.ID)
	if err != nil {
		t.Fatalf("second JoinWithInvite: %v", err)
	}
	if len(first) != 1 || first[0].ID != ws.ID || len(second) != 1 || second[0].ID != ws.ID {
		t.Fatalf("join workspaces first=%+v second=%+v, want one workspace", first, second)
	}

	members, err := m.ListMemberships(ws.ID)
	if err != nil {
		t.Fatalf("ListMemberships: %v", err)
	}
	if len(members) != 1 || members[0].UserID != user.ID {
		t.Fatalf("members = %+v, want one user membership", members)
	}
	list, err := m.ListWorkspaceInvites(ws.ID)
	if err != nil {
		t.Fatalf("ListWorkspaceInvites: %v", err)
	}
	if len(list) != 1 || list[0].UsedCount != 1 {
		t.Fatalf("invite usage = %+v, want one idempotent use", list)
	}
}

func TestWorkspaceInviteRejectsRevokedExpiredAndDuplicateUser(t *testing.T) {
	m := newTestManager(t)
	ws, err := m.CreateWorkspace("Team Reject", "owner")
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	revoked, err := m.CreateWorkspaceInvite(ws.ID, "owner", RoleMember, time.Hour)
	if err != nil {
		t.Fatalf("CreateWorkspaceInvite revoked: %v", err)
	}
	if err := m.RevokeWorkspaceInvite(ws.ID, revoked.Invite.ID); err != nil {
		t.Fatalf("RevokeWorkspaceInvite: %v", err)
	}
	if _, err := m.InvitePreview(revoked.Token); err == nil {
		t.Fatalf("revoked invite preview succeeded, want error")
	}
	if _, _, _, err := m.RegisterWithInvite(revoked.Token, "revoked", "secret1"); err == nil {
		t.Fatalf("revoked invite register succeeded, want error")
	}

	expired, err := m.CreateWorkspaceInvite(ws.ID, "owner", RoleMember, -time.Hour)
	if err != nil {
		t.Fatalf("CreateWorkspaceInvite expired: %v", err)
	}
	if _, err := m.InvitePreview(expired.Token); err == nil {
		t.Fatalf("expired invite preview succeeded, want error")
	}

	valid, err := m.CreateWorkspaceInvite(ws.ID, "owner", RoleMember, time.Hour)
	if err != nil {
		t.Fatalf("CreateWorkspaceInvite valid: %v", err)
	}
	if _, err := m.CreateUser("taken", "secret1"); err != nil {
		t.Fatalf("CreateUser taken: %v", err)
	}
	if _, _, _, err := m.RegisterWithInvite(valid.Token, "taken", "secret1"); err == nil {
		t.Fatalf("duplicate invite register succeeded, want error")
	}
	if members, _ := m.ListMemberships(ws.ID); len(members) != 0 {
		t.Fatalf("duplicate registration created membership: %+v", members)
	}
}
