package manager

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"cube20/internal/quota"
	"cube20/internal/usage"

	_ "github.com/lib/pq"
	"github.com/pelletier/go-toml/v2"
)

const (
	defaultStateDirName    = ".cube20"
	defaultAccountsDirName = ".codex-accounts"
	settingsFileName       = "settings.toml"
	roundRobinFileName     = "run-round-robin.json"
	authFileName           = "auth.json"
	configFileName         = "config.toml"
)

var accountIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

type AccountStatus string

const (
	StatusReady      AccountStatus = "ready"
	StatusRecovering AccountStatus = "recovering"
	StatusDrain      AccountStatus = "drain"
	StatusDisabled   AccountStatus = "disabled"
)

type AccountOwnerMode string

const (
	OwnerCloud  AccountOwnerMode = "cloud"
	OwnerClient AccountOwnerMode = "client"
)

type QuotaSource string

const (
	QuotaSourceCloud  QuotaSource = "cloud"
	QuotaSourceClient QuotaSource = "client"
)

type Account struct {
	ID               string           `json:"id"`
	Label            string           `json:"label"`
	Plan             string           `json:"plan"`
	Status           AccountStatus    `json:"status"`
	CodexHome        string           `json:"codexHome"`
	OwnerMode        AccountOwnerMode `json:"ownerMode"`
	OwnerClientID    string           `json:"ownerClientId,omitempty"`
	Generation       int64            `json:"generation"`
	LeaseID          string           `json:"leaseId,omitempty"`
	LeaseClientID    string           `json:"leaseClientId,omitempty"`
	LeaseHolder      string           `json:"leaseHolder,omitempty"`
	LeaseStartedAt   time.Time        `json:"leaseStartedAt,omitempty"`
	LeaseHeartbeatAt time.Time        `json:"leaseHeartbeatAt,omitempty"`
	LeaseExpiresAt   time.Time        `json:"leaseExpiresAt,omitempty"`
	CreatedAt        time.Time        `json:"createdAt"`
	UpdatedAt        time.Time        `json:"updatedAt"`
	LastError        string           `json:"lastError,omitempty"`
}

type AccountView struct {
	Account
	AuthPresent   bool      `json:"authPresent"`
	AuthPath      string    `json:"authPath"`
	AuthUpdated   time.Time `json:"authUpdated,omitempty"`
	ConfigPresent bool      `json:"configPresent"`
	ConfigPath    string    `json:"configPath"`
	ConfigUpdated time.Time `json:"configUpdated,omitempty"`
	Active        bool      `json:"active"`
	LeaseActive   bool      `json:"leaseActive"`
}

type Lease struct {
	ID          string    `json:"id"`
	AccountID   string    `json:"accountId"`
	ClientID    string    `json:"clientId,omitempty"`
	Holder      string    `json:"holder,omitempty"`
	Generation  int64     `json:"generation"`
	StartedAt   time.Time `json:"startedAt"`
	HeartbeatAt time.Time `json:"heartbeatAt"`
	ExpiresAt   time.Time `json:"expiresAt"`
}

type LeaseSnapshot struct {
	Lease    Lease           `json:"lease"`
	Snapshot ProfileSnapshot `json:"snapshot"`
}

type State struct {
	Version    int                     `json:"version"`
	Accounts   []Account               `json:"accounts"`
	Clients    []Client                `json:"clients,omitempty"`
	Usage      map[string]AccountUsage `json:"usage,omitempty"`
	QuotaCache map[string]QuotaCache   `json:"quotaCache,omitempty"`
	Dispatches []DispatchEvent         `json:"dispatches,omitempty"`
}

type Client struct {
	ID         string     `json:"id"`
	Label      string     `json:"label"`
	TokenHash  string     `json:"tokenHash,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastSeenAt time.Time  `json:"lastSeenAt,omitempty"`
	RevokedAt  *time.Time `json:"revokedAt,omitempty"`
}

type ClientView struct {
	ID         string     `json:"id"`
	Label      string     `json:"label"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastSeenAt time.Time  `json:"lastSeenAt,omitempty"`
	RevokedAt  *time.Time `json:"revokedAt,omitempty"`
	Active     bool       `json:"active"`
}

type DispatchEvent struct {
	ID           string    `json:"id"`
	LeaseID      string    `json:"leaseId"`
	AccountID    string    `json:"accountId"`
	AccountLabel string    `json:"accountLabel,omitempty"`
	ClientID     string    `json:"clientId,omitempty"`
	ClientLabel  string    `json:"clientLabel,omitempty"`
	Holder       string    `json:"holder,omitempty"`
	Event        string    `json:"event"`
	Generation   int64     `json:"generation"`
	CreatedAt    time.Time `json:"createdAt"`
	StartedAt    time.Time `json:"startedAt,omitempty"`
	ExpiresAt    time.Time `json:"expiresAt,omitempty"`
}

type AccountUsage struct {
	AccountID   string             `json:"accountId"`
	ClientID    string             `json:"clientId,omitempty"`
	UpdatedAt   time.Time          `json:"updatedAt"`
	LatestAt    string             `json:"latestAt,omitempty"`
	LatestModel string             `json:"latestModel,omitempty"`
	Today       usage.Tokens       `json:"today"`
	SevenDays   usage.Tokens       `json:"sevenDays"`
	AllTime     usage.Tokens       `json:"allTime"`
	Models      []usage.ModelUsage `json:"models,omitempty"`
}

type QuotaCache struct {
	AccountID        string        `json:"accountId"`
	UpdatedAt        time.Time     `json:"updatedAt"`
	Result           quota.Result  `json:"result"`
	FiveHour         *quota.Window `json:"fiveHour,omitempty"`
	Source           QuotaSource   `json:"source,omitempty"`
	ReporterClientID string        `json:"reporterClientId,omitempty"`
}

type RefreshQueueItem struct {
	AccountID             string           `json:"accountId"`
	Label                 string           `json:"label"`
	Status                AccountStatus    `json:"status"`
	AuthPresent           bool             `json:"authPresent"`
	UpdatedAt             time.Time        `json:"updatedAt,omitempty"`
	ResetsAt              string           `json:"resetsAt,omitempty"`
	RemainingDisplay      string           `json:"remainingDisplay,omitempty"`
	RemainingPercent      float64          `json:"remainingPercent,omitempty"`
	UsedPercent           float64          `json:"usedPercent,omitempty"`
	QuotaStatus           quota.Status     `json:"quotaStatus,omitempty"`
	RefreshOrderReason    string           `json:"refreshOrderReason,omitempty"`
	OwnerMode             AccountOwnerMode `json:"ownerMode,omitempty"`
	OwnerClientID         string           `json:"ownerClientId,omitempty"`
	QuotaSource           QuotaSource      `json:"quotaSource,omitempty"`
	QuotaReporterClientID string           `json:"quotaReporterClientId,omitempty"`
	LeaseActive           bool             `json:"leaseActive,omitempty"`
	LeaseClientID         string           `json:"leaseClientId,omitempty"`
	LeaseExpiresAt        time.Time        `json:"leaseExpiresAt,omitempty"`
}

type roundRobinState struct {
	LastAccountID string `json:"lastAccountId"`
}

type LoadBalanceAccount struct {
	ID                    string           `json:"id"`
	Label                 string           `json:"label"`
	Status                AccountStatus    `json:"status"`
	AuthPresent           bool             `json:"authPresent"`
	ConfigPresent         bool             `json:"configPresent"`
	Active                bool             `json:"active"`
	CodexHome             string           `json:"codexHome"`
	OwnerMode             AccountOwnerMode `json:"ownerMode"`
	OwnerClientID         string           `json:"ownerClientId,omitempty"`
	Generation            int64            `json:"generation"`
	LeaseActive           bool             `json:"leaseActive"`
	LeaseClientID         string           `json:"leaseClientId,omitempty"`
	LeaseHolder           string           `json:"leaseHolder,omitempty"`
	LeaseExpiresAt        time.Time        `json:"leaseExpiresAt,omitempty"`
	Eligible              bool             `json:"eligible"`
	Reason                string           `json:"reason,omitempty"`
	QuotaStatus           quota.Status     `json:"quotaStatus,omitempty"`
	QuotaRemainingDisplay string           `json:"quotaRemainingDisplay,omitempty"`
	QuotaRemainingPercent float64          `json:"quotaRemainingPercent,omitempty"`
	QuotaUsedPercent      float64          `json:"quotaUsedPercent,omitempty"`
	QuotaResetsAt         string           `json:"quotaResetsAt,omitempty"`
	QuotaUpdatedAt        time.Time        `json:"quotaUpdatedAt,omitempty"`
	QuotaScore            float64          `json:"quotaScore,omitempty"`
}

type LoadBalanceStatus struct {
	Policy        string               `json:"policy"`
	StatePath     string               `json:"statePath"`
	LastAccountID string               `json:"lastAccountId"`
	Eligible      []LoadBalanceAccount `json:"eligible"`
	Excluded      []LoadBalanceAccount `json:"excluded"`
}

type Settings struct {
	LiveCodexHome    string `json:"liveCodexHome" toml:"live_codex_home"`
	AccountsDir      string `json:"accountsDir" toml:"accounts_dir"`
	SharedConfigPath string `json:"sharedConfigPath" toml:"-"`
	CloudURL         string `json:"cloudUrl" toml:"cloud_url"`
	CloudToken       string `json:"cloudToken" toml:"cloud_token"`
	DatabaseURL      string `json:"databaseUrl" toml:"database_url"`
}

type JSONProfile struct {
	ID     string          `json:"id"`
	Label  string          `json:"label"`
	Auth   json.RawMessage `json:"auth"`
	Config string          `json:"config"`
}

type ProfileSnapshot struct {
	ID            string           `json:"id"`
	Label         string           `json:"label"`
	Plan          string           `json:"plan,omitempty"`
	Status        AccountStatus    `json:"status,omitempty"`
	Auth          json.RawMessage  `json:"auth"`
	Config        string           `json:"config,omitempty"`
	SourceClient  string           `json:"sourceClient,omitempty"`
	OwnerMode     AccountOwnerMode `json:"ownerMode,omitempty"`
	OwnerClientID string           `json:"ownerClientId,omitempty"`
	LeaseID       string           `json:"leaseId,omitempty"`
	Generation    int64            `json:"generation,omitempty"`
	UpdatedAt     time.Time        `json:"updatedAt,omitempty"`
}

type Manager struct {
	StateDir         string
	StatePath        string
	SettingsPath     string
	AccountsDir      string
	LiveCodexHome    string
	SharedConfigPath string
	CloudURL         string
	CloudToken       string
	DatabaseURL      string
}

func New() (*Manager, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	stateDir := filepath.Join(home, defaultStateDirName)
	settingsPath := filepath.Join(stateDir, settingsFileName)
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, err
	}
	settings, err := loadSettings(settingsPath, defaultSettings(home), home)
	if err != nil {
		return nil, err
	}
	settings = applyEnvironmentOverrides(settings)

	return &Manager{
		StateDir:         stateDir,
		StatePath:        filepath.Join(stateDir, "state.json"),
		SettingsPath:     settingsPath,
		AccountsDir:      settings.AccountsDir,
		LiveCodexHome:    settings.LiveCodexHome,
		SharedConfigPath: settings.SharedConfigPath,
		CloudURL:         settings.CloudURL,
		CloudToken:       settings.CloudToken,
		DatabaseURL:      settings.DatabaseURL,
	}, nil
}

func applyEnvironmentOverrides(settings Settings) Settings {
	if value := strings.TrimSpace(os.Getenv("CUBE_CLOUD_URL")); value != "" {
		settings.CloudURL = value
	}
	if value := strings.TrimSpace(os.Getenv("CUBE_CLOUD_TOKEN")); value != "" {
		settings.CloudToken = value
	}
	if value := firstNonEmpty(os.Getenv("CUBE_DATABASE_URL"), os.Getenv("DATABASE_URL")); value != "" {
		settings.DatabaseURL = value
	}
	return settings
}

func (m *Manager) Ensure() error {
	if err := os.MkdirAll(m.StateDir, 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(m.AccountsDir, 0o700); err != nil {
		return err
	}
	if strings.TrimSpace(m.DatabaseURL) != "" {
		return m.ensurePostgres()
	}
	return nil
}

func (m *Manager) Load() (State, error) {
	if err := m.Ensure(); err != nil {
		return State{}, err
	}
	if strings.TrimSpace(m.DatabaseURL) != "" {
		return m.loadPostgresState()
	}

	data, err := os.ReadFile(m.StatePath)
	if errors.Is(err, os.ErrNotExist) {
		return normalizeState(State{Version: 1, Accounts: []Account{}}), nil
	}
	if err != nil {
		return State{}, err
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, err
	}
	return normalizeState(state), nil
}

func normalizeState(state State) State {
	if state.Version == 0 {
		state.Version = 1
	}
	if state.Accounts == nil {
		state.Accounts = []Account{}
	}
	for i := range state.Accounts {
		if !validAccountStatus(state.Accounts[i].Status) {
			state.Accounts[i].Status = StatusReady
		}
		if !validOwnerMode(state.Accounts[i].OwnerMode) {
			state.Accounts[i].OwnerMode = OwnerCloud
		}
		if state.Accounts[i].Generation < 0 {
			state.Accounts[i].Generation = 0
		}
	}
	if state.Clients == nil {
		state.Clients = []Client{}
	}
	if state.Usage == nil {
		state.Usage = map[string]AccountUsage{}
	}
	if state.QuotaCache == nil {
		state.QuotaCache = map[string]QuotaCache{}
	}
	if state.Dispatches == nil {
		state.Dispatches = []DispatchEvent{}
	}
	return state
}

func validOwnerMode(ownerMode AccountOwnerMode) bool {
	switch ownerMode {
	case OwnerCloud, OwnerClient:
		return true
	default:
		return false
	}
}

func validAccountStatus(status AccountStatus) bool {
	switch status {
	case StatusReady, StatusRecovering, StatusDrain, StatusDisabled:
		return true
	default:
		return false
	}
}

func (m *Manager) Save(state State) error {
	if err := m.Ensure(); err != nil {
		return err
	}
	if strings.TrimSpace(m.DatabaseURL) != "" {
		return m.savePostgresState(state)
	}
	state.Version = 1
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	tmpPath := m.StatePath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpPath, m.StatePath)
}

func (m *Manager) postgresDB(ctx context.Context) (*sql.DB, error) {
	databaseURL := strings.TrimSpace(m.DatabaseURL)
	if databaseURL == "" {
		return nil, errors.New("database_url is not configured")
	}
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func (m *Manager) ensurePostgres() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := m.postgresDB(ctx)
	if err != nil {
		return err
	}
	defer db.Close()
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
	defer db.Close()

	state := normalizeState(State{Version: 1})
	accountRows, err := db.QueryContext(ctx, `SELECT id, label, plan, status, codex_home, owner_mode, owner_client_id, generation, lease_id, lease_client_id, lease_holder, lease_started_at, lease_heartbeat_at, lease_expires_at, created_at, updated_at, last_error, auth_json::text FROM cube_accounts ORDER BY id`)
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
	return normalizeState(state), nil
}

func (m *Manager) savePostgresState(state State) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := m.postgresDB(ctx)
	if err != nil {
		return err
	}
	defer db.Close()

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
			(id, label, plan, status, codex_home, owner_mode, owner_client_id, generation, lease_id, lease_client_id, lease_holder, lease_started_at, lease_heartbeat_at, lease_expires_at, auth_json, created_at, updated_at, last_error)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15::jsonb, $16, $17, $18)
			ON CONFLICT (id) DO UPDATE SET
				label = EXCLUDED.label,
				plan = EXCLUDED.plan,
				status = EXCLUDED.status,
				codex_home = EXCLUDED.codex_home,
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
	return tx.Commit()
}

func (m *Manager) materializeAuth(account Account, raw []byte) error {
	if len(raw) == 0 {
		return nil
	}
	if !json.Valid(raw) {
		return fmt.Errorf("stored auth for %s is not valid JSON", account.ID)
	}
	if err := os.MkdirAll(account.CodexHome, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(account.CodexHome, authFileName), prettyJSON(raw), fileModeFor(authFileName))
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

func (m *Manager) CreateClient(label string) (ClientView, string, error) {
	label = strings.TrimSpace(label)
	if label == "" {
		label = "client"
	}
	state, err := m.Load()
	if err != nil {
		return ClientView{}, "", err
	}
	used := map[string]bool{}
	for _, client := range state.Clients {
		used[client.ID] = true
	}
	id := uniqueFromUsed(label, used)
	if !strings.HasPrefix(id, "client-") {
		id = uniqueFromUsed("client-"+id, used)
	}
	token, err := generatePAT()
	if err != nil {
		return ClientView{}, "", err
	}
	now := time.Now()
	client := Client{
		ID:        id,
		Label:     label,
		TokenHash: hashToken(token),
		CreatedAt: now,
	}
	state.Clients = append(state.Clients, client)
	if err := m.Save(state); err != nil {
		return ClientView{}, "", err
	}
	return clientView(client), token, nil
}

func (m *Manager) ListClients() ([]ClientView, error) {
	state, err := m.Load()
	if err != nil {
		return nil, err
	}
	views := make([]ClientView, 0, len(state.Clients))
	for _, client := range state.Clients {
		views = append(views, clientView(client))
	}
	sort.Slice(views, func(i, j int) bool {
		if views[i].Active != views[j].Active {
			return views[i].Active
		}
		return views[i].ID < views[j].ID
	})
	return views, nil
}

func (m *Manager) RevokeClient(id string) error {
	state, err := m.Load()
	if err != nil {
		return err
	}
	now := time.Now()
	for i := range state.Clients {
		if state.Clients[i].ID == id {
			state.Clients[i].RevokedAt = &now
			return m.Save(state)
		}
	}
	return fmt.Errorf("client %q not found", id)
}

func (m *Manager) AuthenticateClientToken(token string) (ClientView, bool) {
	token = strings.TrimSpace(token)
	if token == "" {
		return ClientView{}, false
	}
	hash := hashToken(token)
	state, err := m.Load()
	if err != nil {
		return ClientView{}, false
	}
	for _, client := range state.Clients {
		if client.RevokedAt != nil || client.TokenHash == "" {
			continue
		}
		if subtleStringEqual(client.TokenHash, hash) {
			return clientView(client), true
		}
	}
	return ClientView{}, false
}

func (m *Manager) TouchClient(id string) error {
	if strings.TrimSpace(id) == "" {
		return nil
	}
	state, err := m.Load()
	if err != nil {
		return err
	}
	now := time.Now()
	for i := range state.Clients {
		if state.Clients[i].ID == id {
			state.Clients[i].LastSeenAt = now
			return m.Save(state)
		}
	}
	return nil
}

func (m *Manager) RecordUsage(accountID, clientID string, summary usage.Summary) error {
	return m.RecordUsageWithContext(accountID, clientID, "", "", summary)
}

func (m *Manager) RecordUsageWithContext(accountID, clientID, leaseID, runID string, summary usage.Summary) error {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return errors.New("usage account id is required")
	}
	if strings.TrimSpace(m.DatabaseURL) != "" {
		return m.recordPostgresUsage(accountID, strings.TrimSpace(clientID), strings.TrimSpace(leaseID), strings.TrimSpace(runID), summary)
	}
	state, err := m.Load()
	if err != nil {
		return err
	}
	if state.Usage == nil {
		state.Usage = map[string]AccountUsage{}
	}
	state.Usage[accountID] = AccountUsage{
		AccountID:   accountID,
		ClientID:    strings.TrimSpace(clientID),
		UpdatedAt:   time.Now(),
		LatestAt:    summary.LatestAt,
		LatestModel: summary.LatestModel,
		Today:       summary.Today,
		SevenDays:   summary.SevenDays,
		AllTime:     summary.AllTime,
		Models:      summary.Models,
	}
	return m.Save(state)
}

func (m *Manager) recordPostgresUsage(accountID, clientID, leaseID, runID string, summary usage.Summary) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := m.postgresDB(ctx)
	if err != nil {
		return err
	}
	defer db.Close()
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

func (m *Manager) UsageStats() (map[string]AccountUsage, error) {
	state, err := m.Load()
	if err != nil {
		return nil, err
	}
	out := map[string]AccountUsage{}
	for id, stat := range state.Usage {
		out[id] = stat
	}
	return out, nil
}

func (m *Manager) DispatchHistory(limit int, clientID string) ([]DispatchEvent, error) {
	if err := m.Ensure(); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > maxDispatchHistory {
		limit = 50
	}
	clientID = strings.TrimSpace(clientID)
	if strings.TrimSpace(m.DatabaseURL) != "" {
		return m.postgresDispatchHistory(limit, clientID)
	}
	state, err := m.Load()
	if err != nil {
		return nil, err
	}
	out := make([]DispatchEvent, 0, minInt(limit, len(state.Dispatches)))
	for _, event := range state.Dispatches {
		if clientID != "" && event.ClientID != clientID {
			continue
		}
		event = enrichDispatchEvent(state, event)
		out = append(out, event)
		if len(out) >= limit {
			break
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

func (m *Manager) postgresDispatchHistory(limit int, clientID string) ([]DispatchEvent, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := m.postgresDB(ctx)
	if err != nil {
		return nil, err
	}
	defer db.Close()

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

func enrichDispatchEvent(state State, event DispatchEvent) DispatchEvent {
	if strings.TrimSpace(event.AccountLabel) == "" {
		for _, account := range state.Accounts {
			if account.ID == event.AccountID {
				event.AccountLabel = account.Label
				break
			}
		}
	}
	if strings.TrimSpace(event.ClientLabel) == "" {
		event.ClientLabel = clientLabelFromState(state, event.ClientID)
	}
	return event
}

func (m *Manager) RefreshQueue() ([]RefreshQueueItem, error) {
	accounts, err := m.ListAccounts()
	if err != nil {
		return nil, err
	}
	state, err := m.Load()
	if err != nil {
		return nil, err
	}
	items := make([]RefreshQueueItem, 0, len(accounts))
	for _, account := range accounts {
		cache := state.QuotaCache[account.ID]
		item := RefreshQueueItem{
			AccountID:             account.ID,
			Label:                 account.Label,
			Status:                account.Status,
			AuthPresent:           account.AuthPresent,
			UpdatedAt:             cache.UpdatedAt,
			QuotaStatus:           cache.Result.Status,
			OwnerMode:             account.OwnerMode,
			OwnerClientID:         account.OwnerClientID,
			QuotaSource:           cache.Source,
			QuotaReporterClientID: cache.ReporterClientID,
			LeaseActive:           account.LeaseActive,
			LeaseClientID:         account.LeaseClientID,
			LeaseExpiresAt:        account.LeaseExpiresAt,
		}
		if cache.FiveHour != nil {
			item.ResetsAt = cache.FiveHour.ResetsAt
			item.RemainingDisplay = cache.FiveHour.RemainingDisplay
			item.RemainingPercent = cache.FiveHour.RemainingPercent
			item.UsedPercent = cache.FiveHour.UsedPercent
		}
		switch {
		case !account.AuthPresent:
			item.RefreshOrderReason = "auth missing"
		case account.OwnerMode == OwnerClient:
			if cache.Result.Status == "" {
				item.RefreshOrderReason = "waiting for client report"
			} else {
				item.RefreshOrderReason = "client reported"
			}
		case account.LeaseActive:
			item.RefreshOrderReason = "leased"
		case cache.FiveHour == nil:
			item.RefreshOrderReason = "quota not checked"
		case cache.FiveHour.ResetsAt == "":
			item.RefreshOrderReason = "reset unknown"
		default:
			item.RefreshOrderReason = "5h reset order"
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		ti := parseRFC3339(items[i].ResetsAt)
		tj := parseRFC3339(items[j].ResetsAt)
		if !ti.IsZero() && !tj.IsZero() {
			return ti.Before(tj)
		}
		if !ti.IsZero() {
			return true
		}
		if !tj.IsZero() {
			return false
		}
		return items[i].AccountID < items[j].AccountID
	})
	return items, nil
}

func (m *Manager) UpdateSettings(liveCodexHome, accountsDir, sharedConfigPath string) (Settings, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Settings{}, err
	}

	settings := Settings{
		LiveCodexHome:    m.LiveCodexHome,
		AccountsDir:      m.AccountsDir,
		SharedConfigPath: m.SharedConfigPath,
		CloudURL:         m.CloudURL,
		CloudToken:       m.CloudToken,
		DatabaseURL:      m.DatabaseURL,
	}
	if strings.TrimSpace(liveCodexHome) != "" {
		settings.LiveCodexHome = expandPath(liveCodexHome, home)
	}
	if strings.TrimSpace(accountsDir) != "" {
		settings.AccountsDir = expandPath(accountsDir, home)
	}
	if settings.LiveCodexHome == "" || settings.AccountsDir == "" {
		return Settings{}, errors.New("settings paths cannot be empty")
	}

	if err := writeSettings(m.SettingsPath, settings); err != nil {
		return Settings{}, err
	}
	m.LiveCodexHome = settings.LiveCodexHome
	m.AccountsDir = settings.AccountsDir
	m.SharedConfigPath = settings.SharedConfigPath
	m.CloudURL = settings.CloudURL
	m.CloudToken = settings.CloudToken
	m.DatabaseURL = settings.DatabaseURL
	if err := m.Ensure(); err != nil {
		return Settings{}, err
	}
	return settings, nil
}

func (m *Manager) UpdateCloudSettings(cloudURL, cloudToken string) (Settings, error) {
	settings := Settings{
		LiveCodexHome:    m.LiveCodexHome,
		AccountsDir:      m.AccountsDir,
		SharedConfigPath: m.SharedConfigPath,
		CloudURL:         m.CloudURL,
		CloudToken:       m.CloudToken,
		DatabaseURL:      m.DatabaseURL,
	}
	if strings.TrimSpace(cloudURL) != "" {
		settings.CloudURL = strings.TrimSpace(cloudURL)
	}
	if strings.TrimSpace(cloudToken) != "" {
		settings.CloudToken = strings.TrimSpace(cloudToken)
	}
	if settings.LiveCodexHome == "" || settings.AccountsDir == "" {
		return Settings{}, errors.New("settings paths cannot be empty")
	}
	if err := writeSettings(m.SettingsPath, settings); err != nil {
		return Settings{}, err
	}
	m.CloudURL = settings.CloudURL
	m.CloudToken = settings.CloudToken
	return settings, nil
}

func (m *Manager) ReadSettingsText() (string, error) {
	if err := m.Ensure(); err != nil {
		return "", err
	}
	data, err := os.ReadFile(m.SettingsPath)
	if errors.Is(err, os.ErrNotExist) {
		settings := Settings{
			LiveCodexHome:    m.LiveCodexHome,
			AccountsDir:      m.AccountsDir,
			SharedConfigPath: m.SharedConfigPath,
			CloudURL:         m.CloudURL,
			CloudToken:       m.CloudToken,
			DatabaseURL:      m.DatabaseURL,
		}
		if err := writeSettings(m.SettingsPath, settings); err != nil {
			return "", err
		}
		data, err = os.ReadFile(m.SettingsPath)
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (m *Manager) WriteSettingsText(raw string) (Settings, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Settings{}, err
	}

	settings, _, err := parseSettingsData([]byte(raw), Settings{
		LiveCodexHome:    m.LiveCodexHome,
		AccountsDir:      m.AccountsDir,
		SharedConfigPath: m.SharedConfigPath,
		CloudURL:         m.CloudURL,
		CloudToken:       m.CloudToken,
		DatabaseURL:      m.DatabaseURL,
	}, home)
	if err != nil {
		return Settings{}, err
	}
	if settings.LiveCodexHome == "" || settings.AccountsDir == "" {
		return Settings{}, errors.New("settings.toml must include live_codex_home and accounts_dir")
	}

	if err := writeSettings(m.SettingsPath, settings); err != nil {
		return Settings{}, err
	}
	m.LiveCodexHome = settings.LiveCodexHome
	m.AccountsDir = settings.AccountsDir
	m.SharedConfigPath = settings.SharedConfigPath
	m.CloudURL = settings.CloudURL
	m.CloudToken = settings.CloudToken
	m.DatabaseURL = settings.DatabaseURL
	if err := m.Ensure(); err != nil {
		return Settings{}, err
	}
	return settings, nil
}

func (m *Manager) AddAccount(id, label string) (Account, error) {
	id = strings.TrimSpace(id)
	label = strings.TrimSpace(label)
	if !accountIDPattern.MatchString(id) {
		return Account{}, fmt.Errorf("account id must match %s", accountIDPattern.String())
	}

	state, err := m.Load()
	if err != nil {
		return Account{}, err
	}

	for _, account := range state.Accounts {
		if account.ID == id {
			return Account{}, fmt.Errorf("account %q already exists", id)
		}
	}

	now := time.Now()
	account := Account{
		ID:        id,
		Label:     label,
		Status:    StatusReady,
		CodexHome: filepath.Join(m.AccountsDir, id),
		OwnerMode: OwnerCloud,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := os.MkdirAll(account.CodexHome, 0o700); err != nil {
		return Account{}, err
	}

	state.Accounts = append(state.Accounts, account)
	if err := m.Save(state); err != nil {
		return Account{}, err
	}
	return account, nil
}

func (m *Manager) ImportLiveProfile(id, label, sourceCodexHome string) (Account, error) {
	var err error
	if strings.TrimSpace(sourceCodexHome) == "" {
		sourceCodexHome = m.LiveCodexHome
	}
	if strings.TrimSpace(sourceCodexHome) == "" {
		sourceCodexHome, err = defaultCodexHome()
		if err != nil {
			return Account{}, err
		}
	}

	sourceAuth := filepath.Join(sourceCodexHome, authFileName)
	if _, err := os.Stat(sourceAuth); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Account{}, fmt.Errorf("no auth.json found in %s", sourceCodexHome)
		}
		return Account{}, err
	}

	auth := readAuthMetadata(sourceAuth)
	state, err := m.Load()
	if err != nil {
		return Account{}, err
	}
	identity := authIdentity(auth)
	if strings.TrimSpace(id) == "" {
		id = sanitizeAccountID(deriveIDFromAuth(auth, label))
		if id == "" {
			id = uniqueFromUsed("profile-"+time.Now().Format("20060102-150405"), accountIDs(state))
		}
	}
	if duplicate, ok := duplicateAccount(state, id, identity); ok {
		return Account{}, fmt.Errorf("auth.json already exists as account %q", duplicate.ID)
	}
	if strings.TrimSpace(label) == "" {
		label = deriveLabelFromAuth(auth)
		if strings.TrimSpace(label) == "" {
			label = filepath.Base(filepath.Clean(sourceCodexHome))
		}
	}

	account, err := m.AddAccount(id, label)
	if err != nil {
		return Account{}, err
	}
	targetAuth := filepath.Join(account.CodexHome, authFileName)
	if err := copyFile(sourceAuth, targetAuth, fileModeFor(authFileName)); err != nil {
		return Account{}, err
	}
	state, err = m.Load()
	if err != nil {
		return Account{}, err
	}
	for i := range state.Accounts {
		if state.Accounts[i].ID == account.ID {
			state.Accounts[i].Generation = 1
			state.Accounts[i].UpdatedAt = time.Now()
			account = state.Accounts[i]
			break
		}
	}
	if err := m.Save(state); err != nil {
		return Account{}, err
	}
	return account, nil
}

func (m *Manager) ImportJSONProfile(profile JSONProfile) (Account, error) {
	return m.UpsertJSONProfile(profile)
}

func (m *Manager) ExportProfileSnapshot(id string) (ProfileSnapshot, error) {
	account, err := m.GetAccount(id)
	if err != nil {
		return ProfileSnapshot{}, err
	}

	authPath := filepath.Join(account.CodexHome, authFileName)
	authRaw, err := os.ReadFile(authPath)
	if errors.Is(err, os.ErrNotExist) {
		return ProfileSnapshot{}, fmt.Errorf("account %q has no auth.json", id)
	}
	if err != nil {
		return ProfileSnapshot{}, err
	}

	updatedAt := account.UpdatedAt
	if info, err := os.Stat(authPath); err == nil && info.ModTime().After(updatedAt) {
		updatedAt = info.ModTime()
	}

	return ProfileSnapshot{
		ID:            account.ID,
		Label:         account.Label,
		Plan:          account.Plan,
		Status:        account.Status,
		Auth:          prettyJSON(authRaw),
		OwnerMode:     account.OwnerMode,
		OwnerClientID: account.OwnerClientID,
		Generation:    account.Generation,
		UpdatedAt:     updatedAt,
	}, nil
}

func (m *Manager) ExportLiveProfileSnapshot(ownerClientID string) (ProfileSnapshot, error) {
	codexHome := strings.TrimSpace(m.LiveCodexHome)
	if codexHome == "" {
		defaultHome, err := defaultCodexHome()
		if err != nil {
			return ProfileSnapshot{}, err
		}
		codexHome = defaultHome
	}
	authPath := filepath.Join(codexHome, authFileName)
	authRaw, err := os.ReadFile(authPath)
	if errors.Is(err, os.ErrNotExist) {
		return ProfileSnapshot{}, fmt.Errorf("no auth.json found in %s", codexHome)
	}
	if err != nil {
		return ProfileSnapshot{}, err
	}
	if !json.Valid(authRaw) {
		return ProfileSnapshot{}, fmt.Errorf("%s is not valid JSON", authPath)
	}
	auth := readAuthMetadata(authPath)
	label := deriveLabelFromAuth(auth)
	id := sanitizeAccountID(deriveIDFromAuth(auth, label))
	if id == "" {
		id = "current-codex"
	}
	updatedAt := time.Now()
	if info, err := os.Stat(authPath); err == nil {
		updatedAt = info.ModTime()
	}
	return ProfileSnapshot{
		ID:            id,
		Label:         label,
		Status:        StatusReady,
		Auth:          prettyJSON(authRaw),
		OwnerMode:     OwnerClient,
		OwnerClientID: strings.TrimSpace(ownerClientID),
		UpdatedAt:     updatedAt,
	}, nil
}

func (m *Manager) UpsertProfileSnapshot(snapshot ProfileSnapshot) (Account, error) {
	lockPath := filepath.Join(m.StateDir, "run-round-robin.lock")
	unlock, err := m.acquireLock(lockPath)
	if err != nil {
		return Account{}, err
	}
	defer unlock()

	account, err := m.UpsertJSONProfile(JSONProfile{
		ID:    snapshot.ID,
		Label: snapshot.Label,
		Auth:  snapshot.Auth,
	})
	if err != nil {
		return Account{}, err
	}

	if snapshot.Status != "" {
		if !validAccountStatus(snapshot.Status) {
			return Account{}, fmt.Errorf("unknown status %q", snapshot.Status)
		}
	}
	if snapshot.OwnerMode != "" && !validOwnerMode(snapshot.OwnerMode) {
		return Account{}, fmt.Errorf("unknown owner mode %q", snapshot.OwnerMode)
	}

	state, err := m.Load()
	if err != nil {
		return Account{}, err
	}
	for i := range state.Accounts {
		if state.Accounts[i].ID != account.ID {
			continue
		}
		if strings.TrimSpace(snapshot.Label) != "" {
			state.Accounts[i].Label = strings.TrimSpace(snapshot.Label)
		}
		if strings.TrimSpace(snapshot.Plan) != "" {
			state.Accounts[i].Plan = strings.TrimSpace(snapshot.Plan)
		}
		if snapshot.Status != "" {
			state.Accounts[i].Status = snapshot.Status
		}
		if snapshot.OwnerMode != "" {
			state.Accounts[i].OwnerMode = snapshot.OwnerMode
		}
		if strings.TrimSpace(snapshot.OwnerClientID) != "" {
			state.Accounts[i].OwnerClientID = strings.TrimSpace(snapshot.OwnerClientID)
		} else if snapshot.OwnerMode == OwnerClient && strings.TrimSpace(snapshot.SourceClient) != "" {
			state.Accounts[i].OwnerClientID = strings.TrimSpace(snapshot.SourceClient)
		}
		state.Accounts[i].UpdatedAt = time.Now()
		account = state.Accounts[i]
		break
	}
	if err := m.Save(state); err != nil {
		return Account{}, err
	}
	return account, nil
}

func (m *Manager) UpsertJSONProfile(profile JSONProfile) (Account, error) {
	authRaw := profile.Auth
	if len(authRaw) == 0 || string(authRaw) == "null" {
		return Account{}, errors.New("profile json must include auth, or upload a raw auth.json")
	}

	var auth map[string]any
	if err := json.Unmarshal(authRaw, &auth); err != nil {
		return Account{}, fmt.Errorf("auth is not valid JSON: %w", err)
	}

	state, err := m.Load()
	if err != nil {
		return Account{}, err
	}

	identity := authIdentity(auth)
	id := strings.TrimSpace(profile.ID)
	if id == "" {
		id = sanitizeAccountID(deriveIDFromAuth(auth, profile.Label))
		if id == "" {
			id = uniqueFromUsed("profile-"+time.Now().Format("20060102-150405"), accountIDs(state))
		}
	}

	existingIndex := -1
	for i, account := range state.Accounts {
		if account.ID == id {
			existingIndex = i
			break
		}
	}
	if existingIndex < 0 && identity != "" {
		for i, account := range state.Accounts {
			existing := readAuthMetadata(filepath.Join(account.CodexHome, authFileName))
			if authIdentity(existing) == identity {
				existingIndex = i
				break
			}
		}
	}

	label := strings.TrimSpace(profile.Label)
	if label == "" {
		label = deriveLabelFromAuth(auth)
	}

	if existingIndex >= 0 {
		account := state.Accounts[existingIndex]
		if accountLeaseActive(account, time.Now()) {
			return Account{}, fmt.Errorf("account %q is currently leased; wait for release or use the lease auth endpoint", account.ID)
		}
		if err := m.writeProfileFiles(account, authRaw); err != nil {
			return Account{}, err
		}
		if label != "" {
			account.Label = label
		}
		account.Generation++
		account.UpdatedAt = time.Now()
		state.Accounts[existingIndex] = account
		if err := m.Save(state); err != nil {
			return Account{}, err
		}
		return account, nil
	}

	account, err := m.AddAccount(id, label)
	if err != nil {
		return Account{}, err
	}
	if err := m.writeProfileFiles(account, authRaw); err != nil {
		return Account{}, err
	}
	state, err = m.Load()
	if err != nil {
		return Account{}, err
	}
	for i := range state.Accounts {
		if state.Accounts[i].ID == account.ID {
			state.Accounts[i].Generation = 1
			state.Accounts[i].UpdatedAt = time.Now()
			account = state.Accounts[i]
			break
		}
	}
	if err := m.Save(state); err != nil {
		return Account{}, err
	}
	return account, nil
}

func (m *Manager) writeProfileFiles(account Account, authRaw json.RawMessage) error {
	if err := os.MkdirAll(account.CodexHome, 0o700); err != nil {
		return err
	}
	authPath := filepath.Join(account.CodexHome, authFileName)
	if err := os.WriteFile(authPath, prettyJSON(authRaw), fileModeFor(authFileName)); err != nil {
		return err
	}
	return nil
}

func (m *Manager) ListAccounts() ([]AccountView, error) {
	state, err := m.Load()
	if err != nil {
		return nil, err
	}
	if nextState, changed, err := m.syncManagedAccounts(state); err != nil {
		return nil, err
	} else if changed {
		state = nextState
		if err := m.Save(state); err != nil {
			return nil, err
		}
	}

	views := make([]AccountView, 0, len(state.Accounts))
	for _, account := range state.Accounts {
		views = append(views, m.accountView(account))
	}
	sort.Slice(views, func(i, j int) bool {
		return views[i].ID < views[j].ID
	})
	return views, nil
}

func (m *Manager) LiveProfileView() AccountView {
	return m.accountView(Account{
		ID:        "current-codex",
		Label:     "Current Codex",
		Status:    StatusReady,
		CodexHome: m.LiveCodexHome,
		OwnerMode: OwnerClient,
	})
}

func (m *Manager) syncManagedAccounts(state State) (State, bool, error) {
	entries, err := os.ReadDir(m.AccountsDir)
	if errors.Is(err, os.ErrNotExist) {
		return state, false, nil
	}
	if err != nil {
		return state, false, err
	}

	used := map[string]bool{}
	knownPaths := map[string]bool{}
	for _, account := range state.Accounts {
		used[account.ID] = true
		knownPaths[filepath.Clean(account.CodexHome)] = true
	}

	changed := false
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		codexHome := filepath.Join(m.AccountsDir, entry.Name())
		if knownPaths[filepath.Clean(codexHome)] || !hasManagedFiles(codexHome) {
			continue
		}
		id := sanitizeAccountID(entry.Name())
		if id == "" || used[id] {
			id = uniqueFromUsed("profile-"+entry.Name(), used)
		}
		auth := readAuthMetadata(filepath.Join(codexHome, authFileName))
		label := deriveLabelFromAuth(auth)
		if label == "" {
			label = entry.Name()
		}
		now := time.Now()
		state.Accounts = append(state.Accounts, Account{
			ID:        id,
			Label:     label,
			Status:    StatusReady,
			CodexHome: codexHome,
			OwnerMode: OwnerCloud,
			CreatedAt: now,
			UpdatedAt: now,
		})
		used[id] = true
		knownPaths[filepath.Clean(codexHome)] = true
		changed = true
	}
	return state, changed, nil
}

func hasManagedFiles(codexHome string) bool {
	_, err := os.Stat(filepath.Join(codexHome, authFileName))
	return err == nil
}

func (m *Manager) uniqueAccountID(base string) string {
	base = sanitizeAccountID(base)
	if base == "" {
		base = "profile-" + time.Now().Format("20060102-150405")
	}
	state, err := m.Load()
	if err != nil {
		return base
	}
	used := map[string]bool{}
	for _, account := range state.Accounts {
		used[account.ID] = true
	}
	if !used[base] {
		return base
	}
	for i := 2; i < 1000; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if len(candidate) > 64 {
			candidate = fmt.Sprintf("%s-%d", base[:64-len(fmt.Sprintf("-%d", i))], i)
		}
		if !used[candidate] {
			return candidate
		}
	}
	return "profile-" + time.Now().Format("20060102-150405")
}

func uniqueFromUsed(base string, used map[string]bool) string {
	base = sanitizeAccountID(base)
	if base == "" {
		base = "profile"
	}
	if !used[base] {
		return base
	}
	for i := 2; i < 1000; i++ {
		suffix := fmt.Sprintf("-%d", i)
		candidate := base
		if len(candidate)+len(suffix) > 64 {
			candidate = candidate[:64-len(suffix)]
		}
		candidate += suffix
		if !used[candidate] {
			return candidate
		}
	}
	return "profile-" + time.Now().Format("20060102-150405")
}

func accountIDs(state State) map[string]bool {
	used := map[string]bool{}
	for _, account := range state.Accounts {
		used[account.ID] = true
	}
	return used
}

func duplicateAccount(state State, id, identity string) (Account, bool) {
	for _, account := range state.Accounts {
		if account.ID == id {
			return account, true
		}
		if identity == "" {
			continue
		}
		existing := readAuthMetadata(filepath.Join(account.CodexHome, authFileName))
		if authIdentity(existing) == identity {
			return account, true
		}
	}
	return Account{}, false
}

func clientView(client Client) ClientView {
	return ClientView{
		ID:         client.ID,
		Label:      client.Label,
		CreatedAt:  client.CreatedAt,
		LastSeenAt: client.LastSeenAt,
		RevokedAt:  client.RevokedAt,
		Active:     client.RevokedAt == nil,
	}
}

func generatePAT() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "cube_pat_" + base64.RawURLEncoding.EncodeToString(raw), nil
}

func generateLeaseID() (string, error) {
	raw := make([]byte, 18)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "lease_" + base64.RawURLEncoding.EncodeToString(raw), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return fmt.Sprintf("%x", sum)
}

func subtleStringEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func parseRFC3339(value string) time.Time {
	if strings.TrimSpace(value) == "" {
		return time.Time{}
	}
	out, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}
	}
	return out
}

func authIdentity(auth map[string]any) string {
	if tokens, ok := auth["tokens"].(map[string]any); ok {
		if accountID, ok := tokens["account_id"].(string); ok && strings.TrimSpace(accountID) != "" {
			return "account_id:" + strings.TrimSpace(accountID)
		}
		if idToken, ok := tokens["id_token"].(string); ok {
			claims := claimsFromIDToken(idToken)
			if sub, ok := claims["sub"].(string); ok && strings.TrimSpace(sub) != "" {
				return "sub:" + strings.TrimSpace(sub)
			}
			if email, ok := claims["email"].(string); ok && strings.TrimSpace(email) != "" {
				return "email:" + strings.ToLower(strings.TrimSpace(email))
			}
		}
	}
	if apiKey, ok := auth["OPENAI_API_KEY"].(string); ok && strings.TrimSpace(apiKey) != "" {
		sum := sha256.Sum256([]byte(strings.TrimSpace(apiKey)))
		return fmt.Sprintf("api_key:%x", sum)
	}
	return ""
}

func deriveIDFromAuth(auth map[string]any, label string) string {
	if tokens, ok := auth["tokens"].(map[string]any); ok {
		if accountID, ok := tokens["account_id"].(string); ok && strings.TrimSpace(accountID) != "" {
			return accountID
		}
		if idToken, ok := tokens["id_token"].(string); ok {
			claims := claimsFromIDToken(idToken)
			if sub, ok := claims["sub"].(string); ok && strings.TrimSpace(sub) != "" {
				return sub
			}
			if email, ok := claims["email"].(string); ok && strings.TrimSpace(email) != "" {
				return strings.Split(email, "@")[0]
			}
		}
	}
	if strings.TrimSpace(label) != "" {
		return label
	}
	if apiKey, ok := auth["OPENAI_API_KEY"].(string); ok && strings.TrimSpace(apiKey) != "" {
		return "api-key"
	}
	return ""
}

func deriveLabelFromAuth(auth map[string]any) string {
	if tokens, ok := auth["tokens"].(map[string]any); ok {
		if idToken, ok := tokens["id_token"].(string); ok {
			claims := claimsFromIDToken(idToken)
			for _, key := range []string{"email", "https://api.openai.com/profile_email", "sub"} {
				if value, ok := claims[key].(string); ok && strings.TrimSpace(value) != "" {
					return value
				}
			}
		}
		if accountID, ok := tokens["account_id"].(string); ok && strings.TrimSpace(accountID) != "" {
			return accountID
		}
	}
	return ""
}

func sanitizeAccountID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var builder strings.Builder
	for _, ch := range value {
		switch {
		case ch >= 'a' && ch <= 'z':
			builder.WriteRune(ch)
		case ch >= 'A' && ch <= 'Z':
			builder.WriteRune(ch)
		case ch >= '0' && ch <= '9':
			builder.WriteRune(ch)
		case ch == '-' || ch == '_':
			builder.WriteRune(ch)
		case ch == '.' || ch == '@' || ch == ' ':
			builder.WriteRune('-')
		}
		if builder.Len() >= 64 {
			break
		}
	}
	out := strings.Trim(builder.String(), "-_")
	if out == "" {
		return ""
	}
	if !((out[0] >= 'a' && out[0] <= 'z') || (out[0] >= 'A' && out[0] <= 'Z') || (out[0] >= '0' && out[0] <= '9')) {
		out = "profile-" + out
	}
	return out
}

func claimsFromIDToken(idToken string) map[string]any {
	parts := strings.Split(strings.TrimSpace(idToken), ".")
	if len(parts) < 2 {
		return map[string]any{}
	}
	payload := parts[1]
	payload = strings.ReplaceAll(payload, "-", "+")
	payload = strings.ReplaceAll(payload, "_", "/")
	for len(payload)%4 != 0 {
		payload += "="
	}
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return map[string]any{}
	}
	var claims map[string]any
	if err := json.Unmarshal(data, &claims); err != nil {
		return map[string]any{}
	}
	return claims
}

func readAuthMetadata(path string) map[string]any {
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string]any{}
	}
	var auth map[string]any
	if err := json.Unmarshal(data, &auth); err != nil {
		return map[string]any{}
	}
	return auth
}

func fileDigest(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum)
}

func prettyJSON(raw json.RawMessage) []byte {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return raw
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return raw
	}
	return append(data, '\n')
}

func (m *Manager) GetAccount(id string) (Account, error) {
	state, err := m.Load()
	if err != nil {
		return Account{}, err
	}
	for _, account := range state.Accounts {
		if account.ID == id {
			return account, nil
		}
	}
	return Account{}, fmt.Errorf("account %q not found", id)
}

func (m *Manager) SetStatus(id string, status AccountStatus) error {
	if !validAccountStatus(status) {
		return fmt.Errorf("unknown status %q", status)
	}

	state, err := m.Load()
	if err != nil {
		return err
	}
	for i := range state.Accounts {
		if state.Accounts[i].ID == id {
			state.Accounts[i].Status = status
			state.Accounts[i].UpdatedAt = time.Now()
			return m.Save(state)
		}
	}
	return fmt.Errorf("account %q not found", id)
}

func (m *Manager) SetLabel(id, label string) error {
	label = strings.TrimSpace(label)
	state, err := m.Load()
	if err != nil {
		return err
	}
	for i := range state.Accounts {
		if state.Accounts[i].ID == id {
			state.Accounts[i].Label = label
			state.Accounts[i].UpdatedAt = time.Now()
			return m.Save(state)
		}
	}
	return fmt.Errorf("account %q not found", id)
}

func (m *Manager) SetOwner(id string, ownerMode AccountOwnerMode, ownerClientID string) error {
	if !validOwnerMode(ownerMode) {
		return fmt.Errorf("unknown owner mode %q", ownerMode)
	}
	state, err := m.Load()
	if err != nil {
		return err
	}
	now := time.Now()
	for i := range state.Accounts {
		if state.Accounts[i].ID == id {
			state.Accounts[i].OwnerMode = ownerMode
			state.Accounts[i].OwnerClientID = strings.TrimSpace(ownerClientID)
			if ownerMode == OwnerClient {
				clearAccountLease(&state.Accounts[i])
			}
			state.Accounts[i].UpdatedAt = now
			return m.Save(state)
		}
	}
	return fmt.Errorf("account %q not found", id)
}

func (m *Manager) DeleteAccount(id string) (Account, error) {
	state, err := m.Load()
	if err != nil {
		return Account{}, err
	}

	index := -1
	var account Account
	for i := range state.Accounts {
		if state.Accounts[i].ID == id {
			index = i
			account = state.Accounts[i]
			break
		}
	}
	if index == -1 {
		return Account{}, fmt.Errorf("account %q not found", id)
	}

	if err := m.validateManagedCodexHome(account.CodexHome); err != nil {
		return Account{}, err
	}
	if err := os.RemoveAll(account.CodexHome); err != nil {
		return Account{}, err
	}

	state.Accounts = append(state.Accounts[:index], state.Accounts[index+1:]...)
	if strings.TrimSpace(m.DatabaseURL) != "" {
		if err := m.deletePostgresAccount(id); err != nil {
			return Account{}, err
		}
		if roundRobin, err := m.loadRoundRobinState(); err == nil && roundRobin.LastAccountID == id {
			_ = m.ResetRoundRobin()
		}
		return account, nil
	}
	if err := m.Save(state); err != nil {
		return Account{}, err
	}
	if roundRobin, err := m.loadRoundRobinState(); err == nil && roundRobin.LastAccountID == id {
		_ = m.ResetRoundRobin()
	}
	return account, nil
}

func (m *Manager) deletePostgresAccount(id string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := m.postgresDB(ctx)
	if err != nil {
		return err
	}
	defer db.Close()
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

func (m *Manager) validateManagedCodexHome(codexHome string) error {
	if strings.TrimSpace(codexHome) == "" {
		return errors.New("account codex home cannot be empty")
	}

	if samePath(codexHome, m.LiveCodexHome) {
		return fmt.Errorf("refusing to delete live CodexHome %s", codexHome)
	}
	if defaultHome, err := defaultCodexHome(); err == nil && samePath(codexHome, defaultHome) {
		return fmt.Errorf("refusing to delete live CodexHome %s", codexHome)
	}
	if !pathWithin(codexHome, m.AccountsDir) {
		return fmt.Errorf("refusing to delete unmanaged CodexHome %s", codexHome)
	}
	return nil
}

func samePath(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" || b == "" {
		return false
	}
	aAbs, errA := filepath.Abs(a)
	bAbs, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	aReal, errA := filepath.EvalSymlinks(aAbs)
	bReal, errB := filepath.EvalSymlinks(bAbs)
	if errA == nil {
		aAbs = aReal
	}
	if errB == nil {
		bAbs = bReal
	}
	return filepath.Clean(aAbs) == filepath.Clean(bAbs)
}

func pathWithin(child, parent string) bool {
	childAbs, err := filepath.Abs(child)
	if err != nil {
		return false
	}
	parentAbs, err := filepath.Abs(parent)
	if err != nil {
		return false
	}
	childReal, err := filepath.EvalSymlinks(childAbs)
	if err == nil {
		childAbs = childReal
	}
	parentReal, err := filepath.EvalSymlinks(parentAbs)
	if err == nil {
		parentAbs = parentReal
	}
	rel, err := filepath.Rel(filepath.Clean(parentAbs), filepath.Clean(childAbs))
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return false
	}
	return true
}

func (m *Manager) LoginCommand(id string) (*exec.Cmd, error) {
	account, err := m.GetAccount(id)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(account.CodexHome, 0o700); err != nil {
		return nil, err
	}
	if err := m.ensureLocalConfigLink(account.CodexHome); err != nil {
		return nil, err
	}

	cmd := exec.Command("codex", "login", "--device-auth")
	cmd.Env = withEnv(os.Environ(), "CODEX_HOME", account.CodexHome)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd, nil
}

func (m *Manager) CodexCommand(id string, args []string) (*exec.Cmd, error) {
	account, err := m.GetAccount(id)
	if err != nil {
		return nil, err
	}
	if account.Status == StatusDisabled {
		return nil, fmt.Errorf("account %q is disabled", id)
	}
	if err := os.MkdirAll(account.CodexHome, 0o700); err != nil {
		return nil, err
	}
	if err := m.ensureLocalConfigLink(account.CodexHome); err != nil {
		return nil, err
	}

	cmd := exec.Command("codex", args...)
	cmd.Env = withEnv(os.Environ(), "CODEX_HOME", account.CodexHome)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd, nil
}

func (m *Manager) SelectAccountForRun() (AccountView, error) {
	lockPath := filepath.Join(m.StateDir, "run-round-robin.lock")
	unlock, err := m.acquireLock(lockPath)
	if err != nil {
		return AccountView{}, err
	}
	defer unlock()

	accounts, err := m.ListAccounts()
	if err != nil {
		return AccountView{}, err
	}
	state, err := m.Load()
	if err != nil {
		return AccountView{}, err
	}

	type candidate struct {
		account AccountView
		score   float64
	}
	available := make([]candidate, 0, len(accounts))
	now := time.Now()
	for _, account := range accounts {
		evaluation := loadBalanceEligibility(account, state.QuotaCache[account.ID], now)
		if evaluation.Eligible {
			available = append(available, candidate{account: account, score: evaluation.QuotaScore})
		}
	}
	if len(available) == 0 {
		return AccountView{}, errors.New("no ready, unleased account with auth.json and available 5h quota is available")
	}
	sort.Slice(available, func(i, j int) bool {
		if !sameLoadBalanceScore(available[i].score, available[j].score) {
			return available[i].score > available[j].score
		}
		return available[i].account.ID < available[j].account.ID
	})

	roundRobin, err := m.loadRoundRobinState()
	if err != nil {
		return AccountView{}, err
	}
	selected := available[0]
	if roundRobin.LastAccountID != "" {
		topLen := loadBalanceTopGroupLen(len(available), func(i int) float64 { return available[i].score })
		for i := 0; i < topLen; i++ {
			if available[i].account.ID == roundRobin.LastAccountID {
				selected = available[(i+1)%topLen]
				break
			}
		}
	}

	if err := m.saveRoundRobinState(roundRobinState{LastAccountID: selected.account.ID}); err != nil {
		return AccountView{}, err
	}
	return selected.account, nil
}

func (m *Manager) ClaimLease(ctx context.Context, clientID, holder string, ttl time.Duration) (LeaseSnapshot, error) {
	if err := m.RecoverExpiredLeases(ctx); err != nil {
		return LeaseSnapshot{}, err
	}

	lockPath := filepath.Join(m.StateDir, "run-round-robin.lock")
	unlock, err := m.acquireLock(lockPath)
	if err != nil {
		return LeaseSnapshot{}, err
	}
	defer unlock()

	state, err := m.Load()
	if err != nil {
		return LeaseSnapshot{}, err
	}
	now := time.Now()
	state, _, _ = expireAccountLeases(state, now)

	type candidate struct {
		index   int
		account Account
		score   float64
	}
	available := []candidate{}
	for i, account := range state.Accounts {
		evaluation := loadBalanceEligibilityForAccount(account, m.accountAuthPresent(account), state.QuotaCache[account.ID], now)
		if !evaluation.Eligible {
			continue
		}
		available = append(available, candidate{index: i, account: account, score: evaluation.QuotaScore})
	}
	sort.Slice(available, func(i, j int) bool {
		if !sameLoadBalanceScore(available[i].score, available[j].score) {
			return available[i].score > available[j].score
		}
		return available[i].account.ID < available[j].account.ID
	})
	if len(available) == 0 {
		if err := m.Save(state); err != nil {
			return LeaseSnapshot{}, err
		}
		return LeaseSnapshot{}, errors.New("no ready, unleased account with auth.json and available 5h quota is available")
	}

	roundRobin, err := m.loadRoundRobinState()
	if err != nil {
		return LeaseSnapshot{}, err
	}
	selected := available[0]
	if roundRobin.LastAccountID != "" {
		topLen := loadBalanceTopGroupLen(len(available), func(i int) float64 { return available[i].score })
		for i, item := range available[:topLen] {
			if item.account.ID == roundRobin.LastAccountID {
				selected = available[(i+1)%topLen]
				break
			}
		}
	}

	leaseID, err := generateLeaseID()
	if err != nil {
		return LeaseSnapshot{}, err
	}
	ttl = normalizeLeaseTTL(ttl)
	account := state.Accounts[selected.index]
	account.LeaseID = leaseID
	account.LeaseClientID = strings.TrimSpace(clientID)
	account.LeaseHolder = strings.TrimSpace(holder)
	account.LeaseStartedAt = now
	account.LeaseHeartbeatAt = now
	account.LeaseExpiresAt = now.Add(ttl)
	account.UpdatedAt = now
	state.Accounts[selected.index] = account
	state.Dispatches = appendDispatchEvent(state.Dispatches, dispatchEventFromAccount(state, account, "claimed", now))

	if err := m.Save(state); err != nil {
		return LeaseSnapshot{}, err
	}
	_ = m.saveRoundRobinState(roundRobinState{LastAccountID: account.ID})

	snapshot, err := m.ExportProfileSnapshot(account.ID)
	if err != nil {
		return LeaseSnapshot{}, err
	}
	snapshot.LeaseID = leaseID
	snapshot.Generation = account.Generation
	snapshot.SourceClient = account.LeaseHolder

	lease := leaseFromAccount(account)
	return LeaseSnapshot{Lease: lease, Snapshot: snapshot}, nil
}

func (m *Manager) TouchLease(leaseID, accountID, clientID, holder string, ttl time.Duration) (Lease, error) {
	lockPath := filepath.Join(m.StateDir, "run-round-robin.lock")
	unlock, err := m.acquireLock(lockPath)
	if err != nil {
		return Lease{}, err
	}
	defer unlock()

	state, err := m.Load()
	if err != nil {
		return Lease{}, err
	}
	now := time.Now()
	state, _, _ = expireAccountLeases(state, now)
	index, account, err := findLeaseAccount(state, accountID, leaseID)
	if err != nil {
		_ = m.Save(state)
		return Lease{}, err
	}
	if err := validateLease(account, leaseID, clientID, now); err != nil {
		_ = m.Save(state)
		return Lease{}, err
	}
	ttl = normalizeLeaseTTL(ttl)
	account.LeaseHolder = firstNonEmpty(strings.TrimSpace(holder), account.LeaseHolder)
	account.LeaseHeartbeatAt = now
	account.LeaseExpiresAt = now.Add(ttl)
	account.UpdatedAt = now
	state.Accounts[index] = account
	if err := m.Save(state); err != nil {
		return Lease{}, err
	}
	return leaseFromAccount(account), nil
}

func (m *Manager) UpdateLeasedProfileSnapshot(snapshot ProfileSnapshot, clientID string, ttl time.Duration) (Account, error) {
	if strings.TrimSpace(snapshot.ID) == "" {
		return Account{}, errors.New("lease auth update needs account id")
	}
	if strings.TrimSpace(snapshot.LeaseID) == "" {
		return Account{}, errors.New("lease auth update needs lease id")
	}
	if len(snapshot.Auth) == 0 || string(snapshot.Auth) == "null" {
		return Account{}, errors.New("lease auth update needs auth")
	}
	if !json.Valid(snapshot.Auth) {
		return Account{}, errors.New("auth is not valid JSON")
	}
	if snapshot.Status != "" && !validAccountStatus(snapshot.Status) {
		return Account{}, fmt.Errorf("unknown status %q", snapshot.Status)
	}

	lockPath := filepath.Join(m.StateDir, "run-round-robin.lock")
	unlock, err := m.acquireLock(lockPath)
	if err != nil {
		return Account{}, err
	}
	defer unlock()

	state, err := m.Load()
	if err != nil {
		return Account{}, err
	}
	now := time.Now()
	state, _, _ = expireAccountLeases(state, now)
	index, account, err := findLeaseAccount(state, snapshot.ID, snapshot.LeaseID)
	if err != nil {
		_ = m.Save(state)
		return Account{}, err
	}
	if err := validateLease(account, snapshot.LeaseID, clientID, now); err != nil {
		_ = m.Save(state)
		return Account{}, err
	}
	if snapshot.Generation != account.Generation {
		return Account{}, fmt.Errorf("auth generation conflict for %s: client has %d, server has %d", account.ID, snapshot.Generation, account.Generation)
	}
	if err := m.writeProfileFiles(account, snapshot.Auth); err != nil {
		return Account{}, err
	}
	if strings.TrimSpace(snapshot.Label) != "" {
		account.Label = strings.TrimSpace(snapshot.Label)
	}
	if strings.TrimSpace(snapshot.Plan) != "" {
		account.Plan = strings.TrimSpace(snapshot.Plan)
	}
	if snapshot.Status != "" {
		account.Status = snapshot.Status
	}
	account.Generation++
	account.LeaseHolder = firstNonEmpty(strings.TrimSpace(snapshot.SourceClient), account.LeaseHolder)
	account.LeaseHeartbeatAt = now
	account.LeaseExpiresAt = now.Add(normalizeLeaseTTL(ttl))
	account.UpdatedAt = now
	state.Accounts[index] = account
	if err := m.Save(state); err != nil {
		return Account{}, err
	}
	return account, nil
}

func (m *Manager) ReleaseLease(accountID, leaseID, clientID string) error {
	lockPath := filepath.Join(m.StateDir, "run-round-robin.lock")
	unlock, err := m.acquireLock(lockPath)
	if err != nil {
		return err
	}
	defer unlock()

	state, err := m.Load()
	if err != nil {
		return err
	}
	now := time.Now()
	state, _, _ = expireAccountLeases(state, now)
	index, account, err := findLeaseAccount(state, accountID, leaseID)
	if err != nil {
		return m.Save(state)
	}
	if err := validateLease(account, leaseID, clientID, now); err != nil {
		return err
	}
	state.Dispatches = appendDispatchEvent(state.Dispatches, dispatchEventFromAccount(state, account, "released", now))
	clearAccountLease(&account)
	account.UpdatedAt = now
	state.Accounts[index] = account
	return m.Save(state)
}

func (m *Manager) AccountHasActiveLease(id string) (bool, error) {
	account, err := m.GetAccount(id)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return false, nil
		}
		return false, err
	}
	return accountLeaseActive(account, time.Now()), nil
}

func (m *Manager) RecoverExpiredLeases(ctx context.Context) error {
	lockPath := filepath.Join(m.StateDir, "run-round-robin.lock")
	unlock, err := m.acquireLock(lockPath)
	if err != nil {
		return err
	}
	state, err := m.Load()
	if err != nil {
		unlock()
		return err
	}
	state, expired, changed := expireAccountLeases(state, time.Now())
	if changed {
		err = m.Save(state)
	}
	unlock()
	if err != nil {
		return err
	}
	for _, id := range expired {
		result, err := m.FetchQuota(ctx, id)
		if err != nil && result.Status == "" {
			continue
		}
	}
	return nil
}

func (m *Manager) LoadBalanceStatus() (LoadBalanceStatus, error) {
	roundRobin, err := m.loadRoundRobinState()
	if err != nil {
		return LoadBalanceStatus{}, err
	}
	accounts, err := m.ListAccounts()
	if err != nil {
		return LoadBalanceStatus{}, err
	}
	state, err := m.Load()
	if err != nil {
		return LoadBalanceStatus{}, err
	}
	now := time.Now()

	status := LoadBalanceStatus{
		Policy:        "quota-aware weighted round-robin",
		StatePath:     filepath.Join(m.StateDir, roundRobinFileName),
		LastAccountID: roundRobin.LastAccountID,
		Eligible:      []LoadBalanceAccount{},
		Excluded:      []LoadBalanceAccount{},
	}
	for _, account := range accounts {
		entry := LoadBalanceAccount{
			ID:             account.ID,
			Label:          account.Label,
			Status:         account.Status,
			AuthPresent:    account.AuthPresent,
			ConfigPresent:  account.ConfigPresent,
			Active:         account.Active,
			CodexHome:      account.CodexHome,
			OwnerMode:      account.OwnerMode,
			OwnerClientID:  account.OwnerClientID,
			Generation:     account.Generation,
			LeaseActive:    account.LeaseActive,
			LeaseClientID:  account.LeaseClientID,
			LeaseHolder:    account.LeaseHolder,
			LeaseExpiresAt: account.LeaseExpiresAt,
		}
		evaluation := loadBalanceEligibility(account, state.QuotaCache[account.ID], now)
		entry.Eligible = evaluation.Eligible
		entry.Reason = evaluation.Reason
		entry.QuotaStatus = evaluation.QuotaStatus
		entry.QuotaRemainingDisplay = evaluation.QuotaRemainingDisplay
		entry.QuotaRemainingPercent = evaluation.QuotaRemainingPercent
		entry.QuotaUsedPercent = evaluation.QuotaUsedPercent
		entry.QuotaResetsAt = evaluation.QuotaResetsAt
		entry.QuotaUpdatedAt = evaluation.QuotaUpdatedAt
		entry.QuotaScore = evaluation.QuotaScore
		if entry.Eligible {
			status.Eligible = append(status.Eligible, entry)
		} else {
			status.Excluded = append(status.Excluded, entry)
		}
	}
	sort.Slice(status.Eligible, func(i, j int) bool {
		if !sameLoadBalanceScore(status.Eligible[i].QuotaScore, status.Eligible[j].QuotaScore) {
			return status.Eligible[i].QuotaScore > status.Eligible[j].QuotaScore
		}
		return status.Eligible[i].ID < status.Eligible[j].ID
	})
	sort.Slice(status.Excluded, func(i, j int) bool {
		return status.Excluded[i].ID < status.Excluded[j].ID
	})
	return status, nil
}

const (
	loadBalanceMinFiveHourRemaining = 5.0
	loadBalanceNearResetWindow      = 90 * time.Minute
	loadBalanceNearResetBonus       = 25.0
	loadBalanceScoreEpsilon         = 0.01
)

type loadBalanceEvaluation struct {
	Eligible              bool
	Reason                string
	QuotaStatus           quota.Status
	QuotaRemainingDisplay string
	QuotaRemainingPercent float64
	QuotaUsedPercent      float64
	QuotaResetsAt         string
	QuotaUpdatedAt        time.Time
	QuotaScore            float64
}

func loadBalanceEligibility(account AccountView, cache QuotaCache, now time.Time) loadBalanceEvaluation {
	return evaluateLoadBalanceFields(
		account.OwnerMode,
		account.Status,
		account.AuthPresent,
		account.LeaseActive,
		account.LeaseExpiresAt,
		cache,
		now,
	)
}

func loadBalanceEligibilityForAccount(account Account, authPresent bool, cache QuotaCache, now time.Time) loadBalanceEvaluation {
	return evaluateLoadBalanceFields(
		account.OwnerMode,
		account.Status,
		authPresent,
		accountLeaseActive(account, now),
		account.LeaseExpiresAt,
		cache,
		now,
	)
}

func evaluateLoadBalanceFields(ownerMode AccountOwnerMode, status AccountStatus, authPresent, leaseActive bool, leaseExpiresAt time.Time, cache QuotaCache, now time.Time) loadBalanceEvaluation {
	evaluation := loadBalanceQuotaEvaluation(cache, now)
	if ownerMode != OwnerCloud {
		evaluation.Reason = fmt.Sprintf("owner is %s", ownerMode)
		return evaluation
	}
	if status != StatusReady {
		evaluation.Reason = fmt.Sprintf("status is %s", status)
		return evaluation
	}
	if !authPresent {
		evaluation.Reason = "auth.json missing"
		return evaluation
	}
	if leaseActive {
		evaluation.Reason = fmt.Sprintf("leased until %s", leaseExpiresAt.Format(time.RFC3339))
		return evaluation
	}
	if evaluation.QuotaStatus == "" {
		evaluation.Reason = "quota not checked"
		return evaluation
	}
	if evaluation.QuotaStatus != quota.StatusSupported {
		evaluation.Reason = fmt.Sprintf("quota is %s", evaluation.QuotaStatus)
		return evaluation
	}
	if cache.FiveHour == nil {
		evaluation.Reason = "5h quota missing"
		return evaluation
	}
	resetAt := parseRFC3339(cache.FiveHour.ResetsAt)
	if resetAt.IsZero() {
		evaluation.Reason = "5h reset unknown"
		return evaluation
	}
	if !resetAt.After(now) {
		evaluation.Reason = "5h reset passed; refresh needed"
		return evaluation
	}
	if cache.FiveHour.RemainingPercent <= loadBalanceMinFiveHourRemaining {
		evaluation.Reason = fmt.Sprintf("5h quota exhausted until %s", resetAt.Format(time.RFC3339))
		return evaluation
	}
	evaluation.Eligible = true
	evaluation.Reason = ""
	evaluation.QuotaScore = loadBalanceQuotaScore(*cache.FiveHour, now)
	return evaluation
}

func loadBalanceQuotaEvaluation(cache QuotaCache, now time.Time) loadBalanceEvaluation {
	evaluation := loadBalanceEvaluation{
		QuotaStatus:    cache.Result.Status,
		QuotaUpdatedAt: cache.UpdatedAt,
	}
	if cache.FiveHour == nil {
		return evaluation
	}
	evaluation.QuotaRemainingDisplay = cache.FiveHour.RemainingDisplay
	evaluation.QuotaRemainingPercent = clampPercent(cache.FiveHour.RemainingPercent)
	evaluation.QuotaUsedPercent = clampPercent(cache.FiveHour.UsedPercent)
	evaluation.QuotaResetsAt = cache.FiveHour.ResetsAt
	return evaluation
}

func loadBalanceQuotaScore(window quota.Window, now time.Time) float64 {
	remaining := clampPercent(window.RemainingPercent)
	if remaining <= loadBalanceMinFiveHourRemaining {
		return 0
	}
	score := remaining
	resetAt := parseRFC3339(window.ResetsAt)
	if resetAt.IsZero() {
		return score
	}
	untilReset := resetAt.Sub(now)
	if untilReset <= 0 {
		return score
	}
	if untilReset < loadBalanceNearResetWindow {
		pressure := 1 - (float64(untilReset) / float64(loadBalanceNearResetWindow))
		score += pressure * loadBalanceNearResetBonus
	}
	return score
}

func loadBalanceTopGroupLen(length int, scoreAt func(int) float64) int {
	if length == 0 {
		return 0
	}
	topScore := scoreAt(0)
	out := 1
	for out < length && sameLoadBalanceScore(topScore, scoreAt(out)) {
		out++
	}
	return out
}

func sameLoadBalanceScore(a, b float64) bool {
	diff := a - b
	return diff >= -loadBalanceScoreEpsilon && diff <= loadBalanceScoreEpsilon
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func clampPercent(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

func (m *Manager) accountAuthPresent(account Account) bool {
	_, err := os.Stat(filepath.Join(account.CodexHome, authFileName))
	return err == nil
}

func accountLeaseActive(account Account, now time.Time) bool {
	return strings.TrimSpace(account.LeaseID) != "" && !account.LeaseExpiresAt.IsZero() && account.LeaseExpiresAt.After(now)
}

func leaseFromAccount(account Account) Lease {
	return Lease{
		ID:          account.LeaseID,
		AccountID:   account.ID,
		ClientID:    account.LeaseClientID,
		Holder:      account.LeaseHolder,
		Generation:  account.Generation,
		StartedAt:   account.LeaseStartedAt,
		HeartbeatAt: account.LeaseHeartbeatAt,
		ExpiresAt:   account.LeaseExpiresAt,
	}
}

const maxDispatchHistory = 200

func dispatchEventFromAccount(state State, account Account, event string, now time.Time) DispatchEvent {
	return DispatchEvent{
		ID:           dispatchEventID(account, event, now),
		LeaseID:      account.LeaseID,
		AccountID:    account.ID,
		AccountLabel: account.Label,
		ClientID:     account.LeaseClientID,
		ClientLabel:  clientLabelFromState(state, account.LeaseClientID),
		Holder:       account.LeaseHolder,
		Event:        event,
		Generation:   account.Generation,
		CreatedAt:    now,
		StartedAt:    account.LeaseStartedAt,
		ExpiresAt:    account.LeaseExpiresAt,
	}
}

func dispatchEventID(account Account, event string, now time.Time) string {
	leaseID := strings.TrimSpace(account.LeaseID)
	if leaseID != "" {
		return leaseID + ":" + event
	}
	return fmt.Sprintf("%s:%s:%d", strings.TrimSpace(account.ID), event, now.UnixNano())
}

func appendDispatchEvent(events []DispatchEvent, event DispatchEvent) []DispatchEvent {
	if strings.TrimSpace(event.ID) == "" {
		return events
	}
	next := make([]DispatchEvent, 0, minInt(len(events)+1, maxDispatchHistory))
	next = append(next, event)
	for _, existing := range events {
		if existing.ID == event.ID {
			continue
		}
		next = append(next, existing)
		if len(next) >= maxDispatchHistory {
			break
		}
	}
	return next
}

func clientLabelFromState(state State, clientID string) string {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return ""
	}
	for _, client := range state.Clients {
		if client.ID == clientID {
			return client.Label
		}
	}
	return ""
}

func clearAccountLease(account *Account) {
	account.LeaseID = ""
	account.LeaseClientID = ""
	account.LeaseHolder = ""
	account.LeaseStartedAt = time.Time{}
	account.LeaseHeartbeatAt = time.Time{}
	account.LeaseExpiresAt = time.Time{}
}

func normalizeLeaseTTL(ttl time.Duration) time.Duration {
	if ttl < 30*time.Second {
		return 90 * time.Second
	}
	if ttl > 30*time.Minute {
		return 30 * time.Minute
	}
	return ttl
}

func expireAccountLeases(state State, now time.Time) (State, []string, bool) {
	expired := []string{}
	changed := false
	for i := range state.Accounts {
		account := state.Accounts[i]
		if strings.TrimSpace(account.LeaseID) == "" {
			continue
		}
		if account.LeaseExpiresAt.IsZero() || account.LeaseExpiresAt.After(now) {
			continue
		}
		leaseID := account.LeaseID
		state.Dispatches = appendDispatchEvent(state.Dispatches, dispatchEventFromAccount(state, account, "expired", now))
		clearAccountLease(&account)
		if account.Status == StatusReady {
			account.Status = StatusRecovering
		}
		account.LastError = fmt.Sprintf("lease %s expired at %s; recovery check pending", leaseID, now.Format(time.RFC3339))
		account.UpdatedAt = now
		state.Accounts[i] = account
		expired = append(expired, account.ID)
		changed = true
	}
	return state, expired, changed
}

func findLeaseAccount(state State, accountID, leaseID string) (int, Account, error) {
	accountID = strings.TrimSpace(accountID)
	leaseID = strings.TrimSpace(leaseID)
	for i, account := range state.Accounts {
		if accountID != "" && account.ID != accountID {
			continue
		}
		if leaseID != "" && account.LeaseID != leaseID {
			continue
		}
		return i, account, nil
	}
	if accountID != "" {
		return -1, Account{}, fmt.Errorf("lease %q for account %q not found", leaseID, accountID)
	}
	return -1, Account{}, fmt.Errorf("lease %q not found", leaseID)
}

func validateLease(account Account, leaseID, clientID string, now time.Time) error {
	leaseID = strings.TrimSpace(leaseID)
	clientID = strings.TrimSpace(clientID)
	if leaseID == "" || account.LeaseID != leaseID {
		return fmt.Errorf("account %s is not held by lease %q", account.ID, leaseID)
	}
	if !accountLeaseActive(account, now) {
		return fmt.Errorf("lease %s for account %s has expired", leaseID, account.ID)
	}
	if clientID != "" && account.LeaseClientID != "" && account.LeaseClientID != clientID {
		return fmt.Errorf("lease %s belongs to client %s", leaseID, account.LeaseClientID)
	}
	return nil
}

func (m *Manager) ResetRoundRobin() error {
	if err := m.Ensure(); err != nil {
		return err
	}
	if strings.TrimSpace(m.DatabaseURL) != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		db, err := m.postgresDB(ctx)
		if err != nil {
			return err
		}
		defer db.Close()
		_, err = db.ExecContext(ctx, `DELETE FROM cube_meta WHERE key = 'round_robin_last_account_id'`)
		return err
	}
	err := os.Remove(filepath.Join(m.StateDir, roundRobinFileName))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (m *Manager) loadRoundRobinState() (roundRobinState, error) {
	if err := m.Ensure(); err != nil {
		return roundRobinState{}, err
	}
	if strings.TrimSpace(m.DatabaseURL) != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		db, err := m.postgresDB(ctx)
		if err != nil {
			return roundRobinState{}, err
		}
		defer db.Close()
		var value string
		err = db.QueryRowContext(ctx, `SELECT value FROM cube_meta WHERE key = 'round_robin_last_account_id'`).Scan(&value)
		if errors.Is(err, sql.ErrNoRows) {
			return roundRobinState{}, nil
		}
		if err != nil {
			return roundRobinState{}, err
		}
		return roundRobinState{LastAccountID: value}, nil
	}
	data, err := os.ReadFile(filepath.Join(m.StateDir, roundRobinFileName))
	if errors.Is(err, os.ErrNotExist) {
		return roundRobinState{}, nil
	}
	if err != nil {
		return roundRobinState{}, err
	}
	var state roundRobinState
	if err := json.Unmarshal(data, &state); err != nil {
		return roundRobinState{}, err
	}
	return state, nil
}

func (m *Manager) saveRoundRobinState(state roundRobinState) error {
	if err := m.Ensure(); err != nil {
		return err
	}
	if strings.TrimSpace(m.DatabaseURL) != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		db, err := m.postgresDB(ctx)
		if err != nil {
			return err
		}
		defer db.Close()
		_, err = db.ExecContext(ctx, `INSERT INTO cube_meta (key, value, updated_at)
			VALUES ('round_robin_last_account_id', $1, now())
			ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`, state.LastAccountID)
		return err
	}
	path := filepath.Join(m.StateDir, roundRobinFileName)
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func (m *Manager) FetchQuota(ctx context.Context, id string) (quota.Result, error) {
	account, err := m.GetAccount(id)
	if err != nil {
		return quota.Result{}, err
	}
	now := time.Now()
	if account.OwnerMode == OwnerClient {
		state, loadErr := m.Load()
		if loadErr == nil {
			if cache, ok := state.QuotaCache[id]; ok && cache.Result.Status != "" {
				result := cache.Result
				result.Source = quotaSourceLabel(cache)
				if cache.ReporterClientID != "" {
					result.Detail = firstNonEmpty(result.Detail, fmt.Sprintf("client-owned account; returning quota reported by %s at %s", cache.ReporterClientID, cache.UpdatedAt.Format(time.RFC3339)))
				} else {
					result.Detail = firstNonEmpty(result.Detail, fmt.Sprintf("client-owned account; returning client-reported quota from %s", cache.UpdatedAt.Format(time.RFC3339)))
				}
				return result, nil
			}
		}
		return quota.Result{
			Status: quota.StatusNotConfigured,
			Source: "client report",
			Detail: "client-owned account; waiting for local cube report",
		}, nil
	}
	if accountLeaseActive(account, now) {
		state, loadErr := m.Load()
		if loadErr == nil {
			if cache, ok := state.QuotaCache[id]; ok && cache.Result.Status != "" {
				result := cache.Result
				result.Source = quotaSourceLabel(cache)
				result.Detail = firstNonEmpty(result.Detail, fmt.Sprintf("account is leased by %s until %s; returning cached quota", account.LeaseClientID, account.LeaseExpiresAt.Format(time.RFC3339)))
				return result, nil
			}
		}
		return quota.Result{
			Status: quota.StatusError,
			Source: "cube lease",
			Detail: fmt.Sprintf("account is leased by %s until %s; quota refresh is paused", account.LeaseClientID, account.LeaseExpiresAt.Format(time.RFC3339)),
		}, nil
	}
	_ = m.syncLiveAuthToManaged(account)
	authPath := filepath.Join(account.CodexHome, authFileName)
	beforeDigest := fileDigest(authPath)
	result, err := quota.FetchForCodexHome(ctx, account.CodexHome, now)
	afterDigest := fileDigest(authPath)
	authChanged := beforeDigest != "" && afterDigest != "" && beforeDigest != afterDigest
	_ = m.recordQuotaResult(id, result, authChanged, QuotaSourceCloud, "")
	return result, err
}

func (m *Manager) RecordQuotaReport(id string, result quota.Result, clientID string) error {
	return m.recordQuotaResult(id, result, false, QuotaSourceClient, strings.TrimSpace(clientID))
}

func (m *Manager) recordQuotaResult(id string, result quota.Result, authChanged bool, source QuotaSource, reporterClientID string) error {
	if strings.TrimSpace(id) == "" {
		return nil
	}
	if strings.TrimSpace(m.DatabaseURL) != "" {
		return m.recordPostgresQuotaResult(id, result, authChanged, source, strings.TrimSpace(reporterClientID))
	}
	state, err := m.Load()
	if err != nil {
		return err
	}
	if state.QuotaCache == nil {
		state.QuotaCache = map[string]QuotaCache{}
	}
	fiveHour := quotaFiveHour(result)
	if source == "" {
		source = QuotaSourceCloud
	}
	state.QuotaCache[id] = QuotaCache{
		AccountID:        id,
		UpdatedAt:        time.Now(),
		Result:           result,
		FiveHour:         fiveHour,
		Source:           source,
		ReporterClientID: strings.TrimSpace(reporterClientID),
	}
	if source == QuotaSourceClient {
		for i := range state.Accounts {
			if state.Accounts[i].ID != id {
				continue
			}
			state.Accounts[i].OwnerMode = OwnerClient
			if strings.TrimSpace(reporterClientID) != "" {
				state.Accounts[i].OwnerClientID = strings.TrimSpace(reporterClientID)
			}
			state.Accounts[i].UpdatedAt = time.Now()
			break
		}
	}
	if result.Status == quota.StatusRefreshInvalid {
		for i := range state.Accounts {
			if state.Accounts[i].ID != id {
				continue
			}
			if state.Accounts[i].Status == StatusReady || state.Accounts[i].Status == StatusRecovering {
				state.Accounts[i].Status = StatusDrain
			}
			state.Accounts[i].LastError = result.Detail
			state.Accounts[i].UpdatedAt = time.Now()
			break
		}
	} else if result.Status == quota.StatusSupported {
		for i := range state.Accounts {
			if state.Accounts[i].ID != id {
				continue
			}
			if strings.TrimSpace(result.Plan) != "" {
				state.Accounts[i].Plan = result.Plan
			}
			if state.Accounts[i].Status == StatusRecovering {
				state.Accounts[i].Status = StatusReady
			}
			state.Accounts[i].LastError = ""
			if authChanged {
				state.Accounts[i].Generation++
			}
			state.Accounts[i].UpdatedAt = time.Now()
			break
		}
	} else if authChanged {
		for i := range state.Accounts {
			if state.Accounts[i].ID != id {
				continue
			}
			state.Accounts[i].Generation++
			state.Accounts[i].UpdatedAt = time.Now()
			break
		}
	}
	return m.Save(state)
}

func (m *Manager) recordPostgresQuotaResult(id string, result quota.Result, authChanged bool, source QuotaSource, reporterClientID string) error {
	if source == "" {
		source = QuotaSourceCloud
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := m.postgresDB(ctx)
	if err != nil {
		return err
	}
	defer db.Close()
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

	if source == QuotaSourceClient {
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

func quotaFiveHour(result quota.Result) *quota.Window {
	for _, window := range result.Quotas {
		if window.Key == "five_hour" || strings.EqualFold(window.Label, "5h") {
			copy := window
			return &copy
		}
	}
	return nil
}

func quotaSourceLabel(cache QuotaCache) string {
	switch cache.Source {
	case QuotaSourceClient:
		if cache.ReporterClientID != "" {
			return "client report:" + cache.ReporterClientID
		}
		return "client report"
	case QuotaSourceCloud:
		return "cloud refresh"
	default:
		return string(cache.Source)
	}
}

func (m *Manager) syncLiveAuthToManaged(account Account) error {
	liveAuth := filepath.Join(m.LiveCodexHome, authFileName)
	managedAuth := filepath.Join(account.CodexHome, authFileName)
	if samePath(liveAuth, managedAuth) {
		return nil
	}

	liveInfo, err := os.Stat(liveAuth)
	if err != nil {
		return nil
	}
	managedInfo, err := os.Stat(managedAuth)
	if err != nil {
		return nil
	}
	if !liveInfo.ModTime().After(managedInfo.ModTime()) {
		return nil
	}

	liveIdentity := authIdentity(readAuthMetadata(liveAuth))
	managedIdentity := authIdentity(readAuthMetadata(managedAuth))
	if liveIdentity == "" || liveIdentity != managedIdentity {
		return nil
	}
	return copyFile(liveAuth, managedAuth, fileModeFor(authFileName))
}

func (m *Manager) FetchUsage(id string) (usage.Summary, error) {
	account, err := m.GetAccount(id)
	if err != nil {
		return usage.Summary{}, err
	}
	return usage.SummarizeCodexHome(account.CodexHome, time.Now()), nil
}

func (m *Manager) ensureLocalConfigLink(codexHome string) error {
	if strings.TrimSpace(codexHome) == "" {
		return nil
	}
	source := CodexConfigPath(m.LiveCodexHome)
	target := filepath.Join(codexHome, configFileName)
	if samePath(source, target) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(source), 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		return err
	}
	if _, err := os.Stat(source); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(source, []byte{}, fileModeFor(configFileName)); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	info, err := os.Lstat(target)
	if err == nil {
		if samePath(target, source) {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			if err := os.Remove(target); err != nil {
				return err
			}
		} else {
			backup, err := nextBackupPath(target)
			if err != nil {
				return err
			}
			if err := os.Rename(target, backup); err != nil {
				return err
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Symlink(source, target)
}

func nextBackupPath(path string) (string, error) {
	base := path + ".cube20.bak"
	if _, err := os.Lstat(base); errors.Is(err, os.ErrNotExist) {
		return base, nil
	} else if err != nil {
		return "", err
	}
	for i := 2; i < 1000; i++ {
		candidate := fmt.Sprintf("%s.%d", base, i)
		if _, err := os.Lstat(candidate); errors.Is(err, os.ErrNotExist) {
			return candidate, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("could not find backup path for %s", path)
}

func (m *Manager) DeployProfile(id, targetCodexHome string) ([]string, error) {
	account, err := m.GetAccount(id)
	if err != nil {
		return nil, err
	}

	if strings.TrimSpace(targetCodexHome) == "" {
		targetCodexHome = m.LiveCodexHome
	}
	if strings.TrimSpace(targetCodexHome) == "" {
		targetCodexHome, err = defaultCodexHome()
		if err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(targetCodexHome, 0o700); err != nil {
		return nil, err
	}

	written := []string{}
	authSource := filepath.Join(account.CodexHome, authFileName)
	if _, err := os.Stat(authSource); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("account %q has no auth.json", id)
		}
		return nil, err
	}
	authTarget := filepath.Join(targetCodexHome, authFileName)
	if err := copyFileWithBackup(authSource, authTarget, fileModeFor(authFileName)); err != nil {
		return nil, err
	}
	written = append(written, authTarget)

	return written, nil
}

func (m *Manager) DeployAuth(id, targetCodexHome string) (string, error) {
	written, err := m.DeployProfile(id, targetCodexHome)
	if err != nil {
		return "", err
	}
	return strings.Join(written, ", "), nil
}

func (m *Manager) accountView(account Account) AccountView {
	authPath := filepath.Join(account.CodexHome, authFileName)
	configPath := CodexConfigPath(m.LiveCodexHome)
	view := AccountView{
		Account:    account,
		AuthPath:   authPath,
		ConfigPath: configPath,
	}

	info, err := os.Stat(authPath)
	if err == nil {
		view.AuthPresent = true
		view.AuthUpdated = info.ModTime()
	}
	if info, err := os.Stat(configPath); err == nil {
		view.ConfigPresent = true
		view.ConfigUpdated = info.ModTime()
	}
	view.Active = m.isAccountActive(account)
	view.LeaseActive = accountLeaseActive(account, time.Now())
	return view
}

func (m *Manager) isAccountActive(account Account) bool {
	liveAuth := filepath.Join(m.LiveCodexHome, authFileName)
	accAuth := filepath.Join(account.CodexHome, authFileName)
	liveData, err1 := os.ReadFile(liveAuth)
	accData, err2 := os.ReadFile(accAuth)
	if err1 != nil || err2 != nil {
		return false
	}
	var liveMap, accMap map[string]any
	_ = json.Unmarshal(liveData, &liveMap)
	_ = json.Unmarshal(accData, &accMap)
	id1 := authIdentity(liveMap)
	id2 := authIdentity(accMap)
	return id1 != "" && id1 == id2
}

func defaultCodexHome() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if value := strings.TrimSpace(os.Getenv("CODEX_HOME")); value != "" {
		return expandPath(value, home), nil
	}
	return filepath.Join(home, ".codex"), nil
}

func CodexConfigPath(codexHome string) string {
	codexHome = strings.TrimSpace(codexHome)
	if codexHome == "" {
		if value, err := defaultCodexHome(); err == nil {
			codexHome = value
		}
	}
	return filepath.Join(codexHome, configFileName)
}

func defaultSettings(home string) Settings {
	liveCodexHome := filepath.Join(home, ".codex")
	if value := strings.TrimSpace(os.Getenv("CODEX_HOME")); value != "" {
		liveCodexHome = expandPath(value, home)
	}
	return Settings{
		LiveCodexHome: liveCodexHome,
		AccountsDir:   filepath.Join(home, defaultAccountsDirName),
		CloudURL:      strings.TrimSpace(os.Getenv("CUBE_CLOUD_URL")),
		CloudToken:    strings.TrimSpace(os.Getenv("CUBE_CLOUD_TOKEN")),
		DatabaseURL:   firstNonEmpty(os.Getenv("CUBE_DATABASE_URL"), os.Getenv("DATABASE_URL")),
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func loadSettings(path string, defaults Settings, home string) (Settings, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := writeSettings(path, defaults); err != nil {
			return Settings{}, err
		}
		return defaults, nil
	}
	if err != nil {
		return Settings{}, err
	}

	settings, changed, err := parseSettingsData(data, defaults, home)
	if err != nil {
		return Settings{}, err
	}
	if changed {
		if err := writeSettings(path, settings); err != nil {
			return Settings{}, err
		}
	}
	return settings, nil
}

func parseSettingsData(data []byte, defaults Settings, home string) (Settings, bool, error) {
	settings := defaults
	if err := toml.Unmarshal(data, &settings); err != nil {
		return Settings{}, false, err
	}

	rawText := string(data)
	changed := strings.Contains(rawText, "shared_settings_path") || strings.Contains(rawText, "shared_config_path")

	settings.LiveCodexHome = expandPath(settings.LiveCodexHome, home)
	settings.AccountsDir = expandPath(settings.AccountsDir, home)
	settings.CloudURL = strings.TrimSpace(settings.CloudURL)
	settings.CloudToken = strings.TrimSpace(settings.CloudToken)
	settings.DatabaseURL = strings.TrimSpace(settings.DatabaseURL)
	return settings, changed, nil
}

func writeSettings(path string, settings Settings) error {
	data, err := toml.Marshal(settings)
	if err != nil {
		return err
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func (m *Manager) acquireLock(lockPath string) (func(), error) {
	if strings.TrimSpace(m.DatabaseURL) != "" {
		return m.acquirePostgresLock(filepath.Base(lockPath))
	}
	start := time.Now()
	for {
		f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			f.Close()
			return func() {
				_ = os.Remove(lockPath)
			}, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		if time.Since(start) > 2*time.Second {
			return nil, errors.New("timeout acquiring lock for round-robin selector")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (m *Manager) acquirePostgresLock(name string) (func(), error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	db, err := m.postgresDB(ctx)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	for {
		var locked bool
		err := db.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtext($1))`, name).Scan(&locked)
		if err != nil {
			_ = db.Close()
			return nil, err
		}
		if locked {
			return func() {
				unlockCtx, unlockCancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer unlockCancel()
				_, _ = db.ExecContext(unlockCtx, `SELECT pg_advisory_unlock(hashtext($1))`, name)
				_ = db.Close()
			}, nil
		}
		if time.Since(start) > 5*time.Second {
			_ = db.Close()
			return nil, fmt.Errorf("timeout acquiring postgres lock %s", name)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func expandPath(value, home string) string {
	value = strings.TrimSpace(value)
	if value == "~" {
		return home
	}
	if strings.HasPrefix(value, "~/") {
		return filepath.Join(home, value[2:])
	}
	return filepath.Clean(value)
}

func fileModeFor(fileName string) os.FileMode {
	if fileName == authFileName {
		return 0o600
	}
	return 0o600
}

func copyFile(source, target string, mode os.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

func copyFileWithBackup(source, target string, mode os.FileMode) error {
	if samePath(source, target) {
		return nil
	}
	if _, err := os.Stat(target); err == nil {
		backup := target + ".backup-" + time.Now().Format("20060102-150405")
		if err := copyFile(target, backup, mode); err != nil {
			return fmt.Errorf("backup existing %s: %w", filepath.Base(target), err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return copyFile(source, target, mode)
}

func withEnv(env []string, key, value string) []string {
	prefix := key + "="
	next := make([]string, 0, len(env)+1)
	replaced := false
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			next = append(next, prefix+value)
			replaced = true
		} else {
			next = append(next, item)
		}
	}
	if !replaced {
		next = append(next, prefix+value)
	}
	return next
}
