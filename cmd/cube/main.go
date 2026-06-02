package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"cube20/internal/manager"
	"cube20/internal/tui"
	"cube20/internal/web"
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
	case "profile":
		return runProfile(m, args[1:])
	case "codex":
		return runCodex(m, args[1:])
	case "codex-auto", "run":
		return runCodexAuto(m, args[1:])
	case "lb":
		return runLoadBalancer(m, args[1:])
	case "dashboard":
		return runDashboard(m, args[1:])
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
	case "import":
		id := ""
		label := ""
		if len(args) > 1 {
			id = args[1]
		}
		if len(args) > 2 {
			label = strings.Join(args[2:], " ")
		}
		account, err := m.ImportLiveProfile(id, label, "")
		if err != nil {
			return err
		}
		fmt.Printf("imported live Codex profile into %s at %s\n", account.ID, account.CodexHome)
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
	case "quota":
		if len(args) != 2 {
			return fmt.Errorf("usage: cube accounts quota <id>")
		}
		return printQuota(m, args[1])
	case "usage":
		if len(args) != 2 {
			return fmt.Errorf("usage: cube accounts usage <id>")
		}
		return printUsage(m, args[1])
	case "delete", "remove":
		if len(args) != 2 {
			return fmt.Errorf("usage: cube accounts delete <id>")
		}
		account, err := m.DeleteAccount(args[1])
		if err != nil {
			return err
		}
		fmt.Printf("deleted account %s at %s\n", account.ID, account.CodexHome)
		return nil
	default:
		return fmt.Errorf("unknown accounts command %q", args[0])
	}
}

func runAuth(m *manager.Manager, args []string) error {
	if len(args) != 2 || args[0] != "deploy" {
		return fmt.Errorf("usage: cube auth deploy <id>")
	}
	return deployProfile(m, args[1])
}

func runProfile(m *manager.Manager, args []string) error {
	if len(args) != 2 || args[0] != "deploy" {
		return fmt.Errorf("usage: cube profile deploy <id>")
	}
	return deployProfile(m, args[1])
}

func deployProfile(m *manager.Manager, id string) error {
	target, err := m.DeployAuth(id, "")
	if err != nil {
		return err
	}
	fmt.Printf("deployed profile files to %s\n", target)
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

func runCodexAuto(m *manager.Manager, args []string) error {
	account, err := m.SelectAccountForRun()
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "cube: selected %s (%s)\n", account.ID, account.CodexHome)
	cmd, err := m.CodexCommand(account.ID, args)
	if err != nil {
		return err
	}
	return cmd.Run()
}

func runLoadBalancer(m *manager.Manager, args []string) error {
	if len(args) == 0 {
		args = []string{"status"}
	}
	switch args[0] {
	case "status":
		status, err := m.LoadBalanceStatus()
		if err != nil {
			return err
		}
		fmt.Printf("policy: %s\nstate: %s\nlast: %s\n", status.Policy, status.StatePath, emptyDash(status.LastAccountID))
		fmt.Println("eligible:")
		if len(status.Eligible) == 0 {
			fmt.Println("  -")
		}
		for _, account := range status.Eligible {
			fmt.Printf("  %s\t%s\t%s\n", account.ID, account.Label, account.CodexHome)
		}
		if len(status.Excluded) > 0 {
			fmt.Println("excluded:")
			for _, account := range status.Excluded {
				fmt.Printf("  %s\t%s\t%s\n", account.ID, account.Status, account.Reason)
			}
		}
		return nil
	case "pick":
		account, err := m.SelectAccountForRun()
		if err != nil {
			return err
		}
		fmt.Printf("%s\t%s\t%s\n", account.ID, account.Label, account.CodexHome)
		return nil
	case "reset":
		if err := m.ResetRoundRobin(); err != nil {
			return err
		}
		fmt.Println("round-robin state reset")
		return nil
	default:
		return fmt.Errorf("usage: cube lb [status|pick|reset]")
	}
}

func runDashboard(m *manager.Manager, args []string) error {
	host := "127.0.0.1"
	port := 8720

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--host":
			if i+1 >= len(args) {
				return fmt.Errorf("missing value for --host")
			}
			host = args[i+1]
			i++
		case "--port":
			if i+1 >= len(args) {
				return fmt.Errorf("missing value for --port")
			}
			nextPort, err := strconv.Atoi(args[i+1])
			if err != nil {
				return fmt.Errorf("invalid --port %q", args[i+1])
			}
			port = nextPort
			i++
		default:
			return fmt.Errorf("unknown dashboard flag %q", args[i])
		}
	}

	return (&web.Server{
		Manager: m,
		Host:    host,
		Port:    port,
	}).ListenAndServe()
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

	fmt.Printf("%-18s %-10s %-8s %-8s %s\n", "ID", "STATUS", "AUTH", "CONFIG", "CODEX_HOME")
	for _, account := range accounts {
		auth := "missing"
		if account.AuthPresent {
			auth = "ready"
		}
		config := "missing"
		if account.ConfigPresent {
			config = "ready"
		}
		fmt.Printf("%-18s %-10s %-8s %-8s %s\n", account.ID, account.Status, auth, config, account.CodexHome)
	}
	return nil
}

func printQuota(m *manager.Manager, id string) error {
	result, err := m.FetchQuota(context.Background(), id)
	if err != nil {
		return err
	}
	fmt.Printf("status: %s\n", result.Status)
	if result.Plan != "" {
		fmt.Printf("plan: %s\n", result.Plan)
	}
	if result.Account != "" {
		fmt.Printf("account: %s\n", result.Account)
	}
	if result.Detail != "" {
		fmt.Printf("detail: %s\n", result.Detail)
	}
	for _, quota := range result.Quotas {
		reset := ""
		if quota.ResetsAt != "" {
			reset = " reset=" + quota.ResetsAt
		}
		fmt.Printf("%s: used=%s remaining=%s%s\n", quota.Label, quota.UsedDisplay, quota.RemainingDisplay, reset)
	}
	return nil
}

func printUsage(m *manager.Manager, id string) error {
	result, err := m.FetchUsage(id)
	if err != nil {
		return err
	}
	fmt.Printf("status: %s\n", result.Status)
	if result.Detail != "" {
		fmt.Printf("detail: %s\n", result.Detail)
	}
	fmt.Printf("files: %d events: %d\n", result.FilesScanned, result.Events)
	fmt.Printf("today: %d tokens (in=%d cached=%d out=%d)\n", result.Today.Total, result.Today.Input, result.Today.CachedInput, result.Today.Output)
	fmt.Printf("7d: %d tokens (in=%d cached=%d out=%d)\n", result.SevenDays.Total, result.SevenDays.Input, result.SevenDays.CachedInput, result.SevenDays.Output)
	fmt.Printf("all: %d tokens (in=%d cached=%d out=%d)\n", result.AllTime.Total, result.AllTime.Input, result.AllTime.CachedInput, result.AllTime.Output)
	if result.LatestAt != "" {
		fmt.Printf("latest: %s %s\n", result.LatestAt, result.LatestModel)
	}
	return nil
}

func emptyDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func printHelp() {
	fmt.Println("cube - local Codex account-pool manager")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  cube")
	fmt.Println("  cube accounts list")
	fmt.Println("  cube accounts add <id> [label]")
	fmt.Println("  cube accounts import [id] [label]")
	fmt.Println("  cube accounts login <id>")
	fmt.Println("  cube accounts quota <id>")
	fmt.Println("  cube accounts usage <id>")
	fmt.Println("  cube accounts status <id> <ready|drain|disabled>")
	fmt.Println("  cube accounts delete <id>")
	fmt.Println("  cube profile deploy <id>")
	fmt.Println("  cube auth deploy <id>")
	fmt.Println("  cube codex <account-id> [codex args...]")
	fmt.Println("  cube codex-auto [codex args...]")
	fmt.Println("  cube run [codex args...]")
	fmt.Println("  cube lb [status|pick|reset]")
	fmt.Println("  cube dashboard [--host 127.0.0.1] [--port 8720]")
}
