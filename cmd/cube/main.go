package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"cube20/internal/manager"
	"cube20/internal/quota"
	"cube20/internal/usage"
	"cube20/internal/web"
)

// swapRemainingThresholdClient mirrors the cloud's swapRemainingThreshold: when
// the local 5h window has less than this percent remaining, proactively swap.
const swapRemainingThresholdClient = 10.0

// rolloutSessionIDRe extracts the trailing session UUID from a codex rollout
// filename: rollout-<ISO8601-with-dashes>-<uuid>.jsonl.
var rolloutSessionIDRe = regexp.MustCompile(`([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})\.jsonl$`)

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
	// Release the Postgres pool (if any) before the process exits. No-op in
	// file mode and idempotent, so the dashboard's own shutdown Close is fine.
	defer m.Close()

	switch args[0] {
	case "run", "cloud-run":
		return runCloudRun(m, args[1:])
	case "config":
		return runConfig(m, args[1:])
	case "cloud":
		return runCloud(m, args[1:])
	case "device":
		return runDevice(m, args[1:])
	case "clients":
		return runClients(m, args[1:])
	case "workspace", "workspaces":
		return runWorkspace(m, args[1:])
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
	Server      string
	Token       string
	Client      string
	Workspace   string
	Device      string
	DeviceLabel string
	Interval    time.Duration
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
		return fmt.Errorf("usage: cube cloud [status|config --server <url> --token <token> [--device-id <id>] [--device-label <name>]|quota <account-id>|relogin <account-id> [--status ready|drain] [--owner cloud|client] [--auth-file <path>]]")
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
	if settings, err := m.DeviceSettings(); err == nil {
		fmt.Printf("device id: %s\n", emptyDash(settings.DeviceID))
		fmt.Printf("device label: %s\n", emptyDash(settings.DeviceLabel))
	}
	return nil
}

func configureCloud(m *manager.Manager, args []string) error {
	server := ""
	token := ""
	deviceID := ""
	deviceLabel := ""
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
		case "--device-id":
			if i+1 >= len(args) {
				return fmt.Errorf("missing value for --device-id")
			}
			deviceID = strings.TrimSpace(args[i+1])
			i++
		case "--device-label":
			if i+1 >= len(args) {
				return fmt.Errorf("missing value for --device-label")
			}
			deviceLabel = strings.TrimSpace(args[i+1])
			i++
		default:
			return fmt.Errorf("unknown cloud config flag %q", args[i])
		}
	}
	if server == "" && token == "" && deviceID == "" && deviceLabel == "" {
		return fmt.Errorf("usage: cube cloud config --server <url> --token <token> [--device-id <id>] [--device-label <name>]")
	}
	// UpdateDeviceSettings preserves device_id/device_label (which the Manager
	// struct does not mirror) and only overwrites non-empty values.
	settings, err := m.UpdateDeviceSettings(server, token, deviceID, deviceLabel)
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
	fmt.Printf("device id: %s\n", emptyDash(settings.DeviceID))
	fmt.Printf("device label: %s\n", emptyDash(settings.DeviceLabel))
	return nil
}

const deviceUsage = "usage: cube device [status|show|config --server <url> --token <cube_dev_...> [--id <deviceId>] [--label <name>]]"

func runDevice(m *manager.Manager, args []string) error {
	if len(args) == 0 {
		return printDeviceStatus(m)
	}
	switch args[0] {
	case "status", "show":
		if len(args) != 1 {
			return fmt.Errorf(deviceUsage)
		}
		return printDeviceStatus(m)
	case "config":
		return configureDevice(m, args[1:])
	default:
		return fmt.Errorf(deviceUsage)
	}
}

func configureDevice(m *manager.Manager, args []string) error {
	server := ""
	token := ""
	deviceID := ""
	deviceLabel := ""
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
		case "--id", "--device-id":
			if i+1 >= len(args) {
				return fmt.Errorf("missing value for %s", args[i])
			}
			deviceID = strings.TrimSpace(args[i+1])
			i++
		case "--label", "--device-label":
			if i+1 >= len(args) {
				return fmt.Errorf("missing value for %s", args[i])
			}
			deviceLabel = strings.TrimSpace(args[i+1])
			i++
		default:
			return fmt.Errorf("unknown device config flag %q", args[i])
		}
	}
	if server == "" && token == "" && deviceID == "" && deviceLabel == "" {
		return fmt.Errorf(deviceUsage)
	}
	settings, err := m.UpdateDeviceSettings(server, token, deviceID, deviceLabel)
	if err != nil {
		return err
	}
	fmt.Printf("device config saved to %s\n", m.SettingsPath)
	fmt.Printf("server: %s\n", emptyDash(settings.CloudURL))
	fmt.Printf("token: %s\n", maskToken(settings.CloudToken))
	fmt.Printf("device id: %s\n", emptyDash(settings.DeviceID))
	fmt.Printf("device label: %s\n", emptyDash(settings.DeviceLabel))
	return nil
}

func printDeviceStatus(m *manager.Manager) error {
	settings, err := m.DeviceSettings()
	if err != nil {
		return err
	}
	fmt.Printf("settings: %s\n", m.SettingsPath)
	fmt.Printf("server: %s\n", emptyDash(settings.CloudURL))
	fmt.Printf("token: %s\n", maskToken(settings.CloudToken))
	fmt.Printf("device id: %s\n", emptyDash(settings.DeviceID))
	fmt.Printf("device label: %s\n", emptyDash(settings.DeviceLabel))
	return nil
}

// maskToken reports whether a token is set without ever printing it in full. It
// shows a short leading fragment (e.g. the cube_dev_/cube_pat_ kind prefix) and
// masks the remainder so the secret is never echoed.
func maskToken(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return "missing"
	}
	if len(token) <= 8 {
		return "configured (****)"
	}
	return fmt.Sprintf("configured (%s****)", token[:8])
}

func defaultCloudSyncOptions(m *manager.Manager) cloudSyncOptions {
	opts := cloudSyncOptions{
		Server:   strings.TrimSpace(m.CloudURL),
		Token:    strings.TrimSpace(m.CloudToken),
		Interval: 60 * time.Second,
	}
	// Device identity (and the CUBE_DEVICE_TOKEN alias) is not mirrored onto the
	// Manager struct, so read it from settings.toml with env overrides applied.
	if settings, err := m.DeviceSettings(); err == nil {
		if value := strings.TrimSpace(settings.CloudURL); value != "" {
			opts.Server = value
		}
		if value := strings.TrimSpace(settings.CloudToken); value != "" {
			opts.Token = value
		}
		opts.Device = strings.TrimSpace(settings.DeviceID)
		opts.DeviceLabel = strings.TrimSpace(settings.DeviceLabel)
	}
	if value := strings.TrimSpace(os.Getenv("CUBE_CLOUD_URL")); value != "" {
		opts.Server = value
	}
	if value := strings.TrimSpace(os.Getenv("CUBE_CLOUD_TOKEN")); value != "" {
		opts.Token = value
	}
	// CUBE_DEVICE_TOKEN is an alias for the bearer token that WINS over
	// CUBE_CLOUD_TOKEN when both are set.
	if value := strings.TrimSpace(os.Getenv("CUBE_DEVICE_TOKEN")); value != "" {
		opts.Token = value
	}
	if value := strings.TrimSpace(os.Getenv("CUBE_DEVICE_ID")); value != "" {
		opts.Device = value
	}
	if value := strings.TrimSpace(os.Getenv("CUBE_DEVICE_LABEL")); value != "" {
		opts.DeviceLabel = value
	}
	if value := strings.TrimSpace(os.Getenv("CUBE_WORKSPACE")); value != "" {
		opts.Workspace = value
	}
	if host, err := os.Hostname(); err == nil {
		opts.Client = host
		if opts.DeviceLabel == "" {
			opts.DeviceLabel = host
		}
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
		case "--device":
			if i+1 >= len(args) {
				return fmt.Errorf("missing value for --device")
			}
			opts.Device = strings.TrimSpace(args[i+1])
			i++
		case "--device-label":
			if i+1 >= len(args) {
				return fmt.Errorf("missing value for --device-label")
			}
			opts.DeviceLabel = strings.TrimSpace(args[i+1])
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
	// Derive a cancellable context from SIGINT/SIGTERM. signal.NotifyContext
	// REMOVES Go's default signal handler, so a Ctrl-C cancels ctx instead of
	// killing the process — the function then returns normally through its
	// defers (scrubAuth below) and the post-loop cleanupRun, so the lease is
	// released and the credential scrubbed before exit rather than leaking until
	// TTL. The codex child shares our process group and the terminal delivers
	// the same SIGINT to it, so it stops on its own and cmd.Run() returns.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	leaseSnapshot, err := claimLeaseSnapshot(ctx, opts)
	if err != nil {
		return err
	}

	pruneOldRuns(filepath.Join(m.StateDir, "runs"))
	codexHome, err := stableRunHome(m.StateDir)
	if err != nil {
		return err
	}
	defer scrubAuth(codexHome)

	lease := leaseSnapshot
	if err := writeSnapshotToStableHome(m, lease.Snapshot, codexHome); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "cube: cloud leased %s (%s); using CODEX_HOME %s\n", lease.Snapshot.ID, lease.Lease.ID, codexHome)

	var runErr, authErr error
	args2 := codexArgs
	for {
		cmd := codexCommandForHome(codexHome, args2)
		var swap bool
		swap, runErr, authErr = runCommandWithLease(ctx, opts, lease, codexHome, cmd)
		if !swap {
			// User exited or codex finished without a swap request.
			break
		}

		sid, sidErr := newestSessionID(codexHome)
		if sidErr != nil {
			fmt.Fprintf(os.Stderr, "cube: cannot resume after swap: %v\n", sidErr)
			break
		}
		newLease, claimErr := claimLeaseSnapshot(ctx, opts)
		if claimErr != nil {
			fmt.Fprintf(os.Stderr, "cube: swap claim failed: %v\n", claimErr)
			break
		}
		// Release the OLD lease only after the NEW one is claimed so the cloud
		// never re-selects the same (rate-limited) account.
		if relErr := releaseLease(ctx, opts, lease.Lease.ID, lease.Snapshot.ID); relErr != nil {
			fmt.Fprintf(os.Stderr, "cube: releasing prior lease failed: %v\n", relErr)
		}
		lease = newLease
		if err := writeSnapshotToStableHome(m, lease.Snapshot, codexHome); err != nil {
			runErr = err
			break
		}
		fmt.Fprintf(os.Stderr, "cube: swapped to %s (%s); resuming session %s\n", lease.Snapshot.ID, lease.Lease.ID, sid)
		args2 = []string{"resume", sid}
	}

	// On a signal-driven cancel the final auth upload inside runCommandWithLease
	// fails with "context canceled" and codex exits non-zero from the SIGINT;
	// neither is a real failure. We still RELEASE the lease (the user is stopping
	// cleanly and the account must be freed now, not at TTL), and we suppress the
	// cancel-artifact errors so Ctrl-C exits cleanly. A genuine releaseErr is
	// still surfaced.
	canceled := ctx.Err() != nil
	usageErr, releaseErr := cleanupRun(ctx, opts, lease, codexHome, authErr == nil || canceled)

	if canceled {
		return releaseErr
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

// cleanupRun performs end-of-run teardown that MUST happen on every exit path,
// including a user Ctrl-C: it pushes the final usage summary and (when release
// is true) releases the lease back to the cloud so the account is not held until
// its TTL. It is deliberately context-tolerant — a cancelled ctx (signal) still
// runs these calls so the lease is freed promptly. Returns the usage-push and
// lease-release errors separately so the caller can fold them into its error.
//
// release should be false only when the lease must NOT be released here (e.g. a
// mid-run auth upload already failed and ownership is uncertain); the swap loop
// itself already releases the OLD lease before claiming the next one.
func cleanupRun(ctx context.Context, opts cloudSyncOptions, lease manager.LeaseSnapshot, codexHome string, release bool) (usageErr, releaseErr error) {
	// If the run context was cancelled by a signal, derive a fresh short-lived
	// context so the teardown HTTP calls are not instantly aborted; the whole
	// point is to release the lease as the process winds down.
	cleanupCtx := ctx
	if ctx.Err() != nil {
		var cancel context.CancelFunc
		cleanupCtx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
	}
	usageErr = pushUsageFromHome(cleanupCtx, opts, lease.Snapshot.ID, lease.Lease.ID, "", codexHome)
	if release {
		releaseErr = releaseLease(cleanupCtx, opts, lease.Lease.ID, lease.Snapshot.ID)
	}
	return usageErr, releaseErr
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
		case "--workspace":
			if i+1 >= len(args) {
				return opts, nil, fmt.Errorf("missing value for --workspace")
			}
			opts.Workspace = strings.TrimSpace(args[i+1])
			i++
		case "--device":
			if i+1 >= len(args) {
				return opts, nil, fmt.Errorf("missing value for --device")
			}
			opts.Device = strings.TrimSpace(args[i+1])
			i++
		case "--device-label":
			if i+1 >= len(args) {
				return opts, nil, fmt.Errorf("missing value for --device-label")
			}
			opts.DeviceLabel = strings.TrimSpace(args[i+1])
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
		Workspace  string `json:"workspace,omitempty"`
		DeviceId   string `json:"deviceId,omitempty"`
		TTLSeconds int    `json:"ttlSeconds"`
	}{
		Client:     opts.Client,
		Workspace:  opts.Workspace,
		DeviceId:   opts.Device,
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

func runCommandWithLease(ctx context.Context, opts cloudSyncOptions, leaseSnapshot manager.LeaseSnapshot, codexHome string, cmd *exec.Cmd) (bool, error, error) {
	authPath := filepath.Join(codexHome, "auth.json")
	lastDigest := localFileDigest(authPath)
	snapshot := leaseSnapshot.Snapshot
	snapshot.LeaseID = leaseSnapshot.Lease.ID
	if snapshot.Generation == 0 {
		snapshot.Generation = leaseSnapshot.Lease.Generation
	}

	var swapOnce sync.Once
	var swapRequested bool
	requestSwap := func(reason string) {
		swapOnce.Do(func() {
			swapRequested = true
			fmt.Fprintf(os.Stderr, "cube: account swap requested (%s); signaling codex to stop\n", reason)
			if cmd.Process != nil {
				_ = cmd.Process.Signal(syscall.SIGTERM)
			}
		})
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
				lease, shouldSwap, err := heartbeatLease(ctx, opts, leaseSnapshot.Lease.ID, snapshot.ID, codexHome)
				if err != nil {
					fmt.Fprintf(os.Stderr, "cube: lease heartbeat failed: %v\n", err)
				} else if lease.Generation > 0 {
					snapshot.Generation = lease.Generation
				}
				nextDigest := localFileDigest(authPath)
				if nextDigest != "" && nextDigest != lastDigest {
					account, pushErr := pushLeasedAuthFromHome(ctx, opts, snapshot, codexHome)
					if pushErr != nil {
						fmt.Fprintf(os.Stderr, "cube: lease auth upload failed: %v\n", pushErr)
					} else {
						snapshot.Generation = account.Generation
						lastDigest = nextDigest
					}
				}
				if swap, reason := swapDecision(usage.LatestRateLimits(codexHome), shouldSwap); swap {
					requestSwap(reason)
					return
				}
			}
		}
	}()

	runErr := cmd.Run()
	close(stop)
	<-stopped

	// If we intentionally signaled codex to stop for a swap, treat the run as a
	// swap (not a user exit) and suppress the SIGTERM exit error.
	swap := swapRequested
	if swap {
		runErr = nil
	}

	account, authErr := pushLeasedAuthFromHome(ctx, opts, snapshot, codexHome)
	if authErr != nil {
		return swap, runErr, authErr
	}
	snapshot.Generation = account.Generation
	return swap, runErr, nil
}

func heartbeatLease(ctx context.Context, opts cloudSyncOptions, leaseID, accountID, codexHome string) (manager.Lease, bool, error) {
	body := struct {
		AccountID        string         `json:"accountId"`
		Client           string         `json:"client"`
		DeviceId         string         `json:"deviceId,omitempty"`
		TTLSeconds       int            `json:"ttlSeconds"`
		FiveHour         *quota.Window  `json:"fiveHour,omitempty"`
		Quotas           []quota.Window `json:"quotas,omitempty"`
		RateLimitReached bool           `json:"rateLimitReached,omitempty"`
	}{
		AccountID:  accountID,
		Client:     opts.Client,
		DeviceId:   opts.Device,
		TTLSeconds: leaseTTLSeconds(opts),
	}
	rl := usage.LatestRateLimits(codexHome)
	body.FiveHour = rateLimitsToWindow(rl)
	body.Quotas = rateLimitsToWindows(rl)
	body.RateLimitReached = rl.ReachedType != ""

	var resp struct {
		manager.Lease
		ShouldSwap bool `json:"shouldSwap"`
	}
	err := cloudJSON(ctx, http.MethodPatch, opts, "/api/sync/leases/"+url.PathEscape(leaseID), body, &resp)
	return resp.Lease, resp.ShouldSwap, err
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

// newestSessionID walks <codexHome>/sessions recursively, picks the
// lexicographically-greatest rollout-*.jsonl filename (rollout names start with
// an ISO timestamp, so lexicographic order is chronological), and returns the
// embedded session UUID. Returns an error if no rollout file is found.
func newestSessionID(codexHome string) (string, error) {
	sessionsDir := filepath.Join(codexHome, "sessions")
	var newestName, newestPath string
	_ = filepath.WalkDir(sessionsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasPrefix(name, "rollout-") || !strings.HasSuffix(name, ".jsonl") {
			return nil
		}
		if newestName == "" || name > newestName {
			newestName = name
			newestPath = path
		}
		return nil
	})
	if newestName == "" {
		return "", fmt.Errorf("no rollout session found under %s", sessionsDir)
	}
	match := rolloutSessionIDRe.FindStringSubmatch(newestName)
	if match == nil {
		return "", fmt.Errorf("could not extract session id from %s", newestPath)
	}
	return match[1], nil
}

// swapDecision is the pure account-swap policy. It prefers the reactive hard
// limit, then the cloud's proactive hint, then a local proactive threshold.
func swapDecision(rl usage.RateLimits, cloudShouldSwap bool) (bool, string) {
	if rl.ReachedType != "" {
		return true, "hard limit reached: " + rl.ReachedType
	}
	if cloudShouldSwap {
		return true, "cloud advised swap"
	}
	if rl.Found && (100-rl.FiveHourUsedPercent) < swapRemainingThresholdClient {
		return true, "5h remaining low"
	}
	return false, ""
}

// rateLimitsToWindow maps a local RateLimits snapshot onto the quota.Window the
// cloud heartbeat expects. Returns nil when no rate_limits were parsed.
func rateLimitsToWindow(rl usage.RateLimits) *quota.Window {
	if !rl.Found {
		return nil
	}
	resetsAt := ""
	if !rl.FiveHourResetsAt.IsZero() {
		resetsAt = rl.FiveHourResetsAt.Format(time.RFC3339)
	}
	return &quota.Window{
		Key:              "five_hour",
		Label:            "5h",
		UsedPercent:      rl.FiveHourUsedPercent,
		RemainingPercent: 100 - rl.FiveHourUsedPercent,
		ResetsAt:         resetsAt,
	}
}

// rateLimitsToWindows builds the full set of quota windows the client knows about
// from its parsed rate-limit record: always the 5h window, plus the 7d window
// when the secondary limit was present. The cloud relies entirely on these
// client reports during a lease (it does not probe quota itself), so reporting
// both windows keeps the binding-window model accurate while an account is held.
func rateLimitsToWindows(rl usage.RateLimits) []quota.Window {
	if !rl.Found {
		return nil
	}
	windows := []quota.Window{}
	if w := rateLimitsToWindow(rl); w != nil {
		windows = append(windows, *w)
	}
	if !rl.SevenDayResetsAt.IsZero() || rl.SevenDayUsedPercent > 0 {
		sevenReset := ""
		if !rl.SevenDayResetsAt.IsZero() {
			sevenReset = rl.SevenDayResetsAt.Format(time.RFC3339)
		}
		windows = append(windows, quota.Window{
			Key:              "seven_day",
			Label:            "7d",
			UsedPercent:      rl.SevenDayUsedPercent,
			RemainingPercent: 100 - rl.SevenDayUsedPercent,
			ResetsAt:         sevenReset,
		})
	}
	return windows
}

// scrubAuth removes the leased credentials (auth.json and the config.toml
// symlink) from codexHome while preserving the sessions/ subtree needed to
// resume after a swap. Missing files are not an error.
func scrubAuth(codexHome string) error {
	for _, name := range []string{"auth.json", "config.toml"} {
		if err := os.Remove(filepath.Join(codexHome, name)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

// stableRunHome creates <baseDir>/runs/<runID>/ where runID is 32 hex chars from
// crypto/rand, and returns the created directory. Unlike a temp dir it persists
// across account swaps so codex sessions survive for resume.
func stableRunHome(baseDir string) (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	dir := filepath.Join(baseDir, "runs", hex.EncodeToString(raw[:]))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// pruneOldRuns best-effort reclaims stale run directories under runsDir. It has
// two jobs:
//
//  1. Scrub leaked credentials: if a run dir's auth.json has an mtime older than
//     staleAuthCutoff (1h), the owning run is almost certainly crashed/abandoned
//     — a live run rewrites auth.json on every heartbeat/swap, so a fresh mtime
//     means a process is still using it, while an hour-stale mtime does not. We
//     remove (scrub) that auth.json so a SIGKILLed run (where the deferred
//     scrubAuth never ran) does not leave the credential on disk indefinitely.
//     We deliberately key on mtime age, NOT on presence, so we never scrub a
//     currently-running sibling run's auth.json.
//  2. Reclaim space: if the dir itself is older than retentionCutoff (7d) it is
//     removed wholesale (sessions and all). Dirs newer than that keep sessions/
//     so a recent run can still be resumed within the retention window.
//
// All errors are ignored (best-effort).
func pruneOldRuns(runsDir string) {
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		return
	}
	now := time.Now()
	staleAuthCutoff := now.Add(-1 * time.Hour)
	retentionCutoff := now.Add(-7 * 24 * time.Hour)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(runsDir, entry.Name())

		// Capture the dir's mtime BEFORE any scrub: removing a file inside it
		// bumps the parent dir's mtime, which would otherwise mask an aged dir
		// and defeat the retention check below.
		var dirMod time.Time
		if info, err := entry.Info(); err == nil {
			dirMod = info.ModTime()
		}

		// (1) Scrub an abandoned credential based on its own mtime age, leaving
		// sessions/ intact for resume within the retention window.
		authPath := filepath.Join(dir, "auth.json")
		if info, err := os.Stat(authPath); err == nil && info.ModTime().Before(staleAuthCutoff) {
			_ = os.Remove(authPath)
			// config.toml is only a symlink to the live config; drop it too so the
			// scrubbed run mirrors a clean scrubAuth.
			_ = os.Remove(filepath.Join(dir, "config.toml"))
		}

		// (2) Reclaim the whole dir once it is older than the retention window.
		if !dirMod.IsZero() && dirMod.Before(retentionCutoff) {
			_ = os.RemoveAll(dir)
		}
	}
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
	if err := writeSnapshotToStableHome(m, snapshot, codexHome); err != nil {
		return "", err
	}
	cleanup = false
	return codexHome, nil
}

// writeSnapshotToStableHome writes the leased auth.json (0600) and symlinks the
// live config.toml into an already-existing codexHome directory. Both the
// credential file and the config symlink are installed atomically via
// write-to-temp + os.Rename so a concurrent `codex resume` reading the same
// directory across a swap never observes a partial auth.json or a missing config
// symlink. The existing auth.json/config.toml are replaced so it can be called
// again across swaps. sessions/ is never touched.
func writeSnapshotToStableHome(m *manager.Manager, snapshot manager.ProfileSnapshot, codexHome string) error {
	authPath := filepath.Join(codexHome, "auth.json")
	if err := writeFileAtomic(authPath, snapshot.Auth, 0o600); err != nil {
		return err
	}
	configLink := filepath.Join(codexHome, "config.toml")
	localConfig := manager.CodexConfigPath(m.LiveCodexHome)
	if _, err := os.Stat(localConfig); err == nil {
		if err := symlinkAtomic(localConfig, configLink); err != nil {
			return err
		}
	} else if errors.Is(err, os.ErrNotExist) {
		// No live config: ensure any stale link/file from a prior run is gone so
		// codex does not read an outdated config.
		if err := os.Remove(configLink); err != nil && !os.IsNotExist(err) {
			return err
		}
	} else {
		return err
	}
	return nil
}

// writeFileAtomic writes data to a sibling temp file then renames it over path.
// os.Rename is atomic on the same filesystem, so a reader sees either the old or
// the new contents — never a truncated/partial file. The temp file inherits the
// target's basename so it lands in the same directory (and thus filesystem).
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	// Clean up a leftover temp from a prior crash before writing.
	if err := os.Remove(tmp); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// symlinkAtomic creates a symlink at linkPath -> target atomically by creating
// it at a temp name and renaming over linkPath, eliminating the
// remove-then-symlink window during which no config symlink exists.
func symlinkAtomic(target, linkPath string) error {
	tmp := linkPath + ".tmp"
	if err := os.Remove(tmp); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	if err := os.Rename(tmp, linkPath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
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

// runDashboard parses flags and starts the web server. Graceful shutdown on
// SIGINT/SIGTERM (signal.NotifyContext, stopping the quota worker, draining
// in-flight requests via http.Server.Shutdown, and closing the manager's DB
// pool) is implemented inside web.Server.ListenAndServe; run()'s deferred
// m.Close is the idempotent backstop. We deliberately do NOT install a second
// signal.NotifyContext here, which would race the server's own handler.
func runDashboard(m *manager.Manager, args []string) error {
	host := "127.0.0.1"
	port := 8720
	// Quota is client-driven: members running `cube run` report their 5h/7d
	// windows on every lease heartbeat, and the cloud persists those. The cloud
	// does NOT probe quota itself by default (interval 0 = worker off). Operators
	// can still opt into background cloud probing with --quota-refresh-interval or
	// CUBE_QUOTA_REFRESH_INTERVAL if they want the old self-refresh behavior.
	quotaRefreshInterval := time.Duration(0)
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
	fmt.Println("  cube cloud config --server <url> --token <cube_pat_...> [--device-id <id>] [--device-label <name>]")
	fmt.Println("  cube cloud quota <account-id>")
	fmt.Println("  cube cloud relogin <account-id> [--status ready|drain] [--owner cloud|client] [--auth-file <path>]")
	fmt.Println("  cube device status")
	fmt.Println("  cube device show")
	fmt.Println("  cube device config --server <url> --token <cube_dev_...> [--id <deviceId>] [--label <name>]")
	fmt.Println("  cube run [--server <url>] [--token <token>] [--workspace <id>] [--device <id>] [--device-label <name>] [--heartbeat 20s] [-- codex args...]")
	fmt.Println("  cube report [--daemon] [--interval 5m] [--device <id>] [--device-label <name>]")
	fmt.Println("  cube config edit")
	fmt.Println("  cube config path")
	fmt.Println("  cube clients list")
	fmt.Println("  cube clients create [label]")
	fmt.Println("  cube clients revoke <client-id>")
	fmt.Println("  cube workspace list")
	fmt.Println("  cube workspace create <name>")
	fmt.Println("  cube workspace members <workspace-id>")
	fmt.Println("  cube workspace invite <workspace-id> <client-id> [--role admin|member]")
	fmt.Println("  cube workspace grant-admin <workspace-id> <client-id>")
	fmt.Println("  cube workspace remove <workspace-id> <client-id>")
	fmt.Println("  cube dashboard [--host 127.0.0.1] [--port 8720] [--cloud-token <admin-token>] [--quota-refresh-interval 5m]")
}
