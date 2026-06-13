package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
			Auth json.RawMessage `json:"auth"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		sawAuth = body.Auth
		_ = json.NewEncoder(w).Encode(map[string]any{
			"matched": true,
			"account": manager.AccountView{
				Account: manager.Account{ID: "work", Label: "Work", OwnerMode: manager.OwnerCloud},
			},
		})
	}))
	defer srv.Close()

	diag, err := identifyLiveAuth(context.Background(), &manager.Manager{LiveCodexHome: live}, cloudSyncOptions{
		Server: srv.URL,
		Token:  "cube_dev_test",
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
