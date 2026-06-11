package main

import (
	"fmt"
	"strings"
	"time"

	"cube20/internal/manager"
)

// runWorkspace implements the local `cube workspace` admin surface, operating
// directly on the manager (Postgres or file state) the same way `cube clients`
// does. It is intended for the platform operator running on the server host.
//
//	cube workspace list
//	cube workspace create <name>
//	cube workspace members <workspace-id>
//	cube workspace invite <workspace-id> <client-id> [--role admin|member]
//	cube workspace remove <workspace-id> <client-id>
//	cube workspace grant-admin <workspace-id> <client-id>
func runWorkspace(m *manager.Manager, args []string) error {
	if len(args) == 0 {
		args = []string{"list"}
	}
	switch args[0] {
	case "list":
		workspaces, err := m.ListWorkspaces()
		if err != nil {
			return err
		}
		if len(workspaces) == 0 {
			fmt.Println("no workspaces")
			return nil
		}
		fmt.Printf("%-24s %-22s %s\n", "ID", "NAME", "CREATED")
		for _, ws := range workspaces {
			created := "-"
			if !ws.CreatedAt.IsZero() {
				created = ws.CreatedAt.Format(time.RFC3339)
			}
			fmt.Printf("%-24s %-22s %s\n", ws.ID, ws.Name, created)
		}
		return nil

	case "create":
		if len(args) < 2 {
			return fmt.Errorf("usage: cube workspace create <name>")
		}
		name := strings.Join(args[1:], " ")
		ws, err := m.CreateWorkspace(name, "cli")
		if err != nil {
			return err
		}
		fmt.Printf("workspace: %s\nname: %s\n", ws.ID, ws.Name)
		return nil

	case "members":
		if len(args) != 2 {
			return fmt.Errorf("usage: cube workspace members <workspace-id>")
		}
		members, err := m.ListMemberships(args[1])
		if err != nil {
			return err
		}
		if len(members) == 0 {
			fmt.Println("no members")
			return nil
		}
		fmt.Printf("%-24s %s\n", "CLIENT_ID", "ROLE")
		for _, ms := range members {
			fmt.Printf("%-24s %s\n", ms.ClientID, ms.Role)
		}
		return nil

	case "invite":
		workspaceID, clientID, role, err := parseInviteArgs(args[1:])
		if err != nil {
			return err
		}
		if err := m.SetMembership(workspaceID, clientID, role); err != nil {
			return err
		}
		fmt.Printf("added %s to %s as %s\n", clientID, workspaceID, role)
		return nil

	case "grant-admin":
		if len(args) != 3 {
			return fmt.Errorf("usage: cube workspace grant-admin <workspace-id> <client-id>")
		}
		if err := m.SetMembership(args[1], args[2], manager.RoleAdmin); err != nil {
			return err
		}
		fmt.Printf("granted admin to %s in %s\n", args[2], args[1])
		return nil

	case "remove":
		if len(args) != 3 {
			return fmt.Errorf("usage: cube workspace remove <workspace-id> <client-id>")
		}
		if err := m.RemoveMembership(args[1], args[2]); err != nil {
			return err
		}
		fmt.Printf("removed %s from %s\n", args[2], args[1])
		return nil

	default:
		return fmt.Errorf("usage: cube workspace [list|create <name>|members <workspace-id>|invite <workspace-id> <client-id> [--role admin|member]|grant-admin <workspace-id> <client-id>|remove <workspace-id> <client-id>]")
	}
}

// parseInviteArgs extracts the workspace id, client id, and optional --role from
// `invite` arguments. Role defaults to member.
func parseInviteArgs(args []string) (workspaceID, clientID string, role manager.WorkspaceRole, err error) {
	role = manager.RoleMember
	positional := []string{}
	for i := 0; i < len(args); i++ {
		if args[i] == "--role" {
			if i+1 >= len(args) {
				return "", "", "", fmt.Errorf("missing value for --role")
			}
			r := manager.WorkspaceRole(strings.TrimSpace(args[i+1]))
			if r != manager.RoleAdmin && r != manager.RoleMember {
				return "", "", "", fmt.Errorf("--role must be admin or member")
			}
			role = r
			i++
			continue
		}
		positional = append(positional, args[i])
	}
	if len(positional) != 2 {
		return "", "", "", fmt.Errorf("usage: cube workspace invite <workspace-id> <client-id> [--role admin|member]")
	}
	return positional[0], positional[1], role, nil
}
