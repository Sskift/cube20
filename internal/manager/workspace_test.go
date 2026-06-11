package manager

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Phase 1: migration. A pre-workspace state (accounts + clients, no workspaces)
// must normalize into one where the default pool exists, every account is in it,
// and every client is a member of it.
func TestNormalizeMigratesToDefaultWorkspace(t *testing.T) {
	now := time.Now()
	state := normalizeState(State{
		Version: 1,
		Accounts: []Account{
			{ID: "acct-1", Status: StatusReady, OwnerMode: OwnerCloud, CreatedAt: now},
		},
		Clients: []Client{
			{ID: "client-1", Label: "alice", CreatedAt: now},
			{ID: "client-2", Label: "bob", CreatedAt: now},
		},
	})

	if state.Accounts[0].WorkspaceID != DefaultWorkspaceID {
		t.Errorf("account workspace = %q, want %q", state.Accounts[0].WorkspaceID, DefaultWorkspaceID)
	}

	foundDefault := false
	for _, w := range state.Workspaces {
		if w.ID == DefaultWorkspaceID {
			foundDefault = true
		}
	}
	if !foundDefault {
		t.Fatal("default workspace not created")
	}

	memberOf := map[string]bool{}
	for _, m := range state.Memberships {
		if m.WorkspaceID == DefaultWorkspaceID && m.Role == RoleMember {
			memberOf[m.ClientID] = true
		}
	}
	if !memberOf["client-1"] || !memberOf["client-2"] {
		t.Errorf("clients not enrolled into default: %+v", state.Memberships)
	}
}

// Migration must be idempotent: normalizing an already-migrated state must not
// duplicate the default workspace or memberships.
func TestNormalizeMigrationIdempotent(t *testing.T) {
	first := normalizeState(State{
		Version:  1,
		Accounts: []Account{{ID: "acct-1", Status: StatusReady, OwnerMode: OwnerCloud}},
		Clients:  []Client{{ID: "client-1", Label: "alice"}},
	})
	second := normalizeState(first)

	defaults := 0
	for _, w := range second.Workspaces {
		if w.ID == DefaultWorkspaceID {
			defaults++
		}
	}
	if defaults != 1 {
		t.Errorf("default workspace count = %d, want 1", defaults)
	}

	memberships := 0
	for _, m := range second.Memberships {
		if m.WorkspaceID == DefaultWorkspaceID && m.ClientID == "client-1" {
			memberships++
		}
	}
	if memberships != 1 {
		t.Errorf("membership count = %d, want 1", memberships)
	}
}

// An empty state (no accounts, no clients) should not fabricate a default pool.
func TestNormalizeEmptyStateNoWorkspace(t *testing.T) {
	state := normalizeState(State{Version: 1})
	if len(state.Workspaces) != 0 {
		t.Errorf("empty state created workspaces: %+v", state.Workspaces)
	}
	if len(state.Memberships) != 0 {
		t.Errorf("empty state created memberships: %+v", state.Memberships)
	}
}

// An explicit non-default account workspace must survive normalization.
func TestNormalizePreservesExplicitWorkspace(t *testing.T) {
	state := normalizeState(State{
		Version: 1,
		Workspaces: []Workspace{
			{ID: "ws-team-a", Name: "Team A"},
		},
		Accounts: []Account{
			{ID: "acct-1", Status: StatusReady, OwnerMode: OwnerCloud, WorkspaceID: "ws-team-a"},
		},
	})
	if state.Accounts[0].WorkspaceID != "ws-team-a" {
		t.Errorf("explicit workspace overwritten: %q", state.Accounts[0].WorkspaceID)
	}
}

// Phase 2: CRUD round-trips through Load/Save (file-backed test manager).
func TestCreateWorkspaceAndList(t *testing.T) {
	m := newTestManager(t)
	ws, err := m.CreateWorkspace("Team A", "admin")
	if err != nil {
		t.Fatalf("CreateWorkspace: %v", err)
	}
	if !strings.HasPrefix(ws.ID, "ws-") {
		t.Errorf("workspace id = %q, want ws- prefix", ws.ID)
	}
	if ws.CreatedBy != "admin" {
		t.Errorf("createdBy = %q, want admin", ws.CreatedBy)
	}
	list, err := m.ListWorkspaces()
	if err != nil {
		t.Fatalf("ListWorkspaces: %v", err)
	}
	found := false
	for _, w := range list {
		if w.ID == ws.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("created workspace not in list: %+v", list)
	}
}

func TestCreateWorkspaceRequiresName(t *testing.T) {
	m := newTestManager(t)
	if _, err := m.CreateWorkspace("  ", "admin"); err == nil {
		t.Fatal("expected error for blank name")
	}
}

func TestSetAndQueryMembership(t *testing.T) {
	m := newTestManager(t)
	ws, _ := m.CreateWorkspace("Team A", "admin")
	client, _, err := m.CreateClient("alice")
	if err != nil {
		t.Fatalf("CreateClient: %v", err)
	}

	if err := m.SetMembership(ws.ID, client.ID, RoleAdmin); err != nil {
		t.Fatalf("SetMembership: %v", err)
	}
	role, ok := m.MembershipRole(ws.ID, client.ID)
	if !ok || role != RoleAdmin {
		t.Errorf("role = %q ok=%v, want admin true", role, ok)
	}

	// Upsert: downgrade to member.
	if err := m.SetMembership(ws.ID, client.ID, RoleMember); err != nil {
		t.Fatalf("SetMembership downgrade: %v", err)
	}
	role, _ = m.MembershipRole(ws.ID, client.ID)
	if role != RoleMember {
		t.Errorf("role after downgrade = %q, want member", role)
	}

	members, err := m.ListMemberships(ws.ID)
	if err != nil {
		t.Fatalf("ListMemberships: %v", err)
	}
	if len(members) != 1 || members[0].ClientID != client.ID {
		t.Errorf("memberships = %+v", members)
	}
}

func TestSetMembershipRejectsUnknownWorkspaceOrClient(t *testing.T) {
	m := newTestManager(t)
	client, _, _ := m.CreateClient("alice")
	if err := m.SetMembership("ws-nope", client.ID, RoleMember); err == nil {
		t.Error("expected error for unknown workspace")
	}
	ws, _ := m.CreateWorkspace("Team A", "admin")
	if err := m.SetMembership(ws.ID, "client-nope", RoleMember); err == nil {
		t.Error("expected error for unknown client")
	}
}

func TestRemoveMembership(t *testing.T) {
	m := newTestManager(t)
	ws, _ := m.CreateWorkspace("Team A", "admin")
	client, _, _ := m.CreateClient("alice")
	if err := m.SetMembership(ws.ID, client.ID, RoleMember); err != nil {
		t.Fatalf("SetMembership: %v", err)
	}
	if err := m.RemoveMembership(ws.ID, client.ID); err != nil {
		t.Fatalf("RemoveMembership: %v", err)
	}
	if _, ok := m.MembershipRole(ws.ID, client.ID); ok {
		t.Error("membership still present after remove")
	}
	if err := m.RemoveMembership(ws.ID, client.ID); err == nil {
		t.Error("expected error removing nonexistent membership")
	}
}

func TestListWorkspacesForClient(t *testing.T) {
	m := newTestManager(t)
	wsA, _ := m.CreateWorkspace("Team A", "admin")
	wsB, _ := m.CreateWorkspace("Team B", "admin")
	_, _ = m.CreateWorkspace("Team C", "admin") // client is NOT in this one
	client, _, _ := m.CreateClient("alice")
	if err := m.SetMembership(wsA.ID, client.ID, RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if err := m.SetMembership(wsB.ID, client.ID, RoleMember); err != nil {
		t.Fatal(err)
	}

	views, err := m.ListWorkspacesForClient(client.ID)
	if err != nil {
		t.Fatalf("ListWorkspacesForClient: %v", err)
	}
	if len(views) != 2 {
		t.Fatalf("got %d workspaces, want 2: %+v", len(views), views)
	}
	roleByID := map[string]WorkspaceRole{}
	for _, v := range views {
		roleByID[v.ID] = v.Role
	}
	if roleByID[wsA.ID] != RoleAdmin || roleByID[wsB.ID] != RoleMember {
		t.Errorf("roles wrong: %+v", roleByID)
	}
}

// setupTwoPoolState builds a state with two workspaces, one healthy account in
// each, and returns the manager. Used by the isolation tests below.
func setupTwoPoolState(t *testing.T) *Manager {
	t.Helper()
	m := newTestManager(t)
	now := time.Now().Add(-time.Minute)
	reset := time.Now().Add(2 * time.Hour)
	accounts := []Account{
		{ID: "acct-a", Label: "acct-a", Status: StatusReady, OwnerMode: OwnerCloud, WorkspaceID: "ws-a", Generation: 1, CodexHome: m.AccountsDir + "/acct-a", CreatedAt: now, UpdatedAt: now},
		{ID: "acct-b", Label: "acct-b", Status: StatusReady, OwnerMode: OwnerCloud, WorkspaceID: "ws-b", Generation: 1, CodexHome: m.AccountsDir + "/acct-b", CreatedAt: now, UpdatedAt: now},
	}
	for _, a := range accounts {
		writeTestAuth(t, a.CodexHome, a.ID)
	}
	state := State{
		Version:  1,
		Accounts: accounts,
		Workspaces: []Workspace{
			{ID: "ws-a", Name: "A", CreatedAt: now, UpdatedAt: now},
			{ID: "ws-b", Name: "B", CreatedAt: now, UpdatedAt: now},
		},
		Clients: []Client{
			{ID: "client-a", Label: "alice", CreatedAt: now},
		},
		Memberships: []Membership{
			{WorkspaceID: "ws-a", ClientID: "client-a", Role: RoleMember, CreatedAt: now},
		},
	}
	if err := m.Save(state); err != nil {
		t.Fatalf("Save: %v", err)
	}
	// Fresh quota windows so both accounts are eligible.
	saveTestQuotaWindows(t, m, "acct-a", 80, reset, 80, reset)
	saveTestQuotaWindows(t, m, "acct-b", 80, reset, 80, reset)
	return m
}

// Phase 3: a member of ws-a leasing in ws-a gets ws-a's account, never ws-b's.
func TestClaimLeaseScopedToWorkspace(t *testing.T) {
	m := setupTwoPoolState(t)
	lease, err := m.ClaimLease(context.Background(), "client-a", "alice", "ws-a", time.Minute)
	if err != nil {
		t.Fatalf("ClaimLease: %v", err)
	}
	if lease.Snapshot.ID != "acct-a" {
		t.Errorf("leased %q, want acct-a (must stay in own pool)", lease.Snapshot.ID)
	}
}

// Phase 3: leasing in a pool with no eligible account fails even though another
// pool has one — pools are isolated.
func TestClaimLeaseEmptyWorkspaceNoCrossPool(t *testing.T) {
	m := setupTwoPoolState(t)
	// ws-a member tries to lease in ws-b's pool directly: should find acct-b only
	// (not acct-a). Confirm scope by leasing ws-b and asserting acct-b.
	lease, err := m.ClaimLease(context.Background(), "client-a", "alice", "ws-b", time.Minute)
	if err != nil {
		t.Fatalf("ClaimLease ws-b: %v", err)
	}
	if lease.Snapshot.ID != "acct-b" {
		t.Errorf("leased %q in ws-b, want acct-b", lease.Snapshot.ID)
	}
}

// Phase 3: LoadBalanceStatus filtered by workspace shows only that pool.
func TestLoadBalanceStatusScopedToWorkspace(t *testing.T) {
	m := setupTwoPoolState(t)
	status, err := m.LoadBalanceStatus("ws-a")
	if err != nil {
		t.Fatalf("LoadBalanceStatus: %v", err)
	}
	all := append(append([]LoadBalanceAccount{}, status.Eligible...), status.Excluded...)
	if len(all) != 1 || all[0].ID != "acct-a" {
		t.Errorf("ws-a status should contain only acct-a, got %+v", all)
	}
}

// Phase 3: ResolveWorkspaceForClient enforces membership and ambiguity rules.
func TestResolveWorkspaceForClient(t *testing.T) {
	m := setupTwoPoolState(t)
	// single membership -> auto-resolves
	ws, err := m.ResolveWorkspaceForClient("client-a", "")
	if err != nil || ws != "ws-a" {
		t.Errorf("auto-resolve = %q, %v; want ws-a", ws, err)
	}
	// explicit non-member workspace -> rejected
	if _, err := m.ResolveWorkspaceForClient("client-a", "ws-b"); err == nil {
		t.Error("expected rejection leasing in non-member workspace ws-b")
	}
	// unknown workspace -> rejected
	if _, err := m.ResolveWorkspaceForClient("client-a", "ws-nope"); err == nil {
		t.Error("expected rejection for unknown workspace")
	}
}

// Review fix (migration marker): once migration has run, a client created later
// (before any account, or in a workspace world) must NOT be auto-enrolled into
// default on a subsequent normalize.
func TestMigrationMarkerStopsReEnrollment(t *testing.T) {
	// First: a legacy flat pool migrates and sets the marker.
	migrated := normalizeState(State{
		Version:  1,
		Accounts: []Account{{ID: "acct-1", Status: StatusReady, OwnerMode: OwnerCloud}},
		Clients:  []Client{{ID: "client-old", Label: "old"}},
	})
	if !migrated.WorkspaceMigrated {
		t.Fatal("expected WorkspaceMigrated=true after legacy migration")
	}

	// Now a NEW client appears (e.g. created post-upgrade). Re-normalize.
	migrated.Clients = append(migrated.Clients, Client{ID: "client-new", Label: "new"})
	again := normalizeState(migrated)

	for _, ms := range again.Memberships {
		if ms.ClientID == "client-new" {
			t.Errorf("new client was auto-enrolled into %s after migration; should require explicit membership", ms.WorkspaceID)
		}
	}
}

// A state already marked migrated must never enroll its clients, even if it
// somehow has the legacy shape (no workspaces + accounts).
func TestMigrationMarkerSuppressesLegacyShape(t *testing.T) {
	state := normalizeState(State{
		Version:           1,
		WorkspaceMigrated: true,
		Accounts:          []Account{{ID: "acct-1", Status: StatusReady, OwnerMode: OwnerCloud}},
		Clients:           []Client{{ID: "client-1", Label: "alice"}},
	})
	for _, ms := range state.Memberships {
		if ms.ClientID == "client-1" {
			t.Error("client enrolled despite WorkspaceMigrated=true")
		}
	}
}

// AccountWorkspace returns the account's pool and existence.
func TestAccountWorkspace(t *testing.T) {
	m := setupTwoPoolState(t)
	ws, ok := m.AccountWorkspace("acct-a")
	if !ok || ws != "ws-a" {
		t.Errorf("AccountWorkspace(acct-a) = %q, %v; want ws-a true", ws, ok)
	}
	if _, ok := m.AccountWorkspace("nope"); ok {
		t.Error("AccountWorkspace(nope) should report not found")
	}
}
