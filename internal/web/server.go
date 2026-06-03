package web

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"cube20/internal/manager"
	"cube20/internal/usage"
	"cube20/web"
)

type Server struct {
	Manager    *manager.Manager
	Host       string
	Port       int
	CloudToken string
}

type authContextKey struct{}

type requestAuth struct {
	Admin    bool
	ClientID string
}

func (s *Server) ListenAndServe() error {
	if s.Host == "" {
		s.Host = "127.0.0.1"
	}
	if s.Port == 0 {
		s.Port = 8720
	}

	mux := http.NewServeMux()
	admin := func(handler http.HandlerFunc) http.HandlerFunc {
		return s.withAdminAuth(handler)
	}
	sync := func(handler http.HandlerFunc) http.HandlerFunc {
		return s.withSyncAuth(handler)
	}
	mux.HandleFunc("/api/sync/push", sync(s.handleSyncPush))
	mux.HandleFunc("/api/sync/pull/", sync(s.handleSyncPull))
	mux.HandleFunc("/api/sync/claim", sync(s.handleSyncClaim))
	mux.HandleFunc("/api/sync/leases", sync(s.handleSyncLeases))
	mux.HandleFunc("/api/sync/leases/", sync(s.handleSyncLeaseAction))
	mux.HandleFunc("/api/sync/usage", sync(s.handleSyncUsage))
	mux.HandleFunc("/api/sync/quota/", sync(s.handleSyncQuota))
	mux.HandleFunc("/api/me", sync(s.handleMe))
	mux.HandleFunc("/api/clients", admin(s.handleClients))
	mux.HandleFunc("/api/clients/", admin(s.handleClientAction))
	mux.HandleFunc("/api/stats", admin(s.handleStats))
	mux.HandleFunc("/api/refresh-queue", admin(s.handleRefreshQueue))
	mux.HandleFunc("/api/accounts/import-json", admin(s.handleImportJSON))
	mux.HandleFunc("/api/accounts/import-live", admin(s.handleImportLive))
	mux.HandleFunc("/api/accounts/pick", admin(s.handleLBPick))
	mux.HandleFunc("/api/lb/pick", admin(s.handleLBPick))
	mux.HandleFunc("/api/lb/reset", admin(s.handleLBReset))
	mux.HandleFunc("/api/lb/status", admin(s.handleLBStatus))
	mux.HandleFunc("/api/accounts", admin(s.handleAccounts))
	mux.HandleFunc("/api/accounts/", admin(s.handleAccountAction))
	mux.HandleFunc("/api/meta", admin(s.handleMeta))
	mux.HandleFunc("/api/settings", admin(s.handleSettings))
	mux.HandleFunc("/api/codex-config", admin(s.handleCodexConfig))

	distSub, err := fs.Sub(webdist.DistFS, "dist")
	if err != nil {
		return fmt.Errorf("failed to sub dist folder: %w", err)
	}
	mux.Handle("/", staticHandler(distSub))

	addr := fmt.Sprintf("%s:%d", s.Host, s.Port)
	fmt.Printf("cube dashboard: http://%s\n", addr)
	if strings.TrimSpace(s.CloudToken) != "" {
		fmt.Println("cube dashboard: API bearer token is required")
	}
	return http.ListenAndServe(addr, mux)
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
			_ = s.Manager.TouchClient(client.ID)
			auth := requestAuth{ClientID: client.ID}
			next(w, r.WithContext(context.WithValue(r.Context(), authContextKey{}, auth)))
			return
		}
		writeError(w, http.StatusUnauthorized, "missing or invalid PAT")
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
	status, err := s.Manager.LoadBalanceStatus()
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
	status, err := s.Manager.LoadBalanceStatus()
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, status)
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
			"settingsPath":       s.Manager.SettingsPath,
			"settingsToml":       text,
			"liveCodexHome":      s.Manager.LiveCodexHome,
			"accountsDir":        s.Manager.AccountsDir,
			"sharedConfigPath":   s.Manager.SharedConfigPath,
			"sharedSettingsPath": s.Manager.SharedConfigPath,
			"cloudUrl":           s.Manager.CloudURL,
			"cloudTokenPresent":  strings.TrimSpace(s.Manager.CloudToken) != "",
		})
	case http.MethodPatch:
		var body struct {
			LiveCodexHome      string `json:"liveCodexHome"`
			AccountsDir        string `json:"accountsDir"`
			SharedConfigPath   string `json:"sharedConfigPath"`
			SharedSettingsPath string `json:"sharedSettingsPath"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json")
			return
		}
		sharedPath := body.SharedSettingsPath
		if strings.TrimSpace(sharedPath) == "" {
			sharedPath = body.SharedConfigPath
		}
		if _, err := s.Manager.UpdateSettings(body.LiveCodexHome, body.AccountsDir, sharedPath); err != nil {
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
			"settingsPath":       s.Manager.SettingsPath,
			"settingsToml":       text,
			"liveCodexHome":      settings.LiveCodexHome,
			"accountsDir":        settings.AccountsDir,
			"sharedConfigPath":   settings.SharedConfigPath,
			"sharedSettingsPath": settings.SharedConfigPath,
			"cloudUrl":           settings.CloudURL,
			"cloudTokenPresent":  strings.TrimSpace(settings.CloudToken) != "",
		})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) writeMeta(w http.ResponseWriter) {
	live := s.Manager.LiveProfileView()
	sharedConfigPath, sharedConfigPresent, sharedConfigUpdated := s.Manager.SharedConfigInfo()
	writeJSON(w, http.StatusOK, map[string]any{
		"statePath":           s.Manager.StatePath,
		"settingsPath":        s.Manager.SettingsPath,
		"accountsDir":         s.Manager.AccountsDir,
		"liveCodexHome":       s.Manager.LiveCodexHome,
		"liveAuthPresent":     live.AuthPresent,
		"liveConfigPresent":   live.ConfigPresent,
		"sharedConfigPath":    sharedConfigPath,
		"sharedSettingsPath":  sharedConfigPath,
		"sharedConfigPresent": sharedConfigPresent,
		"sharedConfigUpdated": sharedConfigUpdated,
	})
}

func (s *Server) handleCodexConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		text, err := s.Manager.ReadSharedConfigText()
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		path, present, updated := s.Manager.SharedConfigInfo()
		writeJSON(w, http.StatusOK, map[string]any{
			"configPath":    path,
			"configToml":    text,
			"configPresent": present,
			"configUpdated": updated,
		})
	case http.MethodPut:
		var body struct {
			ConfigToml string `json:"configToml"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json")
			return
		}
		if err := s.Manager.WriteSharedConfigText(body.ConfigToml); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		text, _ := s.Manager.ReadSharedConfigText()
		path, present, updated := s.Manager.SharedConfigInfo()
		writeJSON(w, http.StatusOK, map[string]any{
			"configPath":    path,
			"configToml":    text,
			"configPresent": present,
			"configUpdated": updated,
		})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
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
	s.refreshBeforeExport(id)
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
	leaseSnapshot, err := s.Manager.ClaimLease(r.Context(), auth.ClientID, firstText(auth.ClientID, r.RemoteAddr), 90*time.Second)
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
	holder := firstText(body.Holder, body.Client, clientID, r.RemoteAddr)
	ttl := time.Duration(body.TTLSeconds) * time.Second
	leaseSnapshot, err := s.Manager.ClaimLease(r.Context(), clientID, holder, ttl)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, leaseSnapshot)
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
			var body struct {
				AccountID  string `json:"accountId"`
				Client     string `json:"client"`
				Holder     string `json:"holder"`
				TTLSeconds int    `json:"ttlSeconds"`
			}
			if r.Body != nil {
				_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
			}
			lease, err := s.Manager.TouchLease(leaseID, body.AccountID, auth.ClientID, firstText(body.Holder, body.Client), time.Duration(body.TTLSeconds)*time.Second)
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, lease)
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
		var body struct {
			AccountID  string `json:"accountId"`
			Client     string `json:"client"`
			Holder     string `json:"holder"`
			TTLSeconds int    `json:"ttlSeconds"`
		}
		if r.Body != nil {
			_ = json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
		}
		lease, err := s.Manager.TouchLease(leaseID, body.AccountID, auth.ClientID, firstText(body.Holder, body.Client), time.Duration(body.TTLSeconds)*time.Second)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, lease)
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
		Usage     usage.Summary `json:"usage"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	auth := authFromRequest(r)
	if err := s.Manager.RecordUsage(body.AccountID, auth.ClientID, body.Usage); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleSyncQuota(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/sync/quota/"), "/")
	if id == "" {
		id = strings.TrimSpace(r.URL.Query().Get("id"))
	}
	if id == "" {
		writeError(w, http.StatusBadRequest, "missing account id")
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

	if auth.Admin {
		writeJSON(w, http.StatusOK, map[string]any{
			"mode":         "admin",
			"admin":        true,
			"clients":      clients,
			"usage":        stats,
			"refreshQueue": queue,
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

	writeJSON(w, http.StatusOK, map[string]any{
		"mode":         "client",
		"admin":        false,
		"client":       currentClient,
		"usage":        clientUsage,
		"totals":       totals,
		"refreshQueue": clientQueue,
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

func (s *Server) refreshBeforeExport(id string) {
	_, _ = s.Manager.FetchQuota(context.Background(), id)
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

func (s *Server) handleImportLive(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	account, err := s.Manager.ImportLiveProfile("", "", "")
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
	case "deploy", "activate":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		written, err := s.Manager.DeployProfile(id, "")
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"target":  s.Manager.LiveCodexHome,
			"written": written,
		})
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

func (s *Server) ManagerAccountView(account manager.Account) manager.AccountView {
	views, err := s.Manager.ListAccounts()
	if err != nil {
		return manager.AccountView{Account: account}
	}
	for _, view := range views {
		if view.ID == account.ID {
			return view
		}
	}
	return manager.AccountView{Account: account}
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
