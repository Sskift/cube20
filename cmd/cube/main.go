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
	if len(args) == 0 {
		printHelp()
		return nil
	}
	if args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		printHelp()
		return nil
	}

	m, err := manager.New()
	if err != nil {
		return err
	}

	switch args[0] {
	case "run", "cloud-run":
		return runCloudRun(m, args[1:])
	case "config":
		return runConfig(m, args[1:])
	case "cloud":
		return runCloud(m, args[1:])
	case "clients":
		return runClients(m, args[1:])
	case "report":
		return runReport(m, args[1:])
	case "dashboard":
		return runDashboard(m, args[1:])
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
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

type cloudSyncOptions struct {
	Server   string
	Token    string
	Client   string
	Interval time.Duration
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
	case "relogin":
		return runCloudRelogin(m, args[1:])
	default:
		return fmt.Errorf("usage: cube cloud [status|config --server <url> --token <token>|quota <account-id>|relogin <account-id> [--status ready|drain] [--owner cloud|client]]")
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

	if err := pushUsageFromHome(ctx, opts, account.ID, "", "", m.LiveCodexHome); err != nil {
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
	usageErr := pushUsageFromHome(context.Background(), opts, snapshot.ID, leaseSnapshot.Lease.ID, "", codexHome)
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

func pushUsageFromHome(ctx context.Context, opts cloudSyncOptions, accountID, leaseID, runID, codexHome string) error {
	summary := usage.SummarizeCodexHome(codexHome, time.Now())
	body := struct {
		AccountID string        `json:"accountId"`
		LeaseID   string        `json:"leaseId,omitempty"`
		RunID     string        `json:"runId,omitempty"`
		Usage     usage.Summary `json:"usage"`
	}{
		AccountID: accountID,
		LeaseID:   leaseID,
		RunID:     runID,
		Usage:     summary,
	}
	return cloudJSON(ctx, http.MethodPost, opts, "/api/sync/usage", body, nil)
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
	quotaRefreshInterval := 5 * time.Minute
	cloudToken := strings.TrimSpace(m.CloudToken)
	if value := strings.TrimSpace(os.Getenv("CUBE_CLOUD_TOKEN")); value != "" {
		cloudToken = value
	}
	if value := strings.TrimSpace(os.Getenv("CUBE_QUOTA_REFRESH_INTERVAL")); value != "" {
		interval, err := time.ParseDuration(value)
		if err != nil {
			return fmt.Errorf("invalid CUBE_QUOTA_REFRESH_INTERVAL %q", value)
		}
		quotaRefreshInterval = interval
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
		case "--quota-refresh-interval":
			if i+1 >= len(args) {
				return fmt.Errorf("missing value for --quota-refresh-interval")
			}
			interval, err := time.ParseDuration(args[i+1])
			if err != nil {
				return fmt.Errorf("invalid --quota-refresh-interval %q", args[i+1])
			}
			quotaRefreshInterval = interval
			i++
		default:
			return fmt.Errorf("unknown dashboard flag %q", args[i])
		}
	}

	return (&web.Server{
		Manager:              m,
		Host:                 host,
		Port:                 port,
		CloudToken:           cloudToken,
		QuotaRefreshInterval: quotaRefreshInterval,
	}).ListenAndServe()
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
	fmt.Println("  cube help")
	fmt.Println("  cube cloud status")
	fmt.Println("  cube cloud config --server <url> --token <cube_pat_...>")
	fmt.Println("  cube cloud quota <account-id>")
	fmt.Println("  cube cloud relogin <account-id> [--status ready|drain] [--owner cloud|client]")
	fmt.Println("  cube run [--server <url>] [--token <token>] [--heartbeat 20s] [-- codex args...]")
	fmt.Println("  cube report [--daemon] [--interval 5m]")
	fmt.Println("  cube config edit")
	fmt.Println("  cube config path")
	fmt.Println("  cube clients list")
	fmt.Println("  cube clients create [label]")
	fmt.Println("  cube clients revoke <client-id>")
	fmt.Println("  cube dashboard [--host 127.0.0.1] [--port 8720] [--cloud-token <admin-token>] [--quota-refresh-interval 5m]")
}
