package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"cube20/internal/manager"
)

// --- FIX B: atomic auth.json write + atomic config symlink swap ---

func TestWriteSnapshotAtomic(t *testing.T) {
	live := t.TempDir()
	// A live config.toml so writeSnapshotToStableHome symlinks it in.
	liveConfig := manager.CodexConfigPath(live)
	if err := os.WriteFile(liveConfig, []byte("model = \"x\"\n"), 0o600); err != nil {
		t.Fatalf("write live config: %v", err)
	}
	m := &manager.Manager{LiveCodexHome: live}

	home := t.TempDir()
	// Pre-seed a sessions/ subtree that must be left untouched across writes.
	sessionPath := filepath.Join(home, "sessions", "2026", "06", "07", "rollout-x.jsonl")
	if err := os.MkdirAll(filepath.Dir(sessionPath), 0o700); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	if err := os.WriteFile(sessionPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write session: %v", err)
	}

	snap := manager.ProfileSnapshot{ID: "acct-1", Auth: []byte(`{"token":"first"}`)}
	if err := writeSnapshotToStableHome(m, snap, home); err != nil {
		t.Fatalf("writeSnapshotToStableHome: %v", err)
	}

	authPath := filepath.Join(home, "auth.json")
	got, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatalf("read auth.json: %v", err)
	}
	if string(got) != `{"token":"first"}` {
		t.Fatalf("auth.json = %q, want first content", string(got))
	}

	info, err := os.Stat(authPath)
	if err != nil {
		t.Fatalf("stat auth.json: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("auth.json perm = %o, want 600", perm)
	}
	inode1 := info.Sys().(*syscall.Stat_t).Ino

	// No leftover temp file from the atomic rename.
	if matches, _ := filepath.Glob(filepath.Join(home, "*.tmp")); len(matches) != 0 {
		t.Fatalf("leftover temp files: %v", matches)
	}
	if _, err := os.Stat(authPath + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("auth.json.tmp should not remain, stat err = %v", err)
	}

	// config.toml must be a symlink pointing at the live config.
	link := filepath.Join(home, "config.toml")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("config.toml is not a symlink: %v", err)
	}
	if target != liveConfig {
		t.Fatalf("config.toml -> %q, want %q", target, liveConfig)
	}

	// sessions/ untouched.
	if _, err := os.Stat(sessionPath); err != nil {
		t.Fatalf("session file should survive write, stat err = %v", err)
	}

	// Second write (swap) replaces auth content atomically and leaves no temp,
	// and the symlink is replaced (not erroring on existing link).
	snap2 := manager.ProfileSnapshot{ID: "acct-2", Auth: []byte(`{"token":"second"}`)}
	if err := writeSnapshotToStableHome(m, snap2, home); err != nil {
		t.Fatalf("writeSnapshotToStableHome (swap): %v", err)
	}
	got2, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatalf("read auth.json after swap: %v", err)
	}
	if string(got2) != `{"token":"second"}` {
		t.Fatalf("auth.json after swap = %q, want second content", string(got2))
	}
	// The swap must be an atomic rename (new inode), not an in-place truncate of
	// the same file that codex could observe mid-write.
	info2, err := os.Stat(authPath)
	if err != nil {
		t.Fatalf("stat auth.json after swap: %v", err)
	}
	if inode2 := info2.Sys().(*syscall.Stat_t).Ino; inode2 == inode1 {
		t.Fatalf("auth.json inode unchanged (%d) across rewrite; expected atomic rename to a new inode", inode2)
	}
	if matches, _ := filepath.Glob(filepath.Join(home, "*.tmp")); len(matches) != 0 {
		t.Fatalf("leftover temp files after swap: %v", matches)
	}
	target2, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("config.toml not a symlink after swap: %v", err)
	}
	if target2 != liveConfig {
		t.Fatalf("config.toml after swap -> %q, want %q", target2, liveConfig)
	}
}

func TestWriteSnapshotNoLiveConfig(t *testing.T) {
	// When there is no live config.toml, no symlink should be created and the
	// write must still succeed (and leave no stale config.toml from a prior run).
	m := &manager.Manager{LiveCodexHome: t.TempDir()}
	home := t.TempDir()

	// Pre-existing config.toml symlink from a prior run should be removed when
	// the live config no longer exists, leaving no dangling/stale link.
	stale := filepath.Join(home, "config.toml")
	if err := os.WriteFile(stale, []byte("stale"), 0o600); err != nil {
		t.Fatalf("seed stale config: %v", err)
	}

	snap := manager.ProfileSnapshot{ID: "acct-1", Auth: []byte(`{"token":"x"}`)}
	if err := writeSnapshotToStableHome(m, snap, home); err != nil {
		t.Fatalf("writeSnapshotToStableHome: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale config.toml should be gone, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "auth.json")); err != nil {
		t.Fatalf("auth.json should exist: %v", err)
	}
}

// --- FIX C: pruneOldRuns scrubs stale leaked auth.json ---

func TestPruneScrubsStaleAuthKeepsSessions(t *testing.T) {
	runs := t.TempDir()
	dir := filepath.Join(runs, "abandoned")
	sessionPath := filepath.Join(dir, "sessions", "rollout-x.jsonl")
	if err := os.MkdirAll(filepath.Dir(sessionPath), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	authPath := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(authPath, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write auth: %v", err)
	}
	if err := os.WriteFile(sessionPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write session: %v", err)
	}

	// Age the auth.json to 2h (older than the 1h abandonment cutoff) but the dir
	// itself is recent (< 7d), so it should NOT be removed wholesale — only the
	// leaked credential scrubbed, sessions kept for the retention window.
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(authPath, old, old); err != nil {
		t.Fatalf("chtimes auth: %v", err)
	}

	pruneOldRuns(runs)

	if _, err := os.Stat(authPath); !os.IsNotExist(err) {
		t.Fatalf("stale auth.json should be scrubbed, stat err = %v", err)
	}
	if _, err := os.Stat(sessionPath); err != nil {
		t.Fatalf("sessions/ should survive prune, stat err = %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("recent run dir should survive prune, stat err = %v", err)
	}
}

func TestPruneLeavesFreshAuthAlone(t *testing.T) {
	runs := t.TempDir()
	dir := filepath.Join(runs, "live")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	authPath := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(authPath, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write auth: %v", err)
	}
	// Fresh auth.json (just written) => a live run; must be left alone.

	pruneOldRuns(runs)

	if _, err := os.Stat(authPath); err != nil {
		t.Fatalf("fresh auth.json should be left alone, stat err = %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("live run dir should survive, stat err = %v", err)
	}
}

func TestPruneRemovesOldDirWithoutAuth(t *testing.T) {
	// The original contract: a run dir with no auth.json older than 7 days is
	// removed entirely.
	runs := t.TempDir()
	dir := filepath.Join(runs, "ancient")
	sub := filepath.Join(dir, "sessions")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	old := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(dir, old, old); err != nil {
		t.Fatalf("chtimes dir: %v", err)
	}

	pruneOldRuns(runs)

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("ancient run dir should be removed, stat err = %v", err)
	}
}

func TestPruneRemovesOldDirAfterScrub(t *testing.T) {
	// A dir that is BOTH old (>7d) and has a stale auth.json should end up fully
	// removed: scrub the credential, then remove the aged dir.
	runs := t.TempDir()
	dir := filepath.Join(runs, "ancient-leaked")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	authPath := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(authPath, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write auth: %v", err)
	}
	old := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(authPath, old, old); err != nil {
		t.Fatalf("chtimes auth: %v", err)
	}
	if err := os.Chtimes(dir, old, old); err != nil {
		t.Fatalf("chtimes dir: %v", err)
	}

	pruneOldRuns(runs)

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("ancient leaked run dir should be removed, stat err = %v", err)
	}
}

// --- FIX A: cleanup helper guarantees lease release on cancel ---

// newTestCloudServer returns an httptest server that records each request's
// (method, path) via observe and always replies 200 {}.
func newTestCloudServer(t *testing.T, observe func(method, path string)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if observe != nil {
			observe(r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	}))
}

func TestCleanupRunReleasesLease(t *testing.T) {
	var sawUsage, sawRelease bool
	srv := newTestCloudServer(t, func(method, path string) {
		switch {
		case method == http.MethodPost && path == "/api/sync/usage":
			sawUsage = true
		case method == http.MethodDelete:
			sawRelease = true
		}
	})
	defer srv.Close()

	opts := cloudSyncOptions{Server: srv.URL, Client: "test"}
	lease := manager.LeaseSnapshot{}
	lease.Lease.ID = "lease-1"
	lease.Snapshot.ID = "acct-1"

	usageErr, releaseErr := cleanupRun(context.Background(), opts, lease, t.TempDir(), true)
	if usageErr != nil {
		t.Fatalf("usageErr = %v", usageErr)
	}
	if releaseErr != nil {
		t.Fatalf("releaseErr = %v", releaseErr)
	}
	if !sawUsage {
		t.Fatalf("expected usage push")
	}
	if !sawRelease {
		t.Fatalf("expected lease release on the cancel/cleanup path")
	}
}

func TestCleanupRunSkipsReleaseWhenRequested(t *testing.T) {
	// When release should be skipped (e.g. auth upload failed mid-run), usage is
	// still pushed but no DELETE is sent so the lease is not double-released.
	var sawUsage, sawRelease bool
	srv := newTestCloudServer(t, func(method, path string) {
		switch {
		case method == http.MethodPost && path == "/api/sync/usage":
			sawUsage = true
		case method == http.MethodDelete:
			sawRelease = true
		}
	})
	defer srv.Close()

	opts := cloudSyncOptions{Server: srv.URL, Client: "test"}
	lease := manager.LeaseSnapshot{}
	lease.Lease.ID = "lease-1"
	lease.Snapshot.ID = "acct-1"

	usageErr, releaseErr := cleanupRun(context.Background(), opts, lease, t.TempDir(), false)
	if usageErr != nil {
		t.Fatalf("usageErr = %v", usageErr)
	}
	if releaseErr != nil {
		t.Fatalf("releaseErr = %v", releaseErr)
	}
	if !sawUsage {
		t.Fatalf("expected usage push even when release skipped")
	}
	if sawRelease {
		t.Fatalf("did not expect lease release when release=false")
	}
}

func TestCleanupRunReleasesOnCancelledContext(t *testing.T) {
	// The Ctrl-C path proxy: even when the passed context is ALREADY cancelled
	// (signal.NotifyContext fired), cleanupRun must still reach the cloud to push
	// usage and release the lease by deriving a fresh short-lived context. If it
	// reused the cancelled ctx, both HTTP calls would abort and the lease would
	// leak until TTL — the exact bug FIX A removes.
	var sawUsage, sawRelease bool
	srv := newTestCloudServer(t, func(method, path string) {
		switch {
		case method == http.MethodPost && path == "/api/sync/usage":
			sawUsage = true
		case method == http.MethodDelete:
			sawRelease = true
		}
	})
	defer srv.Close()

	opts := cloudSyncOptions{Server: srv.URL, Client: "test"}
	lease := manager.LeaseSnapshot{}
	lease.Lease.ID = "lease-1"
	lease.Snapshot.ID = "acct-1"

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // simulate a delivered SIGINT before cleanup runs

	usageErr, releaseErr := cleanupRun(ctx, opts, lease, t.TempDir(), true)
	if usageErr != nil {
		t.Fatalf("usageErr = %v (cleanup must survive a cancelled ctx)", usageErr)
	}
	if releaseErr != nil {
		t.Fatalf("releaseErr = %v (cleanup must survive a cancelled ctx)", releaseErr)
	}
	if !sawUsage {
		t.Fatalf("expected usage push despite cancelled ctx")
	}
	if !sawRelease {
		t.Fatalf("expected lease release despite cancelled ctx (Ctrl-C must free the lease)")
	}
}

func TestPrepareSwapLeaseReleasesNewLeaseWhenWriteFails(t *testing.T) {
	current := manager.LeaseSnapshot{}
	current.Lease.ID = "lease-old"
	current.Snapshot.ID = "acct-old"

	claimed := manager.LeaseSnapshot{}
	claimed.Lease.ID = "lease-new"
	claimed.Snapshot.ID = "acct-new"
	claimed.Snapshot.Auth = []byte(`{"token":"new"}`)

	var released []string
	next, err := prepareSwapLease(
		context.Background(),
		&manager.Manager{},
		cloudSyncOptions{Client: "client-1"},
		current,
		t.TempDir(),
		func(context.Context, cloudSyncOptions) (manager.LeaseSnapshot, error) {
			return claimed, nil
		},
		func(*manager.Manager, manager.ProfileSnapshot, string) error {
			return os.ErrPermission
		},
		func(ctx context.Context, opts cloudSyncOptions, leaseID, accountID string) error {
			released = append(released, leaseID+":"+accountID)
			return nil
		},
	)

	if err == nil {
		t.Fatal("prepareSwapLease() error = nil, want write failure")
	}
	if next.Lease.ID != current.Lease.ID {
		t.Fatalf("next lease = %q, want old lease %q after failed prep", next.Lease.ID, current.Lease.ID)
	}
	if len(released) != 1 || released[0] != "lease-new:acct-new" {
		t.Fatalf("released = %v, want only new lease released", released)
	}
}
