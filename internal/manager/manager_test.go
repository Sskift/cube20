package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"cube20/internal/quota"
)

func TestMaterializeAuthSkipsWriteWhenUnchanged(t *testing.T) {
	m := newTestManager(t)
	account := Account{ID: "acct", CodexHome: filepath.Join(m.AccountsDir, "acct")}
	raw := []byte(`{"OPENAI_API_KEY":"sk-test","tokens":{"access_token":"a"}}`)

	if err := m.materializeAuth(account, raw); err != nil {
		t.Fatalf("first materializeAuth() error = %v", err)
	}
	authPath := filepath.Join(account.CodexHome, authFileName)
	info1, err := os.Stat(authPath)
	if err != nil {
		t.Fatalf("stat after first write error = %v", err)
	}

	// Second call with identical content must NOT rewrite the file. Load()
	// runs constantly, so re-materializing unchanged auth churns credentials
	// on disk for no reason.
	time.Sleep(10 * time.Millisecond)
	if err := m.materializeAuth(account, raw); err != nil {
		t.Fatalf("second materializeAuth() error = %v", err)
	}
	info2, err := os.Stat(authPath)
	if err != nil {
		t.Fatalf("stat after second call error = %v", err)
	}
	if !info1.ModTime().Equal(info2.ModTime()) {
		t.Fatalf("auth.json was rewritten on unchanged content: mtime %v -> %v", info1.ModTime(), info2.ModTime())
	}

	// Changed content MUST be written.
	changed := []byte(`{"OPENAI_API_KEY":"sk-test-2","tokens":{"access_token":"b"}}`)
	if err := m.materializeAuth(account, changed); err != nil {
		t.Fatalf("materializeAuth(changed) error = %v", err)
	}
	got, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatalf("read after change error = %v", err)
	}
	if !bytes.Contains(got, []byte("sk-test-2")) {
		t.Fatalf("auth.json was not updated on changed content: %s", got)
	}
}

func TestCloseFileModeIsNoopAndIdempotent(t *testing.T) {
	m := newTestManager(t)
	// File-mode managers never open the pool, so Close must be a no-op and
	// must be safe to call repeatedly (the server may call it on shutdown
	// after handlers have also touched the manager).
	if err := m.Close(); err != nil {
		t.Fatalf("first Close() error = %v, want nil", err)
	}
	if err := m.Close(); err != nil {
		t.Fatalf("second Close() error = %v, want nil", err)
	}
	if m.db != nil {
		t.Fatal("file-mode manager opened a db pool, want nil")
	}
	// The manager must still be usable after Close in file mode.
	if _, err := m.Load(); err != nil {
		t.Fatalf("Load() after Close error = %v", err)
	}
}

func TestLoadBalanceStatusExcludesClientOwnedAccounts(t *testing.T) {
	m := newTestManager(t)
	saveTestAccounts(t, m, Account{
		ID:            "client-owned",
		OwnerMode:     OwnerClient,
		OwnerClientID: "client-1",
	})

	status, err := m.LoadBalanceStatus()
	if err != nil {
		t.Fatalf("LoadBalanceStatus() error = %v", err)
	}
	if len(status.Eligible) != 0 {
		t.Fatalf("eligible accounts = %v, want none", loadBalanceIDs(status.Eligible))
	}

	account := findLoadBalanceAccount(t, status.Excluded, "client-owned")
	if account.Reason != "owner is client" {
		t.Fatalf("excluded reason = %q, want %q", account.Reason, "owner is client")
	}
	if account.OwnerMode != OwnerClient || account.OwnerClientID != "client-1" {
		t.Fatalf("owner = %s/%q, want client/client-1", account.OwnerMode, account.OwnerClientID)
	}
}

func TestLoadBalanceStatusExcludesActiveLease(t *testing.T) {
	m := newTestManager(t)
	saveTestAccounts(t, m, Account{
		ID:               "leased",
		LeaseID:          "lease-active",
		LeaseClientID:    "client-1",
		LeaseHolder:      "holder-1",
		LeaseStartedAt:   time.Now().Add(-time.Minute),
		LeaseHeartbeatAt: time.Now().Add(-30 * time.Second),
		LeaseExpiresAt:   time.Now().Add(time.Hour),
	})

	status, err := m.LoadBalanceStatus()
	if err != nil {
		t.Fatalf("LoadBalanceStatus() error = %v", err)
	}
	if len(status.Eligible) != 0 {
		t.Fatalf("eligible accounts = %v, want none", loadBalanceIDs(status.Eligible))
	}

	account := findLoadBalanceAccount(t, status.Excluded, "leased")
	if !account.LeaseActive {
		t.Fatal("excluded account LeaseActive = false, want true")
	}
	if !strings.HasPrefix(account.Reason, "leased until ") {
		t.Fatalf("excluded reason = %q, want leased reason", account.Reason)
	}
}

func TestLoadBalanceStatusIncludesCloudOwnedReadyAccount(t *testing.T) {
	m := newTestManager(t)
	saveTestAccounts(t, m, Account{
		ID:        "cloud-ready",
		OwnerMode: OwnerCloud,
		Status:    StatusReady,
	})
	saveTestQuota(t, m, "cloud-ready", 95, time.Now().Add(time.Hour))

	status, err := m.LoadBalanceStatus()
	if err != nil {
		t.Fatalf("LoadBalanceStatus() error = %v", err)
	}
	if got, want := loadBalanceIDs(status.Eligible), []string{"cloud-ready"}; !sameStrings(got, want) {
		t.Fatalf("eligible accounts = %v, want %v", got, want)
	}
	if len(status.Excluded) != 0 {
		t.Fatalf("excluded accounts = %v, want none", loadBalanceIDs(status.Excluded))
	}

	account := status.Eligible[0]
	if !account.AuthPresent || account.OwnerMode != OwnerCloud || account.Status != StatusReady {
		t.Fatalf("eligible account = %+v, want cloud-owned ready account with auth", account)
	}
}

func TestLoadBalanceStatusExcludesExhaustedQuota(t *testing.T) {
	m := newTestManager(t)
	saveTestAccounts(t, m, Account{
		ID:        "exhausted",
		OwnerMode: OwnerCloud,
		Status:    StatusReady,
	})
	saveTestQuota(t, m, "exhausted", 0, time.Now().Add(time.Hour))

	status, err := m.LoadBalanceStatus()
	if err != nil {
		t.Fatalf("LoadBalanceStatus() error = %v", err)
	}
	if len(status.Eligible) != 0 {
		t.Fatalf("eligible accounts = %v, want none", loadBalanceIDs(status.Eligible))
	}

	account := findLoadBalanceAccount(t, status.Excluded, "exhausted")
	if !strings.HasPrefix(account.Reason, "5h quota exhausted until ") {
		t.Fatalf("excluded reason = %q, want quota exhausted reason", account.Reason)
	}
	if account.QuotaRemainingPercent != 0 || account.QuotaStatus != quota.StatusSupported {
		t.Fatalf("quota fields = %+v, want supported 0%% remaining", account)
	}
}

func TestClaimLeaseSkipsExhaustedQuota(t *testing.T) {
	m := newTestManager(t)
	saveTestAccounts(t, m,
		Account{ID: "exhausted"},
		Account{ID: "available"},
	)
	saveTestQuota(t, m, "exhausted", 0, time.Now().Add(time.Hour))
	saveTestQuota(t, m, "available", 80, time.Now().Add(3*time.Hour))

	lease, err := m.ClaimLease(context.Background(), "client-1", "holder-1", time.Minute)
	if err != nil {
		t.Fatalf("ClaimLease() error = %v", err)
	}
	if lease.Lease.AccountID != "available" {
		t.Fatalf("leased account = %q, want available", lease.Lease.AccountID)
	}
}

func TestClaimLeaseWeightsQuotaNearReset(t *testing.T) {
	m := newTestManager(t)
	saveTestAccounts(t, m,
		Account{ID: "far-reset"},
		Account{ID: "near-reset"},
	)
	saveTestQuota(t, m, "far-reset", 80, time.Now().Add(4*time.Hour))
	saveTestQuota(t, m, "near-reset", 70, time.Now().Add(30*time.Minute))

	lease, err := m.ClaimLease(context.Background(), "client-1", "holder-1", time.Minute)
	if err != nil {
		t.Fatalf("ClaimLease() error = %v", err)
	}
	if lease.Lease.AccountID != "near-reset" {
		t.Fatalf("leased account = %q, want near-reset", lease.Lease.AccountID)
	}
}

func TestLoadBalanceStatusExcludesExhaustedSevenDayQuota(t *testing.T) {
	m := newTestManager(t)
	saveTestAccounts(t, m, Account{
		ID:        "weekly-capped",
		OwnerMode: OwnerCloud,
		Status:    StatusReady,
	})
	// 5h healthy (100% remaining) but 7d exhausted (0% remaining).
	saveTestQuotaWindows(t, m, "weekly-capped",
		100, time.Now().Add(time.Hour),
		0, time.Now().Add(72*time.Hour))

	status, err := m.LoadBalanceStatus()
	if err != nil {
		t.Fatalf("LoadBalanceStatus() error = %v", err)
	}
	if len(status.Eligible) != 0 {
		t.Fatalf("eligible accounts = %v, want none", loadBalanceIDs(status.Eligible))
	}
	account := findLoadBalanceAccount(t, status.Excluded, "weekly-capped")
	if !strings.HasPrefix(account.Reason, "7d quota exhausted until ") {
		t.Fatalf("excluded reason = %q, want 7d quota exhausted reason", account.Reason)
	}
	// Display must reflect the binding (7d) window, not the healthy 5h window.
	if account.QuotaRemainingPercent != 0 {
		t.Fatalf("quota remaining = %v, want 0 (binding 7d)", account.QuotaRemainingPercent)
	}
	if account.QuotaBindingWindow != "7d" {
		t.Fatalf("binding window = %q, want 7d", account.QuotaBindingWindow)
	}
	if account.QuotaSevenDayRemainingPercent != 0 {
		t.Fatalf("7d remaining = %v, want 0", account.QuotaSevenDayRemainingPercent)
	}
}

func TestClaimLeaseSkipsExhaustedSevenDayQuota(t *testing.T) {
	m := newTestManager(t)
	saveTestAccounts(t, m,
		Account{ID: "weekly-capped"},
		Account{ID: "available"},
	)
	saveTestQuotaWindows(t, m, "weekly-capped",
		100, time.Now().Add(time.Hour),
		0, time.Now().Add(72*time.Hour))
	saveTestQuotaWindows(t, m, "available",
		80, time.Now().Add(3*time.Hour),
		90, time.Now().Add(96*time.Hour))

	lease, err := m.ClaimLease(context.Background(), "client-1", "holder-1", time.Minute)
	if err != nil {
		t.Fatalf("ClaimLease() error = %v", err)
	}
	if lease.Lease.AccountID != "available" {
		t.Fatalf("leased account = %q, want available", lease.Lease.AccountID)
	}
}

func TestLoadBalanceStatusKeepsAccountFullOnBothWindows(t *testing.T) {
	m := newTestManager(t)
	saveTestAccounts(t, m, Account{
		ID:        "healthy",
		OwnerMode: OwnerCloud,
		Status:    StatusReady,
	})
	saveTestQuotaWindows(t, m, "healthy",
		90, time.Now().Add(time.Hour),
		80, time.Now().Add(72*time.Hour))

	status, err := m.LoadBalanceStatus()
	if err != nil {
		t.Fatalf("LoadBalanceStatus() error = %v", err)
	}
	account := findLoadBalanceAccount(t, status.Eligible, "healthy")
	// Binding window is the lower-remaining 7d (80% < 90%).
	if account.QuotaBindingWindow != "7d" || account.QuotaRemainingPercent != 80 {
		t.Fatalf("binding = %q remaining = %v, want 7d / 80", account.QuotaBindingWindow, account.QuotaRemainingPercent)
	}
}

func TestLoadBalanceStatusClientReportedNoSevenDayStaysEligible(t *testing.T) {
	m := newTestManager(t)
	saveTestAccounts(t, m, Account{
		ID:        "client-only",
		OwnerMode: OwnerCloud,
		Status:    StatusReady,
	})
	// Client-reported style: only a 5h window present, no 7d.
	saveTestQuota(t, m, "client-only", 75, time.Now().Add(2*time.Hour))

	status, err := m.LoadBalanceStatus()
	if err != nil {
		t.Fatalf("LoadBalanceStatus() error = %v", err)
	}
	account := findLoadBalanceAccount(t, status.Eligible, "client-only")
	if account.QuotaBindingWindow != "5h" || account.QuotaRemainingPercent != 75 {
		t.Fatalf("binding = %q remaining = %v, want 5h / 75", account.QuotaBindingWindow, account.QuotaRemainingPercent)
	}
	if account.QuotaSevenDayRemainingPercent != 0 || account.QuotaSevenDayRemainingDisplay != "" {
		t.Fatalf("7d fields should be empty for client-reported account: %+v", account)
	}
}

func TestDispatchHistoryRecordsClaimAndRelease(t *testing.T) {
	m := newTestManager(t)
	saveTestAccounts(t, m, Account{ID: "available"})
	saveTestQuota(t, m, "available", 80, time.Now().Add(time.Hour))
	if _, _, err := m.CreateClient("liushiao-local"); err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}

	lease, err := m.ClaimLease(context.Background(), "client-liushiao-local", "liushiao-local", time.Minute)
	if err != nil {
		t.Fatalf("ClaimLease() error = %v", err)
	}
	if err := m.ReleaseLease("available", lease.Lease.ID, "client-liushiao-local"); err != nil {
		t.Fatalf("ReleaseLease() error = %v", err)
	}

	events, err := m.DispatchHistory(10, "")
	if err != nil {
		t.Fatalf("DispatchHistory() error = %v", err)
	}
	if got, want := len(events), 2; got != want {
		t.Fatalf("dispatch events = %d, want %d: %+v", got, want, events)
	}
	if events[0].Event != "released" || events[1].Event != "claimed" {
		t.Fatalf("dispatch order/events = %+v, want released then claimed", events)
	}
	if events[1].AccountID != "available" || events[1].ClientID != "client-liushiao-local" || events[1].ClientLabel != "liushiao-local" {
		t.Fatalf("claimed event = %+v, want account/client labels", events[1])
	}
}

func TestRecoverExpiredLeasesMovesReadyAccountToRecovering(t *testing.T) {
	m := newTestManager(t)
	expiredAt := time.Now().Add(-time.Minute)
	saveTestAccounts(t, m, Account{
		ID:               "expired",
		Status:           StatusReady,
		LeaseID:          "lease-expired",
		LeaseClientID:    "client-1",
		LeaseHolder:      "holder-1",
		LeaseStartedAt:   expiredAt.Add(-time.Minute),
		LeaseHeartbeatAt: expiredAt.Add(-30 * time.Second),
		LeaseExpiresAt:   expiredAt,
	})

	if err := m.RecoverExpiredLeases(context.Background()); err != nil {
		t.Fatalf("RecoverExpiredLeases() error = %v", err)
	}

	account := getTestAccount(t, m, "expired")
	if account.Status != StatusRecovering {
		t.Fatalf("status = %s, want %s", account.Status, StatusRecovering)
	}
	if account.LeaseID != "" || account.LeaseClientID != "" || !account.LeaseExpiresAt.IsZero() {
		t.Fatalf("lease fields were not cleared: %+v", account)
	}
	if !strings.Contains(account.LastError, "lease lease-expired expired") {
		t.Fatalf("last error = %q, want expired lease detail", account.LastError)
	}
}

func TestRecordQuotaResultFileModeSerializesWithRoundRobinLock(t *testing.T) {
	m := newTestManager(t)
	saveTestAccounts(t, m, Account{
		ID:        "acct",
		Status:    StatusReady,
		OwnerMode: OwnerCloud,
	})

	// Hold the same lock the lease writers use. A correct file-mode
	// recordQuotaResult must serialize its Load->modify->Save behind this
	// lock, otherwise a concurrent quota write can clobber a lease change
	// (lease resurrection -> double dispatch).
	lockPath := filepath.Join(m.StateDir, "run-round-robin.lock")
	unlock, err := m.acquireLock(lockPath)
	if err != nil {
		t.Fatalf("acquireLock() error = %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- m.recordQuotaResult("acct", quota.Result{
			Status: quota.StatusSupported,
			Plan:   "pro",
		}, false, QuotaSourceCloud, "", false)
	}()

	select {
	case <-done:
		unlock()
		t.Fatal("recordQuotaResult returned while the round-robin lock was held; file-mode write is unlocked and can race lease updates")
	case <-time.After(150 * time.Millisecond):
		// Expected: it is blocked waiting for the lock.
	}

	unlock()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("recordQuotaResult() after unlock error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("recordQuotaResult did not finish after the lock was released")
	}
}

func TestAcquireLockSurvivesStaleLockFile(t *testing.T) {
	m := newTestManager(t)
	lockPath := filepath.Join(m.StateDir, "run-round-robin.lock")

	// Simulate a crash that left the lock file on disk while NOBODY holds the
	// lock (SIGKILL/panic while held). With the old O_EXCL sentinel scheme the
	// mere existence of this file wedges every future acquire until a manual rm.
	// A flock-based lock must ignore the residual file and acquire immediately,
	// because flock coordinates via the fd, not the file's existence.
	if err := os.WriteFile(lockPath, []byte("stale"), 0o600); err != nil {
		t.Fatalf("seed stale lock file error = %v", err)
	}

	start := time.Now()
	unlock, err := m.acquireLock(lockPath)
	if err != nil {
		t.Fatalf("acquireLock() over a stale lock file error = %v (stale file must not block)", err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("acquireLock() over a stale lock file took %v; a residual file must not cause a 2s timeout poll", elapsed)
	}
	unlock()
}

func TestAcquireLockReacquireAfterRelease(t *testing.T) {
	m := newTestManager(t)
	lockPath := filepath.Join(m.StateDir, "run-round-robin.lock")

	// Repeated acquire/release must always succeed: releasing the lock must make
	// it immediately available again, and the lock must never become permanently
	// blocked.
	for i := 0; i < 3; i++ {
		unlock, err := m.acquireLock(lockPath)
		if err != nil {
			t.Fatalf("acquireLock() iteration %d error = %v", i, err)
		}
		unlock()
	}
}

func TestAcquireLockBlocksSecondHolderUntilRelease(t *testing.T) {
	m := newTestManager(t)
	lockPath := filepath.Join(m.StateDir, "run-round-robin.lock")

	unlock1, err := m.acquireLock(lockPath)
	if err != nil {
		t.Fatalf("first acquireLock() error = %v", err)
	}

	// While the lock is held, a second acquisition must block (it cannot succeed
	// until the first holder releases). With the 2s ceiling it will time out if
	// we wait long enough, so just confirm it does not return immediately.
	done := make(chan error, 1)
	go func() {
		u, err := m.acquireLock(lockPath)
		if err == nil {
			u()
		}
		done <- err
	}()

	select {
	case <-done:
		unlock1()
		t.Fatal("second acquireLock() returned while the lock was held; mutual exclusion is broken")
	case <-time.After(150 * time.Millisecond):
		// Expected: blocked waiting for the first holder.
	}

	unlock1()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("second acquireLock() after release error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("second acquireLock() did not complete after the first holder released")
	}
}

func TestConcurrentMutatorsDoNotLoseUpdates(t *testing.T) {
	m := newTestManager(t)

	const n = 12
	accounts := make([]Account, 0, n)
	for i := 0; i < n; i++ {
		accounts = append(accounts, Account{
			ID:        fmt.Sprintf("acct-%02d", i),
			Status:    StatusReady,
			OwnerMode: OwnerCloud,
		})
	}
	saveTestAccounts(t, m, accounts...)

	// Fire N concurrent mutators, each touching a DIFFERENT account: half flip
	// status to drain, half set a distinctive label. Without an intra-process
	// lock these Load->mutate->Save calls clobber each other (whole-file
	// rewrite), so some updates are lost. Run with -race to also surface the
	// concurrent state access.
	var wg sync.WaitGroup
	errs := make(chan error, 2*n)
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("acct-%02d", i)
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			errs <- m.SetStatus(id, StatusDrain)
		}(id)
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			errs <- m.SetLabel(id, "label-"+id)
		}(id)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent mutator error = %v", err)
		}
	}

	state, err := m.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(state.Accounts) != n {
		t.Fatalf("accounts = %d, want %d (lost-update dropped an account)", len(state.Accounts), n)
	}
	for _, account := range state.Accounts {
		if account.Status != StatusDrain {
			t.Fatalf("account %s status = %s, want %s (status update lost)", account.ID, account.Status, StatusDrain)
		}
		if account.Label != "label-"+account.ID {
			t.Fatalf("account %s label = %q, want %q (label update lost)", account.ID, account.Label, "label-"+account.ID)
		}
	}
}

func leasedQuotaResult(remaining float64, resetAt time.Time) quota.Result {
	used := 100 - remaining
	window := quota.Window{
		Key:              "five_hour",
		Label:            "5h",
		UsedPercent:      used,
		RemainingPercent: remaining,
		UsedDisplay:      fmt.Sprintf("%.0f%%", used),
		RemainingDisplay: fmt.Sprintf("%.0f%%", remaining),
		ResetsAt:         resetAt.UTC().Format(time.RFC3339),
	}
	return quota.Result{
		Status: quota.StatusSupported,
		Plan:   "pro",
		Quotas: []quota.Window{window},
	}
}

func TestRecordLeasedQuotaKeepsCloudOwner(t *testing.T) {
	m := newTestManager(t)
	saveTestAccounts(t, m, Account{
		ID:               "leased",
		OwnerMode:        OwnerCloud,
		Status:           StatusReady,
		LeaseID:          "lease-active",
		LeaseClientID:    "c1",
		LeaseHolder:      "holder-1",
		LeaseStartedAt:   time.Now().Add(-time.Minute),
		LeaseHeartbeatAt: time.Now().Add(-30 * time.Second),
		LeaseExpiresAt:   time.Now().Add(time.Hour),
	})

	result := leasedQuotaResult(40, time.Now().Add(time.Hour))
	if err := m.RecordLeasedQuota("leased", "lease-active", "c1", result, time.Now()); err != nil {
		t.Fatalf("RecordLeasedQuota() error = %v", err)
	}

	state, err := m.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	cache, ok := state.QuotaCache["leased"]
	if !ok {
		t.Fatal("QuotaCache missing leased entry")
	}
	if cache.Source != QuotaSourceClient {
		t.Fatalf("cache source = %q, want %q", cache.Source, QuotaSourceClient)
	}
	if cache.ReporterClientID != "c1" {
		t.Fatalf("cache reporter = %q, want c1", cache.ReporterClientID)
	}
	if cache.FiveHour == nil {
		t.Fatal("cache FiveHour = nil, want a 5h window")
	}
	account := getTestAccount(t, m, "leased")
	if account.OwnerMode != OwnerCloud {
		t.Fatalf("owner mode = %q, want %q (client lease report must not flip ownership)", account.OwnerMode, OwnerCloud)
	}
	if account.OwnerClientID != "" {
		t.Fatalf("owner client id = %q, want empty", account.OwnerClientID)
	}
}

func TestRecordLeasedQuotaRejectsNonHolder(t *testing.T) {
	m := newTestManager(t)
	saveTestAccounts(t, m, Account{
		ID:               "leased",
		OwnerMode:        OwnerCloud,
		Status:           StatusReady,
		LeaseID:          "lease-active",
		LeaseClientID:    "c1",
		LeaseHolder:      "holder-1",
		LeaseStartedAt:   time.Now().Add(-time.Minute),
		LeaseHeartbeatAt: time.Now().Add(-30 * time.Second),
		LeaseExpiresAt:   time.Now().Add(time.Hour),
	})

	result := leasedQuotaResult(40, time.Now().Add(time.Hour))
	if err := m.RecordLeasedQuota("leased", "lease-active", "c2", result, time.Now()); err == nil {
		t.Fatal("RecordLeasedQuota() with wrong client = nil error, want non-nil")
	}

	state, err := m.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if _, ok := state.QuotaCache["leased"]; ok {
		t.Fatal("QuotaCache must be unchanged when the reporter does not hold the lease")
	}

	// A mismatched lease ID must also be rejected.
	if err := m.RecordLeasedQuota("leased", "wrong-lease", "c1", result, time.Now()); err == nil {
		t.Fatal("RecordLeasedQuota() with wrong lease id = nil error, want non-nil")
	}
}

func TestShouldSwapLease(t *testing.T) {
	m := newTestManager(t)
	saveTestAccounts(t, m, Account{ID: "acct", OwnerMode: OwnerCloud, Status: StatusReady})

	saveTestQuota(t, m, "acct", 5, time.Now().Add(time.Hour))
	swap, err := m.ShouldSwapLease("acct")
	if err != nil {
		t.Fatalf("ShouldSwapLease() error = %v", err)
	}
	if !swap {
		t.Fatalf("ShouldSwapLease() = false, want true for 5%% remaining below threshold %.0f", swapRemainingThreshold)
	}

	saveTestQuota(t, m, "acct", 50, time.Now().Add(time.Hour))
	swap, err = m.ShouldSwapLease("acct")
	if err != nil {
		t.Fatalf("ShouldSwapLease() error = %v", err)
	}
	if swap {
		t.Fatal("ShouldSwapLease() = true, want false for 50% remaining above threshold")
	}

	swap, err = m.ShouldSwapLease("no-cache")
	if err != nil {
		t.Fatalf("ShouldSwapLease() no-cache error = %v", err)
	}
	if swap {
		t.Fatal("ShouldSwapLease() = true for account with no cache, want false")
	}
}

func TestFetchQuotaSkipsNetworkWhenLeased(t *testing.T) {
	m := newTestManager(t)
	saveTestAccounts(t, m, Account{
		ID:               "leased",
		OwnerMode:        OwnerCloud,
		Status:           StatusReady,
		Generation:       3,
		LeaseID:          "lease-active",
		LeaseClientID:    "c1",
		LeaseHolder:      "holder-1",
		LeaseStartedAt:   time.Now().Add(-time.Minute),
		LeaseHeartbeatAt: time.Now().Add(-30 * time.Second),
		LeaseExpiresAt:   time.Now().Add(time.Hour),
	})
	saveTestQuota(t, m, "leased", 73, time.Now().Add(time.Hour))

	authBefore := readTestAuth(t, m, "leased")

	result, err := m.FetchQuota(context.Background(), "leased")
	if err != nil {
		t.Fatalf("FetchQuota() error = %v", err)
	}
	// The leased branch returns the cached result verbatim (status supported)
	// with a "leased by" detail and never performs a network fetch.
	if result.Status != quota.StatusSupported {
		t.Fatalf("status = %q, want %q (cached value returned verbatim)", result.Status, quota.StatusSupported)
	}
	if !strings.Contains(result.Detail, "leased by c1") {
		t.Fatalf("detail = %q, want a leased-by-c1 note", result.Detail)
	}
	if len(result.Quotas) != 1 || result.Quotas[0].RemainingPercent != 73 {
		t.Fatalf("quotas = %+v, want the cached 73%% window", result.Quotas)
	}

	if authAfter := readTestAuth(t, m, "leased"); !bytes.Equal(authAfter, authBefore) {
		t.Fatal("auth.json changed; FetchQuota performed a network fetch for a leased account")
	}
	account := getTestAccount(t, m, "leased")
	if account.Generation != 3 {
		t.Fatalf("generation = %d, want 3 (unchanged; no network fetch)", account.Generation)
	}
}

func TestUpdateLeasedProfileSnapshotRejectsGenerationConflict(t *testing.T) {
	m := newTestManager(t)
	saveTestAccounts(t, m, Account{
		ID:               "leased",
		Generation:       7,
		LeaseID:          "lease-active",
		LeaseClientID:    "client-1",
		LeaseHolder:      "holder-1",
		LeaseStartedAt:   time.Now().Add(-time.Minute),
		LeaseHeartbeatAt: time.Now().Add(-30 * time.Second),
		LeaseExpiresAt:   time.Now().Add(time.Hour),
	})
	before := readTestAuth(t, m, "leased")

	_, err := m.UpdateLeasedProfileSnapshot(ProfileSnapshot{
		ID:         "leased",
		LeaseID:    "lease-active",
		Generation: 6,
		Auth:       json.RawMessage(`{"OPENAI_API_KEY":"sk-test-updated"}`),
	}, "client-1", time.Minute)
	if err == nil {
		t.Fatal("UpdateLeasedProfileSnapshot() error = nil, want generation conflict")
	}
	if !strings.Contains(err.Error(), "auth generation conflict") {
		t.Fatalf("UpdateLeasedProfileSnapshot() error = %v, want generation conflict", err)
	}

	account := getTestAccount(t, m, "leased")
	if account.Generation != 7 {
		t.Fatalf("generation = %d, want 7", account.Generation)
	}
	if account.LeaseID != "lease-active" {
		t.Fatalf("lease id = %q, want lease-active", account.LeaseID)
	}
	if after := readTestAuth(t, m, "leased"); !bytes.Equal(after, before) {
		t.Fatalf("auth.json changed despite generation conflict")
	}
}

func newTestManager(t *testing.T) *Manager {
	t.Helper()

	root := t.TempDir()
	m := &Manager{
		StateDir:      filepath.Join(root, "state"),
		StatePath:     filepath.Join(root, "state", "state.json"),
		SettingsPath:  filepath.Join(root, "state", settingsFileName),
		AccountsDir:   filepath.Join(root, "accounts"),
		LiveCodexHome: filepath.Join(root, "live"),
	}
	if err := m.Ensure(); err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	return m
}

func saveTestAccounts(t *testing.T, m *Manager, accounts ...Account) {
	t.Helper()

	now := time.Now().Add(-time.Minute)
	state := State{
		Version:  1,
		Accounts: make([]Account, 0, len(accounts)),
	}
	for _, account := range accounts {
		if account.ID == "" {
			t.Fatal("test account needs id")
		}
		if account.Label == "" {
			account.Label = account.ID
		}
		if account.Status == "" {
			account.Status = StatusReady
		}
		if account.OwnerMode == "" {
			account.OwnerMode = OwnerCloud
		}
		if account.Generation == 0 {
			account.Generation = 1
		}
		if account.CodexHome == "" {
			account.CodexHome = filepath.Join(m.AccountsDir, account.ID)
		}
		if account.CreatedAt.IsZero() {
			account.CreatedAt = now
		}
		if account.UpdatedAt.IsZero() {
			account.UpdatedAt = now
		}
		writeTestAuth(t, account.CodexHome, account.ID)
		state.Accounts = append(state.Accounts, account)
	}
	if err := m.Save(state); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
}

func saveTestQuota(t *testing.T, m *Manager, accountID string, remaining float64, resetAt time.Time) {
	t.Helper()

	state, err := m.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.QuotaCache == nil {
		state.QuotaCache = map[string]QuotaCache{}
	}
	used := 100 - remaining
	window := quota.Window{
		Key:              "five_hour",
		Label:            "5h",
		UsedPercent:      used,
		RemainingPercent: remaining,
		UsedDisplay:      fmt.Sprintf("%.0f%%", used),
		RemainingDisplay: fmt.Sprintf("%.0f%%", remaining),
		ResetsAt:         resetAt.UTC().Format(time.RFC3339),
	}
	state.QuotaCache[accountID] = QuotaCache{
		AccountID: accountID,
		UpdatedAt: time.Now(),
		Result: quota.Result{
			Status: quota.StatusSupported,
			Plan:   "pro",
			Quotas: []quota.Window{window},
		},
		FiveHour: &window,
		Source:   QuotaSourceCloud,
	}
	if err := m.Save(state); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
}

func saveTestQuotaWindows(t *testing.T, m *Manager, accountID string, fiveRemaining float64, fiveReset time.Time, sevenRemaining float64, sevenReset time.Time) {
	t.Helper()

	state, err := m.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if state.QuotaCache == nil {
		state.QuotaCache = map[string]QuotaCache{}
	}
	mk := func(key, label string, remaining float64, reset time.Time) quota.Window {
		used := 100 - remaining
		return quota.Window{
			Key:              key,
			Label:            label,
			UsedPercent:      used,
			RemainingPercent: remaining,
			UsedDisplay:      fmt.Sprintf("%.0f%%", used),
			RemainingDisplay: fmt.Sprintf("%.0f%%", remaining),
			ResetsAt:         reset.UTC().Format(time.RFC3339),
		}
	}
	five := mk("five_hour", "5h", fiveRemaining, fiveReset)
	seven := mk("seven_day", "7d", sevenRemaining, sevenReset)
	state.QuotaCache[accountID] = QuotaCache{
		AccountID: accountID,
		UpdatedAt: time.Now(),
		Result: quota.Result{
			Status: quota.StatusSupported,
			Plan:   "pro",
			Quotas: []quota.Window{five, seven},
		},
		FiveHour: &five,
		Source:   QuotaSourceCloud,
	}
	if err := m.Save(state); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
}

func writeTestAuth(t *testing.T, codexHome, seed string) {
	t.Helper()

	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", codexHome, err)
	}
	data, err := json.MarshalIndent(map[string]string{
		"OPENAI_API_KEY": "sk-test-" + seed,
	}, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent(auth) error = %v", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Join(codexHome, authFileName), data, fileModeFor(authFileName)); err != nil {
		t.Fatalf("WriteFile(auth.json) error = %v", err)
	}
}

func readTestAuth(t *testing.T, m *Manager, id string) []byte {
	t.Helper()

	account := getTestAccount(t, m, id)
	data, err := os.ReadFile(filepath.Join(account.CodexHome, authFileName))
	if err != nil {
		t.Fatalf("ReadFile(auth.json) error = %v", err)
	}
	return data
}

func getTestAccount(t *testing.T, m *Manager, id string) Account {
	t.Helper()

	account, err := m.GetAccount(id)
	if err != nil {
		t.Fatalf("GetAccount(%q) error = %v", id, err)
	}
	return account
}

func findLoadBalanceAccount(t *testing.T, accounts []LoadBalanceAccount, id string) LoadBalanceAccount {
	t.Helper()

	for _, account := range accounts {
		if account.ID == id {
			return account
		}
	}
	t.Fatalf("account %q not found in %v", id, loadBalanceIDs(accounts))
	return LoadBalanceAccount{}
}

func loadBalanceIDs(accounts []LoadBalanceAccount) []string {
	ids := make([]string, 0, len(accounts))
	for _, account := range accounts {
		ids = append(ids, account.ID)
	}
	return ids
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestReleaseLeaseUnknownReturnsNotFound(t *testing.T) {
	m := newTestManager(t)
	saveTestAccounts(t, m, Account{ID: "available"})
	saveTestQuota(t, m, "available", 80, time.Now().Add(time.Hour))

	// Releasing a lease that was never claimed must surface ErrLeaseNotFound,
	// not a silent nil success.
	err := m.ReleaseLease("available", "bogus-lease-id", "client-x")
	if err == nil {
		t.Fatal("ReleaseLease(unknown) = nil, want ErrLeaseNotFound")
	}
	if !errors.Is(err, ErrLeaseNotFound) {
		t.Fatalf("ReleaseLease(unknown) error = %v, want errors.Is ErrLeaseNotFound", err)
	}

	// A wrong accountID must likewise fail rather than report success.
	if err := m.ReleaseLease("does-not-exist", "bogus", "client-x"); !errors.Is(err, ErrLeaseNotFound) {
		t.Fatalf("ReleaseLease(missing account) error = %v, want ErrLeaseNotFound", err)
	}
}

func TestAccountViewByID(t *testing.T) {
	m := newTestManager(t)
	saveTestAccounts(t, m,
		Account{ID: "alpha", Label: "Alpha"},
		Account{ID: "beta", Label: "Beta"},
	)

	view, err := m.AccountViewByID("beta")
	if err != nil {
		t.Fatalf("AccountViewByID(beta) error = %v", err)
	}
	if view.ID != "beta" || view.Label != "Beta" {
		t.Fatalf("AccountViewByID(beta) = %+v, want id=beta label=Beta", view)
	}
	if !view.AuthPresent {
		t.Fatalf("AccountViewByID(beta).AuthPresent = false, want true (test auth was written)")
	}

	if _, err := m.AccountViewByID("ghost"); !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("AccountViewByID(ghost) error = %v, want ErrAccountNotFound", err)
	}
}

func TestAccountViewByIDDoesNotModifyState(t *testing.T) {
	m := newTestManager(t)
	saveTestAccounts(t, m, Account{ID: "alpha"})

	before, err := os.ReadFile(m.StatePath)
	if err != nil {
		t.Fatalf("ReadFile(state) error = %v", err)
	}
	infoBefore, err := os.Stat(m.StatePath)
	if err != nil {
		t.Fatalf("Stat(state) error = %v", err)
	}

	if _, err := m.AccountViewByID("alpha"); err != nil {
		t.Fatalf("AccountViewByID(alpha) error = %v", err)
	}

	after, err := os.ReadFile(m.StatePath)
	if err != nil {
		t.Fatalf("ReadFile(state) after error = %v", err)
	}
	infoAfter, err := os.Stat(m.StatePath)
	if err != nil {
		t.Fatalf("Stat(state) after error = %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("AccountViewByID mutated state.json content (should be a pure read)")
	}
	if !infoBefore.ModTime().Equal(infoAfter.ModTime()) {
		t.Fatal("AccountViewByID rewrote state.json (mtime changed; should be a pure read)")
	}
}
