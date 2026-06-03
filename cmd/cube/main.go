package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

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
	case "cloud":
		return runCloud(m, args[1:])
	case "cloud-run":
		return runCloudRun(m, args[1:])
	case "sync":
		return runSync(m, args[1:])
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

type cloudSyncOptions struct {
	Server   string
	Token    string
	Client   string
	Interval time.Duration
	Deploy   bool
	Pull     bool
	All      bool
}

func runCloud(m *manager.Manager, args []string) error {
	if len(args) == 0 {
		return printCloudStatus(m)
	}
	switch args[0] {
	case "status":
		if len(args) != 1 {
			return fmt.Errorf("usage: cube cloud status")
		}
		return printCloudStatus(m)
	case "config":
		return configureCloud(m, args[1:])
	default:
		return fmt.Errorf("usage: cube cloud [status|config --server <url> --token <token>]")
	}
}

func printCloudStatus(m *manager.Manager) error {
	fmt.Printf("settings: %s\n", m.SettingsPath)
	fmt.Printf("server: %s\n", emptyDash(m.CloudURL))
	token := "missing"
	if strings.TrimSpace(m.CloudToken) != "" {
		token = "configured"
	}
	fmt.Printf("token: %s\n", token)
	return nil
}

func configureCloud(m *manager.Manager, args []string) error {
	server := ""
	token := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--server":
			if i+1 >= len(args) {
				return fmt.Errorf("missing value for --server")
			}
			server = strings.TrimSpace(args[i+1])
			i++
		case "--token":
			if i+1 >= len(args) {
				return fmt.Errorf("missing value for --token")
			}
			token = strings.TrimSpace(args[i+1])
			i++
		default:
			return fmt.Errorf("unknown cloud config flag %q", args[i])
		}
	}
	if server == "" && token == "" {
		return fmt.Errorf("usage: cube cloud config --server <url> --token <token>")
	}
	settings, err := m.UpdateCloudSettings(server, token)
	if err != nil {
		return err
	}
	fmt.Printf("cloud config saved to %s\n", m.SettingsPath)
	fmt.Printf("server: %s\n", emptyDash(settings.CloudURL))
	tokenStatus := "missing"
	if strings.TrimSpace(settings.CloudToken) != "" {
		tokenStatus = "configured"
	}
	fmt.Printf("token: %s\n", tokenStatus)
	return nil
}

func runSync(m *manager.Manager, args []string) error {
	if len(args) == 0 {
		return syncUsage()
	}

	switch args[0] {
	case "push":
		ids, opts, err := parseCloudSyncOptions(m, args[1:])
		if err != nil {
			return err
		}
		ids, err = resolveSyncIDs(m, ids, opts.All)
		if err != nil {
			return err
		}
		for _, id := range ids {
			if err := pushSnapshot(context.Background(), m, opts, id); err != nil {
				return err
			}
		}
		return nil
	case "pull":
		ids, opts, err := parseCloudSyncOptions(m, args[1:])
		if err != nil {
			return err
		}
		if opts.All {
			return fmt.Errorf("cube sync pull needs an explicit account id")
		}
		if len(ids) != 1 {
			return fmt.Errorf("usage: cube sync pull <id> --server <url> [--token <token>] [--deploy]")
		}
		return pullSnapshot(context.Background(), m, opts, ids[0])
	case "claim":
		ids, opts, err := parseCloudSyncOptions(m, args[1:])
		if err != nil {
			return err
		}
		if opts.All || len(ids) != 0 {
			return fmt.Errorf("usage: cube sync claim --server <url> [--token <token>] [--deploy]")
		}
		return claimSnapshot(context.Background(), m, opts)
	case "daemon":
		ids, opts, err := parseCloudSyncOptions(m, args[1:])
		if err != nil {
			return err
		}
		ids, err = resolveSyncIDs(m, ids, opts.All)
		if err != nil {
			return err
		}
		return runSyncDaemon(m, opts, ids)
	default:
		return syncUsage()
	}
}

func syncUsage() error {
	return fmt.Errorf("usage: cube sync <push|pull|claim|daemon> [id|--all] [--server <url>] [--token <token>] [--deploy] [--pull] [--interval 60s]")
}

func defaultCloudSyncOptions(m *manager.Manager) cloudSyncOptions {
	opts := cloudSyncOptions{
		Server:   strings.TrimSpace(m.CloudURL),
		Token:    strings.TrimSpace(m.CloudToken),
		Interval: 60 * time.Second,
	}
	if value := strings.TrimSpace(os.Getenv("CUBE_CLOUD_URL")); value != "" {
		opts.Server = value
	}
	if value := strings.TrimSpace(os.Getenv("CUBE_CLOUD_TOKEN")); value != "" {
		opts.Token = value
	}
	if host, err := os.Hostname(); err == nil {
		opts.Client = host
	}
	return opts
}

func parseCloudSyncOptions(m *manager.Manager, args []string) ([]string, cloudSyncOptions, error) {
	opts := defaultCloudSyncOptions(m)

	ids := []string{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--server":
			if i+1 >= len(args) {
				return nil, opts, fmt.Errorf("missing value for --server")
			}
			opts.Server = strings.TrimSpace(args[i+1])
			i++
		case "--token":
			if i+1 >= len(args) {
				return nil, opts, fmt.Errorf("missing value for --token")
			}
			opts.Token = strings.TrimSpace(args[i+1])
			i++
		case "--client":
			if i+1 >= len(args) {
				return nil, opts, fmt.Errorf("missing value for --client")
			}
			opts.Client = strings.TrimSpace(args[i+1])
			i++
		case "--interval":
			if i+1 >= len(args) {
				return nil, opts, fmt.Errorf("missing value for --interval")
			}
			interval, err := time.ParseDuration(args[i+1])
			if err != nil {
				return nil, opts, fmt.Errorf("invalid --interval %q", args[i+1])
			}
			opts.Interval = interval
			i++
		case "--deploy":
			opts.Deploy = true
		case "--pull":
			opts.Pull = true
		case "--all":
			opts.All = true
		default:
			if strings.HasPrefix(args[i], "--") {
				return nil, opts, fmt.Errorf("unknown sync flag %q", args[i])
			}
			ids = append(ids, args[i])
		}
	}
	if opts.Server == "" {
		return nil, opts, fmt.Errorf("missing cloud server; run cube cloud config --server <url> --token <token>, pass --server, or set CUBE_CLOUD_URL")
	}
	if opts.Interval < time.Second {
		return nil, opts, fmt.Errorf("--interval must be at least 1s")
	}
	return ids, opts, nil
}

func resolveSyncIDs(m *manager.Manager, ids []string, all bool) ([]string, error) {
	if all {
		views, err := m.ListAccounts()
		if err != nil {
			return nil, err
		}
		out := []string{}
		for _, view := range views {
			if view.AuthPresent {
				out = append(out, view.ID)
			}
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("no accounts with auth.json are available")
		}
		return out, nil
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("missing account id, or pass --all")
	}
	return ids, nil
}

func runCloudRun(m *manager.Manager, args []string) error {
	opts, codexArgs, err := parseCloudRunOptions(m, args)
	if err != nil {
		return err
	}
	account, err := claimProfile(context.Background(), m, opts, false)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "cube: cloud selected %s (%s)\n", account.ID, account.CodexHome)

	cmd, err := m.CodexCommand(account.ID, codexArgs)
	if err != nil {
		return err
	}
	runErr := cmd.Run()
	pushErr := pushSnapshot(context.Background(), m, opts, account.ID)
	if runErr != nil && pushErr != nil {
		return fmt.Errorf("codex failed: %w; auth push failed: %v", runErr, pushErr)
	}
	if pushErr != nil {
		return pushErr
	}
	return runErr
}

func parseCloudRunOptions(m *manager.Manager, args []string) (cloudSyncOptions, []string, error) {
	opts := defaultCloudSyncOptions(m)
	codexArgs := []string{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--server":
			if i+1 >= len(args) {
				return opts, nil, fmt.Errorf("missing value for --server")
			}
			opts.Server = strings.TrimSpace(args[i+1])
			i++
		case "--token":
			if i+1 >= len(args) {
				return opts, nil, fmt.Errorf("missing value for --token")
			}
			opts.Token = strings.TrimSpace(args[i+1])
			i++
		case "--client":
			if i+1 >= len(args) {
				return opts, nil, fmt.Errorf("missing value for --client")
			}
			opts.Client = strings.TrimSpace(args[i+1])
			i++
		case "--":
			codexArgs = append(codexArgs, args[i+1:]...)
			i = len(args)
		default:
			codexArgs = append(codexArgs, args[i:]...)
			i = len(args)
		}
	}
	if opts.Server == "" {
		return opts, nil, fmt.Errorf("missing cloud server; run cube cloud config --server <url> --token <token>, pass --server, or set CUBE_CLOUD_URL")
	}
	return opts, codexArgs, nil
}

func pushSnapshot(ctx context.Context, m *manager.Manager, opts cloudSyncOptions, id string) error {
	snapshot, err := m.ExportProfileSnapshot(id)
	if err != nil {
		return err
	}
	snapshot.SourceClient = opts.Client

	var account manager.AccountView
	if err := cloudJSON(ctx, http.MethodPost, opts, "/api/sync/push", snapshot, &account); err != nil {
		return err
	}
	fmt.Printf("pushed %s to %s\n", account.ID, opts.Server)
	return nil
}

func pullSnapshot(ctx context.Context, m *manager.Manager, opts cloudSyncOptions, id string) error {
	var snapshot manager.ProfileSnapshot
	if err := cloudJSON(ctx, http.MethodGet, opts, "/api/sync/pull/"+url.PathEscape(id), nil, &snapshot); err != nil {
		return err
	}
	account, err := m.UpsertProfileSnapshot(snapshot)
	if err != nil {
		return err
	}
	fmt.Printf("pulled %s from %s\n", account.ID, opts.Server)
	if opts.Deploy {
		target, err := m.DeployAuth(account.ID, "")
		if err != nil {
			return err
		}
		fmt.Printf("deployed profile files to %s\n", target)
	}
	return nil
}

func claimSnapshot(ctx context.Context, m *manager.Manager, opts cloudSyncOptions) error {
	_, err := claimProfile(ctx, m, opts, opts.Deploy)
	return err
}

func claimProfile(ctx context.Context, m *manager.Manager, opts cloudSyncOptions, deploy bool) (manager.Account, error) {
	var snapshot manager.ProfileSnapshot
	if err := cloudJSON(ctx, http.MethodPost, opts, "/api/sync/claim", nil, &snapshot); err != nil {
		return manager.Account{}, err
	}
	account, err := m.UpsertProfileSnapshot(snapshot)
	if err != nil {
		return manager.Account{}, err
	}
	fmt.Printf("claimed %s from %s\n", account.ID, opts.Server)
	if deploy {
		target, err := m.DeployAuth(account.ID, "")
		if err != nil {
			return manager.Account{}, err
		}
		fmt.Printf("deployed profile files to %s\n", target)
	}
	return account, nil
}

func runSyncDaemon(m *manager.Manager, opts cloudSyncOptions, ids []string) error {
	for {
		for _, id := range ids {
			if err := pushSnapshot(context.Background(), m, opts, id); err != nil {
				fmt.Fprintf(os.Stderr, "cube sync: push %s failed: %v\n", id, err)
			}
			if opts.Pull {
				if err := pullSnapshot(context.Background(), m, opts, id); err != nil {
					fmt.Fprintf(os.Stderr, "cube sync: pull %s failed: %v\n", id, err)
				}
			}
		}
		time.Sleep(opts.Interval)
	}
}

func cloudJSON(ctx context.Context, method string, opts cloudSyncOptions, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(opts.Server, "/")+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if opts.Token != "" {
		req.Header.Set("Authorization", "Bearer "+opts.Token)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var payload struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal(raw, &payload); err == nil && payload.Error != "" {
			return fmt.Errorf("%s: %s", resp.Status, payload.Error)
		}
		return fmt.Errorf("%s", resp.Status)
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return err
		}
	}
	return nil
}

func runDashboard(m *manager.Manager, args []string) error {
	host := "127.0.0.1"
	port := 8720
	cloudToken := strings.TrimSpace(m.CloudToken)
	if value := strings.TrimSpace(os.Getenv("CUBE_CLOUD_TOKEN")); value != "" {
		cloudToken = value
	}

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
		case "--cloud-token":
			if i+1 >= len(args) {
				return fmt.Errorf("missing value for --cloud-token")
			}
			cloudToken = strings.TrimSpace(args[i+1])
			i++
		default:
			return fmt.Errorf("unknown dashboard flag %q", args[i])
		}
	}

	return (&web.Server{
		Manager:    m,
		Host:       host,
		Port:       port,
		CloudToken: cloudToken,
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

	sharedPath, sharedPresent, _ := m.SharedConfigInfo()
	sharedStatus := "missing"
	if sharedPresent {
		sharedStatus = "ready"
	}
	fmt.Printf("shared settings: %s (%s)\n", sharedPath, sharedStatus)
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
	fmt.Println("  cube cloud status")
	fmt.Println("  cube cloud config --server <url> --token <token>")
	fmt.Println("  cube cloud-run [--server <url>] [--token <token>] [-- codex args...]")
	fmt.Println("  cube sync push <id|--all> [--server <url>] [--token <token>]")
	fmt.Println("  cube sync pull <id> [--server <url>] [--token <token>] [--deploy]")
	fmt.Println("  cube sync claim [--server <url>] [--token <token>] [--deploy]")
	fmt.Println("  cube sync daemon <id|--all> [--server <url>] [--token <token>] [--pull] [--interval 60s]")
	fmt.Println("  cube dashboard [--host 127.0.0.1] [--port 8720] [--cloud-token <token>]")
}
