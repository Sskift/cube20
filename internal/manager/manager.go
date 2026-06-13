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
	WorkspaceID      string           `json:"workspaceId"`
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
	AuthPresent   bool         `json:"authPresent"`
	AuthPath      string       `json:"authPath"`
	AuthUpdated   time.Time    `json:"authUpdated,omitempty"`
	ConfigPresent bool         `json:"configPresent"`
	ConfigPath    string       `json:"configPath"`
	ConfigUpdated time.Time    `json:"configUpdated,omitempty"`
	Active        bool         `json:"active"`
	LeaseActive   bool         `json:"leaseActive"`
	LeaseKind     string       `json:"leaseKind,omitempty"`
	RuntimeState  RuntimeState `json:"runtimeState,omitempty"`
	RuntimeReason string       `json:"runtimeReason,omitempty"`
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
	Version     int               `json:"version"`
	Accounts    []Account         `json:"accounts"`
	Clients     []Client          `json:"clients,omitempty"`
	Users       []User            `json:"users,omitempty"`
	Sessions    []Session         `json:"sessions,omitempty"`
	Workspaces  []Workspace       `json:"workspaces,omitempty"`
	Memberships []Membership      `json:"memberships,omitempty"`
	Invites     []WorkspaceInvite `json:"invites,omitempty"`
	// WorkspaceMigrated records that the one-time legacy flat-pool migration has
	// already run, so it never re-enrolls clients created after the upgrade. It
	// is set the first time migrateDefaultWorkspace runs and persisted alongside
	// the rest of the state (a row in cube_meta on Postgres).
	WorkspaceMigrated bool `json:"workspaceMigrated,omitempty"`
	// UserDeviceMigrated records that the one-time Client->User+Device migration
	// has run, so existing devices created after the upgrade are not retro-fitted
	// with synthetic users.
	UserDeviceMigrated bool                    `json:"userDeviceMigrated,omitempty"`
	Usage              map[string]AccountUsage `json:"usage,omitempty"`
	QuotaCache         map[string]QuotaCache   `json:"quotaCache,omitempty"`
	Dispatches         []DispatchEvent         `json:"dispatches,omitempty"`
}

type Client struct {
	ID         string     `json:"id"`
	UserID     string     `json:"userId,omitempty"`
	Label      string     `json:"label"`
	TokenHash  string     `json:"tokenHash,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastSeenAt time.Time  `json:"lastSeenAt,omitempty"`
	RevokedAt  *time.Time `json:"revokedAt,omitempty"`
}

// Device is the new name for a Client: the per-machine bearer-token holder. The
// underlying struct/table is reused unchanged (plus UserID) so the existing
// token lifecycle keeps working verbatim during and after migration.
type Device = Client

// User is a website identity: a username + password. A user owns one or more
// devices. There is intentionally no email or other PII — this is a small
// internal tool.
type User struct {
	ID           string     `json:"id"`
	Username     string     `json:"username"`
	PasswordHash string     `json:"passwordHash,omitempty"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`
	LastLoginAt  time.Time  `json:"lastLoginAt,omitempty"`
	DisabledAt   *time.Time `json:"disabledAt,omitempty"`
}

// UserView is the secret-free projection of a User for API responses.
type UserView struct {
	ID          string    `json:"id"`
	Username    string    `json:"username"`
	CreatedAt   time.Time `json:"createdAt"`
	LastLoginAt time.Time `json:"lastLoginAt,omitempty"`
	Disabled    bool      `json:"disabled"`
	DeviceCount int       `json:"deviceCount"`
}

// Session is a server-stored website session. The cookie carries an opaque
// token; only its sha256 hash is persisted so a session can be revoked by
// deleting the row.
type Session struct {
	ID         string    `json:"id"`
	TokenHash  string    `json:"tokenHash,omitempty"`
	UserID     string    `json:"userId"`
	CreatedAt  time.Time `json:"createdAt"`
	ExpiresAt  time.Time `json:"expiresAt"`
	LastSeenAt time.Time `json:"lastSeenAt,omitempty"`
}

type ClientView struct {
	ID         string     `json:"id"`
	UserID     string     `json:"userId,omitempty"`
	Label      string     `json:"label"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastSeenAt time.Time  `json:"lastSeenAt,omitempty"`
	RevokedAt  *time.Time `json:"revokedAt,omitempty"`
	Active     bool       `json:"active"`
}

// DeviceView is the device-facing alias of ClientView.
type DeviceView = ClientView

// WorkspaceRole is a member's role within a single workspace. Roles are stored
// per-membership, so the same client can be an admin of one workspace and a
// plain member of another.
type WorkspaceRole string

const (
	RoleAdmin  WorkspaceRole = "admin"
	RoleMember WorkspaceRole = "member"
)

// DefaultWorkspaceID is the pool every pre-workspace account and client is
// migrated into. Requests that omit a workspace resolve here, so old clients
// that predate the multi-tenant change keep working unchanged.
const DefaultWorkspaceID = "default"

// Workspace is an isolated account pool. Accounts belong to exactly one
// workspace; load balancing, leases, and quota views are all scoped to it.
type Workspace struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedBy string    `json:"createdBy,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Membership links a client to a workspace with a role (many-to-many). A client
// may hold memberships in several workspaces at once.
type Membership struct {
	WorkspaceID string        `json:"workspaceId"`
	UserID      string        `json:"userId,omitempty"`
	Username    string        `json:"username,omitempty"`
	ClientID    string        `json:"clientId,omitempty"`
	ClientLabel string        `json:"clientLabel,omitempty"`
	Role        WorkspaceRole `json:"role"`
	CreatedAt   time.Time     `json:"createdAt"`
}

// WorkspaceInvite is a reusable link that lets a new or existing website user
// join a workspace. TokenHash stores sha256(token); the plaintext token is
// returned once at creation time and never persisted.
type WorkspaceInvite struct {
	ID          string        `json:"id"`
	WorkspaceID string        `json:"workspaceId"`
	Role        WorkspaceRole `json:"role"`
	TokenHash   string        `json:"tokenHash,omitempty"`
	CreatedBy   string        `json:"createdBy,omitempty"`
	CreatedAt   time.Time     `json:"createdAt"`
	ExpiresAt   time.Time     `json:"expiresAt"`
	RevokedAt   *time.Time    `json:"revokedAt,omitempty"`
	UsedCount   int           `json:"usedCount"`
	LastUsedAt  time.Time     `json:"lastUsedAt,omitempty"`
}

type WorkspaceInviteView struct {
	ID            string        `json:"id"`
	WorkspaceID   string        `json:"workspaceId"`
	WorkspaceName string        `json:"workspaceName,omitempty"`
	Role          WorkspaceRole `json:"role"`
	TokenHash     string        `json:"-"`
	CreatedBy     string        `json:"createdBy,omitempty"`
	CreatedAt     time.Time     `json:"createdAt"`
	ExpiresAt     time.Time     `json:"expiresAt"`
	RevokedAt     *time.Time    `json:"revokedAt,omitempty"`
	UsedCount     int           `json:"usedCount"`
	LastUsedAt    time.Time     `json:"lastUsedAt,omitempty"`
	Valid         bool          `json:"valid"`
}

type WorkspaceInviteCreated struct {
	Invite WorkspaceInviteView `json:"invite"`
	Token  string              `json:"token"`
	URL    string              `json:"url,omitempty"`
}

type InvitePreview struct {
	Valid         bool          `json:"valid"`
	WorkspaceID   string        `json:"workspaceId"`
	WorkspaceName string        `json:"workspaceName"`
	Role          WorkspaceRole `json:"role"`
	ExpiresAt     time.Time     `json:"expiresAt"`
}

type DispatchEvent struct {
	ID           string    `json:"id"`
	LeaseID      string    `json:"leaseId"`
	AccountID    string    `json:"accountId"`
	AccountLabel string    `json:"accountLabel,omitempty"`
	ClientID     string    `json:"clientId,omitempty"`
	ClientLabel  string    `json:"clientLabel,omitempty"`
	UserID       string    `json:"userId,omitempty"`
	Username     string    `json:"username,omitempty"`
	DeviceID     string    `json:"deviceId,omitempty"`
	DeviceLabel  string    `json:"deviceLabel,omitempty"`
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
	AccountID                string           `json:"accountId"`
	Label                    string           `json:"label"`
	Status                   AccountStatus    `json:"status"`
	AuthPresent              bool             `json:"authPresent"`
	UpdatedAt                time.Time        `json:"updatedAt,omitempty"`
	ResetsAt                 string           `json:"resetsAt,omitempty"`
	RemainingDisplay         string           `json:"remainingDisplay,omitempty"`
	RemainingPercent         float64          `json:"remainingPercent,omitempty"`
	UsedPercent              float64          `json:"usedPercent,omitempty"`
	FiveHourResetsAt         string           `json:"fiveHourResetsAt,omitempty"`
	FiveHourRemainingDisplay string           `json:"fiveHourRemainingDisplay,omitempty"`
	FiveHourRemainingPercent float64          `json:"fiveHourRemainingPercent,omitempty"`
	FiveHourUsedPercent      float64          `json:"fiveHourUsedPercent,omitempty"`
	SevenDayResetsAt         string           `json:"sevenDayResetsAt,omitempty"`
	SevenDayRemainingDisplay string           `json:"sevenDayRemainingDisplay,omitempty"`
	SevenDayRemainingPercent float64          `json:"sevenDayRemainingPercent,omitempty"`
	SevenDayUsedPercent      float64          `json:"sevenDayUsedPercent,omitempty"`
	BindingWindow            string           `json:"bindingWindow,omitempty"`
	QuotaStatus              quota.Status     `json:"quotaStatus,omitempty"`
	RefreshOrderReason       string           `json:"refreshOrderReason,omitempty"`
	OwnerMode                AccountOwnerMode `json:"ownerMode,omitempty"`
	OwnerClientID            string           `json:"ownerClientId,omitempty"`
	QuotaSource              QuotaSource      `json:"quotaSource,omitempty"`
	QuotaReporterClientID    string           `json:"quotaReporterClientId,omitempty"`
	LeaseActive              bool             `json:"leaseActive,omitempty"`
	LeaseClientID            string           `json:"leaseClientId,omitempty"`
	LeaseExpiresAt           time.Time        `json:"leaseExpiresAt,omitempty"`
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

	quotaFetcher func(context.Context, string, time.Time) (quota.Result, error)
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
		if strings.TrimSpace(state.Accounts[i].WorkspaceID) == "" {
			state.Accounts[i].WorkspaceID = DefaultWorkspaceID
		}
		if state.Accounts[i].Generation < 0 {
			state.Accounts[i].Generation = 0
		}
	}
	if state.Clients == nil {
		state.Clients = []Client{}
	}
	if state.Users == nil {
		state.Users = []User{}
	}
	if state.Sessions == nil {
		state.Sessions = []Session{}
	}
	for i := range state.Users {
		state.Users[i].Username = strings.ToLower(strings.TrimSpace(state.Users[i].Username))
	}
	if state.Workspaces == nil {
		state.Workspaces = []Workspace{}
	}
	if state.Memberships == nil {
		state.Memberships = []Membership{}
	}
	if state.Invites == nil {
		state.Invites = []WorkspaceInvite{}
	}
	migrateDefaultWorkspace(&state)
	migrateUsersAndDevices(&state)
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

func validWorkspaceRole(role WorkspaceRole) bool {
	switch role {
	case RoleAdmin, RoleMember:
		return true
	default:
		return false
	}
}

// workspaceOrDefault normalizes a possibly-empty workspace id to the default
// pool, so a row never persists with an empty workspace_id.
func workspaceOrDefault(id string) string {
	if strings.TrimSpace(id) == "" {
		return DefaultWorkspaceID
	}
	return id
}

// migrateDefaultWorkspace makes a pre-workspace state self-consistent without
// capturing accounts/clients created after the upgrade. Two concerns:
//
//	(A) Always ensure the default workspace ROW exists when any account lives in
//	    it — idempotent, safe on every Load.
//	(B) ONE-TIME enroll of pre-workspace clients into the default pool. This runs
//	    only while state.WorkspaceMigrated is false AND the raw state has the
//	    legacy flat-pool signature (no workspaces, has accounts). The flag is then
//	    set and persisted, so enrollment never re-fires for clients created after
//	    the upgrade — even in the window before the first account exists.
func migrateDefaultWorkspace(state *State) {
	legacyFlatPool := !state.WorkspaceMigrated && len(state.Workspaces) == 0 && len(state.Accounts) > 0

	// (A) Default workspace row for any account homed in it. Account workspace
	// ids were already defaulted to DefaultWorkspaceID by normalizeState.
	accountInDefault := false
	for i := range state.Accounts {
		if state.Accounts[i].WorkspaceID == DefaultWorkspaceID {
			accountInDefault = true
			break
		}
	}
	if (accountInDefault || legacyFlatPool) && !hasWorkspace(state.Workspaces, DefaultWorkspaceID) {
		now := time.Now()
		state.Workspaces = append(state.Workspaces, Workspace{
			ID:        DefaultWorkspaceID,
			Name:      "Default",
			CreatedAt: now,
			UpdatedAt: now,
		})
	}

	// (B) One-time legacy client enrollment.
	if !legacyFlatPool {
		return
	}
	existing := make(map[string]bool, len(state.Memberships))
	for _, m := range state.Memberships {
		existing[m.WorkspaceID+"\x00"+m.ClientID] = true
	}
	now := time.Now()
	for _, client := range state.Clients {
		key := DefaultWorkspaceID + "\x00" + client.ID
		if existing[key] {
			continue
		}
		state.Memberships = append(state.Memberships, Membership{
			WorkspaceID: DefaultWorkspaceID,
			ClientID:    client.ID,
			Role:        RoleMember,
			CreatedAt:   now,
		})
		existing[key] = true
	}
	state.WorkspaceMigrated = true
}

func hasWorkspace(workspaces []Workspace, id string) bool {
	for i := range workspaces {
		if workspaces[i].ID == id {
			return true
		}
	}
	return false
}

// migrateUsersAndDevices is the one-time Client->User+Device migration. It runs
// only when there are legacy clients but no users yet, gated by the
// UserDeviceMigrated marker so devices created after the upgrade are never
// retro-fitted with synthetic users. Each legacy client (which already IS the
// device row) gets a synthesized User whose username derives from the client
// label/id, the client's UserID is pointed at it, and any membership for that
// client is given the same UserID. Token hashes are never touched, so existing
// CLIs keep authenticating unchanged. Passwords are empty (login disabled) until
// the user sets one through the website.
func migrateUsersAndDevices(state *State) {
	if state.UserDeviceMigrated {
		return
	}
	if len(state.Users) > 0 || len(state.Clients) == 0 {
		state.UserDeviceMigrated = true
		return
	}

	usedUserIDs := map[string]bool{}
	usedUsernames := map[string]bool{}
	now := time.Now()
	// One user per distinct legacy client. (In practice the live deployment has a
	// single client; this handles N defensively.)
	for i := range state.Clients {
		if strings.TrimSpace(state.Clients[i].UserID) != "" {
			continue
		}
		base := strings.TrimSpace(state.Clients[i].Label)
		if base == "" {
			base = state.Clients[i].ID
		}
		username := strings.ToLower(sanitizeAccountID(base))
		if username == "" {
			username = "user"
		}
		username = uniqueFromUsed(username, usedUsernames)
		usedUsernames[username] = true

		userID := uniqueFromUsed("user-"+username, usedUserIDs)
		usedUserIDs[userID] = true

		state.Users = append(state.Users, User{
			ID:        userID,
			Username:  username,
			CreatedAt: state.Clients[i].CreatedAt,
			UpdatedAt: now,
		})
		state.Clients[i].UserID = userID

		for j := range state.Memberships {
			if state.Memberships[j].ClientID == state.Clients[i].ID && strings.TrimSpace(state.Memberships[j].UserID) == "" {
				state.Memberships[j].UserID = userID
			}
		}
	}
	state.UserDeviceMigrated = true
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
