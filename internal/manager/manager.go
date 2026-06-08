package manager

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"cube20/internal/quota"
	"cube20/internal/usage"

	_ "github.com/lib/pq"
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

const swapRemainingThreshold = 10.0 // 5h remaining-% below this suggests swapping accounts

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

	// stateMu serializes file-mode state mutations within this process. The
	// concurrent HTTP server can drive many exported mutators at once, and each
	// does a Load->mutate->Save against the whole state.json; without this lock
	// two mutators clobber each other (lost update / corrupt rename). stateMu is
	// the OUTERMOST lock: a method that also needs the cross-process file lock
	// (acquireLock) MUST take stateMu first, then acquireLock — never the
	// reverse — to avoid lock-order inversion. Postgres mode does not use this
	// mutex (row-level SQL is already atomic); it guards file-mode paths only.
	stateMu sync.Mutex

	// dbMu guards the lazily-opened, process-level Postgres connection pool.
	// File-mode managers never open it, so db stays nil.
	dbMu sync.Mutex
	db   *sql.DB
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

// postgresDB returns the process-level connection pool, opening it once on
// first use. database/sql.DB is itself a concurrency-safe pool meant to be
// long-lived, so every caller shares this one and must NOT call Close on it;
// the pool is released exactly once via Manager.Close on server shutdown.
func (m *Manager) postgresDB(ctx context.Context) (*sql.DB, error) {
	databaseURL := strings.TrimSpace(m.DatabaseURL)
	if databaseURL == "" {
		return nil, errors.New("database_url is not configured")
	}

	m.dbMu.Lock()
	defer m.dbMu.Unlock()
	if m.db != nil {
		return m.db, nil
	}
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxIdleTime(5 * time.Minute)
	db.SetConnMaxLifetime(30 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	m.db = db
	return m.db, nil
}

// Close releases the process-level Postgres connection pool, if one was opened.
// It is safe to call on a file-mode manager (no-op) and safe to call more than
// once. The long-running dashboard server calls this on shutdown.
func (m *Manager) Close() error {
	m.dbMu.Lock()
	defer m.dbMu.Unlock()
	if m.db == nil {
		return nil
	}
	err := m.db.Close()
	m.db = nil
	return err
}

const (
	loadBalanceMinFiveHourRemaining = 5.0
	loadBalanceNearResetWindow      = 90 * time.Minute
	loadBalanceNearResetBonus       = 25.0
	loadBalanceScoreEpsilon         = 0.01
)

const maxDispatchHistory = 200

// secretFileMode is the permission for cube-managed files. Both auth.json
// (OAuth/API secrets) and config.toml are owner-only local state.
const secretFileMode os.FileMode = 0o600
