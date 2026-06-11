package manager

import "testing"

func TestPasswordHashRoundTrip(t *testing.T) {
	h, err := hashPassword("hunter2")
	if err != nil {
		t.Fatalf("hashPassword: %v", err)
	}
	if h == "" || h == "hunter2" {
		t.Fatalf("hash looks wrong: %q", h)
	}
	if !checkPassword(h, "hunter2") {
		t.Error("correct password rejected")
	}
	if checkPassword(h, "wrong") {
		t.Error("wrong password accepted")
	}
	if checkPassword("", "hunter2") {
		t.Error("empty stored hash must reject (login-disabled user)")
	}
	if checkPassword("garbage", "hunter2") {
		t.Error("malformed stored hash must reject")
	}
}

// Phase 1: legacy Client -> User+Device migration via normalizeState.
func TestMigrateUsersAndDevices(t *testing.T) {
	state := normalizeState(State{
		Version: 1,
		Accounts: []Account{
			{ID: "acct-1", Status: StatusReady, OwnerMode: OwnerCloud, WorkspaceID: "default"},
		},
		Clients: []Client{
			{ID: "client-liushiao-local", Label: "liushiao-local"},
		},
		Workspaces: []Workspace{{ID: "default", Name: "Default"}},
		Memberships: []Membership{
			{WorkspaceID: "default", ClientID: "client-liushiao-local", Role: RoleMember},
		},
	})

	if !state.UserDeviceMigrated {
		t.Fatal("expected UserDeviceMigrated=true")
	}
	if len(state.Users) != 1 {
		t.Fatalf("expected 1 user, got %d", len(state.Users))
	}
	u := state.Users[0]
	if u.Username != "liushiao-local" {
		t.Errorf("username = %q, want liushiao-local", u.Username)
	}
	// device (client) now points at the user
	if state.Clients[0].UserID != u.ID {
		t.Errorf("client.UserID = %q, want %q", state.Clients[0].UserID, u.ID)
	}
	// membership got the user id too
	if state.Memberships[0].UserID != u.ID {
		t.Errorf("membership.UserID = %q, want %q", state.Memberships[0].UserID, u.ID)
	}
	// token untouched (login disabled until password set)
	if u.PasswordHash != "" {
		t.Error("migrated user should have empty password (login disabled)")
	}
}

// Migration is one-time: a device created after migration must not spawn a user.
func TestMigrateUsersIdempotentAndGated(t *testing.T) {
	first := normalizeState(State{
		Version:     1,
		Clients:     []Client{{ID: "client-1", Label: "alice"}},
		Workspaces:  []Workspace{{ID: "default", Name: "Default"}},
		Memberships: []Membership{{WorkspaceID: "default", ClientID: "client-1", Role: RoleMember}},
	})
	if len(first.Users) != 1 {
		t.Fatalf("first pass: want 1 user, got %d", len(first.Users))
	}
	// add a new device post-migration
	first.Clients = append(first.Clients, Client{ID: "client-2", Label: "bob-laptop"})
	second := normalizeState(first)
	if len(second.Users) != 1 {
		t.Errorf("post-migration device must not spawn a user; got %d users", len(second.Users))
	}
}

// Phase 2: user/device/session CRUD round-trips.
func TestUserDeviceSessionLifecycle(t *testing.T) {
	m := newTestManager(t)

	// register
	u, err := m.CreateUser("Alice", "secret1")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if u.Username != "alice" {
		t.Errorf("username lowercased = %q", u.Username)
	}
	// duplicate rejected (case-insensitive)
	if _, err := m.CreateUser("ALICE", "secret2"); err == nil {
		t.Error("duplicate username must be rejected")
	}
	// short password rejected
	if _, err := m.CreateUser("bob", "x"); err == nil {
		t.Error("short password must be rejected")
	}

	// authenticate
	if _, ok := m.AuthenticateUser("alice", "secret1"); !ok {
		t.Error("correct creds rejected")
	}
	if _, ok := m.AuthenticateUser("alice", "wrong"); ok {
		t.Error("wrong password accepted")
	}
	if _, ok := m.AuthenticateUser("ghost", "secret1"); ok {
		t.Error("unknown user accepted")
	}

	// device mint + auth
	dev, token, err := m.CreateDevice(u.ID, "work-laptop")
	if err != nil {
		t.Fatalf("CreateDevice: %v", err)
	}
	if token == "" || dev.UserID != u.ID {
		t.Fatalf("device wrong: %+v token=%q", dev, token)
	}
	gotDev, ownerID, ok := m.AuthenticateDeviceToken(token)
	if !ok || ownerID != u.ID || gotDev.ID != dev.ID {
		t.Errorf("device auth failed: ok=%v owner=%q", ok, ownerID)
	}
	devices, _ := m.ListDevices(u.ID)
	if len(devices) != 1 {
		t.Fatalf("want 1 device, got %d", len(devices))
	}

	// device count surfaced on user view
	uv, _ := m.GetUser(u.ID)
	if uv.DeviceCount != 1 {
		t.Errorf("deviceCount = %d, want 1", uv.DeviceCount)
	}

	// revoke device -> token no longer authenticates
	if err := m.RevokeDevice(dev.ID); err != nil {
		t.Fatalf("RevokeDevice: %v", err)
	}
	if _, _, ok := m.AuthenticateDeviceToken(token); ok {
		t.Error("revoked device token still authenticates")
	}

	// session create + resolve
	sessTok, err := m.CreateSession(u.ID)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	got, sessID, ok := m.ResolveSession(sessTok)
	if !ok || got.ID != u.ID {
		t.Fatalf("ResolveSession failed: ok=%v", ok)
	}
	// logout
	if err := m.DeleteSession(sessID); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, _, ok := m.ResolveSession(sessTok); ok {
		t.Error("deleted session still resolves")
	}

	// password change drops sessions
	s2, _ := m.CreateSession(u.ID)
	if err := m.SetUserPassword(u.ID, "newsecret"); err != nil {
		t.Fatalf("SetUserPassword: %v", err)
	}
	if _, _, ok := m.ResolveSession(s2); ok {
		t.Error("password change must invalidate existing sessions")
	}
	if _, ok := m.AuthenticateUser("alice", "newsecret"); !ok {
		t.Error("new password should work")
	}

	// disable user -> auth + session resolve fail
	s3, _ := m.CreateSession(u.ID)
	if err := m.SetUserDisabled(u.ID, true); err != nil {
		t.Fatalf("SetUserDisabled: %v", err)
	}
	if _, ok := m.AuthenticateUser("alice", "newsecret"); ok {
		t.Error("disabled user must not authenticate")
	}
	if _, _, ok := m.ResolveSession(s3); ok {
		t.Error("disabled user session must not resolve")
	}
}
