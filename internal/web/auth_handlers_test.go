package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// doReq issues a request with optional JSON body and an optional cookie jar
// (carried via the returned recorder's Set-Cookie). Returns the recorder.
func doJSON(server *Server, method, path, body string, cookies []*http.Cookie) *httptest.ResponseRecorder {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, bytes.NewBufferString(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	for _, c := range cookies {
		r.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, r)
	return rec
}

func sessionCookie(rec *httptest.ResponseRecorder) []*http.Cookie {
	return rec.Result().Cookies()
}

func TestRegisterLoginSessionFlow(t *testing.T) {
	server, _, _, _ := newTestServer(t)

	// register -> sets cookie
	rec := doJSON(server, http.MethodPost, "/api/auth/register", `{"username":"alice","password":"secret1"}`, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register status = %d body = %s", rec.Code, rec.Body.String())
	}
	cookies := sessionCookie(rec)
	if len(cookies) == 0 {
		t.Fatal("register did not set a session cookie")
	}

	// /api/auth/me with cookie works
	rec = doJSON(server, http.MethodGet, "/api/auth/me", "", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("me status = %d body = %s", rec.Code, rec.Body.String())
	}
	var me struct {
		User struct {
			Username string `json:"username"`
		} `json:"user"`
	}
	json.Unmarshal(rec.Body.Bytes(), &me)
	if me.User.Username != "alice" {
		t.Errorf("me username = %q, want alice", me.User.Username)
	}

	// me without cookie -> 401
	rec = doJSON(server, http.MethodGet, "/api/auth/me", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("me without cookie status = %d, want 401", rec.Code)
	}

	// duplicate register -> 400
	rec = doJSON(server, http.MethodPost, "/api/auth/register", `{"username":"alice","password":"other1"}`, nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("dup register status = %d, want 400", rec.Code)
	}

	// login bad password -> 401
	rec = doJSON(server, http.MethodPost, "/api/auth/login", `{"username":"alice","password":"nope"}`, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("bad login status = %d, want 401", rec.Code)
	}

	// login good -> cookie
	rec = doJSON(server, http.MethodPost, "/api/auth/login", `{"username":"alice","password":"secret1"}`, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestDeviceMintAndIsolation(t *testing.T) {
	server, _, _, _ := newTestServer(t)
	// alice registers
	rec := doJSON(server, http.MethodPost, "/api/auth/register", `{"username":"alice","password":"secret1"}`, nil)
	aliceCookies := sessionCookie(rec)
	// bob registers
	rec = doJSON(server, http.MethodPost, "/api/auth/register", `{"username":"bob","password":"secret1"}`, nil)
	bobCookies := sessionCookie(rec)

	// alice mints a device
	rec = doJSON(server, http.MethodPost, "/api/devices", `{"label":"work"}`, aliceCookies)
	if rec.Code != http.StatusCreated {
		t.Fatalf("device mint status = %d body = %s", rec.Code, rec.Body.String())
	}
	var created struct {
		Device struct {
			ID string `json:"id"`
		} `json:"device"`
		Token string `json:"token"`
	}
	json.Unmarshal(rec.Body.Bytes(), &created)
	if created.Token == "" || created.Device.ID == "" {
		t.Fatal("device mint returned empty token/id")
	}

	// alice lists -> sees 1
	rec = doJSON(server, http.MethodGet, "/api/devices", "", aliceCookies)
	var aliceList struct {
		Devices []struct{ ID string } `json:"devices"`
	}
	json.Unmarshal(rec.Body.Bytes(), &aliceList)
	if len(aliceList.Devices) != 1 {
		t.Errorf("alice device count = %d, want 1", len(aliceList.Devices))
	}

	// bob lists -> sees 0 (isolation)
	rec = doJSON(server, http.MethodGet, "/api/devices", "", bobCookies)
	var bobList struct {
		Devices []struct{ ID string } `json:"devices"`
	}
	json.Unmarshal(rec.Body.Bytes(), &bobList)
	if len(bobList.Devices) != 0 {
		t.Errorf("bob should see 0 devices, got %d", len(bobList.Devices))
	}

	// bob cannot revoke alice's device -> 403
	rec = doJSON(server, http.MethodDelete, "/api/devices/"+created.Device.ID, "", bobCookies)
	if rec.Code != http.StatusForbidden {
		t.Errorf("cross-user revoke status = %d, want 403", rec.Code)
	}

	// the minted device token authenticates against the sync API
	leaseReq := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	leaseReq.Header.Set("Authorization", "Bearer "+created.Token)
	leaseRec := httptest.NewRecorder()
	server.Handler().ServeHTTP(leaseRec, leaseReq)
	if leaseRec.Code != http.StatusOK {
		t.Errorf("device token /api/me status = %d, want 200", leaseRec.Code)
	}
}

func TestSessionUserCanReadPersonalMe(t *testing.T) {
	server, _, _, _ := newTestServer(t)

	rec := doJSON(server, http.MethodPost, "/api/auth/register", `{"username":"alice","password":"secret1"}`, nil)
	cookies := sessionCookie(rec)
	rec = doJSON(server, http.MethodPost, "/api/devices", `{"label":"laptop"}`, cookies)
	if rec.Code != http.StatusCreated {
		t.Fatalf("device mint status = %d body = %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(server, http.MethodPost, "/api/workspaces", `{"name":"Alice Pool"}`, cookies)
	if rec.Code != http.StatusCreated {
		t.Fatalf("workspace create status = %d body = %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(server, http.MethodGet, "/api/me", "", cookies)
	if rec.Code != http.StatusOK {
		t.Fatalf("session /api/me status = %d body = %s", rec.Code, rec.Body.String())
	}
	var me struct {
		Mode  string `json:"mode"`
		Admin bool   `json:"admin"`
		User  struct {
			Username string `json:"username"`
		} `json:"user"`
		Devices    []struct{ Label string } `json:"devices"`
		Workspaces []struct {
			ID   string `json:"id"`
			Role string `json:"role"`
		} `json:"workspaces"`
	}
	json.Unmarshal(rec.Body.Bytes(), &me)
	if me.Mode != "user" {
		t.Errorf("mode = %q, want user", me.Mode)
	}
	if me.Admin {
		t.Error("session user /api/me reported admin=true")
	}
	if me.User.Username != "alice" {
		t.Errorf("username = %q, want alice", me.User.Username)
	}
	if len(me.Devices) != 1 || me.Devices[0].Label != "laptop" {
		t.Errorf("devices = %+v, want one laptop", me.Devices)
	}
	if len(me.Workspaces) != 1 || me.Workspaces[0].Role != "admin" {
		t.Errorf("workspaces = %+v, want one admin workspace", me.Workspaces)
	}
}

func TestSessionUserMeWinsOverDeviceBearer(t *testing.T) {
	server, _, _, _ := newTestServer(t)

	rec := doJSON(server, http.MethodPost, "/api/auth/register", `{"username":"alice","password":"secret1"}`, nil)
	aliceCookies := sessionCookie(rec)
	rec = doJSON(server, http.MethodPost, "/api/devices", `{"label":"alice-laptop"}`, aliceCookies)
	if rec.Code != http.StatusCreated {
		t.Fatalf("alice device mint status = %d body = %s", rec.Code, rec.Body.String())
	}
	var aliceDevice struct {
		Token string `json:"token"`
	}
	json.Unmarshal(rec.Body.Bytes(), &aliceDevice)
	if aliceDevice.Token == "" {
		t.Fatal("alice device token is empty")
	}

	rec = doJSON(server, http.MethodPost, "/api/auth/register", `{"username":"bob","password":"secret1"}`, nil)
	bobCookies := sessionCookie(rec)

	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.Header.Set("Authorization", "Bearer "+aliceDevice.Token)
	for _, cookie := range bobCookies {
		req.AddCookie(cookie)
	}
	resp := httptest.NewRecorder()
	server.Handler().ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("mixed auth /api/me status = %d body = %s", resp.Code, resp.Body.String())
	}
	var me struct {
		Mode string `json:"mode"`
		User struct {
			Username string `json:"username"`
		} `json:"user"`
		Client struct {
			ID string `json:"id"`
		} `json:"client"`
	}
	json.Unmarshal(resp.Body.Bytes(), &me)
	if me.Mode != "user" {
		t.Fatalf("mode = %q, want user; body = %s", me.Mode, resp.Body.String())
	}
	if me.User.Username != "bob" {
		t.Errorf("username = %q, want bob", me.User.Username)
	}
	if me.Client.ID != "" {
		t.Errorf("client id = %q, want empty session-user payload", me.Client.ID)
	}
}

func TestDevicesRequireLogin(t *testing.T) {
	server, _, _, _ := newTestServer(t)
	rec := doJSON(server, http.MethodPost, "/api/devices", `{"label":"x"}`, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("device mint without login status = %d, want 401", rec.Code)
	}
}

// Security regression (Codex finding #1): a workspace admin must NOT get
// platform-wide admin (all users / all devices / cross-tenant audit).
func TestWorkspaceAdminIsNotPlatformAdmin(t *testing.T) {
	server, m, adminToken, _ := newTestServer(t)

	// register a user and make them admin of a workspace
	rec := doJSON(server, http.MethodPost, "/api/auth/register", `{"username":"wsadmin","password":"secret1"}`, nil)
	cookies := sessionCookie(rec)
	var reg struct {
		User struct{ ID string } `json:"user"`
	}
	json.Unmarshal(rec.Body.Bytes(), &reg)

	wsRec := do(server, http.MethodPost, "/api/workspaces", adminToken, `{"name":"Team A"}`)
	var ws struct{ ID string }
	json.Unmarshal(wsRec.Body.Bytes(), &ws)
	if err := m.SetMembership(ws.ID, reg.User.ID, "admin"); err != nil {
		t.Fatalf("SetMembership: %v", err)
	}

	// workspace admin must be denied the platform users roster
	rec = doJSON(server, http.MethodGet, "/api/users", "", cookies)
	if rec.Code != http.StatusForbidden {
		t.Errorf("workspace admin GET /api/users = %d, want 403", rec.Code)
	}
	// and the cross-tenant audit
	rec = doJSON(server, http.MethodGet, "/api/dispatches", "", cookies)
	if rec.Code != http.StatusForbidden {
		t.Errorf("workspace admin GET /api/dispatches = %d, want 403", rec.Code)
	}
	// cloud-token admin still allowed
	rec2 := do(server, http.MethodGet, "/api/users", adminToken, "")
	if rec2.Code != http.StatusOK {
		t.Errorf("cloud-token GET /api/users = %d, want 200", rec2.Code)
	}
}

// A logged-in user can create a workspace and becomes its admin, then sees it
// in their own workspace list and can manage members.
func TestUserSelfServeWorkspace(t *testing.T) {
	server, _, _, _ := newTestServer(t)
	rec := doJSON(server, http.MethodPost, "/api/auth/register", `{"username":"founder","password":"secret1"}`, nil)
	cookies := sessionCookie(rec)

	// create a workspace as a normal logged-in user
	rec = doJSON(server, http.MethodPost, "/api/workspaces", `{"name":"My Pool"}`, cookies)
	if rec.Code != http.StatusCreated {
		t.Fatalf("self-serve create status = %d body = %s", rec.Code, rec.Body.String())
	}
	var ws struct{ ID string }
	json.Unmarshal(rec.Body.Bytes(), &ws)
	if ws.ID == "" {
		t.Fatal("no workspace id returned")
	}

	// it shows up in the creator's workspace list with admin role
	rec = doJSON(server, http.MethodGet, "/api/workspaces", "", cookies)
	var list struct {
		Workspaces []struct {
			ID   string `json:"id"`
			Role string `json:"role"`
		} `json:"workspaces"`
	}
	json.Unmarshal(rec.Body.Bytes(), &list)
	found := false
	for _, w := range list.Workspaces {
		if w.ID == ws.ID {
			found = true
			if w.Role != "admin" {
				t.Errorf("creator role = %q, want admin", w.Role)
			}
		}
	}
	if !found {
		t.Errorf("creator does not see own workspace: %+v", list.Workspaces)
	}

	// creator (workspace admin) can list members
	rec = doJSON(server, http.MethodGet, "/api/workspaces/"+ws.ID+"/members", "", cookies)
	if rec.Code != http.StatusOK {
		t.Errorf("creator list members = %d, want 200", rec.Code)
	}

	// a non-member user cannot manage it
	rec = doJSON(server, http.MethodPost, "/api/auth/register", `{"username":"outsider","password":"secret1"}`, nil)
	outsider := sessionCookie(rec)
	rec = doJSON(server, http.MethodGet, "/api/workspaces/"+ws.ID+"/members", "", outsider)
	if rec.Code != http.StatusForbidden {
		t.Errorf("outsider list members = %d, want 403", rec.Code)
	}
}
