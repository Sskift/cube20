package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"cube20/internal/manager"
	"cube20/internal/quota"
)

func TestAdminTokenCanAccessAdminRoute(t *testing.T) {
	server, _, adminToken, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestMissingTokenCannotAccessAdminRoute(t *testing.T) {
	server, _, _, _ := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestPATCanReadMe(t *testing.T) {
	server, _, _, pat := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.Header.Set("Authorization", "Bearer "+pat)
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestPATCannotAccessAdminRoute(t *testing.T) {
	server, _, _, pat := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/accounts", nil)
	req.Header.Set("Authorization", "Bearer "+pat)
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestPATCanClaimLease(t *testing.T) {
	server, _, _, pat := newTestServer(t)
	req := httptest.NewRequest(http.MethodPost, "/api/sync/leases", bytes.NewBufferString(`{"ttlSeconds":90}`))
	req.Header.Set("Authorization", "Bearer "+pat)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestPATCannotPullAuthSnapshot(t *testing.T) {
	server, _, _, pat := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/sync/pull/work", nil)
	req.Header.Set("Authorization", "Bearer "+pat)
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("cannot pull auth snapshots")) {
		t.Fatalf("body = %s, want pull denial", rec.Body.String())
	}
}

func TestPATCannotArbitraryPushAuth(t *testing.T) {
	server, _, _, pat := newTestServer(t)
	body, err := json.Marshal(manager.ProfileSnapshot{
		ID:     "work",
		Status: manager.StatusReady,
		Auth:   json.RawMessage(`{"OPENAI_API_KEY":"sk-updated"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/sync/push", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+pat)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestPATCanPushOwnClientReport(t *testing.T) {
	server, _, _, pat := newTestServer(t)
	body, err := json.Marshal(manager.ProfileSnapshot{
		ID:        "local-report",
		Status:    manager.StatusReady,
		OwnerMode: manager.OwnerClient,
		Auth:      json.RawMessage(`{"OPENAI_API_KEY":"sk-local"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/sync/push", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+pat)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	server.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

// claimLease drives POST /api/sync/leases as a PAT client and returns the new
// lease id plus the account id assigned by the manager.
func claimLease(t *testing.T, server *Server, pat string) (leaseID, accountID string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/sync/leases", bytes.NewBufferString(`{"ttlSeconds":90}`))
	req.Header.Set("Authorization", "Bearer "+pat)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("claim status = %d body = %s", rec.Code, rec.Body.String())
	}
	var snapshot manager.LeaseSnapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode lease snapshot: %v body = %s", err, rec.Body.String())
	}
	if snapshot.Lease.ID == "" || snapshot.Lease.AccountID == "" {
		t.Fatalf("claim returned empty lease id/account: %+v", snapshot.Lease)
	}
	return snapshot.Lease.ID, snapshot.Lease.AccountID
}

// heartbeatResult mirrors the heartbeatResponse wire shape: promoted lease
// fields plus shouldSwap.
type heartbeatResult struct {
	ID         string `json:"id"`
	AccountID  string `json:"accountId"`
	ClientID   string `json:"clientId"`
	Generation int64  `json:"generation"`
	ShouldSwap bool   `json:"shouldSwap"`
}

func doHeartbeat(t *testing.T, server *Server, pat, method, path, body string) heartbeatResult {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Authorization", "Bearer "+pat)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("heartbeat status = %d body = %s", rec.Code, rec.Body.String())
	}
	var out heartbeatResult
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode heartbeat response: %v body = %s", err, rec.Body.String())
	}
	return out
}

func TestHeartbeatReturnsShouldSwapWhenLow(t *testing.T) {
	server, _, _, pat := newTestServer(t)
	leaseID, accountID := claimLease(t, server, pat)

	body := `{"accountId":"` + accountID + `","client":"tester","ttlSeconds":80,"fiveHour":{"key":"five_hour","label":"5h","usedPercent":95,"remainingPercent":5}}`
	out := doHeartbeat(t, server, pat, http.MethodPatch, "/api/sync/leases/"+leaseID, body)

	if out.ID != leaseID {
		t.Fatalf("lease id = %q, want %q (lease fields must be present)", out.ID, leaseID)
	}
	if out.AccountID != accountID {
		t.Fatalf("account id = %q, want %q", out.AccountID, accountID)
	}
	if !out.ShouldSwap {
		t.Fatalf("shouldSwap = false, want true for 5%% remaining")
	}
}

func TestHeartbeatNoSwapWhenHealthy(t *testing.T) {
	server, _, _, pat := newTestServer(t)
	leaseID, accountID := claimLease(t, server, pat)

	body := `{"accountId":"` + accountID + `","client":"tester","ttlSeconds":80,"fiveHour":{"key":"five_hour","label":"5h","usedPercent":20,"remainingPercent":80}}`
	out := doHeartbeat(t, server, pat, http.MethodPatch, "/api/sync/leases/"+leaseID, body)

	if out.ID != leaseID {
		t.Fatalf("lease id = %q, want %q", out.ID, leaseID)
	}
	if out.ShouldSwap {
		t.Fatalf("shouldSwap = true, want false for 80%% remaining")
	}
}

func TestHeartbeatRateLimitReachedForcesSwap(t *testing.T) {
	server, _, _, pat := newTestServer(t)
	leaseID, accountID := claimLease(t, server, pat)

	// Healthy 5h window would normally yield shouldSwap=false; rateLimitReached
	// must override it to true.
	body := `{"accountId":"` + accountID + `","client":"tester","ttlSeconds":80,"rateLimitReached":true,"fiveHour":{"key":"five_hour","label":"5h","usedPercent":20,"remainingPercent":80}}`
	out := doHeartbeat(t, server, pat, http.MethodPatch, "/api/sync/leases/"+leaseID, body)

	if !out.ShouldSwap {
		t.Fatalf("shouldSwap = false, want true when rateLimitReached=true")
	}
}

func TestHeartbeatLeasedQuotaKeepsCloudOwner(t *testing.T) {
	server, m, _, pat := newTestServer(t)
	leaseID, accountID := claimLease(t, server, pat)

	body := `{"accountId":"` + accountID + `","client":"tester","ttlSeconds":80,"fiveHour":{"key":"five_hour","label":"5h","usedPercent":95,"remainingPercent":5}}`
	_ = doHeartbeat(t, server, pat, http.MethodPatch, "/api/sync/leases/"+leaseID, body)

	state, err := m.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	var found bool
	for _, account := range state.Accounts {
		if account.ID != accountID {
			continue
		}
		found = true
		if account.OwnerMode != manager.OwnerCloud {
			t.Fatalf("ownerMode = %q, want %q (RecordLeasedQuota must not flip owner)", account.OwnerMode, manager.OwnerCloud)
		}
	}
	if !found {
		t.Fatalf("account %q not found in state", accountID)
	}
	cache, ok := state.QuotaCache[accountID]
	if !ok {
		t.Fatalf("quota cache missing for account %q", accountID)
	}
	if cache.Source != manager.QuotaSourceClient {
		t.Fatalf("quota cache source = %q, want %q", cache.Source, manager.QuotaSourceClient)
	}
}

func TestHeartbeatExplicitPathParity(t *testing.T) {
	server, _, _, pat := newTestServer(t)
	leaseID, accountID := claimLease(t, server, pat)

	body := `{"accountId":"` + accountID + `","client":"tester","ttlSeconds":80,"fiveHour":{"key":"five_hour","label":"5h","usedPercent":95,"remainingPercent":5}}`
	out := doHeartbeat(t, server, pat, http.MethodPost, "/api/sync/leases/"+leaseID+"/heartbeat", body)

	if out.ID != leaseID {
		t.Fatalf("lease id = %q, want %q", out.ID, leaseID)
	}
	if !out.ShouldSwap {
		t.Fatalf("shouldSwap = false, want true via explicit /heartbeat path")
	}
}

func newTestServer(t *testing.T) (*Server, *manager.Manager, string, string) {
	t.Helper()
	m := newWebTestManager(t)
	if _, err := m.UpsertJSONProfile(manager.JSONProfile{
		ID:    "work",
		Label: "Work",
		Auth:  json.RawMessage(`{"OPENAI_API_KEY":"sk-test"}`),
	}); err != nil {
		t.Fatalf("UpsertJSONProfile() error = %v", err)
	}
	state, err := m.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	window := quota.Window{
		Key:              "five_hour",
		Label:            "5h",
		UsedPercent:      0,
		RemainingPercent: 100,
		UsedDisplay:      "0%",
		RemainingDisplay: "100%",
		ResetsAt:         time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	}
	state.QuotaCache["work"] = manager.QuotaCache{
		AccountID: "work",
		UpdatedAt: time.Now(),
		Result: quota.Result{
			Status: quota.StatusSupported,
			Plan:   "pro",
			Quotas: []quota.Window{window},
		},
		FiveHour: &window,
		Source:   manager.QuotaSourceCloud,
	}
	if err := m.Save(state); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	client, pat, err := m.CreateClient("tester")
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
	}
	// Enroll the test client into the default pool so lease claims resolve a
	// workspace. The seeded "work" account lives in the default workspace.
	if err := m.SetMembership(manager.DefaultWorkspaceID, client.ID, manager.RoleMember); err != nil {
		t.Fatalf("SetMembership() error = %v", err)
	}
	adminToken := "admin-token"
	return &Server{Manager: m, CloudToken: adminToken}, m, adminToken, pat
}

func newWebTestManager(t *testing.T) *manager.Manager {
	t.Helper()
	root := t.TempDir()
	m := &manager.Manager{
		StateDir:      filepath.Join(root, "state"),
		StatePath:     filepath.Join(root, "state", "state.json"),
		SettingsPath:  filepath.Join(root, "state", "settings.toml"),
		AccountsDir:   filepath.Join(root, "accounts"),
		LiveCodexHome: filepath.Join(root, "live"),
	}
	if err := m.Ensure(); err != nil {
		t.Fatalf("Ensure() error = %v", err)
	}
	return m
}

// TestManagerAccountViewReturnsView confirms ManagerAccountView returns the
// per-account view (with the cached quota the test seeds), matching what the
// old ListAccounts-based implementation produced.
func TestManagerAccountViewReturnsView(t *testing.T) {
	server, m, _, _ := newTestServer(t)
	state, err := m.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	var work manager.Account
	for _, a := range state.Accounts {
		if a.ID == "work" {
			work = a
		}
	}
	if work.ID == "" {
		t.Fatalf("seed account %q not found", "work")
	}

	view := server.ManagerAccountView(work)
	if view.ID != "work" {
		t.Fatalf("ManagerAccountView().ID = %q, want %q", view.ID, "work")
	}
	if view.Label != "Work" {
		t.Fatalf("ManagerAccountView().Label = %q, want %q", view.Label, "Work")
	}
}

// TestManagerAccountViewUnknownFallsBack confirms a view for an account that is
// not in state falls back to the bare account rather than erroring.
func TestManagerAccountViewUnknownFallsBack(t *testing.T) {
	server, _, _, _ := newTestServer(t)
	ghost := manager.Account{ID: "ghost", Label: "Ghost"}
	view := server.ManagerAccountView(ghost)
	if view.ID != "ghost" || view.Label != "Ghost" {
		t.Fatalf("ManagerAccountView(ghost) = %+v, want bare account fallback", view)
	}
}

// TestManagerAccountViewDoesNotRewriteState is the regression test for the fix:
// answering a single-account view must NOT rewrite the whole state file. The old
// implementation called ListAccounts -> syncManagedAccounts -> Save on every
// call. We assert state.json's bytes AND mtime are unchanged across the call.
func TestManagerAccountViewDoesNotRewriteState(t *testing.T) {
	server, m, _, _ := newTestServer(t)
	state, err := m.Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	var work manager.Account
	for _, a := range state.Accounts {
		if a.ID == "work" {
			work = a
		}
	}

	before, err := os.ReadFile(m.StatePath)
	if err != nil {
		t.Fatalf("read state before: %v", err)
	}
	infoBefore, err := os.Stat(m.StatePath)
	if err != nil {
		t.Fatalf("stat state before: %v", err)
	}

	// Sleep a hair so a rewrite would produce a distinguishable mtime.
	time.Sleep(10 * time.Millisecond)
	_ = server.ManagerAccountView(work)

	after, err := os.ReadFile(m.StatePath)
	if err != nil {
		t.Fatalf("read state after: %v", err)
	}
	infoAfter, err := os.Stat(m.StatePath)
	if err != nil {
		t.Fatalf("stat state after: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("ManagerAccountView rewrote state.json content (should be a pure read)")
	}
	if !infoBefore.ModTime().Equal(infoAfter.ModTime()) {
		t.Fatal("ManagerAccountView rewrote state.json (mtime changed; should be a pure read)")
	}
}
