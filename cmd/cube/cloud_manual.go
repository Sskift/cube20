package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cube20/internal/manager"
)

const (
	defaultManualLiveTTL    = 8 * time.Hour
	defaultManualLiveHolder = "manual-direct-codex"
)

type cloudBorrowLiveOptions struct {
	Account string
	TTL     time.Duration
	Holder  string
}

type cloudReturnLiveOptions struct {
	Account string
	LeaseID string
}

type cloudKeepaliveLiveOptions struct {
	Interval    time.Duration
	Watch       bool
	Workspace   string
	Device      string
	DeviceLabel string
}

type manualBorrowRequest struct {
	Account     string          `json:"account"`
	Auth        json.RawMessage `json:"auth"`
	TTLSeconds  int             `json:"ttlSeconds"`
	Holder      string          `json:"holder"`
	Client      string          `json:"client"`
	Workspace   string          `json:"workspace,omitempty"`
	DeviceID    string          `json:"deviceId,omitempty"`
	DeviceLabel string          `json:"deviceLabel,omitempty"`
}

type manualReturnRequest struct {
	Account     string          `json:"account,omitempty"`
	LeaseID     string          `json:"leaseId,omitempty"`
	Auth        json.RawMessage `json:"auth,omitempty"`
	Client      string          `json:"client,omitempty"`
	Workspace   string          `json:"workspace,omitempty"`
	DeviceID    string          `json:"deviceId,omitempty"`
	DeviceLabel string          `json:"deviceLabel,omitempty"`
}

type manualReturnResponse struct {
	Released bool                `json:"released"`
	Account  manager.AccountView `json:"account"`
	Lease    manager.Lease       `json:"lease"`
}

func runCloudBorrowLive(m *manager.Manager, args []string) error {
	manual, err := parseCloudBorrowLiveOptions(args)
	if err != nil {
		return err
	}
	opts := defaultCloudSyncOptions(m)
	if opts.Server == "" {
		return fmt.Errorf("missing cloud server; run cube cloud config --server <url> --token <token>, or set CUBE_CLOUD_URL")
	}

	authRaw, authPath, err := readLiveAuthFile(m)
	if err != nil {
		return err
	}
	body := manualBorrowRequest{
		Account:     manual.Account,
		Auth:        authRaw,
		TTLSeconds:  int(manual.TTL.Seconds()),
		Holder:      manual.Holder,
		Client:      opts.Client,
		Workspace:   opts.Workspace,
		DeviceID:    opts.Device,
		DeviceLabel: opts.DeviceLabel,
	}
	var lease manager.LeaseSnapshot
	if err := cloudJSON(context.Background(), http.MethodPost, opts, "/api/sync/manual-borrow", body, &lease); err != nil {
		return err
	}

	account := firstNonEmptyString(lease.Lease.AccountID, lease.Snapshot.ID, manual.Account)
	leaseID := firstNonEmptyString(lease.Lease.ID, lease.Snapshot.LeaseID)
	fmt.Printf("borrowed %s lease=%s holder=%s ttl=%s auth=%s\n", account, emptyDash(leaseID), manual.Holder, manual.TTL, authPath)
	return nil
}

func runCloudReturnLive(m *manager.Manager, args []string) error {
	manual, err := parseCloudReturnLiveOptions(args)
	if err != nil {
		return err
	}
	opts := defaultCloudSyncOptions(m)
	if opts.Server == "" {
		return fmt.Errorf("missing cloud server; run cube cloud config --server <url> --token <token>, or set CUBE_CLOUD_URL")
	}

	account := manual.Account
	leaseID := manual.LeaseID
	authRaw, authPresent, err := readOptionalLiveAuthFile(m)
	if err != nil {
		return err
	}
	if authPresent {
		diag, identifyErr := identifyLiveAuth(context.Background(), m, opts)
		if identifyErr != nil {
			if account == "" && leaseID == "" {
				return fmt.Errorf("identify live auth failed: %w", identifyErr)
			}
			fmt.Fprintf(os.Stderr, "cube: warning: identify live auth failed, using explicit return-live parameters: %v\n", identifyErr)
		} else if diag.Matched {
			if account == "" {
				account = diag.Account.ID
			}
			if leaseID == "" {
				leaseID = diag.Account.LeaseID
			}
		}
	}
	if account == "" && leaseID == "" {
		return fmt.Errorf("return-live needs a matched live auth or --account <id-or-label>/--lease <lease-id>")
	}

	body := manualReturnRequest{
		Account:     account,
		LeaseID:     leaseID,
		Auth:        authRaw,
		Client:      opts.Client,
		Workspace:   opts.Workspace,
		DeviceID:    opts.Device,
		DeviceLabel: opts.DeviceLabel,
	}
	var out manualReturnResponse
	if err := cloudJSON(context.Background(), http.MethodPost, opts, "/api/sync/manual-return", body, &out); err != nil {
		return err
	}

	printedAccount := firstNonEmptyString(out.Account.ID, out.Lease.AccountID, account)
	printedLease := firstNonEmptyString(out.Lease.ID, leaseID)
	fmt.Printf("returned %s lease=%s\n", emptyDash(printedAccount), emptyDash(printedLease))
	return nil
}

func runCloudKeepaliveLive(m *manager.Manager, args []string) error {
	manual, err := parseCloudKeepaliveLiveOptions(args)
	if err != nil {
		return err
	}
	opts := defaultCloudSyncOptions(m)
	if manual.Interval > 0 {
		opts.Interval = manual.Interval
	}
	if manual.Workspace != "" {
		opts.Workspace = manual.Workspace
	}
	if manual.Device != "" {
		opts.Device = manual.Device
	}
	if manual.DeviceLabel != "" {
		opts.DeviceLabel = manual.DeviceLabel
	}
	if opts.Server == "" {
		return fmt.Errorf("missing cloud server; run cube cloud config --server <url> --token <token>, or set CUBE_CLOUD_URL")
	}
	if opts.Interval < 5*time.Second {
		return fmt.Errorf("--interval must be at least 5s")
	}

	ctx := context.Background()
	if !manual.Watch {
		return keepaliveLiveOnce(ctx, m, opts)
	}
	for {
		if err := keepaliveLiveOnce(ctx, m, opts); err != nil {
			return err
		}
		time.Sleep(opts.Interval)
	}
}

func keepaliveLiveOnce(ctx context.Context, m *manager.Manager, opts cloudSyncOptions) error {
	diag, err := identifyLiveAuth(ctx, m, opts)
	if err != nil {
		return err
	}
	if !diag.AuthPresent {
		return fmt.Errorf("live auth missing at %s", diag.AuthPath)
	}
	if !diag.Matched {
		return fmt.Errorf("live auth is not matched to a managed account")
	}
	accountID := strings.TrimSpace(diag.Account.ID)
	leaseID := strings.TrimSpace(diag.Account.LeaseID)
	if accountID == "" || leaseID == "" || !diag.Account.LeaseActive {
		return fmt.Errorf("matched account %s has no active lease", emptyDash(accountID))
	}

	lease, shouldSwap, telemetryMissing, err := heartbeatLease(ctx, opts, leaseID, accountID, m.LiveCodexHome)
	if err != nil {
		return err
	}
	if err := pushUsageFromHome(ctx, opts, accountID, leaseID, "", m.LiveCodexHome); err != nil {
		return err
	}
	fmt.Printf("keepalive %s lease=%s ttl=%s", accountID, lease.ID, time.Until(lease.ExpiresAt).Round(time.Second))
	if telemetryMissing {
		fmt.Print(" telemetry=missing")
	}
	if shouldSwap {
		fmt.Print(" swap=true")
	}
	fmt.Println()
	return nil
}

func parseCloudBorrowLiveOptions(args []string) (cloudBorrowLiveOptions, error) {
	opts := cloudBorrowLiveOptions{
		TTL:    defaultManualLiveTTL,
		Holder: defaultManualLiveHolder,
	}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--account":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("missing value for --account")
			}
			opts.Account = strings.TrimSpace(args[i+1])
			i++
		case "--ttl":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("missing value for --ttl")
			}
			ttl, err := time.ParseDuration(args[i+1])
			if err != nil {
				return opts, fmt.Errorf("invalid --ttl %q", args[i+1])
			}
			opts.TTL = ttl
			i++
		case "--holder":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("missing value for --holder")
			}
			opts.Holder = strings.TrimSpace(args[i+1])
			i++
		default:
			return opts, fmt.Errorf("unknown borrow-live flag %q", args[i])
		}
	}
	if opts.Account == "" {
		return opts, fmt.Errorf("usage: cube cloud borrow-live --account <id-or-label> [--ttl 8h] [--holder manual-direct-codex]")
	}
	if opts.TTL <= 0 {
		return opts, fmt.Errorf("--ttl must be positive")
	}
	if opts.Holder == "" {
		opts.Holder = defaultManualLiveHolder
	}
	return opts, nil
}

func parseCloudReturnLiveOptions(args []string) (cloudReturnLiveOptions, error) {
	var opts cloudReturnLiveOptions
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--account":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("missing value for --account")
			}
			opts.Account = strings.TrimSpace(args[i+1])
			i++
		case "--lease":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("missing value for --lease")
			}
			opts.LeaseID = strings.TrimSpace(args[i+1])
			i++
		default:
			return opts, fmt.Errorf("unknown return-live flag %q", args[i])
		}
	}
	return opts, nil
}

func parseCloudKeepaliveLiveOptions(args []string) (cloudKeepaliveLiveOptions, error) {
	opts := cloudKeepaliveLiveOptions{Interval: 60 * time.Second}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--watch", "--daemon":
			opts.Watch = true
		case "--interval", "--heartbeat":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("missing value for %s", args[i])
			}
			interval, err := time.ParseDuration(args[i+1])
			if err != nil {
				return opts, fmt.Errorf("invalid %s %q", args[i], args[i+1])
			}
			opts.Interval = interval
			i++
		case "--workspace":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("missing value for --workspace")
			}
			opts.Workspace = strings.TrimSpace(args[i+1])
			i++
		case "--device", "--device-id":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("missing value for %s", args[i])
			}
			opts.Device = strings.TrimSpace(args[i+1])
			i++
		case "--device-label":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("missing value for --device-label")
			}
			opts.DeviceLabel = strings.TrimSpace(args[i+1])
			i++
		default:
			return opts, fmt.Errorf("unknown keepalive-live flag %q", args[i])
		}
	}
	if opts.Interval <= 0 {
		return opts, fmt.Errorf("--interval must be positive")
	}
	return opts, nil
}

func readLiveAuthFile(m *manager.Manager) (json.RawMessage, string, error) {
	path := liveAuthPath(m)
	authRaw, err := readAuthFile(path)
	if err != nil {
		return nil, path, err
	}
	return authRaw, path, nil
}

func readOptionalLiveAuthFile(m *manager.Manager) (json.RawMessage, bool, error) {
	path := liveAuthPath(m)
	authRaw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("could not read auth file %s: %w", path, err)
	}
	if !json.Valid(authRaw) {
		return nil, false, fmt.Errorf("auth file %s is not valid JSON", path)
	}
	return json.RawMessage(authRaw), true, nil
}

func liveAuthPath(m *manager.Manager) string {
	codexHome := strings.TrimSpace(m.LiveCodexHome)
	if codexHome == "" {
		if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
			codexHome = filepath.Join(home, ".codex")
		}
	}
	return filepath.Join(codexHome, "auth.json")
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
