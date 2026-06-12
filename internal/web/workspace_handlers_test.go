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

// A bearer PAT (no session) cannot create a workspace; the admin token can.
// (Logged-in session users also can — covered by TestUserSelfServeWorkspace.)
func TestWorkspaceCreateRequiresAdmin(t *testing.T) {
	server, _, adminToken, pat := newTestServer(t)

	// A PAT carries no session/user identity, so the session-guarded route
	// rejects it with 401 before the handler runs.
	rec := do(server, http.MethodPost, "/api/workspaces", pat, `{"name":"Team A"}`)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("PAT create status = %d body = %s, want 401", rec.Code, rec.Body.String())
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

// A non-member session user cannot manage a workspace's members; a workspace
// admin can. (Workspaces are user/session-scoped now, not PAT-scoped.)
func TestWorkspaceMemberManagementRequiresWorkspaceAdmin(t *testing.T) {
	server, _, adminToken, _ := newTestServer(t)

	// admin token creates a workspace and a member user
	rec := do(server, http.MethodPost, "/api/workspaces", adminToken, `{"name":"Team A"}`)
	var ws manager.Workspace
	json.Unmarshal(rec.Body.Bytes(), &ws)

	rec = doJSON(server, http.MethodPost, "/api/auth/register", `{"username":"member1","password":"secret1"}`, nil)
	memberCookies := sessionCookie(rec)
	var reg struct {
		User struct{ ID string } `json:"user"`
	}
	json.Unmarshal(rec.Body.Bytes(), &reg)

	// non-member session user is forbidden from managing ws members
	rec = doJSON(server, http.MethodPost, "/api/workspaces/"+ws.ID+"/members", `{"userId":"`+reg.User.ID+`","role":"member"}`, memberCookies)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-member invite status = %d body = %s, want 403", rec.Code, rec.Body.String())
	}

	// admin token can add the user as a workspace admin
	rec = do(server, http.MethodPost, "/api/workspaces/"+ws.ID+"/members", adminToken, `{"userId":"`+reg.User.ID+`","role":"admin"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin invite status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}

	// now that user (workspace admin) can list members
	rec = doJSON(server, http.MethodGet, "/api/workspaces/"+ws.ID+"/members", "", memberCookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("workspace-admin list members status = %d body = %s, want 200", rec.Code, rec.Body.String())
	}
}

// A session user sees only their own workspaces via GET /api/workspaces; the
// admin token sees all.
func TestWorkspaceListScopedToMember(t *testing.T) {
	server, _, adminToken, _ := newTestServer(t)

	rec := do(server, http.MethodPost, "/api/workspaces", adminToken, `{"name":"Team A"}`)
	var wsA manager.Workspace
	json.Unmarshal(rec.Body.Bytes(), &wsA)

	// a fresh user belongs to no workspace
	rec = doJSON(server, http.MethodPost, "/api/auth/register", `{"username":"loner","password":"secret1"}`, nil)
	cookies := sessionCookie(rec)

	rec = doJSON(server, http.MethodGet, "/api/workspaces", "", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body = %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Workspaces []manager.WorkspaceMembershipView `json:"workspaces"`
	}
	json.Unmarshal(rec.Body.Bytes(), &resp)
	for _, w := range resp.Workspaces {
		if w.ID == wsA.ID {
			t.Errorf("non-member should not see wsA: %+v", resp.Workspaces)
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
