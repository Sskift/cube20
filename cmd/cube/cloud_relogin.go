package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"cube20/internal/manager"
	"cube20/internal/quota"
)

type cloudReloginOptions struct {
	AccountID string
	Status    manager.AccountStatus
	Owner     manager.AccountOwnerMode
}

func runCloudRelogin(m *manager.Manager, args []string) error {
	relogin, err := parseCloudReloginOptions(args)
	if err != nil {
		return err
	}

	opts := defaultCloudSyncOptions(m)
	if opts.Server == "" {
		return fmt.Errorf("missing cloud server; run cube cloud config --server <url> --token <token>, or set CUBE_CLOUD_URL")
	}

	// SIGINT/SIGTERM cancels ctx instead of killing the process (NotifyContext
	// removes the default handler), so the deferred os.RemoveAll below still runs
	// and the temporary credential directory is removed on Ctrl-C rather than
	// lingering on disk. The interactive `codex login` child shares our process
	// group and receives the same terminal SIGINT, so it exits and
	// runDeviceLogin returns, letting this function unwind through its defers.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	codexHome, err := os.MkdirTemp("", "cube20-relogin-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(codexHome)

	fmt.Fprintf(os.Stderr, "cube: logging in %s with temporary CODEX_HOME\n", relogin.AccountID)
	if err := runDeviceLogin(codexHome); err != nil {
		if ctx.Err() != nil {
			// User interrupted the login; exit cleanly (temp dir already cleaned
			// up by the deferred RemoveAll).
			return ctx.Err()
		}
		return fmt.Errorf("codex login failed: %w", err)
	}

	authRaw, err := readTempAuth(codexHome)
	if err != nil {
		return err
	}

	snapshot := manager.ProfileSnapshot{
		ID:           relogin.AccountID,
		Status:       relogin.Status,
		Auth:         authRaw,
		SourceClient: opts.Client,
		OwnerMode:    relogin.Owner,
		UpdatedAt:    time.Now(),
	}

	var account manager.AccountView
	if err := cloudJSON(ctx, http.MethodPost, opts, "/api/sync/push", snapshot, &account); err != nil {
		if strings.Contains(err.Error(), "403") || strings.Contains(strings.ToLower(err.Error()), "forbidden") {
			return fmt.Errorf("upload failed: %w; cloud relogin replaces stored auth and requires an admin token", err)
		}
		return fmt.Errorf("upload failed: %w", err)
	}
	fmt.Printf("uploaded %s owner=%s status=%s\n", account.ID, account.OwnerMode, account.Status)

	var result quota.Result
	if err := cloudJSON(ctx, http.MethodGet, opts, "/api/sync/quota/"+url.PathEscape(relogin.AccountID), nil, &result); err != nil {
		return fmt.Errorf("quota check failed after upload: %w; next: run cube cloud quota %s", err, relogin.AccountID)
	}
	printQuotaResult(result)
	printReloginNextStep(relogin, result)
	return nil
}

func parseCloudReloginOptions(args []string) (cloudReloginOptions, error) {
	opts := cloudReloginOptions{
		Status: manager.StatusReady,
		Owner:  manager.OwnerCloud,
	}
	ids := []string{}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--status":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("missing value for --status")
			}
			status := manager.AccountStatus(strings.TrimSpace(args[i+1]))
			switch status {
			case manager.StatusReady, manager.StatusDrain:
				opts.Status = status
			default:
				return opts, fmt.Errorf("--status must be ready or drain")
			}
			i++
		case "--owner":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("missing value for --owner")
			}
			owner := manager.AccountOwnerMode(strings.TrimSpace(args[i+1]))
			switch owner {
			case manager.OwnerCloud, manager.OwnerClient:
				opts.Owner = owner
			default:
				return opts, fmt.Errorf("--owner must be cloud or client")
			}
			i++
		default:
			if strings.HasPrefix(args[i], "--") {
				return opts, fmt.Errorf("unknown relogin flag %q", args[i])
			}
			ids = append(ids, strings.TrimSpace(args[i]))
		}
	}
	if len(ids) != 1 || ids[0] == "" {
		return opts, fmt.Errorf("usage: cube cloud relogin <account-id> [--status ready|drain] [--owner cloud|client]")
	}
	opts.AccountID = ids[0]
	return opts, nil
}

func runDeviceLogin(codexHome string) error {
	cmd := exec.Command("codex", "login", "--device-auth")
	cmd.Env = setEnv(os.Environ(), "CODEX_HOME", codexHome)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func readTempAuth(codexHome string) (json.RawMessage, error) {
	authPath := filepath.Join(codexHome, "auth.json")
	authRaw, err := os.ReadFile(authPath)
	if err != nil {
		return nil, fmt.Errorf("could not read temporary auth.json: %w", err)
	}
	if !json.Valid(authRaw) {
		return nil, fmt.Errorf("temporary auth.json is not valid JSON")
	}
	return json.RawMessage(authRaw), nil
}

func printReloginNextStep(relogin cloudReloginOptions, result quota.Result) {
	switch result.Status {
	case quota.StatusSupported:
		if relogin.Owner == manager.OwnerCloud && relogin.Status == manager.StatusReady {
			fmt.Printf("next: %s is ready for cube run\n", relogin.AccountID)
			return
		}
		if relogin.Status == manager.StatusDrain {
			fmt.Printf("next: %s is drained; relogin again with --status ready when it should rejoin the pool\n", relogin.AccountID)
			return
		}
		fmt.Printf("next: %s is client-owned; keep quota fresh with cube report\n", relogin.AccountID)
	case quota.StatusRefreshInvalid:
		fmt.Printf("next: %s still has invalid refresh auth; rerun relogin or keep it drained\n", relogin.AccountID)
	default:
		fmt.Printf("next: review quota status before leasing %s\n", relogin.AccountID)
	}
}
