package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"cube20/internal/manager"
)

func TestParseCloudBorrowLiveOptionsDefaults(t *testing.T) {
	opts, err := parseCloudBorrowLiveOptions([]string{"--account", "acct-1"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if opts.Account != "acct-1" {
		t.Errorf("Account = %q, want acct-1", opts.Account)
	}
	if opts.TTL != 8*time.Hour {
		t.Errorf("TTL = %v, want 8h", opts.TTL)
	}
	if opts.Holder != "manual-direct-codex" {
		t.Errorf("Holder = %q, want manual-direct-codex", opts.Holder)
	}
}

func TestParseCloudBorrowLiveOptionsRequiresAccount(t *testing.T) {
	if _, err := parseCloudBorrowLiveOptions([]string{"--ttl", "30m"}); err == nil {
		t.Fatal("expected error when --account is missing")
	}
	if _, err := parseCloudBorrowLiveOptions([]string{"--account"}); err == nil {
		t.Fatal("expected error when --account has no value")
	}
}

func TestRunCloudBorrowLivePostsLiveAuthWithoutQuotaRefresh(t *testing.T) {
	live := t.TempDir()
	authRaw := []byte(`{"tokens":{"id_token":"live"}}`)
	if err := os.WriteFile(filepath.Join(live, "auth.json"), authRaw, 0o600); err != nil {
		t.Fatalf("write auth: %v", err)
	}

	var paths []string
	var sawToken string
	var body struct {
		Account    string          `json:"account"`
		Auth       json.RawMessage `json:"auth"`
		TTLSeconds int             `json:"ttlSeconds"`
		Holder     string          `json:"holder"`
		Client     string          `json:"client"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		if r.URL.Path == "/api/sync/quota/acct-1" {
			t.Fatalf("borrow-live must not refresh quota")
		}
		if r.URL.Path != "/api/sync/manual-borrow" {
			t.Fatalf("path = %q, want manual borrow endpoint", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("method = %q, want POST", r.Method)
		}
		sawToken = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(manager.LeaseSnapshot{
			Lease:    manager.Lease{ID: "lease-manual", AccountID: "acct-1"},
			Snapshot: manager.ProfileSnapshot{ID: "acct-1"},
		})
	}))
	defer srv.Close()

	err := runCloudBorrowLive(&manager.Manager{
		LiveCodexHome: live,
		CloudURL:      srv.URL,
		CloudToken:    "cube_dev_test",
	}, []string{"--account", "acct-1", "--ttl", "30m", "--holder", "manual"})
	if err != nil {
		t.Fatalf("runCloudBorrowLive: %v", err)
	}
	if sawToken != "Bearer cube_dev_test" {
		t.Fatalf("authorization = %q, want bearer token", sawToken)
	}
	if body.Account != "acct-1" {
		t.Errorf("account = %q, want acct-1", body.Account)
	}
	if string(body.Auth) != string(authRaw) {
		t.Errorf("auth = %s, want %s", body.Auth, authRaw)
	}
	if body.TTLSeconds != 1800 {
		t.Errorf("ttlSeconds = %d, want 1800", body.TTLSeconds)
	}
	if body.Holder != "manual" {
		t.Errorf("holder = %q, want manual", body.Holder)
	}
	if body.Client == "" {
		t.Error("client should be populated from hostname")
	}
	if !slices.Equal(paths, []string{"POST /api/sync/manual-borrow"}) {
		t.Fatalf("paths = %v, want only manual borrow", paths)
	}
}

func TestRunCloudReturnLiveIdentifiesLiveAuthThenReturns(t *testing.T) {
	live := t.TempDir()
	authRaw := []byte(`{"tokens":{"id_token":"live"}}`)
	if err := os.WriteFile(filepath.Join(live, "auth.json"), authRaw, 0o600); err != nil {
		t.Fatalf("write auth: %v", err)
	}

	var paths []string
	var returnBody struct {
		Account string          `json:"account"`
		LeaseID string          `json:"leaseId"`
		Auth    json.RawMessage `json:"auth"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case "/api/sync/identify-auth":
			var body struct {
				Auth json.RawMessage `json:"auth"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode identify request: %v", err)
			}
			if string(body.Auth) != string(authRaw) {
				t.Fatalf("identify auth = %s, want %s", body.Auth, authRaw)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"matched": true,
				"account": manager.AccountView{
					Account: manager.Account{
						ID:      "acct-live",
						Label:   "Live",
						LeaseID: "lease-live",
					},
					LeaseActive: true,
				},
			})
		case "/api/sync/manual-return":
			if r.Method != http.MethodPost {
				t.Fatalf("return method = %q, want POST", r.Method)
			}
			if err := json.NewDecoder(r.Body).Decode(&returnBody); err != nil {
				t.Fatalf("decode return request: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"account": manager.AccountView{
					Account: manager.Account{ID: "acct-live"},
				},
				"released": true,
			})
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer srv.Close()

	err := runCloudReturnLive(&manager.Manager{
		LiveCodexHome: live,
		CloudURL:      srv.URL,
		CloudToken:    "cube_dev_test",
	}, nil)
	if err != nil {
		t.Fatalf("runCloudReturnLive: %v", err)
	}
	if !slices.Equal(paths, []string{"POST /api/sync/identify-auth", "POST /api/sync/manual-return"}) {
		t.Fatalf("paths = %v, want identify then manual return", paths)
	}
	if returnBody.Account != "acct-live" {
		t.Errorf("account = %q, want acct-live", returnBody.Account)
	}
	if returnBody.LeaseID != "lease-live" {
		t.Errorf("leaseId = %q, want lease-live", returnBody.LeaseID)
	}
	if string(returnBody.Auth) != string(authRaw) {
		t.Errorf("auth = %s, want %s", returnBody.Auth, authRaw)
	}
}

func TestRunCloudReturnLiveUsesExplicitAccountAndLeaseWithoutLiveAuth(t *testing.T) {
	var body struct {
		Account string          `json:"account"`
		LeaseID string          `json:"leaseId"`
		Auth    json.RawMessage `json:"auth"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/sync/manual-return" {
			t.Fatalf("path = %q, want only manual return", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"released": true})
	}))
	defer srv.Close()

	err := runCloudReturnLive(&manager.Manager{
		LiveCodexHome: t.TempDir(),
		CloudURL:      srv.URL,
		CloudToken:    "cube_dev_test",
	}, []string{"--account", "acct-arg", "--lease", "lease-arg"})
	if err != nil {
		t.Fatalf("runCloudReturnLive: %v", err)
	}
	if body.Account != "acct-arg" {
		t.Errorf("account = %q, want acct-arg", body.Account)
	}
	if body.LeaseID != "lease-arg" {
		t.Errorf("leaseId = %q, want lease-arg", body.LeaseID)
	}
	if len(body.Auth) != 0 {
		t.Errorf("auth = %s, want omitted", body.Auth)
	}
}

func TestRunCloudKeepaliveLiveIdentifiesHeartbeatsAndReportsUsage(t *testing.T) {
	live := t.TempDir()
	authRaw := []byte(`{"tokens":{"id_token":"live"}}`)
	if err := os.WriteFile(filepath.Join(live, "auth.json"), authRaw, 0o600); err != nil {
		t.Fatalf("write auth: %v", err)
	}

	var paths []string
	var heartbeatBody struct {
		AccountID  string `json:"accountId"`
		Client     string `json:"client"`
		DeviceID   string `json:"deviceId"`
		TTLSeconds int    `json:"ttlSeconds"`
	}
	var usageBody struct {
		AccountID string `json:"accountId"`
		LeaseID   string `json:"leaseId"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case "/api/sync/identify-auth":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"matched": true,
				"account": manager.AccountView{
					Account: manager.Account{
						ID:          "acct-live",
						LeaseID:     "lease-live",
						LeaseHolder: "manual-direct-codex",
					},
					LeaseActive: true,
					LeaseKind:   "manual",
				},
			})
		case "/api/sync/leases/lease-live":
			if r.Method != http.MethodPatch {
				t.Fatalf("heartbeat method = %q, want PATCH", r.Method)
			}
			if err := json.NewDecoder(r.Body).Decode(&heartbeatBody); err != nil {
				t.Fatalf("decode heartbeat: %v", err)
			}
			_ = json.NewEncoder(w).Encode(manager.Lease{ID: "lease-live", AccountID: "acct-live", ClientID: "dev-a"})
		case "/api/sync/usage":
			if r.Method != http.MethodPost {
				t.Fatalf("usage method = %q, want POST", r.Method)
			}
			if err := json.NewDecoder(r.Body).Decode(&usageBody); err != nil {
				t.Fatalf("decode usage: %v", err)
			}
			_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer srv.Close()

	err := runCloudKeepaliveLive(&manager.Manager{
		LiveCodexHome: live,
		CloudURL:      srv.URL,
		CloudToken:    "cube_dev_test",
	}, []string{"--interval", "30s", "--device", "dev-a"})
	if err != nil {
		t.Fatalf("runCloudKeepaliveLive: %v", err)
	}
	wantPaths := []string{"POST /api/sync/identify-auth", "PATCH /api/sync/leases/lease-live", "POST /api/sync/usage"}
	if !slices.Equal(paths, wantPaths) {
		t.Fatalf("paths = %v, want %v", paths, wantPaths)
	}
	if heartbeatBody.AccountID != "acct-live" || heartbeatBody.DeviceID != "dev-a" {
		t.Fatalf("heartbeat body = %+v, want acct-live/dev-a", heartbeatBody)
	}
	if heartbeatBody.TTLSeconds != 28800 {
		t.Fatalf("ttlSeconds = %d, want 28800 for manual keepalive", heartbeatBody.TTLSeconds)
	}
	if heartbeatBody.Client == "" {
		t.Fatal("heartbeat client should be populated")
	}
	if usageBody.AccountID != "acct-live" || usageBody.LeaseID != "lease-live" {
		t.Fatalf("usage body = %+v, want acct-live/lease-live", usageBody)
	}
}
