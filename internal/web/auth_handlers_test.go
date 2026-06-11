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
