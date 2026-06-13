package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cube20/internal/manager"
)

func TestIdentifyLiveAuthPostsLocalAuthForReadOnlyMatch(t *testing.T) {
	live := t.TempDir()
	authRaw := []byte(`{"OPENAI_API_KEY":"sk-live"}`)
	if err := os.WriteFile(filepath.Join(live, "auth.json"), authRaw, 0o600); err != nil {
		t.Fatalf("write live auth: %v", err)
	}

	var sawAuth json.RawMessage
	var sawWorkspace string
	var sawDevice string
	var sawToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/sync/identify-auth" {
			t.Fatalf("path = %q, want identify endpoint", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("method = %q, want POST", r.Method)
		}
		sawToken = r.Header.Get("Authorization")
		var body struct {
			Auth      json.RawMessage `json:"auth"`
			Workspace string          `json:"workspace"`
			DeviceID  string          `json:"deviceId"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		sawAuth = body.Auth
		sawWorkspace = body.Workspace
		sawDevice = body.DeviceID
		_ = json.NewEncoder(w).Encode(map[string]any{
			"matched": true,
			"account": manager.AccountView{
				Account: manager.Account{ID: "work", Label: "Work", OwnerMode: manager.OwnerCloud},
			},
		})
	}))
	defer srv.Close()

	diag, err := identifyLiveAuth(context.Background(), &manager.Manager{LiveCodexHome: live}, cloudSyncOptions{
		Server:    srv.URL,
		Token:     "cube_dev_test",
		Workspace: "ws-a",
		Device:    "dev-a",
	})
	if err != nil {
		t.Fatalf("identifyLiveAuth() error = %v", err)
	}
	if !diag.AuthPresent || !diag.Matched || diag.Account.ID != "work" {
		t.Fatalf("diagnosis = %+v, want matched live auth", diag)
	}
	if string(sawAuth) != string(authRaw) {
		t.Fatalf("posted auth = %s, want %s", sawAuth, authRaw)
	}
	if sawToken != "Bearer cube_dev_test" {
		t.Fatalf("authorization = %q, want bearer token", sawToken)
	}
	if sawWorkspace != "ws-a" {
		t.Fatalf("workspace = %q, want ws-a", sawWorkspace)
	}
	if sawDevice != "dev-a" {
		t.Fatalf("deviceId = %q, want dev-a", sawDevice)
	}
}

func TestIdentifyLiveAuthSkipsCloudWhenLocalAuthMissing(t *testing.T) {
	live := t.TempDir()
	diag, err := identifyLiveAuth(context.Background(), &manager.Manager{LiveCodexHome: live}, cloudSyncOptions{
		Server: "http://127.0.0.1:1",
		Token:  "cube_dev_test",
	})
	if err != nil {
		t.Fatalf("identifyLiveAuth() error = %v", err)
	}
	if diag.AuthPresent || diag.Checked {
		t.Fatalf("diagnosis = %+v, want no auth and no cloud check", diag)
	}
	if diag.AuthPath != filepath.Join(live, "auth.json") {
		t.Fatalf("auth path = %q, want live auth path", diag.AuthPath)
	}
}

func TestPrintCloudStatusShowsWorkspace(t *testing.T) {
	t.Setenv("CUBE_WORKSPACE", "ws-a")
	out := captureStdout(t, func() {
		if err := printCloudStatus(&manager.Manager{
			LiveCodexHome: t.TempDir(),
			CloudURL:      "http://127.0.0.1:1",
			CloudToken:    "cube_dev_test",
		}); err != nil {
			t.Fatalf("printCloudStatus: %v", err)
		}
	})
	if !strings.Contains(out, "workspace: ws-a\n") {
		t.Fatalf("status output = %q, want workspace line", out)
	}
}

func TestDefaultCloudSyncOptionsUsesPersistedWorkspace(t *testing.T) {
	root := t.TempDir()
	m := &manager.Manager{
		StateDir:      filepath.Join(root, "state"),
		StatePath:     filepath.Join(root, "state", "state.json"),
		SettingsPath:  filepath.Join(root, "state", "settings.toml"),
		AccountsDir:   filepath.Join(root, "accounts"),
		LiveCodexHome: filepath.Join(root, "live"),
	}
	if err := m.Ensure(); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if _, err := m.UpdateDeviceSettings("http://cube.local", "cube-token", "dev-a", "host-a", "ws-a"); err != nil {
		t.Fatalf("UpdateDeviceSettings: %v", err)
	}

	opts := defaultCloudSyncOptions(m)

	if opts.Workspace != "ws-a" {
		t.Fatalf("workspace = %q, want ws-a", opts.Workspace)
	}
	if opts.Device != "dev-a" {
		t.Fatalf("device = %q, want dev-a", opts.Device)
	}
	if opts.DeviceLabel != "host-a" {
		t.Fatalf("device label = %q, want host-a", opts.DeviceLabel)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	wClosed := false
	rClosed := false
	defer func() {
		os.Stdout = old
		if !wClosed {
			_ = w.Close()
		}
		if !rClosed {
			_ = r.Close()
		}
	}()
	fn()
	if err := w.Close(); err != nil {
		t.Fatalf("close stdout pipe writer: %v", err)
	}
	wClosed = true
	os.Stdout = old
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close stdout pipe reader: %v", err)
	}
	rClosed = true
	return string(data)
}
