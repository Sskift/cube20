package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWorkspaceInviteRegisterAndDeviceFlow(t *testing.T) {
	server, _, _, _ := newTestServer(t)

	rec := doJSON(server, http.MethodPost, "/api/auth/register", `{"username":"owner","password":"secret1"}`, nil)
	ownerCookies := sessionCookie(rec)
	rec = doJSON(server, http.MethodPost, "/api/workspaces", `{"name":"Invite Pool"}`, ownerCookies)
	if rec.Code != http.StatusCreated {
		t.Fatalf("workspace create status = %d body = %s", rec.Code, rec.Body.String())
	}
	var ws struct {
		ID string `json:"id"`
	}
	json.Unmarshal(rec.Body.Bytes(), &ws)

	rec = doJSON(server, http.MethodPost, "/api/workspaces/"+ws.ID+"/invites", `{"role":"member","expiresInHours":24}`, ownerCookies)
	if rec.Code != http.StatusCreated {
		t.Fatalf("invite create status = %d body = %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Invite struct {
			ID          string `json:"id"`
			WorkspaceID string `json:"workspaceId"`
			TokenHash   string `json:"tokenHash"`
			Role        string `json:"role"`
		} `json:"invite"`
		Token string `json:"token"`
		URL   string `json:"url"`
	}
	json.Unmarshal(rec.Body.Bytes(), &created)
	if created.Token == "" || created.Invite.ID == "" || created.Invite.TokenHash != "" || created.Invite.WorkspaceID != ws.ID || created.Invite.Role != "member" {
		t.Fatalf("created invite = %+v", created)
	}

	rec = doJSON(server, http.MethodGet, "/api/invites/"+created.Token, "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("invite preview status = %d body = %s", rec.Code, rec.Body.String())
	}
	var preview struct {
		Valid         bool   `json:"valid"`
		WorkspaceID   string `json:"workspaceId"`
		WorkspaceName string `json:"workspaceName"`
		Role          string `json:"role"`
	}
	json.Unmarshal(rec.Body.Bytes(), &preview)
	if !preview.Valid || preview.WorkspaceID != ws.ID || preview.WorkspaceName != "Invite Pool" || preview.Role != "member" {
		t.Fatalf("preview = %+v", preview)
	}

	rec = doJSON(server, http.MethodPost, "/api/invites/"+created.Token+"/register", `{"username":"newmember","password":"secret1"}`, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("invite register status = %d body = %s", rec.Code, rec.Body.String())
	}
	memberCookies := sessionCookie(rec)
	var registered struct {
		User struct {
			ID       string `json:"id"`
			Username string `json:"username"`
		} `json:"user"`
		Workspaces []struct {
			ID   string `json:"id"`
			Role string `json:"role"`
		} `json:"workspaces"`
	}
	json.Unmarshal(rec.Body.Bytes(), &registered)
	if registered.User.Username != "newmember" || len(registered.Workspaces) != 1 || registered.Workspaces[0].ID != ws.ID || registered.Workspaces[0].Role != "member" {
		t.Fatalf("registered = %+v", registered)
	}

	rec = doJSON(server, http.MethodPost, "/api/devices", `{"label":"laptop"}`, memberCookies)
	if rec.Code != http.StatusCreated {
		t.Fatalf("device create status = %d body = %s", rec.Code, rec.Body.String())
	}
	var deviceCreated struct {
		Token string `json:"token"`
	}
	json.Unmarshal(rec.Body.Bytes(), &deviceCreated)
	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.Header.Set("Authorization", "Bearer "+deviceCreated.Token)
	resp := httptest.NewRecorder()
	server.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("device /api/me status = %d body = %s", resp.Code, resp.Body.String())
	}
	var me struct {
		Mode       string `json:"mode"`
		Workspaces []struct {
			ID   string `json:"id"`
			Role string `json:"role"`
		} `json:"workspaces"`
	}
	json.Unmarshal(resp.Body.Bytes(), &me)
	if me.Mode != "client" || len(me.Workspaces) != 1 || me.Workspaces[0].ID != ws.ID {
		t.Fatalf("device me = %+v", me)
	}
}

func TestWorkspaceInviteJoinExistingUserIsIdempotent(t *testing.T) {
	server, _, _, _ := newTestServer(t)
	rec := doJSON(server, http.MethodPost, "/api/auth/register", `{"username":"owner2","password":"secret1"}`, nil)
	ownerCookies := sessionCookie(rec)
	rec = doJSON(server, http.MethodPost, "/api/workspaces", `{"name":"Join Pool"}`, ownerCookies)
	var ws struct {
		ID string `json:"id"`
	}
	json.Unmarshal(rec.Body.Bytes(), &ws)
	rec = doJSON(server, http.MethodPost, "/api/workspaces/"+ws.ID+"/invites", `{"role":"member"}`, ownerCookies)
	var created struct {
		Token string `json:"token"`
	}
	json.Unmarshal(rec.Body.Bytes(), &created)

	rec = doJSON(server, http.MethodPost, "/api/auth/register", `{"username":"existing","password":"secret1"}`, nil)
	existingCookies := sessionCookie(rec)
	for i := 0; i < 2; i++ {
		rec = doJSON(server, http.MethodPost, "/api/invites/"+created.Token+"/join", `{}`, existingCookies)
		if rec.Code != http.StatusOK {
			t.Fatalf("join #%d status = %d body = %s", i+1, rec.Code, rec.Body.String())
		}
	}
	rec = doJSON(server, http.MethodGet, "/api/workspaces/"+ws.ID+"/members", "", ownerCookies)
	var members struct {
		Members []struct {
			UserID string `json:"userId"`
			Role   string `json:"role"`
		} `json:"members"`
	}
	json.Unmarshal(rec.Body.Bytes(), &members)
	foundExisting := 0
	for _, member := range members.Members {
		if member.UserID == "user-existing" {
			foundExisting++
			if member.Role != "member" {
				t.Fatalf("existing role = %q, want member", member.Role)
			}
		}
	}
	if foundExisting != 1 {
		t.Fatalf("existing membership count = %d, members = %+v", foundExisting, members.Members)
	}
}

func TestWorkspaceInviteAuthorizationAndRevocation(t *testing.T) {
	server, _, _, _ := newTestServer(t)
	rec := doJSON(server, http.MethodPost, "/api/auth/register", `{"username":"owner3","password":"secret1"}`, nil)
	ownerCookies := sessionCookie(rec)
	rec = doJSON(server, http.MethodPost, "/api/workspaces", `{"name":"Secure Pool"}`, ownerCookies)
	var ws struct {
		ID string `json:"id"`
	}
	json.Unmarshal(rec.Body.Bytes(), &ws)

	rec = doJSON(server, http.MethodPost, "/api/auth/register", `{"username":"outsider","password":"secret1"}`, nil)
	outsiderCookies := sessionCookie(rec)
	rec = doJSON(server, http.MethodPost, "/api/workspaces/"+ws.ID+"/invites", `{"role":"member"}`, outsiderCookies)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("outsider create status = %d body = %s, want 403", rec.Code, rec.Body.String())
	}

	rec = doJSON(server, http.MethodPost, "/api/workspaces/"+ws.ID+"/invites", `{"role":"member"}`, ownerCookies)
	if rec.Code != http.StatusCreated {
		t.Fatalf("owner create status = %d body = %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Invite struct {
			ID string `json:"id"`
		} `json:"invite"`
		Token string `json:"token"`
	}
	json.Unmarshal(rec.Body.Bytes(), &created)
	rec = doJSON(server, http.MethodGet, "/api/workspaces/"+ws.ID+"/invites", "", ownerCookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body = %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() == "" || rec.Body.String() == "null" {
		t.Fatalf("empty list body")
	}

	rec = doJSON(server, http.MethodDelete, "/api/workspaces/"+ws.ID+"/invites/"+created.Invite.ID, "", ownerCookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("revoke status = %d body = %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(server, http.MethodGet, "/api/invites/"+created.Token, "", nil)
	if rec.Code != http.StatusGone {
		t.Fatalf("revoked preview status = %d body = %s, want 410", rec.Code, rec.Body.String())
	}
	rec = doJSON(server, http.MethodPost, "/api/invites/"+created.Token+"/register", `{"username":"late","password":"secret1"}`, nil)
	if rec.Code != http.StatusGone {
		t.Fatalf("revoked register status = %d body = %s, want 410", rec.Code, rec.Body.String())
	}
}
