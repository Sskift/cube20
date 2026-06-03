package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"cube20/internal/manager"
	"cube20/internal/quota"
	"cube20/internal/tui"
	"cube20/internal/usage"
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
	case "codex-auto":
		return runCodexAuto(m, args[1:])
	case "run", "cloud-run":
		return runCloudRun(m, args[1:])
	case "config":
		return runConfig(m, args[1:])
	case "lb":
		return runLoadBalancer(m, args[1:])
	case "cloud":
		return runCloud(m, args[1:])
	case "clients":
		return runClients(m, args[1:])
	case "sync":
		return runSync(m, args[1:])
	case "report":
		return runReport(m, args[1:])
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
			return fmt.Errorf("usage: cube accounts status <id> <ready|recovering|drain|disabled>")
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

func runConfig(m *manager.Manager, args []string) error {
	if len(args) == 0 {
		args = []string{"edit"}
	}
	switch args[0] {
	case "edit":
		if len(args) != 1 {
			return fmt.Errorf("usage: cube config edit")
		}
		return editCodexConfig(m)
	case "path":
		if len(args) != 1 {
			return fmt.Errorf("usage: cube config path")
		}
		fmt.Println(manager.CodexConfigPath(m.LiveCodexHome))
		return nil
	default:
		return fmt.Errorf("usage: cube config [edit|path]")
	}
}

func editCodexConfig(m *manager.Manager) error {
	path := manager.CodexConfigPath(m.LiveCodexHome)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(path, []byte{}, 0o600); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	editor := strings.TrimSpace(os.Getenv("EDITOR"))
	if editor == "" {
		editor = "vim"
	}
	parts := splitArgs(editor)
	if len(parts) == 0 {
		parts = []string{"vim"}
	}
	cmd := exec.Command(parts[0], append(parts[1:], path)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func splitArgs(input string) []string {
	if strings.TrimSpace(input) == "" {
		return []string{}
	}
	return strings.Fields(input)
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
	case "quota":
		return runCloudQuota(m, args[1:])
	default:
		return fmt.Errorf("usage: cube cloud [status|config --server <url> --token <token>|quota <account-id>]")
	}
}

func runClients(m *manager.Manager, args []string) error {
	if len(args) == 0 {
		args = []string{"list"}
	}
	switch args[0] {
	case "list":
		clients, err := m.ListClients()
		if err != nil {
			return err
		}
		if len(clients) == 0 {
			fmt.Println("no clients")
			return nil
		}
		fmt.Printf("%-24s %-22s %-8s %s\n", "ID", "LABEL", "ACTIVE", "LAST_SEEN")
		for _, client := range clients {
			lastSeen := "-"
			if !client.LastSeenAt.IsZero() {
				lastSeen = client.LastSeenAt.Format(time.RFC3339)
			}
			fmt.Printf("%-24s %-22s %-8t %s\n", client.ID, client.Label, client.Active, lastSeen)
		}
		return nil
	case "create":
		label := ""
		if len(args) > 1 {
			label = strings.Join(args[1:], " ")
		}
		client, token, err := m.CreateClient(label)
		if err != nil {
			return err
		}
		fmt.Printf("client: %s\nlabel: %s\ntoken: %s\n", client.ID, client.Label, token)
		return nil
	case "revoke":
		if len(args) != 2 {
			return fmt.Errorf("usage: cube clients revoke <client-id>")
		}
		if err := m.RevokeClient(args[1]); err != nil {
			return err
		}
		fmt.Printf("revoked %s\n", args[1])
		return nil
	default:
		return fmt.Errorf("usage: cube clients [list|create [label]|revoke <client-id>]")
	}
}

func runCloudQuota(m *manager.Manager, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: cube cloud quota <account-id>")
	}
	opts := defaultCloudSyncOptions(m)
	if opts.Server == "" {
		return fmt.Errorf("missing cloud server; run cube cloud config --server <url> --token <token>, or set CUBE_CLOUD_URL")
	}
	var result quota.Result
	if err := cloudJSON(context.Background(), http.MethodGet, opts, "/api/sync/quota/"+url.PathEscape(args[0]), nil, &result); err != nil {
		return err
	}
	printQuotaResult(result)
	return nil
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

func runReport(m *manager.Manager, args []string) error {
	opts := defaultCloudSyncOptions(m)
	opts.Interval = 5 * time.Minute
	daemon := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--server":
			if i+1 >= len(args) {
				return fmt.Errorf("missing value for --server")
			}
			opts.Server = strings.TrimSpace(args[i+1])
			i++
		case "--token":
			if i+1 >= len(args) {
				return fmt.Errorf("missing value for --token")
			}
			opts.Token = strings.TrimSpace(args[i+1])
			i++
		case "--client":
			if i+1 >= len(args) {
				return fmt.Errorf("missing value for --client")
			}
			opts.Client = strings.TrimSpace(args[i+1])
			i++
		case "--interval":
			if i+1 >= len(args) {
				return fmt.Errorf("missing value for --interval")
			}
			interval, err := time.ParseDuration(args[i+1])
			if err != nil {
				return fmt.Errorf("invalid --interval %q", args[i+1])
			}
			opts.Interval = interval
			i++
		case "--daemon", "--watch":
			daemon = true
		default:
			return fmt.Errorf("unknown report flag %q", args[i])
		}
	}
	if opts.Server == "" {
		return fmt.Errorf("missing cloud server; run cube cloud config --server <url> --token <token>, pass --server, or set CUBE_CLOUD_URL")
	}
	if opts.Interval < 30*time.Second {
		return fmt.Errorf("--interval must be at least 30s")
	}
	if !daemon {
		return reportLiveOnce(context.Background(), m, opts)
	}
	for {
		if err := reportLiveOnce(context.Background(), m, opts); err != nil {
			fmt.Fprintf(os.Stderr, "cube report: %v\n", err)
		}
		time.Sleep(opts.Interval)
	}
}

func reportLiveOnce(ctx context.Context, m *manager.Manager, opts cloudSyncOptions) error {
	snapshot, err := m.ExportLiveProfileSnapshot("")
	if err != nil {
		return err
	}
	snapshot.SourceClient = opts.Client
	snapshot.OwnerMode = manager.OwnerClient
	snapshot.OwnerClientID = ""

	account, err := pushReportSnapshot(ctx, opts, snapshot)
	if err != nil {
		return err
	}

	result, quotaErr := quota.FetchForCodexHome(ctx, m.LiveCodexHome, time.Now())
	if refreshedSnapshot, err := m.ExportLiveProfileSnapshot(""); err == nil {
		refreshedSnapshot.SourceClient = opts.Client
		refreshedSnapshot.OwnerMode = manager.OwnerClient
		refreshedSnapshot.OwnerClientID = ""
		if refreshedAccount, pushErr := pushReportSnapshot(ctx, opts, refreshedSnapshot); pushErr == nil {
			account = refreshedAccount
		} else if quotaErr == nil {
			return pushErr
		}
	}

	if err := pushUsageFromHome(ctx, opts, account.ID, m.LiveCodexHome); err != nil {
		return err
	}
	if result.Status != "" {
		if err := pushQuotaReport(ctx, opts, account.ID, result); err != nil {
			return err
		}
	}
	fmt.Printf("reported %s owner=client quota=%s source=%s\n", account.ID, result.Status, result.Source)
	if quotaErr != nil {
		return quotaErr
	}
	return nil
}

func pushReportSnapshot(ctx context.Context, opts cloudSyncOptions, snapshot manager.ProfileSnapshot) (manager.AccountView, error) {
	var account manager.AccountView
	if err := cloudJSON(ctx, http.MethodPost, opts, "/api/sync/push", snapshot, &account); err != nil {
		return manager.AccountView{}, err
	}
	return account, nil
}

func pushQuotaReport(ctx context.Context, opts cloudSyncOptions, accountID string, result quota.Result) error {
	body := struct {
		Result quota.Result `json:"result"`
	}{
		Result: result,
	}
	return cloudJSON(ctx, http.MethodPost, opts, "/api/sync/quota/"+url.PathEscape(accountID), body, nil)
}

func runCloudRun(m *manager.Manager, args []string) error {
	opts, codexArgs, err := parseCloudRunOptions(m, args)
	if err != nil {
		return err
	}
	leaseSnapshot, err := claimLeaseSnapshot(context.Background(), opts)
	if err != nil {
		return err
	}
	snapshot := leaseSnapshot.Snapshot
	codexHome, err := writeSnapshotToTempHome(m, snapshot)
	if err != nil {
		return err
	}
	defer os.RemoveAll(codexHome)

	fmt.Fprintf(os.Stderr, "cube: cloud leased %s (%s); using temporary CODEX_HOME\n", snapshot.ID, leaseSnapshot.Lease.ID)
	cmd := codexCommandForHome(codexHome, codexArgs)
	runErr, authErr := runCommandWithLease(context.Background(), opts, leaseSnapshot, codexHome, cmd)
	usageErr := pushUsageFromHome(context.Background(), opts, snapshot.ID, codexHome)
	var releaseErr error
	if authErr == nil {
		releaseErr = releaseLease(context.Background(), opts, leaseSnapshot.Lease.ID, snapshot.ID)
	}

	if runErr != nil {
		if authErr != nil || usageErr != nil || releaseErr != nil {
			return fmt.Errorf("codex failed: %w; auth upload: %v; usage upload: %v; lease release: %v", runErr, authErr, usageErr, releaseErr)
		}
		return runErr
	}
	if authErr != nil {
		return authErr
	}
	if usageErr != nil {
		return usageErr
	}
	return releaseErr
}

func parseCloudRunOptions(m *manager.Manager, args []string) (cloudSyncOptions, []string, error) {
	opts := defaultCloudSyncOptions(m)
	opts.Interval = 20 * time.Second
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
		case "--heartbeat", "--interval":
			if i+1 >= len(args) {
				return opts, nil, fmt.Errorf("missing value for %s", args[i])
			}
			interval, err := time.ParseDuration(args[i+1])
			if err != nil {
				return opts, nil, fmt.Errorf("invalid %s %q", args[i], args[i+1])
			}
			opts.Interval = interval
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
	if opts.Interval < 5*time.Second {
		return opts, nil, fmt.Errorf("--heartbeat must be at least 5s")
	}
	return opts, codexArgs, nil
}

func claimProfileSnapshot(ctx context.Context, opts cloudSyncOptions) (manager.ProfileSnapshot, error) {
	var snapshot manager.ProfileSnapshot
	if err := cloudJSON(ctx, http.MethodPost, opts, "/api/sync/claim", nil, &snapshot); err != nil {
		return manager.ProfileSnapshot{}, err
	}
	if strings.TrimSpace(snapshot.ID) == "" {
		return manager.ProfileSnapshot{}, errors.New("cloud claim returned an empty account id")
	}
	if len(snapshot.Auth) == 0 || string(snapshot.Auth) == "null" {
		return manager.ProfileSnapshot{}, fmt.Errorf("cloud claim for %s returned no auth", snapshot.ID)
	}
	return snapshot, nil
}

func claimLeaseSnapshot(ctx context.Context, opts cloudSyncOptions) (manager.LeaseSnapshot, error) {
	body := struct {
		Client     string `json:"client"`
		TTLSeconds int    `json:"ttlSeconds"`
	}{
		Client:     opts.Client,
		TTLSeconds: leaseTTLSeconds(opts),
	}
	var leaseSnapshot manager.LeaseSnapshot
	if err := cloudJSON(ctx, http.MethodPost, opts, "/api/sync/leases", body, &leaseSnapshot); err != nil {
		return manager.LeaseSnapshot{}, err
	}
	if strings.TrimSpace(leaseSnapshot.Lease.ID) == "" {
		return manager.LeaseSnapshot{}, errors.New("cloud lease returned an empty lease id")
	}
	if strings.TrimSpace(leaseSnapshot.Snapshot.ID) == "" {
		return manager.LeaseSnapshot{}, errors.New("cloud lease returned an empty account id")
	}
	if len(leaseSnapshot.Snapshot.Auth) == 0 || string(leaseSnapshot.Snapshot.Auth) == "null" {
		return manager.LeaseSnapshot{}, fmt.Errorf("cloud lease for %s returned no auth", leaseSnapshot.Snapshot.ID)
	}
	leaseSnapshot.Snapshot.LeaseID = leaseSnapshot.Lease.ID
	leaseSnapshot.Snapshot.Generation = leaseSnapshot.Lease.Generation
	return leaseSnapshot, nil
}

func runCommandWithLease(ctx context.Context, opts cloudSyncOptions, leaseSnapshot manager.LeaseSnapshot, codexHome string, cmd *exec.Cmd) (error, error) {
	authPath := filepath.Join(codexHome, "auth.json")
	lastDigest := localFileDigest(authPath)
	snapshot := leaseSnapshot.Snapshot
	snapshot.LeaseID = leaseSnapshot.Lease.ID
	if snapshot.Generation == 0 {
		snapshot.Generation = leaseSnapshot.Lease.Generation
	}

	stop := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(opts.Interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				lease, err := heartbeatLease(ctx, opts, leaseSnapshot.Lease.ID, snapshot.ID)
				if err != nil {
					fmt.Fprintf(os.Stderr, "cube: lease heartbeat failed: %v\n", err)
				} else if lease.Generation > 0 {
					snapshot.Generation = lease.Generation
				}
				nextDigest := localFileDigest(authPath)
				if nextDigest != "" && nextDigest != lastDigest {
					account, err := pushLeasedAuthFromHome(ctx, opts, snapshot, codexHome)
					if err != nil {
						fmt.Fprintf(os.Stderr, "cube: lease auth upload failed: %v\n", err)
						continue
					}
					snapshot.Generation = account.Generation
					lastDigest = nextDigest
				}
			}
		}
	}()

	runErr := cmd.Run()
	close(stop)
	<-stopped

	account, authErr := pushLeasedAuthFromHome(ctx, opts, snapshot, codexHome)
	if authErr != nil {
		return runErr, authErr
	}
	snapshot.Generation = account.Generation
	return runErr, nil
}

func heartbeatLease(ctx context.Context, opts cloudSyncOptions, leaseID, accountID string) (manager.Lease, error) {
	body := struct {
		AccountID  string `json:"accountId"`
		Client     string `json:"client"`
		TTLSeconds int    `json:"ttlSeconds"`
	}{
		AccountID:  accountID,
		Client:     opts.Client,
		TTLSeconds: leaseTTLSeconds(opts),
	}
	var lease manager.Lease
	err := cloudJSON(ctx, http.MethodPatch, opts, "/api/sync/leases/"+url.PathEscape(leaseID), body, &lease)
	return lease, err
}

func pushLeasedAuthFromHome(ctx context.Context, opts cloudSyncOptions, snapshot manager.ProfileSnapshot, codexHome string) (manager.AccountView, error) {
	authRaw, err := os.ReadFile(filepath.Join(codexHome, "auth.json"))
	if err != nil {
		return manager.AccountView{}, err
	}
	snapshot.Auth = authRaw
	snapshot.Config = ""
	snapshot.SourceClient = opts.Client
	snapshot.UpdatedAt = time.Now()

	var account manager.AccountView
	path := "/api/sync/leases/" + url.PathEscape(snapshot.LeaseID) + "/auth"
	if err := cloudJSON(ctx, http.MethodPut, opts, path, snapshot, &account); err != nil {
		return manager.AccountView{}, err
	}
	return account, nil
}

func releaseLease(ctx context.Context, opts cloudSyncOptions, leaseID, accountID string) error {
	body := struct {
		AccountID string `json:"accountId"`
	}{
		AccountID: accountID,
	}
	return cloudJSON(ctx, http.MethodDelete, opts, "/api/sync/leases/"+url.PathEscape(leaseID), body, nil)
}

func leaseTTLSeconds(opts cloudSyncOptions) int {
	ttl := opts.Interval * 4
	if ttl < 90*time.Second {
		ttl = 90 * time.Second
	}
	if ttl > 30*time.Minute {
		ttl = 30 * time.Minute
	}
	return int(ttl.Seconds())
}

func localFileDigest(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum)
}

func writeSnapshotToTempHome(m *manager.Manager, snapshot manager.ProfileSnapshot) (string, error) {
	codexHome, err := os.MkdirTemp("", "cube20-run-*")
	if err != nil {
		return "", err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(codexHome)
		}
	}()
	authPath := filepath.Join(codexHome, "auth.json")
	if err := os.WriteFile(authPath, snapshot.Auth, 0o600); err != nil {
		return "", err
	}
	localConfig := manager.CodexConfigPath(m.LiveCodexHome)
	if _, err := os.Stat(localConfig); err == nil {
		if err := os.Symlink(localConfig, filepath.Join(codexHome, "config.toml")); err != nil {
			return "", err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	cleanup = false
	return codexHome, nil
}

func codexCommandForHome(codexHome string, args []string) *exec.Cmd {
	cmd := exec.Command("codex", args...)
	cmd.Env = setEnv(os.Environ(), "CODEX_HOME", codexHome)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd
}

func pushAuthFromHome(ctx context.Context, opts cloudSyncOptions, snapshot manager.ProfileSnapshot, codexHome string) error {
	authRaw, err := os.ReadFile(filepath.Join(codexHome, "auth.json"))
	if err != nil {
		return err
	}
	snapshot.Auth = authRaw
	snapshot.Config = ""
	snapshot.SourceClient = opts.Client
	snapshot.UpdatedAt = time.Now()

	var account manager.AccountView
	if err := cloudJSON(ctx, http.MethodPost, opts, "/api/sync/push", snapshot, &account); err != nil {
		return err
	}
	return nil
}

func pushUsageFromHome(ctx context.Context, opts cloudSyncOptions, accountID, codexHome string) error {
	summary := usage.SummarizeCodexHome(codexHome, time.Now())
	body := struct {
		AccountID string        `json:"accountId"`
		Usage     usage.Summary `json:"usage"`
	}{
		AccountID: accountID,
		Usage:     summary,
	}
	return cloudJSON(ctx, http.MethodPost, opts, "/api/sync/usage", body, nil)
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
	snapshot, err := claimProfileSnapshot(ctx, opts)
	if err != nil {
		return manager.Account{}, err
	}
	if strings.TrimSpace(snapshot.LeaseID) != "" {
		defer func() {
			if err := releaseLease(context.Background(), opts, snapshot.LeaseID, snapshot.ID); err != nil {
				fmt.Fprintf(os.Stderr, "cube sync: release lease %s failed: %v\n", snapshot.LeaseID, err)
			}
		}()
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

	fmt.Printf("codex config: %s\n", manager.CodexConfigPath(m.LiveCodexHome))
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
	printQuotaResult(result)
	return nil
}

func printQuotaResult(result quota.Result) {
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

func setEnv(env []string, key, value string) []string {
	prefix := key + "="
	next := make([]string, 0, len(env)+1)
	replaced := false
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			next = append(next, prefix+value)
			replaced = true
		} else {
			next = append(next, item)
		}
	}
	if !replaced {
		next = append(next, prefix+value)
	}
	return next
}

func printHelp() {
	fmt.Println("cube - Codex account-pool manager")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  cube")
	fmt.Println("  cube run [--server <url>] [--token <token>] [--heartbeat 20s] [-- codex args...]")
	fmt.Println("  cube cloud config --server <url> --token <cube_pat_...>")
	fmt.Println("  cube cloud quota <account-id>")
	fmt.Println("  cube report [--daemon] [--interval 5m]")
	fmt.Println("  cube config edit")
	fmt.Println("  cube config path")
	fmt.Println("  cube clients list")
	fmt.Println("  cube clients create [label]")
	fmt.Println("  cube clients revoke <client-id>")
	fmt.Println("  cube dashboard [--host 127.0.0.1] [--port 8720] [--cloud-token <admin-token>]")
	fmt.Println()
	fmt.Println("Legacy/local admin tools:")
	fmt.Println("  cube accounts list")
	fmt.Println("  cube accounts add <id> [label]")
	fmt.Println("  cube accounts import [id] [label]")
	fmt.Println("  cube accounts login <id>")
	fmt.Println("  cube accounts quota <id>")
	fmt.Println("  cube accounts usage <id>")
	fmt.Println("  cube accounts status <id> <ready|recovering|drain|disabled>")
	fmt.Println("  cube accounts delete <id>")
	fmt.Println("  cube profile deploy <id>")
	fmt.Println("  cube auth deploy <id>")
	fmt.Println("  cube codex <account-id> [codex args...]")
	fmt.Println("  cube codex-auto [codex args...]")
	fmt.Println("  cube lb [status|pick|reset]")
	fmt.Println("  cube cloud status")
	fmt.Println("  cube sync push <id|--all> [--server <url>] [--token <token>]")
	fmt.Println("  cube sync pull <id> [--server <url>] [--token <token>] [--deploy]")
	fmt.Println("  cube sync claim [--server <url>] [--token <token>] [--deploy]")
	fmt.Println("  cube sync daemon <id|--all> [--server <url>] [--token <token>] [--pull] [--interval 60s]")
}
