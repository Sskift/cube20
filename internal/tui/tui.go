package tui

import (
	"bufio"
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
		fmt.Println("  l  login account with Codex device auth")
		fmt.Println("  d  deploy account auth.json to ~/.codex/auth.json")
		fmt.Println("  r  run Codex using an account CODEX_HOME")
		fmt.Println("  s  set account status")
		fmt.Println("  q  quit")
		fmt.Println()

		action := a.prompt("Choose action")
		switch strings.ToLower(action) {
		case "a":
			if err := a.addAccount(); err != nil {
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
		case "r":
			if err := a.runCodex(); err != nil {
				a.pause(err.Error())
			}
		case "s":
			if err := a.setStatus(); err != nil {
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
	fmt.Printf("homes: %s\n", a.Manager.AccountsDir)
	fmt.Println()

	if len(accounts) == 0 {
		fmt.Println("No accounts yet. Press 'a' to add one.")
		return nil
	}

	fmt.Printf("%-18s %-10s %-8s %-6s %s\n", "ID", "STATUS", "AUTH", "PLAN", "CODEX_HOME")
	fmt.Println(strings.Repeat("-", 86))
	for _, account := range accounts {
		auth := "missing"
		if account.AuthPresent {
			auth = "ready"
		}
		plan := account.Plan
		if plan == "" {
			plan = "-"
		}
		fmt.Printf("%-18s %-10s %-8s %-6s %s\n", account.ID, account.Status, auth, plan, account.CodexHome)
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
	a.pause(fmt.Sprintf("deployed auth to %s", target))
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
