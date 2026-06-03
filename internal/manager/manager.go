package manager

import (
	"context"
	"crypto/sha256"
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

	"github.com/pelletier/go-toml/v2"
)

const (
	defaultStateDirName    = ".cube20"
	defaultAccountsDirName = ".codex-accounts"
	settingsFileName       = "settings.toml"
	sharedSettingsFileName = "shared-settings.toml"
	roundRobinFileName     = "run-round-robin.json"
	authFileName           = "auth.json"
	configFileName         = "config.toml"
)

var accountIDPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

type AccountStatus string

const (
	StatusReady    AccountStatus = "ready"
	StatusDrain    AccountStatus = "drain"
	StatusDisabled AccountStatus = "disabled"
)

type Account struct {
	ID        string        `json:"id"`
	Label     string        `json:"label"`
	Plan      string        `json:"plan"`
	Status    AccountStatus `json:"status"`
	CodexHome string        `json:"codexHome"`
	CreatedAt time.Time     `json:"createdAt"`
	UpdatedAt time.Time     `json:"updatedAt"`
	LastError string        `json:"lastError,omitempty"`
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
}

type State struct {
	Version  int       `json:"version"`
	Accounts []Account `json:"accounts"`
}

type roundRobinState struct {
	LastAccountID string `json:"lastAccountId"`
}

type LoadBalanceAccount struct {
	ID            string        `json:"id"`
	Label         string        `json:"label"`
	Status        AccountStatus `json:"status"`
	AuthPresent   bool          `json:"authPresent"`
	ConfigPresent bool          `json:"configPresent"`
	Active        bool          `json:"active"`
	CodexHome     string        `json:"codexHome"`
	Eligible      bool          `json:"eligible"`
	Reason        string        `json:"reason,omitempty"`
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
	SharedConfigPath string `json:"sharedConfigPath" toml:"shared_settings_path"`
}

type JSONProfile struct {
	ID     string          `json:"id"`
	Label  string          `json:"label"`
	Auth   json.RawMessage `json:"auth"`
	Config string          `json:"config"`
}

type Manager struct {
	StateDir         string
	StatePath        string
	SettingsPath     string
	AccountsDir      string
	LiveCodexHome    string
	SharedConfigPath string
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

	return &Manager{
		StateDir:         stateDir,
		StatePath:        filepath.Join(stateDir, "state.json"),
		SettingsPath:     settingsPath,
		AccountsDir:      settings.AccountsDir,
		LiveCodexHome:    settings.LiveCodexHome,
		SharedConfigPath: settings.SharedConfigPath,
	}, nil
}

func (m *Manager) Ensure() error {
	if err := os.MkdirAll(m.StateDir, 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(m.AccountsDir, 0o700); err != nil {
		return err
	}
	if strings.TrimSpace(m.SharedConfigPath) != "" {
		if err := os.MkdirAll(filepath.Dir(m.SharedConfigPath), 0o700); err != nil {
			return err
		}
		_ = m.syncSharedConfigFromCodexHome(m.LiveCodexHome, false)
	}
	return nil
}

func (m *Manager) Load() (State, error) {
	if err := m.Ensure(); err != nil {
		return State{}, err
	}

	data, err := os.ReadFile(m.StatePath)
	if errors.Is(err, os.ErrNotExist) {
		return State{Version: 1, Accounts: []Account{}}, nil
	}
	if err != nil {
		return State{}, err
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, err
	}
	if state.Version == 0 {
		state.Version = 1
	}
	return state, nil
}

func (m *Manager) Save(state State) error {
	if err := m.Ensure(); err != nil {
		return err
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

func (m *Manager) UpdateSettings(liveCodexHome, accountsDir, sharedConfigPath string) (Settings, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Settings{}, err
	}

	settings := Settings{
		LiveCodexHome:    m.LiveCodexHome,
		AccountsDir:      m.AccountsDir,
		SharedConfigPath: m.SharedConfigPath,
	}
	if strings.TrimSpace(liveCodexHome) != "" {
		settings.LiveCodexHome = expandPath(liveCodexHome, home)
	}
	if strings.TrimSpace(accountsDir) != "" {
		settings.AccountsDir = expandPath(accountsDir, home)
	}
	if strings.TrimSpace(sharedConfigPath) != "" {
		settings.SharedConfigPath = expandPath(sharedConfigPath, home)
	}
	if settings.LiveCodexHome == "" || settings.AccountsDir == "" || settings.SharedConfigPath == "" {
		return Settings{}, errors.New("settings paths cannot be empty")
	}

	if err := writeSettings(m.SettingsPath, settings); err != nil {
		return Settings{}, err
	}
	m.LiveCodexHome = settings.LiveCodexHome
	m.AccountsDir = settings.AccountsDir
	m.SharedConfigPath = settings.SharedConfigPath
	if err := m.Ensure(); err != nil {
		return Settings{}, err
	}
	return settings, nil
}

func (m *Manager) ReadSettingsText() (string, error) {
	if err := m.Ensure(); err != nil {
		return "", err
	}
	data, err := os.ReadFile(m.SettingsPath)
	if errors.Is(err, os.ErrNotExist) {
		settings := Settings{LiveCodexHome: m.LiveCodexHome, AccountsDir: m.AccountsDir, SharedConfigPath: m.SharedConfigPath}
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
	}, home)
	if err != nil {
		return Settings{}, err
	}
	if settings.LiveCodexHome == "" || settings.AccountsDir == "" || settings.SharedConfigPath == "" {
		return Settings{}, errors.New("settings.toml must include live_codex_home, accounts_dir, and shared_settings_path")
	}

	if err := writeSettings(m.SettingsPath, settings); err != nil {
		return Settings{}, err
	}
	m.LiveCodexHome = settings.LiveCodexHome
	m.AccountsDir = settings.AccountsDir
	m.SharedConfigPath = settings.SharedConfigPath
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
	_ = m.syncSharedConfigFromCodexHome(sourceCodexHome, false)
	return account, nil
}

func (m *Manager) ImportJSONProfile(profile JSONProfile) (Account, error) {
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
	if duplicate, ok := duplicateAccount(state, id, identity); ok {
		return Account{}, fmt.Errorf("auth.json already exists as account %q", duplicate.ID)
	}
	label := strings.TrimSpace(profile.Label)
	if label == "" {
		label = deriveLabelFromAuth(auth)
	}

	account, err := m.AddAccount(id, label)
	if err != nil {
		return Account{}, err
	}

	authPath := filepath.Join(account.CodexHome, authFileName)
	if err := os.WriteFile(authPath, prettyJSON(authRaw), fileModeFor(authFileName)); err != nil {
		return Account{}, err
	}

	if strings.TrimSpace(profile.Config) != "" {
		if err := m.WriteSharedConfigText(profile.Config); err != nil {
			return Account{}, err
		}
	}

	return account, nil
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
	switch status {
	case StatusReady, StatusDrain, StatusDisabled:
	default:
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
	if err := m.Save(state); err != nil {
		return Account{}, err
	}
	if roundRobin, err := m.loadRoundRobinState(); err == nil && roundRobin.LastAccountID == id {
		_ = m.ResetRoundRobin()
	}
	return account, nil
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
	if err := m.syncSharedConfigToCodexHome(account.CodexHome); err != nil {
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
	if err := m.syncSharedConfigToCodexHome(account.CodexHome); err != nil {
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

	available := make([]AccountView, 0, len(accounts))
	for _, account := range accounts {
		if account.Status == StatusReady && account.AuthPresent {
			available = append(available, account)
		}
	}
	if len(available) == 0 {
		return AccountView{}, errors.New("no ready account with auth.json is available")
	}
	if len(available) == 1 {
		if err := m.saveRoundRobinState(roundRobinState{LastAccountID: available[0].ID}); err != nil {
			return AccountView{}, err
		}
		return available[0], nil
	}

	roundRobin, err := m.loadRoundRobinState()
	if err != nil {
		return AccountView{}, err
	}
	selected := available[0]
	if roundRobin.LastAccountID != "" {
		for i, account := range available {
			if account.ID == roundRobin.LastAccountID {
				selected = available[(i+1)%len(available)]
				break
			}
		}
	}

	if err := m.saveRoundRobinState(roundRobinState{LastAccountID: selected.ID}); err != nil {
		return AccountView{}, err
	}
	return selected, nil
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

	status := LoadBalanceStatus{
		Policy:        "round-robin",
		StatePath:     filepath.Join(m.StateDir, roundRobinFileName),
		LastAccountID: roundRobin.LastAccountID,
		Eligible:      []LoadBalanceAccount{},
		Excluded:      []LoadBalanceAccount{},
	}
	for _, account := range accounts {
		entry := LoadBalanceAccount{
			ID:            account.ID,
			Label:         account.Label,
			Status:        account.Status,
			AuthPresent:   account.AuthPresent,
			ConfigPresent: account.ConfigPresent,
			Active:        account.Active,
			CodexHome:     account.CodexHome,
		}
		entry.Eligible, entry.Reason = loadBalanceEligibility(account)
		if entry.Eligible {
			status.Eligible = append(status.Eligible, entry)
		} else {
			status.Excluded = append(status.Excluded, entry)
		}
	}
	return status, nil
}

func loadBalanceEligibility(account AccountView) (bool, string) {
	if account.Status != StatusReady {
		return false, fmt.Sprintf("status is %s", account.Status)
	}
	if !account.AuthPresent {
		return false, "auth.json missing"
	}
	return true, ""
}

func (m *Manager) ResetRoundRobin() error {
	if err := m.Ensure(); err != nil {
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
	_ = m.syncLiveAuthToManaged(account)
	return quota.FetchForCodexHome(ctx, account.CodexHome, time.Now())
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

func (m *Manager) SharedConfigInfo() (string, bool, time.Time) {
	if strings.TrimSpace(m.SharedConfigPath) == "" {
		return "", false, time.Time{}
	}
	info, err := os.Stat(m.SharedConfigPath)
	if err != nil {
		return m.SharedConfigPath, false, time.Time{}
	}
	return m.SharedConfigPath, true, info.ModTime()
}

func (m *Manager) ReadSharedConfigText() (string, error) {
	if err := m.Ensure(); err != nil {
		return "", err
	}
	if _, present, _ := m.SharedConfigInfo(); !present {
		return "", nil
	}
	data, err := os.ReadFile(m.SharedConfigPath)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (m *Manager) WriteSharedConfigText(raw string) error {
	if strings.TrimSpace(m.SharedConfigPath) == "" {
		return errors.New("shared_settings_path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(m.SharedConfigPath), 0o700); err != nil {
		return err
	}
	tmpPath := m.SharedConfigPath + ".tmp"
	if err := os.WriteFile(tmpPath, []byte(raw), fileModeFor(configFileName)); err != nil {
		return err
	}
	return os.Rename(tmpPath, m.SharedConfigPath)
}

func (m *Manager) syncSharedConfigFromCodexHome(codexHome string, overwrite bool) error {
	if strings.TrimSpace(codexHome) == "" || strings.TrimSpace(m.SharedConfigPath) == "" {
		return nil
	}
	source := filepath.Join(codexHome, configFileName)
	if samePath(source, m.SharedConfigPath) {
		return nil
	}
	if _, err := os.Stat(source); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if !overwrite {
		if _, err := os.Stat(m.SharedConfigPath); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Dir(m.SharedConfigPath), 0o700); err != nil {
		return err
	}
	return copyFile(source, m.SharedConfigPath, fileModeFor(configFileName))
}

func (m *Manager) syncSharedConfigToCodexHome(codexHome string) error {
	if strings.TrimSpace(codexHome) == "" || strings.TrimSpace(m.SharedConfigPath) == "" {
		return nil
	}
	if _, err := os.Stat(m.SharedConfigPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	target := filepath.Join(codexHome, configFileName)
	if samePath(m.SharedConfigPath, target) {
		return nil
	}
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		return err
	}
	return copyFile(m.SharedConfigPath, target, fileModeFor(configFileName))
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

	if _, present, _ := m.SharedConfigInfo(); present {
		configTarget := filepath.Join(targetCodexHome, configFileName)
		if err := copyFileWithBackup(m.SharedConfigPath, configTarget, fileModeFor(configFileName)); err != nil {
			return nil, err
		}
		written = append(written, configTarget)
	}
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
	configPath, configPresent, configUpdated := m.SharedConfigInfo()
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
	view.ConfigPresent = configPresent
	view.ConfigUpdated = configUpdated
	view.Active = m.isAccountActive(account)
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

func defaultSettings(home string) Settings {
	liveCodexHome := filepath.Join(home, ".codex")
	if value := strings.TrimSpace(os.Getenv("CODEX_HOME")); value != "" {
		liveCodexHome = expandPath(value, home)
	}
	return Settings{
		LiveCodexHome:    liveCodexHome,
		AccountsDir:      filepath.Join(home, defaultAccountsDirName),
		SharedConfigPath: filepath.Join(home, defaultStateDirName, sharedSettingsFileName),
	}
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

	var legacy struct {
		SharedConfigPath string `toml:"shared_config_path"`
	}
	_ = toml.Unmarshal(data, &legacy)

	rawText := string(data)
	hasSharedSettingsPath := strings.Contains(rawText, "shared_settings_path")
	changed := !hasSharedSettingsPath || strings.Contains(rawText, "shared_config_path")
	if !hasSharedSettingsPath && strings.TrimSpace(legacy.SharedConfigPath) != "" {
		settings.SharedConfigPath = legacy.SharedConfigPath
	}

	settings.LiveCodexHome = expandPath(settings.LiveCodexHome, home)
	settings.AccountsDir = expandPath(settings.AccountsDir, home)
	if strings.TrimSpace(settings.SharedConfigPath) == "" {
		settings.SharedConfigPath = defaults.SharedConfigPath
		changed = true
	}
	beforeMigration := settings.SharedConfigPath
	settings.SharedConfigPath = expandPath(settings.SharedConfigPath, home)
	settings.SharedConfigPath = migrateDefaultSharedSettingsPath(settings.SharedConfigPath, defaults.SharedConfigPath, home)
	if settings.SharedConfigPath != beforeMigration {
		changed = true
	}
	return settings, changed, nil
}

func migrateDefaultSharedSettingsPath(path, defaultPath, home string) string {
	oldDefault := filepath.Join(home, defaultStateDirName, configFileName)
	if !samePath(path, oldDefault) {
		return path
	}
	if _, err := os.Stat(defaultPath); errors.Is(err, os.ErrNotExist) {
		if _, oldErr := os.Stat(oldDefault); oldErr == nil {
			_ = os.MkdirAll(filepath.Dir(defaultPath), 0o700)
			_ = copyFile(oldDefault, defaultPath, fileModeFor(configFileName))
		}
	}
	return defaultPath
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
