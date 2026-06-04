package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cube20/internal/quota"
)

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
