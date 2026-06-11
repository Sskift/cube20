package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cube20/internal/manager"
)

func TestParseCloudReloginOptionsAuthFile(t *testing.T) {
	opts, err := parseCloudReloginOptions([]string{
		"acct-1", "--status", "drain", "--owner", "client", "--auth-file", "/tmp/a.json",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if opts.AccountID != "acct-1" {
		t.Errorf("AccountID = %q, want acct-1", opts.AccountID)
	}
	if opts.Status != manager.StatusDrain {
		t.Errorf("Status = %q, want drain", opts.Status)
	}
	if opts.Owner != manager.OwnerClient {
		t.Errorf("Owner = %q, want client", opts.Owner)
	}
	if opts.AuthFile != "/tmp/a.json" {
		t.Errorf("AuthFile = %q, want /tmp/a.json", opts.AuthFile)
	}
}

func TestParseCloudReloginOptionsDefaults(t *testing.T) {
	opts, err := parseCloudReloginOptions([]string{"acct-1"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if opts.Status != manager.StatusReady {
		t.Errorf("default Status = %q, want ready", opts.Status)
	}
	if opts.Owner != manager.OwnerCloud {
		t.Errorf("default Owner = %q, want cloud", opts.Owner)
	}
	if opts.AuthFile != "" {
		t.Errorf("default AuthFile = %q, want empty", opts.AuthFile)
	}
}

func TestParseCloudReloginOptionsAuthFileMissingValue(t *testing.T) {
	if _, err := parseCloudReloginOptions([]string{"acct-1", "--auth-file"}); err == nil {
		t.Fatal("expected error for --auth-file with no value")
	}
}

// sanitizeAuthFileID must never emit a path separator, so a hostile account ID
// cannot make recoveredAuthPath escape StateDir.
func TestSanitizeAuthFileIDNoEscape(t *testing.T) {
	cases := map[string]string{
		"d4104449-6582-4e29": "d4104449-6582-4e29",
		"../../etc/passwd":   "______etc_passwd",
		"a/b\\c":             "a_b_c",
		"":                   "account",
	}
	for in, want := range cases {
		if got := sanitizeAuthFileID(in); got != want {
			t.Errorf("sanitizeAuthFileID(%q) = %q, want %q", in, got, want)
		}
		if strings.ContainsAny(sanitizeAuthFileID(in), "/\\") {
			t.Errorf("sanitizeAuthFileID(%q) leaked a separator", in)
		}
	}
}

func TestRecoveredAuthRoundTrip(t *testing.T) {
	m := &manager.Manager{StateDir: t.TempDir()}
	auth := json.RawMessage(`{"tokens":{"id_token":"x"}}`)

	saved, err := saveRecoveredAuth(m, "acct-1", auth)
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if filepath.Dir(saved) != m.StateDir {
		t.Errorf("saved outside StateDir: %s", saved)
	}

	// File mode must be 0600 — it holds a real credential.
	info, err := os.Stat(saved)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("perm = %o, want 600", perm)
	}

	got, err := readAuthFile(saved)
	if err != nil {
		t.Fatalf("readAuthFile: %v", err)
	}
	if string(got) != string(auth) {
		t.Errorf("round-trip mismatch: got %s, want %s", got, auth)
	}

	removeRecoveredAuth(m, "acct-1")
	if _, err := os.Stat(saved); !os.IsNotExist(err) {
		t.Errorf("recovered auth not removed: stat err = %v", err)
	}
}

func TestReadAuthFileRejectsInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readAuthFile(path); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if _, err := readAuthFile(filepath.Join(dir, "missing.json")); err == nil {
		t.Fatal("expected error for missing file")
	}
}
