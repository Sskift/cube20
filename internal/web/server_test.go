package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"cube20/internal/manager"
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
	_, pat, err := m.CreateClient("tester")
	if err != nil {
		t.Fatalf("CreateClient() error = %v", err)
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
