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
	AuthFile  string // when set, skip `codex login` and upload this auth.json
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

	var authRaw json.RawMessage

	// --auth-file skips the interactive login entirely and re-uses an existing
	// auth.json. This is the recovery path: when a previous relogin logged in
	// successfully but failed to upload (e.g. a client PAT hitting the admin-only
	// /api/sync/push, or a transient network error), the credential was saved to
	// disk and can be re-pushed with a proper token — no second browser round.
	if relogin.AuthFile != "" {
		authRaw, err = readAuthFile(relogin.AuthFile)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "cube: uploading %s from %s (skipping codex login)\n", relogin.AccountID, relogin.AuthFile)
	} else {
		codexHome, err := os.MkdirTemp("", "cube20-relogin-*")
		if err != nil {
			return err
		}
		defer os.RemoveAll(codexHome)

		fmt.Fprintf(os.Stderr, "cube: logging in %s with temporary CODEX_HOME\n", relogin.AccountID)
		if err := runDeviceLogin(codexHome); err != nil {
			if ctx.Err() != nil {
				// User interrupted the login; exit cleanly (temp dir already
				// cleaned up by the deferred RemoveAll).
				return ctx.Err()
			}
			return fmt.Errorf("codex login failed: %w", err)
		}

		authRaw, err = readTempAuth(codexHome)
		if err != nil {
			return err
		}
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
		// The login already succeeded and the credential is in memory; persist it
		// so the user can retry the upload (with a proper token) instead of
		// re-running the whole browser login. Skip when we read it from a file —
		// it is already safely on disk at relogin.AuthFile.
		hint := ""
		if relogin.AuthFile == "" {
			if saved, saveErr := saveRecoveredAuth(m, relogin.AccountID, authRaw); saveErr == nil {
				hint = fmt.Sprintf("\n  credential saved to %s\n  retry without re-login: cube cloud relogin %s --auth-file %s --status %s --owner %s",
					saved, relogin.AccountID, saved, relogin.Status, relogin.Owner)
			} else {
				fmt.Fprintf(os.Stderr, "cube: warning: could not save recovered credential: %v\n", saveErr)
			}
		}
		if strings.Contains(err.Error(), "403") || strings.Contains(strings.ToLower(err.Error()), "forbidden") {
			return fmt.Errorf("upload failed: %w; cloud relogin replaces stored auth and requires an admin token%s", err, hint)
		}
		return fmt.Errorf("upload failed: %w%s", err, hint)
	}
	fmt.Printf("uploaded %s owner=%s status=%s\n", account.ID, account.OwnerMode, account.Status)

	// Upload succeeded: drop any stale recovered credential for this account so a
	// real secret does not linger on disk after it is no longer needed.
	removeRecoveredAuth(m, relogin.AccountID)

	var result quota.Result
	if err := cloudJSON(ctx, http.MethodGet, opts, "/api/sync/quota/"+url.PathEscape(relogin.AccountID), nil, &result); err != nil {
		return fmt.Errorf("quota check failed after upload: %w; next: run cube cloud quota %s", err, relogin.AccountID)
	}
	printQuotaResult(result)
	printReloginNextStep(relogin, result)
	return nil
}

// recoveredAuthPath is where a successfully-logged-in but not-yet-uploaded
// credential is parked so it can be retried without a second browser login.
func recoveredAuthPath(m *manager.Manager, accountID string) string {
	return filepath.Join(m.StateDir, "recovered-auth-"+sanitizeAuthFileID(accountID)+".json")
}

// sanitizeAuthFileID keeps the recovered-auth filename to a safe charset so an
// account ID can never escape m.StateDir via path separators.
func sanitizeAuthFileID(id string) string {
	mapped := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, id)
	if mapped == "" {
		return "account"
	}
	return mapped
}

func saveRecoveredAuth(m *manager.Manager, accountID string, authRaw json.RawMessage) (string, error) {
	path := recoveredAuthPath(m, accountID)
	if err := os.WriteFile(path, authRaw, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func removeRecoveredAuth(m *manager.Manager, accountID string) {
	_ = os.Remove(recoveredAuthPath(m, accountID))
}

func readAuthFile(path string) (json.RawMessage, error) {
	authRaw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("could not read auth file %s: %w", path, err)
	}
	if !json.Valid(authRaw) {
		return nil, fmt.Errorf("auth file %s is not valid JSON", path)
	}
	return json.RawMessage(authRaw), nil
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
		case "--auth-file":
			if i+1 >= len(args) {
				return opts, fmt.Errorf("missing value for --auth-file")
			}
			opts.AuthFile = strings.TrimSpace(args[i+1])
			i++
		default:
			if strings.HasPrefix(args[i], "--") {
				return opts, fmt.Errorf("unknown relogin flag %q", args[i])
			}
			ids = append(ids, strings.TrimSpace(args[i]))
		}
	}
	if len(ids) != 1 || ids[0] == "" {
		return opts, fmt.Errorf("usage: cube cloud relogin <account-id> [--status ready|drain] [--owner cloud|client] [--auth-file <path>]")
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
