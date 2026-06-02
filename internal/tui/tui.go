package tui

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"cube20/internal/manager"
)

type App struct {
	Manager *manager.Manager
	In      *bufio.Reader
}

func New(m *manager.Manager) *App {
	return &App{
		Manager: m,
		In:      bufio.NewReader(os.Stdin),
	}
}

func (a *App) Run() error {
	for {
		a.clear()
		if err := a.renderAccounts(); err != nil {
			return err
		}

		fmt.Println()
		fmt.Println("Actions")
		fmt.Println("  a  add account")
		fmt.Println("  i  import current Codex profile")
		fmt.Println("  l  login account with Codex device auth")
		fmt.Println("  d  deploy account profile to live Codex")
		fmt.Println("  o  refresh subscription quota")
		fmt.Println("  u  summarize local token usage")
		fmt.Println("  r  run Codex using an account CODEX_HOME")
		fmt.Println("  s  set account status")
		fmt.Println("  x  delete account")
		fmt.Println("  q  quit")
		fmt.Println()

		action := a.prompt("Choose action")
		switch strings.ToLower(action) {
		case "a":
			if err := a.addAccount(); err != nil {
				a.pause(err.Error())
			}
		case "i":
			if err := a.importProfile(); err != nil {
				a.pause(err.Error())
			}
		case "l":
			if err := a.loginAccount(); err != nil {
				a.pause(err.Error())
			}
		case "d":
			if err := a.deployAuth(); err != nil {
				a.pause(err.Error())
			}
		case "o":
			if err := a.showQuota(); err != nil {
				a.pause(err.Error())
			}
		case "u":
			if err := a.showUsage(); err != nil {
				a.pause(err.Error())
			}
		case "r":
			if err := a.runCodex(); err != nil {
				a.pause(err.Error())
			}
		case "s":
			if err := a.setStatus(); err != nil {
				a.pause(err.Error())
			}
		case "x":
			if err := a.deleteAccount(); err != nil {
				a.pause(err.Error())
			}
		case "q":
			return nil
		default:
			a.pause("unknown action")
		}
	}
}

func (a *App) renderAccounts() error {
	accounts, err := a.Manager.ListAccounts()
	if err != nil {
		return err
	}

	fmt.Println("cube20 account pool")
	fmt.Println("===================")
	fmt.Printf("state: %s\n", a.Manager.StatePath)
	fmt.Printf("settings: %s\n", a.Manager.SettingsPath)
	fmt.Printf("live: %s\n", a.Manager.LiveCodexHome)
	fmt.Printf("homes: %s\n", a.Manager.AccountsDir)
	fmt.Println()

	if len(accounts) == 0 {
		fmt.Println("No accounts yet. Press 'a' to add one.")
		return nil
	}

	fmt.Printf("%-18s %-10s %-8s %-8s %-6s %s\n", "ID", "STATUS", "AUTH", "CONFIG", "PLAN", "CODEX_HOME")
	fmt.Println(strings.Repeat("-", 86))
	for _, account := range accounts {
		auth := "missing"
		if account.AuthPresent {
			auth = "ready"
		}
		config := "missing"
		if account.ConfigPresent {
			config = "ready"
		}
		plan := account.Plan
		if plan == "" {
			plan = "-"
		}
		fmt.Printf("%-18s %-10s %-8s %-8s %-6s %s\n", account.ID, account.Status, auth, config, plan, account.CodexHome)
	}
	return nil
}

func (a *App) addAccount() error {
	id := a.prompt("Account id")
	label := a.prompt("Label (optional)")
	account, err := a.Manager.AddAccount(id, label)
	if err != nil {
		return err
	}
	a.pause(fmt.Sprintf("added %s at %s", account.ID, account.CodexHome))
	return nil
}

func (a *App) importProfile() error {
	id := a.prompt("Account id (empty to auto)")
	label := a.prompt("Label (optional)")
	account, err := a.Manager.ImportLiveProfile(id, label, "")
	if err != nil {
		return err
	}
	a.pause(fmt.Sprintf("imported current Codex profile into %s at %s", account.ID, account.CodexHome))
	return nil
}

func (a *App) loginAccount() error {
	id := a.prompt("Account id")
	cmd, err := a.Manager.LoginCommand(id)
	if err != nil {
		return err
	}
	fmt.Println()
	fmt.Printf("Starting Codex login for %s. Follow the device auth prompt.\n", id)
	if err := cmd.Run(); err != nil {
		return err
	}
	a.pause("login command finished")
	return nil
}

func (a *App) deployAuth() error {
	id := a.prompt("Account id")
	target, err := a.Manager.DeployAuth(id, "")
	if err != nil {
		return err
	}
	a.pause(fmt.Sprintf("deployed profile files to %s", target))
	return nil
}

func (a *App) showQuota() error {
	id := a.prompt("Account id")
	result, err := a.Manager.FetchQuota(context.Background(), id)
	if err != nil {
		return err
	}
	lines := []string{fmt.Sprintf("quota status: %s", result.Status)}
	if result.Plan != "" {
		lines = append(lines, fmt.Sprintf("plan: %s", result.Plan))
	}
	if result.Detail != "" {
		lines = append(lines, fmt.Sprintf("detail: %s", result.Detail))
	}
	for _, quota := range result.Quotas {
		lines = append(lines, fmt.Sprintf("%s used=%s remaining=%s", quota.Label, quota.UsedDisplay, quota.RemainingDisplay))
	}
	a.pause(strings.Join(lines, "\n"))
	return nil
}

func (a *App) showUsage() error {
	id := a.prompt("Account id")
	result, err := a.Manager.FetchUsage(id)
	if err != nil {
		return err
	}
	a.pause(fmt.Sprintf(
		"usage status: %s\nfiles: %d events: %d\ntoday: %d tokens\n7d: %d tokens\nall: %d tokens",
		result.Status,
		result.FilesScanned,
		result.Events,
		result.Today.Total,
		result.SevenDays.Total,
		result.AllTime.Total,
	))
	return nil
}

func (a *App) runCodex() error {
	id := a.prompt("Account id")
	argsText := a.prompt("Codex args (empty for interactive)")
	args := splitArgs(argsText)
	cmd, err := a.Manager.CodexCommand(id, args)
	if err != nil {
		return err
	}
	fmt.Println()
	fmt.Printf("Running CODEX_HOME-scoped codex for %s.\n", id)
	return cmd.Run()
}

func (a *App) setStatus() error {
	id := a.prompt("Account id")
	status := manager.AccountStatus(a.prompt("Status (ready/drain/disabled)"))
	if err := a.Manager.SetStatus(id, status); err != nil {
		return err
	}
	a.pause("status updated")
	return nil
}

func (a *App) deleteAccount() error {
	id := a.prompt("Account id to delete")
	confirm := a.prompt(fmt.Sprintf("Are you sure you want to delete %s? (y/n)", id))
	if strings.ToLower(confirm) != "y" {
		a.pause("cancelled")
		return nil
	}
	account, err := a.Manager.DeleteAccount(id)
	if err != nil {
		return err
	}
	a.pause(fmt.Sprintf("deleted account %s at %s", account.ID, account.CodexHome))
	return nil
}

func (a *App) prompt(label string) string {
	fmt.Printf("%s: ", label)
	text, _ := a.In.ReadString('\n')
	return strings.TrimSpace(text)
}

func (a *App) pause(message string) {
	fmt.Println()
	fmt.Println(message)
	fmt.Print("Press enter to continue...")
	_, _ = a.In.ReadString('\n')
}

func (a *App) clear() {
	fmt.Print("\033[2J\033[H")
}

func splitArgs(input string) []string {
	if strings.TrimSpace(input) == "" {
		return []string{}
	}
	return strings.Fields(input)
}
