package main

import (
	"testing"

	"cube20/internal/manager"
)

func TestParseInviteArgs(t *testing.T) {
	ws, client, role, err := parseInviteArgs([]string{"ws-a", "client-1"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if ws != "ws-a" || client != "client-1" || role != manager.RoleMember {
		t.Errorf("got (%q, %q, %q), want (ws-a, client-1, member)", ws, client, role)
	}

	ws, client, role, err = parseInviteArgs([]string{"ws-a", "client-1", "--role", "admin"})
	if err != nil {
		t.Fatalf("parse with role: %v", err)
	}
	if role != manager.RoleAdmin {
		t.Errorf("role = %q, want admin", role)
	}

	if _, _, _, err := parseInviteArgs([]string{"ws-a"}); err == nil {
		t.Error("expected error for missing client id")
	}
	if _, _, _, err := parseInviteArgs([]string{"ws-a", "client-1", "--role", "bogus"}); err == nil {
		t.Error("expected error for invalid role")
	}
	if _, _, _, err := parseInviteArgs([]string{"ws-a", "client-1", "--role"}); err == nil {
		t.Error("expected error for --role without value")
	}
}

func TestParseCloudRunOptionsWorkspace(t *testing.T) {
	m := &manager.Manager{}
	opts, _, err := parseCloudRunOptions(m, []string{"--server", "http://x", "--workspace", "ws-a"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if opts.Workspace != "ws-a" {
		t.Errorf("workspace = %q, want ws-a", opts.Workspace)
	}

	if _, _, err := parseCloudRunOptions(m, []string{"--server", "http://x", "--workspace"}); err == nil {
		t.Error("expected error for --workspace without value")
	}
}
