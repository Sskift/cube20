package web

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"cube20/internal/manager"
	"cube20/internal/quota"
	"cube20/internal/usage"
	"cube20/web"
)

type Server struct {
	Manager              *manager.Manager
	Host                 string
	Port                 int
	CloudToken           string
	QuotaRefreshInterval time.Duration

	httpServer  *http.Server
	limiterOnce sync.Once
	limiterImpl *loginLimiter
}

// limiter lazily builds the login rate limiter so a zero-value Server (struct
// literal in main.go) needs no constructor change.
func (s *Server) limiterGet() *loginLimiter {
	s.limiterOnce.Do(func() { s.limiterImpl = newLoginLimiter() })
	return s.limiterImpl
}

type authContextKey struct{}

type requestAuth struct {
	Admin     bool
	ClientID  string
	UserID    string
	DeviceID  string
	SessionID string
}

// principal returns the workspace-membership key for this caller: the user id
// for a website session, else the device/client id for a bearer token. Workspace
// membership and role checks key on this so both auth kinds resolve correctly.
func (a requestAuth) principal() string {
	if a.UserID != "" {
		return a.UserID
	}
	return a.ClientID
}

func (s *Server) ListenAndServe() error {
	if s.Host == "" {
		s.Host = "127.0.0.1"
	}
	if s.Port == 0 {
		s.Port = 8720
	}

	// Fix #9: refuse to silently run wide open. When no admin token is set every
	// request is treated as admin (see adminAuthorized); binding that to a
	// non-loopback address exposes full admin with zero auth.
	if !isLoopbackHost(s.Host) && strings.TrimSpace(s.CloudToken) == "" {
		return errors.New("refusing to bind " + s.Host + " without an admin token: set CUBE_CLOUD_TOKEN")
	}

	// Fix #8: cancellable context so the quota worker stops on shutdown, and a
	// signal-aware context so we can drain in-flight requests gracefully.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if s.QuotaRefreshInterval > 0 {
		StartQuotaWorker(ctx, s.Manager, s.QuotaRefreshInterval, log.Printf)
	}

	addr := fmt.Sprintf("%s:%d", s.Host, s.Port)
	fmt.Printf("cube dashboard: http://%s\n", addr)
	if strings.TrimSpace(s.CloudToken) != "" {
		fmt.Println("cube dashboard: API bearer token is required")
	}

	// Fix #5: use an explicit *http.Server with bounded timeouts instead of
	// http.ListenAndServe (no timeouts -> slow-loris / resource exhaustion).
	srv := s.newHTTPServer(addr)
	s.httpServer = srv

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- srv.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		// Server stopped on its own before any shutdown signal.
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		// Received SIGINT/SIGTERM: cancel the worker, drain requests, release DB.
		cancel()
		s.shutdown()
		if err := <-serveErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

// newHTTPServer builds the dashboard HTTP server with bounded timeouts so a
// single slow client cannot tie up server resources indefinitely (Fix #5).
func (s *Server) newHTTPServer(addr string) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
}

// shutdown gracefully drains in-flight requests and releases the manager's DB
// pool. Safe to call once on the shutdown path (Fix #8).
func (s *Server) shutdown() {
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if s.httpServer != nil {
		if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
			log.Printf("cube dashboard: graceful shutdown failed: %v", err)
		}
	}
	if s.Manager != nil {
		if err := s.Manager.Close(); err != nil {
			log.Printf("cube dashboard: closing manager failed: %v", err)
		}
	}
}

// isLoopbackHost reports whether host is a loopback bind target. An empty host
// is treated as loopback because ListenAndServe defaults it to 127.0.0.1.
func isLoopbackHost(host string) bool {
	host = strings.TrimSpace(host)
	if host == "" || host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	admin := func(handler http.HandlerFunc) http.HandlerFunc {
		return s.withAdminAuth(handler)
	}
	sync := func(handler http.HandlerFunc) http.HandlerFunc {
		return s.withSyncAuth(handler)
	}
	session := func(handler http.HandlerFunc) http.HandlerFunc {
		return s.withSessionAuth(handler)
	}
	anyAuth := func(handler http.HandlerFunc) http.HandlerFunc {
		return s.withAnyAuth(handler)
	}
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/readyz", s.handleReadyz)
	// Website auth (session cookie). register/login are unauthenticated.
	mux.HandleFunc("/api/auth/register", s.handleAuthRegister)
	mux.HandleFunc("/api/auth/login", s.handleAuthLogin)
	mux.HandleFunc("/api/auth/logout", session(s.handleAuthLogout))
	mux.HandleFunc("/api/auth/me", session(s.handleAuthMe))
	mux.HandleFunc("/api/devices", session(s.handleDevices))
	mux.HandleFunc("/api/devices/", session(s.handleDeviceAction))
	mux.HandleFunc("/api/users", session(s.handleUsers))
	mux.HandleFunc("/api/users/", session(s.handleUserAction))
	mux.HandleFunc("/api/sync/push", sync(s.handleSyncPush))
	mux.HandleFunc("/api/sync/pull/", sync(s.handleSyncPull))
	mux.HandleFunc("/api/sync/claim", sync(s.handleSyncClaim))
	mux.HandleFunc("/api/sync/leases", sync(s.handleSyncLeases))
	mux.HandleFunc("/api/sync/leases/", sync(s.handleSyncLeaseAction))
	mux.HandleFunc("/api/sync/usage", sync(s.handleSyncUsage))
	mux.HandleFunc("/api/sync/quota/", sync(s.handleSyncQuota))
	mux.HandleFunc("/api/me", anyAuth(s.handleMe))
	mux.HandleFunc("/api/workspaces", session(s.handleWorkspaces))
	mux.HandleFunc("/api/workspaces/", session(s.handleWorkspaceAction))
	mux.HandleFunc("/api/clients", admin(s.handleClients))
	mux.HandleFunc("/api/clients/", admin(s.handleClientAction))
	mux.HandleFunc("/api/stats", admin(s.handleStats))
	mux.HandleFunc("/api/dispatches", session(s.handleDispatches))
	mux.HandleFunc("/api/refresh-queue", admin(s.handleRefreshQueue))
	mux.HandleFunc("/api/accounts/import-json", admin(s.handleImportJSON))
	mux.HandleFunc("/api/accounts/pick", admin(s.handleLBPick))
	mux.HandleFunc("/api/lb/pick", admin(s.handleLBPick))
	mux.HandleFunc("/api/lb/reset", admin(s.handleLBReset))
	mux.HandleFunc("/api/lb/status", admin(s.handleLBStatus))
	mux.HandleFunc("/api/accounts", admin(s.handleAccounts))
	mux.HandleFunc("/api/accounts/", admin(s.handleAccountAction))
	mux.HandleFunc("/api/meta", admin(s.handleMeta))
	mux.HandleFunc("/api/settings", admin(s.handleSettings))

	distSub, err := fs.Sub(webdist.DistFS, "dist")
	if err != nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to sub dist folder: %v", err))
		})
	}
	mux.Handle("/", staticHandler(distSub))
	return mux
}

func (s *Server) withAdminAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if auth, ok := s.adminAuthorized(r); ok {
			next(w, r.WithContext(context.WithValue(r.Context(), authContextKey{}, auth)))
			return
		}
		writeError(w, http.StatusUnauthorized, "missing or invalid admin token")
	}
}

func (s *Server) withSyncAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if auth, ok := s.adminAuthorized(r); ok {
			next(w, r.WithContext(context.WithValue(r.Context(), authContextKey{}, auth)))
			return
		}
		token := requestToken(r)
		if client, ok := s.Manager.AuthenticateClientToken(token); ok {
			// The client row IS the device; setting ClientID=DeviceID keeps every
			// existing ClientID-keyed handler working, while UserID/DeviceID give
			// the new audit dimension.
			auth := requestAuth{ClientID: client.ID, DeviceID: client.ID, UserID: client.UserID}
			if message := clientPATSyncForbidden(r); message != "" {
				writeError(w, http.StatusForbidden, message)
				return
			}
			_ = s.Manager.TouchClient(client.ID)
			next(w, r.WithContext(context.WithValue(r.Context(), authContextKey{}, auth)))
			return
		}
		writeError(w, http.StatusUnauthorized, "missing or invalid PAT")
	}
}

func (s *Server) withAnyAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if auth, ok := s.adminAuthorized(r); ok {
			next(w, r.WithContext(context.WithValue(r.Context(), authContextKey{}, auth)))
			return
		}
		token := requestToken(r)
		if client, ok := s.Manager.AuthenticateClientToken(token); ok {
			auth := requestAuth{ClientID: client.ID, DeviceID: client.ID, UserID: client.UserID}
			if message := clientPATSyncForbidden(r); message != "" {
				writeError(w, http.StatusForbidden, message)
				return
			}
			_ = s.Manager.TouchClient(client.ID)
			next(w, r.WithContext(context.WithValue(r.Context(), authContextKey{}, auth)))
			return
		}
		if cookie, err := r.Cookie(sessionCookieName); err == nil && cookie.Value != "" {
			if user, sessionID, ok := s.Manager.ResolveSession(cookie.Value); ok {
				auth := requestAuth{UserID: user.ID, SessionID: sessionID}
				next(w, r.WithContext(context.WithValue(r.Context(), authContextKey{}, auth)))
				return
			}
		}
		writeError(w, http.StatusUnauthorized, "missing or invalid credentials")
	}
}

func (s *Server) adminAuthorized(r *http.Request) (requestAuth, bool) {
	expected := strings.TrimSpace(s.CloudToken)
	if expected == "" {
		return requestAuth{Admin: true}, true
	}
	candidate := requestToken(r)
	if len(candidate) != len(expected) {
		return requestAuth{}, false
	}
	return requestAuth{Admin: true}, subtle.ConstantTimeCompare([]byte(candidate), []byte(expected)) == 1
}

func requestToken(r *http.Request) string {
	candidate := strings.TrimSpace(r.Header.Get("X-Cube-Token"))
	if candidate == "" {
		auth := strings.TrimSpace(r.Header.Get("Authorization"))
		if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
			candidate = strings.TrimSpace(auth[len("bearer "):])
		}
	}
	if candidate == "" {
		candidate = strings.TrimSpace(r.URL.Query().Get("token"))
	}
	return candidate
}

func authFromRequest(r *http.Request) requestAuth {
	auth, _ := r.Context().Value(authContextKey{}).(requestAuth)
	return auth
}

func clientPATSyncForbidden(r *http.Request) string {
	path := r.URL.Path
	switch {
	case path == "/api/me":
		if r.Method == http.MethodGet {
			return ""
		}
		return "client PATs can only read /api/me"
	case path == "/api/sync/claim":
		if r.Method == http.MethodGet || r.Method == http.MethodPost {
			return ""
		}
		return "client PATs can only claim leases with GET or POST"
	case path == "/api/sync/leases":
		if r.Method == http.MethodPost {
			return ""
		}
		return "client PATs can only create leases at /api/sync/leases"
	case strings.HasPrefix(path, "/api/sync/leases/"):
		return clientPATLeaseActionForbidden(r)
	case path == "/api/sync/push":
		if r.Method == http.MethodPost {
			return ""
		}
		return "client PATs can only upload leased auth or client-owned reports with POST"
	case path == "/api/sync/usage":
		if r.Method == http.MethodPost {
			return ""
		}
		return "client PATs can only upload usage reports"
	case strings.HasPrefix(path, "/api/sync/quota/"):
		if r.Method == http.MethodPost || r.Method == http.MethodPut {
			return ""
		}
		return "client PATs can only upload quota reports; use an admin token to fetch quota"
	case strings.HasPrefix(path, "/api/sync/pull/"):
		return "client PATs cannot pull auth snapshots; use an admin token"
	case path == "/api/workspaces":
		// Members list their own workspaces (GET); POST is gated to the admin
		// token inside the handler. Both are allowed past the PAT gate here.
		return ""
	case strings.HasPrefix(path, "/api/workspaces/"):
		// Workspace member management; the handler enforces workspace-admin role.
		return ""
	default:
		return "client PAT is not allowed to call this sync route"
	}
}

func clientPATLeaseActionForbidden(r *http.Request) string {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/sync/leases/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) == 1 && parts[0] != "" {
		if r.Method == http.MethodPatch || r.Method == http.MethodPost || r.Method == http.MethodDelete {
			return ""
		}
		return "client PATs can only heartbeat or release their own leases"
	}
	if len(parts) == 2 && parts[0] != "" {
		switch parts[1] {
		case "heartbeat":
			if r.Method == http.MethodPatch || r.Method == http.MethodPost {
				return ""
			}
			return "client PATs can only heartbeat their own leases with PATCH or POST"
		case "auth":
			if r.Method == http.MethodPost || r.Method == http.MethodPut {
				return ""
			}
			return "client PATs can only upload auth for their own leases with POST or PUT"
		}
	}
	return "client PATs can only heartbeat, release, or upload auth for their own leases"
}

func staticHandler(dist fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		assetPath := strings.TrimPrefix(r.URL.Path, "/")
		if assetPath == "" {
			assetPath = "index.html"
		}
		if _, err := fs.Stat(dist, assetPath); err != nil {
			r = r.Clone(r.Context())
			r.URL.Path = "/index.html"
		}
		fileServer.ServeHTTP(w, r)
	})
}

func (s *Server) handleLBPick(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	account, err := s.Manager.SelectAccountForRun()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.ManagerAccountView(account.Account))
}

func (s *Server) handleLBStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	status, err := s.Manager.LoadBalanceStatus(strings.TrimSpace(r.URL.Query().Get("workspace")))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleLBReset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if err := s.Manager.ResetRoundRobin(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	status, err := s.Manager.LoadBalanceStatus(strings.TrimSpace(r.URL.Query().Get("workspace")))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleDispatches(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.userIsPlatformAdmin(authFromRequest(r)) {
		writeError(w, http.StatusForbidden, "admin only")
		return
	}
	limit := 50
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		limit = value
	}
	events, err := s.Manager.DispatchHistory(limit, "")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, events)
}

func (s *Server) handleMeta(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	s.writeMeta(w)
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		text, err := s.Manager.ReadSettingsText()
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"settingsPath":      s.Manager.SettingsPath,
			"settingsToml":      text,
			"liveCodexHome":     s.Manager.LiveCodexHome,
			"accountsDir":       s.Manager.AccountsDir,
			"cloudUrl":          s.Manager.CloudURL,
			"cloudTokenPresent": strings.TrimSpace(s.Manager.CloudToken) != "",
		})
	case http.MethodPatch:
		var body struct {
			LiveCodexHome string `json:"liveCodexHome"`
			AccountsDir   string `json:"accountsDir"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json")
			return
		}
		if _, err := s.Manager.UpdateSettings(body.LiveCodexHome, body.AccountsDir, ""); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.writeMeta(w)
	case http.MethodPut:
		var body struct {
			SettingsToml string `json:"settingsToml"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json")
			return
		}
		settings, err := s.Manager.WriteSettingsText(body.SettingsToml)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		text, _ := s.Manager.ReadSettingsText()
		writeJSON(w, http.StatusOK, map[string]any{
			"settingsPath":      s.Manager.SettingsPath,
			"settingsToml":      text,
			"liveCodexHome":     settings.LiveCodexHome,
			"accountsDir":       settings.AccountsDir,
			"cloudUrl":          settings.CloudURL,
			"cloudTokenPresent": strings.TrimSpace(settings.CloudToken) != "",
		})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) writeMeta(w http.ResponseWriter) {
	live := s.Manager.LiveProfileView()
	writeJSON(w, http.StatusOK, map[string]any{
		"statePath":         s.Manager.StatePath,
		"settingsPath":      s.Manager.SettingsPath,
		"accountsDir":       s.Manager.AccountsDir,
		"liveCodexHome":     s.Manager.LiveCodexHome,
		"liveAuthPresent":   live.AuthPresent,
		"liveConfigPresent": live.ConfigPresent,
	})
}

func (s *Server) handleSyncPush(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	var snapshot manager.ProfileSnapshot
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&snapshot); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	auth := authFromRequest(r)
	if !auth.Admin && strings.TrimSpace(snapshot.LeaseID) == "" {
		if strings.TrimSpace(snapshot.ID) == "" || snapshot.OwnerMode != manager.OwnerClient {
			writeError(w, http.StatusForbidden, "client PATs can only push leased auth or their own client-owned report")
			return
		}
		if existing, err := s.Manager.GetAccount(snapshot.ID); err == nil {
			if existing.OwnerMode != manager.OwnerClient {
				writeError(w, http.StatusForbidden, "client PATs cannot replace cloud-owned auth")
				return
			}
			if existing.OwnerClientID != "" && existing.OwnerClientID != auth.ClientID {
				writeError(w, http.StatusForbidden, "client PATs can only update accounts owned by the same client")
				return
			}
		}
		snapshot.OwnerClientID = auth.ClientID
		snapshot.SourceClient = auth.ClientID
	}
	if snapshot.OwnerMode == manager.OwnerClient && strings.TrimSpace(snapshot.OwnerClientID) == "" {
		snapshot.OwnerClientID = auth.ClientID
	}
	if strings.TrimSpace(snapshot.SourceClient) == "" {
		snapshot.SourceClient = auth.ClientID
	}
	var account manager.Account
	var err error
	if strings.TrimSpace(snapshot.LeaseID) != "" {
		account, err = s.Manager.UpdateLeasedProfileSnapshot(snapshot, auth.ClientID, 90*time.Second)
	} else {
		if strings.TrimSpace(snapshot.ID) != "" {
			leased, leaseErr := s.Manager.AccountHasActiveLease(snapshot.ID)
			if leaseErr != nil {
				writeError(w, http.StatusBadRequest, leaseErr.Error())
				return
			}
			if leased {
				writeError(w, http.StatusConflict, "account is currently leased; use the lease auth endpoint or wait for release")
				return
			}
		}
		account, err = s.Manager.UpsertProfileSnapshot(snapshot)
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.ManagerAccountView(account))
}

func (s *Server) handleSyncPull(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/sync/pull/"), "/")
	if id == "" {
		id = strings.TrimSpace(r.URL.Query().Get("id"))
	}
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing account id")
		return
	}
	s.refreshBeforeExport(r.Context(), id)
	snapshot, err := s.Manager.ExportProfileSnapshot(id)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (s *Server) handleSyncClaim(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	auth := authFromRequest(r)
	workspaceID, err := s.Manager.ResolveWorkspaceForClient(auth.ClientID, strings.TrimSpace(r.URL.Query().Get("workspace")))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	leaseSnapshot, err := s.Manager.ClaimLease(r.Context(), auth.ClientID, firstText(auth.ClientID, r.RemoteAddr), workspaceID, 90*time.Second)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, leaseSnapshot.Snapshot)
}

func (s *Server) handleSyncLeases(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		ClientID   string `json:"clientId"`
		Client     string `json:"client"`
		Holder     string `json:"holder"`
		Workspace  string `json:"workspace"`
		TTLSeconds int    `json:"ttlSeconds"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
	}
	auth := authFromRequest(r)
	clientID := strings.TrimSpace(auth.ClientID)
	if clientID == "" {
		clientID = strings.TrimSpace(body.ClientID)
	}
	requestedWS := firstText(strings.TrimSpace(body.Workspace), strings.TrimSpace(r.URL.Query().Get("workspace")))
	workspaceID, err := s.Manager.ResolveWorkspaceForClient(clientID, requestedWS)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	holder := firstText(body.Holder, body.Client, clientID, r.RemoteAddr)
	ttl := time.Duration(body.TTLSeconds) * time.Second
	leaseSnapshot, err := s.Manager.ClaimLease(r.Context(), clientID, holder, workspaceID, ttl)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, leaseSnapshot)
}

// heartbeatResponse is the lease-heartbeat wire shape: the lease fields promote
// to the top level (backward compatible with the old bare-lease response) plus
// a shouldSwap hint telling the client to roll to a fresher account.
type heartbeatResponse struct {
	manager.Lease
	ShouldSwap bool `json:"shouldSwap"`
}

// heartbeatLease is the shared handler for both lease-heartbeat routes (the
// single-segment PATCH/POST on /api/sync/leases/{id} and the explicit
// /api/sync/leases/{id}/heartbeat). It records any client-reported 5h quota
// window (best-effort), refreshes the lease, and returns a swap hint.
func (s *Server) heartbeatLease(w http.ResponseWriter, r *http.Request, leaseID string, auth requestAuth) {
	var body struct {
		AccountID        string         `json:"accountId"`
		Client           string         `json:"client"`
		Holder           string         `json:"holder"`
		TTLSeconds       int            `json:"ttlSeconds"`
		FiveHour         *quota.Window  `json:"fiveHour"`
		Quotas           []quota.Window `json:"quotas"`
		RateLimitReached bool           `json:"rateLimitReached"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
	}
	ttl := time.Duration(body.TTLSeconds) * time.Second
	accountID := strings.TrimSpace(body.AccountID)

	// Best-effort: persist the client-reported quota windows without flipping the
	// account's owner mode. Newer clients send the full window set in Quotas
	// (5h + 7d); older clients send only FiveHour. The cloud does not probe quota
	// itself during a lease, so these reports are the sole source of truth while
	// an account is held. A failed report (e.g. the lease just expired) must never
	// break the heartbeat, so we ignore the error and proceed.
	windows := body.Quotas
	if len(windows) == 0 && body.FiveHour != nil {
		windows = []quota.Window{*body.FiveHour}
	}
	if len(windows) > 0 {
		result := quota.Result{
			Status: quota.StatusSupported,
			Quotas: windows,
		}
		_ = s.Manager.RecordLeasedQuota(accountID, leaseID, auth.ClientID, result, time.Now())
	}

	lease, err := s.Manager.TouchLease(leaseID, accountID, auth.ClientID, firstText(body.Holder, body.Client), ttl)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	swap, _ := s.Manager.ShouldSwapLease(accountID)
	if body.RateLimitReached {
		swap = true
	}
	writeJSON(w, http.StatusOK, heartbeatResponse{Lease: lease, ShouldSwap: swap})
}

func (s *Server) handleSyncLeaseAction(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/sync/leases/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(w, r)
		return
	}
	leaseID := parts[0]
	auth := authFromRequest(r)

	if len(parts) == 1 {
		switch r.Method {
		case http.MethodPatch, http.MethodPost:
			s.heartbeatLease(w, r, leaseID, auth)
			return
		case http.MethodDelete:
			var body struct {
				AccountID string `json:"accountId"`
			}
			if r.Body != nil {
				_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
			}
			if err := s.Manager.ReleaseLease(body.AccountID, leaseID, auth.ClientID); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"released": true, "leaseId": leaseID})
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}

	if len(parts) == 2 && parts[1] == "heartbeat" {
		if r.Method != http.MethodPost && r.Method != http.MethodPatch {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		s.heartbeatLease(w, r, leaseID, auth)
		return
	}

	if len(parts) == 2 && parts[1] == "auth" {
		if r.Method != http.MethodPost && r.Method != http.MethodPut {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var snapshot manager.ProfileSnapshot
		if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&snapshot); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json")
			return
		}
		if strings.TrimSpace(snapshot.LeaseID) == "" {
			snapshot.LeaseID = leaseID
		}
		account, err := s.Manager.UpdateLeasedProfileSnapshot(snapshot, auth.ClientID, 90*time.Second)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, s.ManagerAccountView(account))
		return
	}

	http.NotFound(w, r)
}

func (s *Server) handleSyncUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		AccountID string        `json:"accountId"`
		LeaseID   string        `json:"leaseId"`
		RunID     string        `json:"runId"`
		Usage     usage.Summary `json:"usage"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	auth := authFromRequest(r)
	if !s.requireAccountReportAccess(w, auth, body.AccountID) {
		return
	}
	if err := s.Manager.RecordUsageWithContext(body.AccountID, auth.ClientID, body.LeaseID, body.RunID, body.Usage); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleSyncQuota(w http.ResponseWriter, r *http.Request) {
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/sync/quota/"), "/")
	if id == "" {
		id = strings.TrimSpace(r.URL.Query().Get("id"))
	}
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing account id")
		return
	}
	if r.Method == http.MethodPost || r.Method == http.MethodPut {
		var body struct {
			Result quota.Result `json:"result"`
			Quota  quota.Result `json:"quota"`
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
		if err != nil {
			writeError(w, http.StatusBadRequest, "could not read quota report")
			return
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json")
			return
		}
		result := body.Result
		if result.Status == "" {
			result = body.Quota
		}
		if result.Status == "" {
			if err := json.Unmarshal(raw, &result); err != nil || result.Status == "" {
				writeError(w, http.StatusBadRequest, "quota report needs result.status")
				return
			}
		}
		auth := authFromRequest(r)
		if !s.requireAccountReportAccess(w, auth, id) {
			return
		}
		if err := s.Manager.RecordQuotaReport(id, result, auth.ClientID); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "accountId": id})
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	result, err := s.Manager.FetchQuota(r.Context(), id)
	if err != nil {
		if result.Status != "" {
			writeJSON(w, http.StatusOK, result)
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleClients(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		clients, err := s.Manager.ListClients()
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, clients)
	case http.MethodPost:
		var body struct {
			Label string `json:"label"`
		}
		if r.Body != nil {
			_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
		}
		client, token, err := s.Manager.CreateClient(body.Label)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{
			"client": client,
			"token":  token,
		})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleClientAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/clients/"), "/")
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing client id")
		return
	}
	if err := s.Manager.RevokeClient(id); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"revoked": true, "id": id})
}

func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	stats, err := s.Manager.UsageStats()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	auth := authFromRequest(r)
	if auth.UserID != "" && auth.ClientID == "" && !auth.Admin {
		user, ok := s.Manager.GetUser(auth.UserID)
		if !ok {
			writeError(w, http.StatusUnauthorized, "user not found")
			return
		}
		devices, err := s.Manager.ListDevices(auth.UserID)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		workspaces, err := s.Manager.ListWorkspacesForClient(auth.UserID)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"mode":       "user",
			"admin":      false,
			"user":       user,
			"devices":    devices,
			"workspaces": workspaces,
		})
		return
	}

	clients, err := s.Manager.ListClients()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	stats, err := s.Manager.UsageStats()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	queue, err := s.Manager.RefreshQueue()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	dispatches, err := s.Manager.DispatchHistory(50, "")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if auth.Admin {
		writeJSON(w, http.StatusOK, map[string]any{
			"mode":         "admin",
			"admin":        true,
			"clients":      clients,
			"usage":        stats,
			"refreshQueue": queue,
			"dispatches":   dispatches,
		})
		return
	}

	var currentClient manager.ClientView
	for _, client := range clients {
		if client.ID == auth.ClientID {
			currentClient = client
			break
		}
	}
	if currentClient.ID == "" {
		writeError(w, http.StatusUnauthorized, "client token is no longer active")
		return
	}

	accountIDs := map[string]bool{}
	clientUsage := []manager.AccountUsage{}
	totals := struct {
		Today     usage.Tokens `json:"today"`
		SevenDays usage.Tokens `json:"sevenDays"`
		AllTime   usage.Tokens `json:"allTime"`
	}{}
	for accountID, stat := range stats {
		if stat.ClientID != auth.ClientID {
			continue
		}
		if stat.AccountID == "" {
			stat.AccountID = accountID
		}
		accountIDs[stat.AccountID] = true
		clientUsage = append(clientUsage, stat)
		totals.Today = addTokens(totals.Today, stat.Today)
		totals.SevenDays = addTokens(totals.SevenDays, stat.SevenDays)
		totals.AllTime = addTokens(totals.AllTime, stat.AllTime)
	}

	clientQueue := []manager.RefreshQueueItem{}
	for _, item := range queue {
		if accountIDs[item.AccountID] {
			clientQueue = append(clientQueue, item)
		}
	}
	clientDispatches, err := s.Manager.DispatchHistory(50, auth.ClientID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	workspaces, err := s.Manager.ListWorkspacesForClient(auth.ClientID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"mode":         "client",
		"admin":        false,
		"client":       currentClient,
		"workspaces":   workspaces,
		"usage":        clientUsage,
		"totals":       totals,
		"refreshQueue": clientQueue,
		"dispatches":   clientDispatches,
	})
}

func addTokens(left, right usage.Tokens) usage.Tokens {
	left.Input += right.Input
	left.CachedInput += right.CachedInput
	left.Output += right.Output
	left.Total += right.Total
	return left
}

func (s *Server) handleRefreshQueue(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	queue, err := s.Manager.RefreshQueue()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, queue)
}

func (s *Server) refreshBeforeExport(ctx context.Context, id string) {
	_, _ = s.Manager.FetchQuota(ctx, id)
}

func (s *Server) handleAccounts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		accounts, err := s.Manager.ListAccounts()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, accounts)
	case http.MethodPost:
		var body struct {
			ID    string `json:"id"`
			Label string `json:"label"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json")
			return
		}
		account, err := s.Manager.AddAccount(body.ID, body.Label)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, s.ManagerAccountView(account))
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleImportJSON(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	raw, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not read json")
		return
	}

	profile, err := parseProfileUpload(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	account, err := s.Manager.ImportJSONProfile(profile)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, s.ManagerAccountView(account))
}

func (s *Server) handleAccountAction(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/accounts/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 1 && parts[0] != "" {
		if r.Method != http.MethodDelete {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		account, err := s.Manager.DeleteAccount(parts[0])
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"deleted":   true,
			"id":        account.ID,
			"codexHome": account.CodexHome,
		})
		return
	}
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	id := parts[0]
	action := parts[1]

	switch action {
	case "label":
		if r.Method != http.MethodPatch {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var body struct {
			Label string `json:"label"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json")
			return
		}
		if err := s.Manager.SetLabel(id, body.Label); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"label": body.Label})
	case "status":
		if r.Method != http.MethodPatch {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var body struct {
			Status manager.AccountStatus `json:"status"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json")
			return
		}
		if err := s.Manager.SetStatus(id, body.Status); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": string(body.Status)})
	case "workspace":
		if r.Method != http.MethodPatch {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var body struct {
			WorkspaceID string `json:"workspaceId"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json")
			return
		}
		if err := s.Manager.SetAccountWorkspace(id, body.WorkspaceID); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"workspaceId": body.WorkspaceID})
	case "owner":
		if r.Method != http.MethodPatch {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var body struct {
			OwnerMode     manager.AccountOwnerMode `json:"ownerMode"`
			OwnerClientID string                   `json:"ownerClientId"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json")
			return
		}
		if err := s.Manager.SetOwner(id, body.OwnerMode, body.OwnerClientID); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"ownerMode": string(body.OwnerMode), "ownerClientId": body.OwnerClientID})
	case "quota":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		result, err := s.Manager.FetchQuota(r.Context(), id)
		if err != nil {
			if result.Status != "" {
				writeJSON(w, http.StatusOK, result)
				return
			}
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, result)
	case "usage":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		result, err := s.Manager.FetchUsage(id)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, result)
	default:
		http.NotFound(w, r)
	}
}

// ManagerAccountView builds the view for a single account. It uses
// AccountViewByID, a plain read, instead of ListAccounts: the latter runs
// syncManagedAccounts and may rewrite the entire state file on every call, so
// using it to answer a single-account response turned each create/import/status
// response into an O(N) load-and-save of all accounts. On any read error (or a
// race where the account is gone) it falls back to the bare account so the
// caller still gets a usable view.
func (s *Server) ManagerAccountView(account manager.Account) manager.AccountView {
	view, err := s.Manager.AccountViewByID(account.ID)
	if err != nil {
		return manager.AccountView{Account: account}
	}
	return view
}

func parseProfileUpload(raw []byte) (manager.JSONProfile, error) {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return manager.JSONProfile{}, fmt.Errorf("invalid JSON: %w", err)
	}

	if authRaw, ok := root["auth"]; ok {
		profile := manager.JSONProfile{Auth: authRaw}
		profile.ID = rawString(root["id"])
		profile.Label = rawString(root["label"])
		profile.Config = rawString(root["config"])
		if profile.Config == "" {
			profile.Config = rawString(root["configToml"])
		}
		if profile.Config == "" {
			profile.Config = rawString(root["config_toml"])
		}
		if profile.Config == "" {
			profile.Config = rawString(root["settings"])
		}
		if profile.Config == "" {
			profile.Config = rawString(root["settingsToml"])
		}
		if profile.Config == "" {
			profile.Config = rawString(root["settings_toml"])
		}
		return profile, nil
	}

	return manager.JSONProfile{
		ID:    rawString(root["id"]),
		Label: rawString(root["label"]),
		Auth:  json.RawMessage(raw),
	}, nil
}

func rawString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return ""
	}
	return strings.TrimSpace(text)
}

func firstText(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// indexTemplate has been deleted in favor of embedding web/dist/index.html.
