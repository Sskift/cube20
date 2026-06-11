package manager

import (
	"bytes"
	"context"
	"cube20/internal/quota"
	"cube20/internal/usage"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (m *Manager) ensurePostgres() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := m.postgresDB(ctx)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext('cube20_schema'))`); err != nil {
		return err
	}

	statements := []string{
		`CREATE TABLE IF NOT EXISTS cube_accounts (
			id text PRIMARY KEY,
			label text NOT NULL DEFAULT '',
			plan text NOT NULL DEFAULT '',
			status text NOT NULL DEFAULT 'ready',
			codex_home text NOT NULL DEFAULT '',
			owner_mode text NOT NULL DEFAULT 'cloud',
			owner_client_id text NOT NULL DEFAULT '',
			generation bigint NOT NULL DEFAULT 0,
			lease_id text NOT NULL DEFAULT '',
			lease_client_id text NOT NULL DEFAULT '',
			lease_holder text NOT NULL DEFAULT '',
			lease_started_at timestamptz,
			lease_heartbeat_at timestamptz,
			lease_expires_at timestamptz,
			auth_json jsonb,
			created_at timestamptz NOT NULL DEFAULT now(),
			updated_at timestamptz NOT NULL DEFAULT now(),
			last_error text NOT NULL DEFAULT ''
		)`,
		`ALTER TABLE cube_accounts ADD COLUMN IF NOT EXISTS owner_mode text NOT NULL DEFAULT 'cloud'`,
		`ALTER TABLE cube_accounts ADD COLUMN IF NOT EXISTS owner_client_id text NOT NULL DEFAULT ''`,
		`ALTER TABLE cube_accounts ADD COLUMN IF NOT EXISTS generation bigint NOT NULL DEFAULT 0`,
		`ALTER TABLE cube_accounts ADD COLUMN IF NOT EXISTS lease_id text NOT NULL DEFAULT ''`,
		`ALTER TABLE cube_accounts ADD COLUMN IF NOT EXISTS lease_client_id text NOT NULL DEFAULT ''`,
		`ALTER TABLE cube_accounts ADD COLUMN IF NOT EXISTS lease_holder text NOT NULL DEFAULT ''`,
		`ALTER TABLE cube_accounts ADD COLUMN IF NOT EXISTS lease_started_at timestamptz`,
		`ALTER TABLE cube_accounts ADD COLUMN IF NOT EXISTS lease_heartbeat_at timestamptz`,
		`ALTER TABLE cube_accounts ADD COLUMN IF NOT EXISTS lease_expires_at timestamptz`,
		`CREATE TABLE IF NOT EXISTS cube_clients (
			id text PRIMARY KEY,
			label text NOT NULL DEFAULT '',
			token_hash text NOT NULL,
			created_at timestamptz NOT NULL DEFAULT now(),
			last_seen_at timestamptz,
			revoked_at timestamptz
		)`,
		`CREATE TABLE IF NOT EXISTS cube_usage (
			account_id text PRIMARY KEY,
			client_id text NOT NULL DEFAULT '',
			updated_at timestamptz NOT NULL DEFAULT now(),
			latest_at text NOT NULL DEFAULT '',
			latest_model text NOT NULL DEFAULT '',
			today jsonb NOT NULL DEFAULT '{}'::jsonb,
			seven_days jsonb NOT NULL DEFAULT '{}'::jsonb,
			all_time jsonb NOT NULL DEFAULT '{}'::jsonb,
			models jsonb NOT NULL DEFAULT '[]'::jsonb
		)`,
		`CREATE TABLE IF NOT EXISTS cube_usage_events (
			account_id text NOT NULL DEFAULT '',
			client_id text NOT NULL DEFAULT '',
			lease_id text NOT NULL DEFAULT '',
			run_id text NOT NULL DEFAULT '',
			model text NOT NULL DEFAULT '',
			reported_at timestamptz NOT NULL DEFAULT now(),
			latest_at text NOT NULL DEFAULT '',
			today jsonb NOT NULL DEFAULT '{}'::jsonb,
			seven_days jsonb NOT NULL DEFAULT '{}'::jsonb,
			all_time jsonb NOT NULL DEFAULT '{}'::jsonb,
			summary_status text NOT NULL DEFAULT '',
			summary_detail text NOT NULL DEFAULT '',
			summary_files_scanned integer NOT NULL DEFAULT 0,
			summary_events integer NOT NULL DEFAULT 0,
			summary_latest_at text NOT NULL DEFAULT '',
			summary_latest_model text NOT NULL DEFAULT '',
			schema_version integer NOT NULL DEFAULT 1,
			PRIMARY KEY (account_id, client_id, lease_id, run_id, model)
		)`,
		`CREATE TABLE IF NOT EXISTS cube_dispatch_events (
			id text PRIMARY KEY,
			lease_id text NOT NULL DEFAULT '',
			account_id text NOT NULL DEFAULT '',
			account_label text NOT NULL DEFAULT '',
			client_id text NOT NULL DEFAULT '',
			client_label text NOT NULL DEFAULT '',
			holder text NOT NULL DEFAULT '',
			event text NOT NULL DEFAULT '',
			generation bigint NOT NULL DEFAULT 0,
			created_at timestamptz NOT NULL DEFAULT now(),
			started_at timestamptz,
			expires_at timestamptz
		)`,
		`CREATE INDEX IF NOT EXISTS cube_dispatch_events_created_idx ON cube_dispatch_events (created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS cube_dispatch_events_client_idx ON cube_dispatch_events (client_id, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS cube_dispatch_events_account_idx ON cube_dispatch_events (account_id, created_at DESC)`,
		`CREATE TABLE IF NOT EXISTS cube_quota_cache (
			account_id text PRIMARY KEY,
			updated_at timestamptz NOT NULL DEFAULT now(),
			result jsonb NOT NULL DEFAULT '{}'::jsonb,
			five_hour jsonb,
			quota_source text NOT NULL DEFAULT 'cloud',
			reporter_client_id text NOT NULL DEFAULT ''
		)`,
		`ALTER TABLE cube_quota_cache ADD COLUMN IF NOT EXISTS quota_source text NOT NULL DEFAULT 'cloud'`,
		`ALTER TABLE cube_quota_cache ADD COLUMN IF NOT EXISTS reporter_client_id text NOT NULL DEFAULT ''`,
		`CREATE TABLE IF NOT EXISTS cube_meta (
			key text PRIMARY KEY,
			value text NOT NULL DEFAULT '',
			updated_at timestamptz NOT NULL DEFAULT now()
		)`,
		`CREATE TABLE IF NOT EXISTS cube_workspaces (
			id text PRIMARY KEY,
			name text NOT NULL DEFAULT '',
			created_by text NOT NULL DEFAULT '',
			created_at timestamptz NOT NULL DEFAULT now(),
			updated_at timestamptz NOT NULL DEFAULT now()
		)`,
		`CREATE TABLE IF NOT EXISTS cube_memberships (
			workspace_id text NOT NULL,
			client_id text NOT NULL,
			role text NOT NULL DEFAULT 'member',
			created_at timestamptz NOT NULL DEFAULT now(),
			PRIMARY KEY (workspace_id, client_id)
		)`,
		`CREATE INDEX IF NOT EXISTS cube_memberships_client_idx ON cube_memberships (client_id)`,
		`ALTER TABLE cube_accounts ADD COLUMN IF NOT EXISTS workspace_id text NOT NULL DEFAULT 'default'`,
		`CREATE INDEX IF NOT EXISTS cube_accounts_workspace_idx ON cube_accounts (workspace_id)`,
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (m *Manager) loadPostgresState() (State, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := m.postgresDB(ctx)
	if err != nil {
		return State{}, err
	}

	state := normalizeState(State{Version: 1})
	accountRows, err := db.QueryContext(ctx, `SELECT id, label, plan, status, codex_home, workspace_id, owner_mode, owner_client_id, generation, lease_id, lease_client_id, lease_holder, lease_started_at, lease_heartbeat_at, lease_expires_at, created_at, updated_at, last_error, auth_json::text FROM cube_accounts ORDER BY id`)
	if err != nil {
		return State{}, err
	}
	defer accountRows.Close()
	for accountRows.Next() {
		var account Account
		var statusText string
		var ownerModeText string
		var authText sql.NullString
		var leaseStarted sql.NullTime
		var leaseHeartbeat sql.NullTime
		var leaseExpires sql.NullTime
		if err := accountRows.Scan(
			&account.ID,
			&account.Label,
			&account.Plan,
			&statusText,
			&account.CodexHome,
			&account.WorkspaceID,
			&ownerModeText,
			&account.OwnerClientID,
			&account.Generation,
			&account.LeaseID,
			&account.LeaseClientID,
			&account.LeaseHolder,
			&leaseStarted,
			&leaseHeartbeat,
			&leaseExpires,
			&account.CreatedAt,
			&account.UpdatedAt,
			&account.LastError,
			&authText,
		); err != nil {
			return State{}, err
		}
		account.Status = AccountStatus(statusText)
		if account.Status == "" {
			account.Status = StatusReady
		}
		account.OwnerMode = AccountOwnerMode(ownerModeText)
		if !validOwnerMode(account.OwnerMode) {
			account.OwnerMode = OwnerCloud
		}
		if strings.TrimSpace(account.WorkspaceID) == "" {
			account.WorkspaceID = DefaultWorkspaceID
		}
		if strings.TrimSpace(account.CodexHome) == "" {
			account.CodexHome = filepath.Join(m.AccountsDir, account.ID)
		}
		if leaseStarted.Valid {
			account.LeaseStartedAt = leaseStarted.Time
		}
		if leaseHeartbeat.Valid {
			account.LeaseHeartbeatAt = leaseHeartbeat.Time
		}
		if leaseExpires.Valid {
			account.LeaseExpiresAt = leaseExpires.Time
		}
		if authText.Valid && strings.TrimSpace(authText.String) != "" {
			if err := m.materializeAuth(account, []byte(authText.String)); err != nil {
				return State{}, err
			}
		}
		state.Accounts = append(state.Accounts, account)
	}
	if err := accountRows.Err(); err != nil {
		return State{}, err
	}

	clientRows, err := db.QueryContext(ctx, `SELECT id, label, token_hash, created_at, last_seen_at, revoked_at FROM cube_clients ORDER BY id`)
	if err != nil {
		return State{}, err
	}
	defer clientRows.Close()
	for clientRows.Next() {
		var client Client
		var lastSeen sql.NullTime
		var revoked sql.NullTime
		if err := clientRows.Scan(&client.ID, &client.Label, &client.TokenHash, &client.CreatedAt, &lastSeen, &revoked); err != nil {
			return State{}, err
		}
		if lastSeen.Valid {
			client.LastSeenAt = lastSeen.Time
		}
		if revoked.Valid {
			client.RevokedAt = &revoked.Time
		}
		state.Clients = append(state.Clients, client)
	}
	if err := clientRows.Err(); err != nil {
		return State{}, err
	}

	workspaceRows, err := db.QueryContext(ctx, `SELECT id, name, created_by, created_at, updated_at FROM cube_workspaces ORDER BY id`)
	if err != nil {
		return State{}, err
	}
	defer workspaceRows.Close()
	for workspaceRows.Next() {
		var ws Workspace
		if err := workspaceRows.Scan(&ws.ID, &ws.Name, &ws.CreatedBy, &ws.CreatedAt, &ws.UpdatedAt); err != nil {
			return State{}, err
		}
		state.Workspaces = append(state.Workspaces, ws)
	}
	if err := workspaceRows.Err(); err != nil {
		return State{}, err
	}

	membershipRows, err := db.QueryContext(ctx, `SELECT workspace_id, client_id, role, created_at FROM cube_memberships ORDER BY workspace_id, client_id`)
	if err != nil {
		return State{}, err
	}
	defer membershipRows.Close()
	for membershipRows.Next() {
		var ms Membership
		var roleText string
		if err := membershipRows.Scan(&ms.WorkspaceID, &ms.ClientID, &roleText, &ms.CreatedAt); err != nil {
			return State{}, err
		}
		ms.Role = WorkspaceRole(roleText)
		if !validWorkspaceRole(ms.Role) {
			ms.Role = RoleMember
		}
		state.Memberships = append(state.Memberships, ms)
	}
	if err := membershipRows.Err(); err != nil {
		return State{}, err
	}

	usageRows, err := db.QueryContext(ctx, `SELECT account_id, client_id, updated_at, latest_at, latest_model, today::text, seven_days::text, all_time::text, models::text FROM cube_usage`)
	if err != nil {
		return State{}, err
	}
	defer usageRows.Close()
	for usageRows.Next() {
		var stat AccountUsage
		var todayText, sevenText, allText, modelsText string
		if err := usageRows.Scan(&stat.AccountID, &stat.ClientID, &stat.UpdatedAt, &stat.LatestAt, &stat.LatestModel, &todayText, &sevenText, &allText, &modelsText); err != nil {
			return State{}, err
		}
		_ = json.Unmarshal([]byte(todayText), &stat.Today)
		_ = json.Unmarshal([]byte(sevenText), &stat.SevenDays)
		_ = json.Unmarshal([]byte(allText), &stat.AllTime)
		_ = json.Unmarshal([]byte(modelsText), &stat.Models)
		state.Usage[stat.AccountID] = stat
	}
	if err := usageRows.Err(); err != nil {
		return State{}, err
	}

	quotaRows, err := db.QueryContext(ctx, `SELECT account_id, updated_at, result::text, five_hour::text, quota_source, reporter_client_id FROM cube_quota_cache`)
	if err != nil {
		return State{}, err
	}
	defer quotaRows.Close()
	for quotaRows.Next() {
		var cache QuotaCache
		var resultText string
		var fiveText sql.NullString
		var sourceText string
		if err := quotaRows.Scan(&cache.AccountID, &cache.UpdatedAt, &resultText, &fiveText, &sourceText, &cache.ReporterClientID); err != nil {
			return State{}, err
		}
		cache.Source = QuotaSource(sourceText)
		if cache.Source == "" {
			cache.Source = QuotaSourceCloud
		}
		_ = json.Unmarshal([]byte(resultText), &cache.Result)
		if fiveText.Valid && strings.TrimSpace(fiveText.String) != "" {
			var window quota.Window
			if err := json.Unmarshal([]byte(fiveText.String), &window); err == nil {
				cache.FiveHour = &window
			}
		}
		state.QuotaCache[cache.AccountID] = cache
	}
	if err := quotaRows.Err(); err != nil {
		return State{}, err
	}

	var migratedValue string
	if err := db.QueryRowContext(ctx, `SELECT value FROM cube_meta WHERE key = 'workspace_migrated'`).Scan(&migratedValue); err != nil {
		if err != sql.ErrNoRows {
			return State{}, err
		}
	}
	state.WorkspaceMigrated = migratedValue == "true"

	return normalizeState(state), nil
}
func (m *Manager) savePostgresState(state State) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := m.postgresDB(ctx)
	if err != nil {
		return err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	state = normalizeState(state)
	now := time.Now()
	for _, account := range state.Accounts {
		if account.CreatedAt.IsZero() {
			account.CreatedAt = now
		}
		if account.UpdatedAt.IsZero() {
			account.UpdatedAt = now
		}
		if account.Status == "" {
			account.Status = StatusReady
		}
		if !validOwnerMode(account.OwnerMode) {
			account.OwnerMode = OwnerCloud
		}
		if strings.TrimSpace(account.CodexHome) == "" {
			account.CodexHome = filepath.Join(m.AccountsDir, account.ID)
		}
		var leaseStarted any
		if !account.LeaseStartedAt.IsZero() {
			leaseStarted = account.LeaseStartedAt
		}
		var leaseHeartbeat any
		if !account.LeaseHeartbeatAt.IsZero() {
			leaseHeartbeat = account.LeaseHeartbeatAt
		}
		var leaseExpires any
		if !account.LeaseExpiresAt.IsZero() {
			leaseExpires = account.LeaseExpiresAt
		}
		authJSON, err := m.readAccountAuthJSON(account)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO cube_accounts
			(id, label, plan, status, codex_home, workspace_id, owner_mode, owner_client_id, generation, lease_id, lease_client_id, lease_holder, lease_started_at, lease_heartbeat_at, lease_expires_at, auth_json, created_at, updated_at, last_error)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16::jsonb, $17, $18, $19)
			ON CONFLICT (id) DO UPDATE SET
				label = EXCLUDED.label,
				plan = EXCLUDED.plan,
				status = EXCLUDED.status,
				codex_home = EXCLUDED.codex_home,
				workspace_id = EXCLUDED.workspace_id,
				owner_mode = EXCLUDED.owner_mode,
				owner_client_id = EXCLUDED.owner_client_id,
				generation = EXCLUDED.generation,
				lease_id = EXCLUDED.lease_id,
				lease_client_id = EXCLUDED.lease_client_id,
				lease_holder = EXCLUDED.lease_holder,
				lease_started_at = EXCLUDED.lease_started_at,
				lease_heartbeat_at = EXCLUDED.lease_heartbeat_at,
				lease_expires_at = EXCLUDED.lease_expires_at,
				auth_json = EXCLUDED.auth_json,
				updated_at = EXCLUDED.updated_at,
				last_error = EXCLUDED.last_error
			WHERE cube_accounts.updated_at <= EXCLUDED.updated_at`,
			account.ID,
			account.Label,
			account.Plan,
			string(account.Status),
			account.CodexHome,
			workspaceOrDefault(account.WorkspaceID),
			string(account.OwnerMode),
			account.OwnerClientID,
			account.Generation,
			account.LeaseID,
			account.LeaseClientID,
			account.LeaseHolder,
			leaseStarted,
			leaseHeartbeat,
			leaseExpires,
			authJSON,
			account.CreatedAt,
			account.UpdatedAt,
			account.LastError,
		); err != nil {
			return err
		}
	}

	for _, client := range state.Clients {
		if client.CreatedAt.IsZero() {
			client.CreatedAt = now
		}
		var lastSeen any
		if !client.LastSeenAt.IsZero() {
			lastSeen = client.LastSeenAt
		}
		var revoked any
		if client.RevokedAt != nil {
			revoked = *client.RevokedAt
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO cube_clients
			(id, label, token_hash, created_at, last_seen_at, revoked_at)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (id) DO UPDATE SET
				label = EXCLUDED.label,
				token_hash = CASE WHEN EXCLUDED.token_hash <> '' THEN EXCLUDED.token_hash ELSE cube_clients.token_hash END,
				last_seen_at = CASE
					WHEN cube_clients.last_seen_at IS NULL THEN EXCLUDED.last_seen_at
					WHEN EXCLUDED.last_seen_at IS NULL THEN cube_clients.last_seen_at
					WHEN EXCLUDED.last_seen_at > cube_clients.last_seen_at THEN EXCLUDED.last_seen_at
					ELSE cube_clients.last_seen_at
				END,
				revoked_at = COALESCE(EXCLUDED.revoked_at, cube_clients.revoked_at)`,
			client.ID,
			client.Label,
			client.TokenHash,
			client.CreatedAt,
			lastSeen,
			revoked,
		); err != nil {
			return err
		}
	}

	for _, ws := range state.Workspaces {
		if strings.TrimSpace(ws.ID) == "" {
			continue
		}
		if ws.CreatedAt.IsZero() {
			ws.CreatedAt = now
		}
		if ws.UpdatedAt.IsZero() {
			ws.UpdatedAt = now
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO cube_workspaces
			(id, name, created_by, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (id) DO UPDATE SET
				name = EXCLUDED.name,
				created_by = EXCLUDED.created_by,
				updated_at = EXCLUDED.updated_at`,
			ws.ID,
			ws.Name,
			ws.CreatedBy,
			ws.CreatedAt,
			ws.UpdatedAt,
		); err != nil {
			return err
		}
	}

	// Memberships are upsert-only here, mirroring accounts and clients: a generic
	// whole-state Save must never delete rows it merely didn't see in a possibly
	// stale snapshot (TouchClient saves on every PAT request). Revocation goes
	// through the dedicated deletePostgresMembership path instead, so a concurrent
	// remove can't be resurrected by a racing save.
	for _, ms := range state.Memberships {
		if strings.TrimSpace(ms.WorkspaceID) == "" || strings.TrimSpace(ms.ClientID) == "" {
			continue
		}
		role := ms.Role
		if !validWorkspaceRole(role) {
			role = RoleMember
		}
		if ms.CreatedAt.IsZero() {
			ms.CreatedAt = now
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO cube_memberships
			(workspace_id, client_id, role, created_at)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (workspace_id, client_id) DO UPDATE SET
				role = EXCLUDED.role`,
			ms.WorkspaceID,
			ms.ClientID,
			string(role),
			ms.CreatedAt,
		); err != nil {
			return err
		}
	}

	for _, event := range state.Dispatches {
		if strings.TrimSpace(event.ID) == "" {
			continue
		}
		if event.CreatedAt.IsZero() {
			event.CreatedAt = now
		}
		var startedAt any
		if !event.StartedAt.IsZero() {
			startedAt = event.StartedAt
		}
		var expiresAt any
		if !event.ExpiresAt.IsZero() {
			expiresAt = event.ExpiresAt
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO cube_dispatch_events
			(id, lease_id, account_id, account_label, client_id, client_label, holder, event, generation, created_at, started_at, expires_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
			ON CONFLICT (id) DO UPDATE SET
				account_label = EXCLUDED.account_label,
				client_label = EXCLUDED.client_label,
				holder = EXCLUDED.holder,
				event = EXCLUDED.event,
				generation = EXCLUDED.generation,
				created_at = EXCLUDED.created_at,
				started_at = EXCLUDED.started_at,
				expires_at = EXCLUDED.expires_at`,
			event.ID,
			event.LeaseID,
			event.AccountID,
			event.AccountLabel,
			event.ClientID,
			event.ClientLabel,
			event.Holder,
			event.Event,
			event.Generation,
			event.CreatedAt,
			startedAt,
			expiresAt,
		); err != nil {
			return err
		}
	}

	for accountID, stat := range state.Usage {
		if stat.AccountID == "" {
			stat.AccountID = accountID
		}
		if stat.UpdatedAt.IsZero() {
			stat.UpdatedAt = now
		}
		today, err := jsonText(stat.Today)
		if err != nil {
			return err
		}
		sevenDays, err := jsonText(stat.SevenDays)
		if err != nil {
			return err
		}
		allTime, err := jsonText(stat.AllTime)
		if err != nil {
			return err
		}
		models, err := jsonText(stat.Models)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO cube_usage
			(account_id, client_id, updated_at, latest_at, latest_model, today, seven_days, all_time, models)
			VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7::jsonb, $8::jsonb, $9::jsonb)
			ON CONFLICT (account_id) DO UPDATE SET
				client_id = EXCLUDED.client_id,
				updated_at = EXCLUDED.updated_at,
				latest_at = EXCLUDED.latest_at,
				latest_model = EXCLUDED.latest_model,
				today = EXCLUDED.today,
				seven_days = EXCLUDED.seven_days,
				all_time = EXCLUDED.all_time,
				models = EXCLUDED.models
			WHERE cube_usage.updated_at <= EXCLUDED.updated_at`,
			stat.AccountID,
			stat.ClientID,
			stat.UpdatedAt,
			stat.LatestAt,
			stat.LatestModel,
			today,
			sevenDays,
			allTime,
			models,
		); err != nil {
			return err
		}
	}

	for accountID, cache := range state.QuotaCache {
		if cache.AccountID == "" {
			cache.AccountID = accountID
		}
		if cache.UpdatedAt.IsZero() {
			cache.UpdatedAt = now
		}
		if cache.Source == "" {
			cache.Source = QuotaSourceCloud
		}
		result, err := jsonText(cache.Result)
		if err != nil {
			return err
		}
		var fiveHour sql.NullString
		if cache.FiveHour != nil {
			fiveHour, err = jsonText(*cache.FiveHour)
			if err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO cube_quota_cache
			(account_id, updated_at, result, five_hour, quota_source, reporter_client_id)
			VALUES ($1, $2, $3::jsonb, $4::jsonb, $5, $6)
			ON CONFLICT (account_id) DO UPDATE SET
				updated_at = EXCLUDED.updated_at,
				result = EXCLUDED.result,
				five_hour = EXCLUDED.five_hour,
				quota_source = EXCLUDED.quota_source,
				reporter_client_id = EXCLUDED.reporter_client_id
			WHERE cube_quota_cache.updated_at <= EXCLUDED.updated_at`,
			cache.AccountID,
			cache.UpdatedAt,
			result,
			fiveHour,
			string(cache.Source),
			cache.ReporterClientID,
		); err != nil {
			return err
		}
	}

	if state.WorkspaceMigrated {
		if _, err := tx.ExecContext(ctx, `INSERT INTO cube_meta (key, value, updated_at)
			VALUES ('workspace_migrated', 'true', now())
			ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = EXCLUDED.updated_at`); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (m *Manager) materializeAuth(account Account, raw []byte) error {
	if len(raw) == 0 {
		return nil
	}
	if !json.Valid(raw) {
		return fmt.Errorf("stored auth for %s is not valid JSON", account.ID)
	}
	authPath := filepath.Join(account.CodexHome, authFileName)
	desired := prettyJSON(raw)
	// Load() runs on nearly every operation and previously rewrote every
	// account's auth.json each time, churning credentials on disk. prettyJSON
	// is deterministic, so skip the write when the on-disk copy already matches
	// the stored snapshot. This keeps the digest-based generation/quota logic
	// (which hashes auth.json) stable across reads.
	if existing, err := os.ReadFile(authPath); err == nil && bytes.Equal(existing, desired) {
		return nil
	}
	if err := os.MkdirAll(account.CodexHome, 0o700); err != nil {
		return err
	}
	return os.WriteFile(authPath, desired, fileModeFor(authFileName))
}
func (m *Manager) readAccountAuthJSON(account Account) (sql.NullString, error) {
	raw, err := os.ReadFile(filepath.Join(account.CodexHome, authFileName))
	if errors.Is(err, os.ErrNotExist) {
		return sql.NullString{}, nil
	}
	if err != nil {
		return sql.NullString{}, err
	}
	if !json.Valid(raw) {
		return sql.NullString{}, fmt.Errorf("%s is not valid JSON", filepath.Join(account.CodexHome, authFileName))
	}
	return sql.NullString{String: string(prettyJSON(raw)), Valid: true}, nil
}
func jsonText(value any) (sql.NullString, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return sql.NullString{}, err
	}
	return sql.NullString{String: string(data), Valid: true}, nil
}
func (m *Manager) recordPostgresUsage(accountID, clientID, leaseID, runID string, summary usage.Summary) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := m.postgresDB(ctx)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := time.Now()
	today, err := jsonText(summary.Today)
	if err != nil {
		return err
	}
	sevenDays, err := jsonText(summary.SevenDays)
	if err != nil {
		return err
	}
	allTime, err := jsonText(summary.AllTime)
	if err != nil {
		return err
	}
	models, err := jsonText(summary.Models)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO cube_usage
		(account_id, client_id, updated_at, latest_at, latest_model, today, seven_days, all_time, models)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb, $7::jsonb, $8::jsonb, $9::jsonb)
		ON CONFLICT (account_id) DO UPDATE SET
			client_id = EXCLUDED.client_id,
			updated_at = EXCLUDED.updated_at,
			latest_at = EXCLUDED.latest_at,
			latest_model = EXCLUDED.latest_model,
			today = EXCLUDED.today,
			seven_days = EXCLUDED.seven_days,
			all_time = EXCLUDED.all_time,
			models = EXCLUDED.models
		WHERE cube_usage.updated_at <= EXCLUDED.updated_at`,
		accountID,
		clientID,
		now,
		summary.LatestAt,
		summary.LatestModel,
		today,
		sevenDays,
		allTime,
		models,
	); err != nil {
		return err
	}

	if strings.TrimSpace(runID) == "" {
		runID = usageEventRunID(summary, now)
	}
	events := UsageEventsFromSummary(NewUsageEventContext(accountID, clientID, leaseID, runID, now), summary)
	for _, event := range events {
		eventToday, err := jsonText(event.Today)
		if err != nil {
			return err
		}
		eventSevenDays, err := jsonText(event.SevenDays)
		if err != nil {
			return err
		}
		eventAllTime, err := jsonText(event.AllTime)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO cube_usage_events
			(account_id, client_id, lease_id, run_id, model, reported_at, latest_at, today, seven_days, all_time, summary_status, summary_detail, summary_files_scanned, summary_events, summary_latest_at, summary_latest_model, schema_version)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9::jsonb, $10::jsonb, $11, $12, $13, $14, $15, $16, $17)
			ON CONFLICT (account_id, client_id, lease_id, run_id, model) DO UPDATE SET
				reported_at = EXCLUDED.reported_at,
				latest_at = EXCLUDED.latest_at,
				today = EXCLUDED.today,
				seven_days = EXCLUDED.seven_days,
				all_time = EXCLUDED.all_time,
				summary_status = EXCLUDED.summary_status,
				summary_detail = EXCLUDED.summary_detail,
				summary_files_scanned = EXCLUDED.summary_files_scanned,
				summary_events = EXCLUDED.summary_events,
				summary_latest_at = EXCLUDED.summary_latest_at,
				summary_latest_model = EXCLUDED.summary_latest_model,
				schema_version = EXCLUDED.schema_version`,
			event.AccountID,
			event.ClientID,
			event.LeaseID,
			event.RunID,
			event.Model,
			event.ReportedAt,
			event.LatestAt,
			eventToday,
			eventSevenDays,
			eventAllTime,
			event.SummaryStatus,
			event.SummaryDetail,
			event.SummaryFilesScanned,
			event.SummaryEvents,
			event.SummaryLatestAt,
			event.SummaryLatestModel,
			event.SchemaVersion,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func (m *Manager) postgresDispatchHistory(limit int, clientID string) ([]DispatchEvent, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := m.postgresDB(ctx)
	if err != nil {
		return nil, err
	}

	query := `SELECT
		d.id,
		d.lease_id,
		d.account_id,
		COALESCE(NULLIF(a.label, ''), d.account_label),
		d.client_id,
		COALESCE(NULLIF(c.label, ''), d.client_label),
		d.holder,
		d.event,
		d.generation,
		d.created_at,
		d.started_at,
		d.expires_at
	FROM cube_dispatch_events d
	LEFT JOIN cube_accounts a ON a.id = d.account_id
	LEFT JOIN cube_clients c ON c.id = d.client_id`
	args := []any{}
	if clientID != "" {
		query += ` WHERE d.client_id = $1`
		args = append(args, clientID)
	}
	query += fmt.Sprintf(` ORDER BY d.created_at DESC LIMIT $%d`, len(args)+1)
	args = append(args, limit)

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	events := []DispatchEvent{}
	for rows.Next() {
		var event DispatchEvent
		var startedAt sql.NullTime
		var expiresAt sql.NullTime
		if err := rows.Scan(
			&event.ID,
			&event.LeaseID,
			&event.AccountID,
			&event.AccountLabel,
			&event.ClientID,
			&event.ClientLabel,
			&event.Holder,
			&event.Event,
			&event.Generation,
			&event.CreatedAt,
			&startedAt,
			&expiresAt,
		); err != nil {
			return nil, err
		}
		if startedAt.Valid {
			event.StartedAt = startedAt.Time
		}
		if expiresAt.Valid {
			event.ExpiresAt = expiresAt.Time
		}
		events = append(events, event)
	}
	return events, rows.Err()
}
func (m *Manager) deletePostgresAccount(id string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := m.postgresDB(ctx)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, statement := range []string{
		`DELETE FROM cube_usage_events WHERE account_id = $1`,
		`DELETE FROM cube_usage WHERE account_id = $1`,
		`DELETE FROM cube_quota_cache WHERE account_id = $1`,
		`DELETE FROM cube_accounts WHERE id = $1`,
	} {
		if _, err := tx.ExecContext(ctx, statement, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// deletePostgresMembership removes a single membership row directly, mirroring
// deletePostgresAccount. Membership revocation must use this targeted delete
// rather than relying on the generic upsert-only Save, which never removes rows.
func (m *Manager) deletePostgresMembership(workspaceID, clientID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := m.postgresDB(ctx)
	if err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, `DELETE FROM cube_memberships WHERE workspace_id = $1 AND client_id = $2`, workspaceID, clientID)
	return err
}

// recordPostgresQuotaResult is the Postgres-mode counterpart of
// writeQuotaResultLocked. It is UNTESTED LOCALLY (the test suite runs only in
// file mode; production runs Postgres), so the reasoning below is deliberate.
//
// Lease-resurrection safety / file-mode parity: this function writes ONLY the
// cube_quota_cache row (guarded by `updated_at <= EXCLUDED.updated_at`) and, via
// row-level `UPDATE cube_accounts ... WHERE id = $1`, the columns owner_mode,
// owner_client_id, status, last_error, plan, generation, updated_at. It NEVER
// touches any lease column (lease_id, lease_client_id, lease_holder,
// lease_started_at, lease_heartbeat_at, lease_expires_at). A cloud quota refresh
// therefore CANNOT revive a released or expired lease here — there is simply no
// statement that writes a lease field. This matches the file-mode semantic:
// writeQuotaResultLocked likewise never writes lease columns; its lock exists
// only to stop a whole-file state.json rewrite from clobbering a concurrent
// lease Save. Postgres has no such whole-file clobber (each UPDATE is row-level
// and atomic, and the quota UPDATE does not even mention the lease columns), so
// the invariant holds without extra serialization.
//
// Deadlock safety: we intentionally do NOT take acquirePostgresLock here. That
// helper pins a dedicated connection for a session advisory lock, and taking it
// twice from one goroutine deadlocks (two PG sessions block each other). Every
// caller reaches this function with NO advisory lock held: the PG branches of
// RecordLeasedQuota and recordQuotaResult return before their file-mode
// acquireLock calls, and RecoverExpiredLeases releases its advisory lock before
// the FetchQuota -> recordQuotaResult re-probe. Adding a lock here would risk a
// production deadlock for no correctness gain, so the narrow, provably-safe
// choice is to add none.
func (m *Manager) recordPostgresQuotaResult(id string, result quota.Result, authChanged bool, source QuotaSource, reporterClientID string, flipOwner bool) error {
	if source == "" {
		source = QuotaSourceCloud
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := m.postgresDB(ctx)
	if err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	now := time.Now()
	resultJSON, err := jsonText(result)
	if err != nil {
		return err
	}
	var fiveHourJSON sql.NullString
	if fiveHour := quotaFiveHour(result); fiveHour != nil {
		fiveHourJSON, err = jsonText(*fiveHour)
		if err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO cube_quota_cache
		(account_id, updated_at, result, five_hour, quota_source, reporter_client_id)
		VALUES ($1, $2, $3::jsonb, $4::jsonb, $5, $6)
		ON CONFLICT (account_id) DO UPDATE SET
			updated_at = EXCLUDED.updated_at,
			result = EXCLUDED.result,
			five_hour = EXCLUDED.five_hour,
			quota_source = EXCLUDED.quota_source,
			reporter_client_id = EXCLUDED.reporter_client_id
		WHERE cube_quota_cache.updated_at <= EXCLUDED.updated_at`,
		id,
		now,
		resultJSON,
		fiveHourJSON,
		string(source),
		reporterClientID,
	); err != nil {
		return err
	}

	if flipOwner && source == QuotaSourceClient {
		if reporterClientID != "" {
			if _, err := tx.ExecContext(ctx, `UPDATE cube_accounts
				SET owner_mode = 'client', owner_client_id = $2, updated_at = $3
				WHERE id = $1`, id, reporterClientID, now); err != nil {
				return err
			}
		} else {
			if _, err := tx.ExecContext(ctx, `UPDATE cube_accounts
				SET owner_mode = 'client', updated_at = $2
				WHERE id = $1`, id, now); err != nil {
				return err
			}
		}
	}

	switch result.Status {
	case quota.StatusRefreshInvalid:
		if _, err := tx.ExecContext(ctx, `UPDATE cube_accounts
			SET status = CASE WHEN status IN ('ready', 'recovering') THEN 'drain' ELSE status END,
				last_error = $2,
				updated_at = $3
			WHERE id = $1`, id, result.Detail, now); err != nil {
			return err
		}
	case quota.StatusSupported:
		generationDelta := int64(0)
		if authChanged {
			generationDelta = 1
		}
		if _, err := tx.ExecContext(ctx, `UPDATE cube_accounts
			SET plan = CASE WHEN $2 <> '' THEN $2 ELSE plan END,
				status = CASE WHEN status = 'recovering' THEN 'ready' ELSE status END,
				last_error = '',
				generation = generation + $3,
				updated_at = $4
			WHERE id = $1`, id, result.Plan, generationDelta, now); err != nil {
			return err
		}
	default:
		if authChanged {
			if _, err := tx.ExecContext(ctx, `UPDATE cube_accounts
				SET generation = generation + 1, updated_at = $2
				WHERE id = $1`, id, now); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
func (m *Manager) acquirePostgresLock(name string) (func(), error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	db, err := m.postgresDB(ctx)
	if err != nil {
		return nil, err
	}
	// A session-level advisory lock is bound to a single connection, so pin a
	// dedicated connection from the shared pool and run both the lock and its
	// matching unlock on that same conn. Returning the conn to the pool (via
	// conn.Close) — never closing the shared *sql.DB — is what releases it;
	// ConnMaxLifetime bounds the lock even if an unlock query ever fails on a
	// dead connection, since Postgres drops session locks when the backing
	// connection closes.
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	for {
		var locked bool
		err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtext($1))`, name).Scan(&locked)
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
		if locked {
			return func() {
				unlockCtx, unlockCancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer unlockCancel()
				_, _ = conn.ExecContext(unlockCtx, `SELECT pg_advisory_unlock(hashtext($1))`, name)
				_ = conn.Close()
			}, nil
		}
		if time.Since(start) > 5*time.Second {
			_ = conn.Close()
			return nil, fmt.Errorf("timeout acquiring postgres lock %s", name)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
