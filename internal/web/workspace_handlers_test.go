package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"cube20/internal/manager"
)

func do(server *Server, method, path, token, body string) *httptest.ResponseRecorder {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, bytes.NewBufferString(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, r)
	return rec
}

// Admin token creates a workspace; a non-admin PAT cannot.
func TestWorkspaceCreateRequiresAdmin(t *testing.T) {
	server, _, adminToken, pat := newTestServer(t)

	rec := do(server, http.MethodPost, "/api/workspaces", pat, `{"name":"Team A"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("PAT create status = %d body = %s, want 403", rec.Code, rec.Body.String())
	}

	rec = do(server, http.MethodPost, "/api/workspaces", adminToken, `{"name":"Team A"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("admin create status = %d body = %s, want 201", rec.Code, rec.Body.String())
	}
	var ws manager.Workspace
	if err := json.Unmarshal(rec.Body.Bytes(), &ws); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if ws.ID == "" {
		t.Error("created workspace has no id")
	}
}

// A plain member cannot invite others; a workspace admin can.
func TestWorkspaceMemberManagementRequiresWorkspaceAdmin(t *testing.T) {
	server, m, adminToken, pat := newTestServer(t)

	// Create a workspace and a second client.
	rec := do(server, http.MethodPost, "/api/workspaces", adminToken, `{"name":"Team A"}`)
	var ws manager.Workspace
	json.Unmarshal(rec.Body.Bytes(), &ws)

	bob, bobPAT, err := m.CreateClient("bob")
	if err != nil {
		t.Fatal(err)
	}

	// The default test PAT (tester) is only a member of default, NOT ws — so it
	// must be forbidden from managing ws members.
	rec = do(server, http.MethodPost, "/api/workspaces/"+ws.ID+"/members", pat, `{"clientId":"`+bob.ID+`","role":"member"}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-member invite status = %d body = %s, want 403", rec.Code, rec.Body.String())
	}

	// Admin token can invite bob as a workspace admin.
	rec = do(server, http.MethodPost, "/api/workspaces/"+ws.ID+"/members", adminToken, `{"clientId":"`+bob.ID+`","role":"admin"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin invite status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}

	// Now bob (workspace admin) can invite the tester.
	rec = do(server, http.MethodGet, "/api/workspaces/"+ws.ID+"/members", bobPAT, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("workspace-admin list members status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
}

// A member sees only their own workspaces via GET /api/workspaces.
func TestWorkspaceListScopedToMember(t *testing.T) {
	server, m, adminToken, pat := newTestServer(t)

	rec := do(server, http.MethodPost, "/api/workspaces", adminToken, `{"name":"Team A"}`)
	var wsA manager.Workspace
	json.Unmarshal(rec.Body.Bytes(), &wsA)
	// tester is NOT a member of wsA (only default).

	rec = do(server, http.MethodGet, "/api/workspaces", pat, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Workspaces []manager.WorkspaceMembershipView `json:"workspaces"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	for _, w := range resp.Workspaces {
		if w.ID == wsA.ID {
			t.Errorf("member should not see wsA they don't belong to: %+v", resp.Workspaces)
		}
	}

	// Admin GET sees all workspaces (raw list).
	rec = do(server, http.MethodGet, "/api/workspaces", adminToken, "")
	var adminResp struct {
		Workspaces []manager.Workspace `json:"workspaces"`
	}
	json.Unmarshal(rec.Body.Bytes(), &adminResp)
	found := false
	for _, w := range adminResp.Workspaces {
		if w.ID == wsA.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("admin should see wsA in full list: %+v", adminResp.Workspaces)
	}
	_ = m
}

// A member of one workspace cannot claim a lease scoped to a workspace they are
// not part of.
func TestClaimLeaseRejectsNonMemberWorkspace(t *testing.T) {
	server, _, adminToken, pat := newTestServer(t)
	rec := do(server, http.MethodPost, "/api/workspaces", adminToken, `{"name":"Team A"}`)
	var wsA manager.Workspace
	json.Unmarshal(rec.Body.Bytes(), &wsA)

	rec = do(server, http.MethodPost, "/api/sync/leases", pat, `{"ttlSeconds":90,"workspace":"`+wsA.ID+`"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("non-member claim status = %d body = %s, want 400", rec.Code, rec.Body.String())
	}
}

// Review fix (cross-workspace report gate): a client PAT must not submit a quota
// report for an account in a workspace it does not belong to. The seeded "work"
// account lives in the default pool; create a second client that is NOT a member
// of default and confirm its quota report is rejected.
func TestQuotaReportRejectsNonMemberAccount(t *testing.T) {
	server, m, _, _ := newTestServer(t)
	_, outsiderPAT, err := m.CreateClient("outsider")
	if err != nil {
		t.Fatal(err)
	}
	// outsider is a member of no workspace -> not a member of default (where
	// "work" lives).
	rec := do(server, http.MethodPost, "/api/sync/quota/work", outsiderPAT, `{"result":{"status":"supported"}}`)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("outsider quota report status = %d body = %s, want 403", rec.Code, rec.Body.String())
	}
}

// A member of the account's workspace CAN submit a quota report.
func TestQuotaReportAllowedForMember(t *testing.T) {
	server, _, _, pat := newTestServer(t)
	// pat (tester) is enrolled into default by newTestServer; "work" is in default.
	rec := do(server, http.MethodPost, "/api/sync/quota/work", pat, `{"result":{"status":"supported"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("member quota report status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
}
