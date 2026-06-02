package main

import (
	"fmt"
	"os"
	"strings"

	"cube20/internal/manager"
	"cube20/internal/tui"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "cube:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	m, err := manager.New()
	if err != nil {
		return err
	}

	if len(args) == 0 {
		return tui.New(m).Run()
	}

	switch args[0] {
	case "accounts":
		return runAccounts(m, args[1:])
	case "auth":
		return runAuth(m, args[1:])
	case "codex":
		return runCodex(m, args[1:])
	case "help", "-h", "--help":
		printHelp()
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runAccounts(m *manager.Manager, args []string) error {
	if len(args) == 0 {
		return listAccounts(m)
	}

	switch args[0] {
	case "list":
		return listAccounts(m)
	case "add":
		if len(args) < 2 {
			return fmt.Errorf("usage: cube accounts add <id> [label]")
		}
		label := ""
		if len(args) > 2 {
			label = strings.Join(args[2:], " ")
		}
		account, err := m.AddAccount(args[1], label)
		if err != nil {
			return err
		}
		fmt.Printf("added %s at %s\n", account.ID, account.CodexHome)
		return nil
	case "login":
		if len(args) != 2 {
			return fmt.Errorf("usage: cube accounts login <id>")
		}
		cmd, err := m.LoginCommand(args[1])
		if err != nil {
			return err
		}
		return cmd.Run()
	case "status":
		if len(args) != 3 {
			return fmt.Errorf("usage: cube accounts status <id> <ready|drain|disabled>")
		}
		return m.SetStatus(args[1], manager.AccountStatus(args[2]))
	default:
		return fmt.Errorf("unknown accounts command %q", args[0])
	}
}

func runAuth(m *manager.Manager, args []string) error {
	if len(args) != 2 || args[0] != "deploy" {
		return fmt.Errorf("usage: cube auth deploy <id>")
	}
	target, err := m.DeployAuth(args[1], "")
	if err != nil {
		return err
	}
	fmt.Printf("deployed auth to %s\n", target)
	return nil
}

func runCodex(m *manager.Manager, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: cube codex <account-id> [codex args...]")
	}
	cmd, err := m.CodexCommand(args[0], args[1:])
	if err != nil {
		return err
	}
	return cmd.Run()
}

func listAccounts(m *manager.Manager) error {
	accounts, err := m.ListAccounts()
	if err != nil {
		return err
	}
	if len(accounts) == 0 {
		fmt.Println("no accounts")
		return nil
	}

	fmt.Printf("%-18s %-10s %-8s %s\n", "ID", "STATUS", "AUTH", "CODEX_HOME")
	for _, account := range accounts {
		auth := "missing"
		if account.AuthPresent {
			auth = "ready"
		}
		fmt.Printf("%-18s %-10s %-8s %s\n", account.ID, account.Status, auth, account.CodexHome)
	}
	return nil
}

func printHelp() {
	fmt.Println("cube - local Codex account-pool manager")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  cube")
	fmt.Println("  cube accounts list")
	fmt.Println("  cube accounts add <id> [label]")
	fmt.Println("  cube accounts login <id>")
	fmt.Println("  cube accounts status <id> <ready|drain|disabled>")
	fmt.Println("  cube auth deploy <id>")
	fmt.Println("  cube codex <account-id> [codex args...]")
}
