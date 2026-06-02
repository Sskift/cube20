package manager

import (
	"context"
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
	"strconv"
	"strings"
	"time"

	"cube20/internal/quota"
	"cube20/internal/usage"
)

const (
	defaultStateDirName    = ".cube20"
	defaultAccountsDirName = ".codex-accounts"
	settingsFileName       = "settings.toml"
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
}

type State struct {
	Version  int       `json:"version"`
	Accounts []Account `json:"accounts"`
}

type Settings struct {
	LiveCodexHome string `json:"liveCodexHome"`
	AccountsDir   string `json:"accountsDir"`
}

type JSONProfile struct {
	ID     string          `json:"id"`
	Label  string          `json:"label"`
	Auth   json.RawMessage `json:"auth"`
	Config string          `json:"config"`
}

type Manager struct {
	StateDir      string
	StatePath     string
	SettingsPath  string
	AccountsDir   string
	LiveCodexHome string
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
		StateDir:      stateDir,
		StatePath:     filepath.Join(stateDir, "state.json"),
		SettingsPath:  settingsPath,
		AccountsDir:   settings.AccountsDir,
		LiveCodexHome: settings.LiveCodexHome,
	}, nil
}

func (m *Manager) Ensure() error {
	if err := os.MkdirAll(m.StateDir, 0o700); err != nil {
		return err
	}
	return os.MkdirAll(m.AccountsDir, 0o700)
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

func (m *Manager) UpdateSettings(liveCodexHome, accountsDir string) (Settings, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Settings{}, err
	}

	settings := Settings{
		LiveCodexHome: m.LiveCodexHome,
		AccountsDir:   m.AccountsDir,
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

	filesToCopy := []string{}
	for _, fileName := range []string{authFileName, configFileName} {
		source := filepath.Join(sourceCodexHome, fileName)
		if _, err := os.Stat(source); err == nil {
			filesToCopy = append(filesToCopy, fileName)
		} else if !errors.Is(err, os.ErrNotExist) {
			return Account{}, err
		}
	}

	if len(filesToCopy) == 0 {
		return Account{}, fmt.Errorf("no auth.json or config.toml found in %s", sourceCodexHome)
	}

	auth := readAuthMetadata(filepath.Join(sourceCodexHome, authFileName))
	if strings.TrimSpace(id) == "" {
		id = m.uniqueAccountID(deriveIDFromAuth(auth, label))
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
	for _, fileName := range filesToCopy {
		source := filepath.Join(sourceCodexHome, fileName)
		target := filepath.Join(account.CodexHome, fileName)
		if err := copyFile(source, target, fileModeFor(fileName)); err != nil {
			return Account{}, err
		}
	}
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

	id := strings.TrimSpace(profile.ID)
	if id == "" {
		id = m.uniqueAccountID(deriveIDFromAuth(auth, profile.Label))
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
		configPath := filepath.Join(account.CodexHome, configFileName)
		if err := os.WriteFile(configPath, []byte(profile.Config), fileModeFor(configFileName)); err != nil {
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
	for _, fileName := range []string{authFileName, configFileName} {
		if _, err := os.Stat(filepath.Join(codexHome, fileName)); err == nil {
			return true
		}
	}
	return false
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

func (m *Manager) LoginCommand(id string) (*exec.Cmd, error) {
	account, err := m.GetAccount(id)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(account.CodexHome, 0o700); err != nil {
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

	cmd := exec.Command("codex", args...)
	cmd.Env = withEnv(os.Environ(), "CODEX_HOME", account.CodexHome)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd, nil
}

func (m *Manager) FetchQuota(ctx context.Context, id string) (quota.Result, error) {
	account, err := m.GetAccount(id)
	if err != nil {
		return quota.Result{}, err
	}
	return quota.FetchForCodexHome(ctx, account.CodexHome, time.Now())
}

func (m *Manager) FetchUsage(id string) (usage.Summary, error) {
	account, err := m.GetAccount(id)
	if err != nil {
		return usage.Summary{}, err
	}
	return usage.SummarizeCodexHome(account.CodexHome, time.Now()), nil
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
	missing := []string{}
	for _, fileName := range []string{authFileName, configFileName} {
		source := filepath.Join(account.CodexHome, fileName)
		if _, err := os.Stat(source); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				missing = append(missing, fileName)
				continue
			}
			return nil, err
		}

		target := filepath.Join(targetCodexHome, fileName)
		if _, err := os.Stat(target); err == nil {
			backup := target + ".backup-" + time.Now().Format("20060102-150405")
			if err := copyFile(target, backup, fileModeFor(fileName)); err != nil {
				return nil, fmt.Errorf("backup existing %s: %w", fileName, err)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}

		if err := copyFile(source, target, fileModeFor(fileName)); err != nil {
			return nil, err
		}
		written = append(written, target)
	}

	if len(written) == 0 {
		return nil, fmt.Errorf("account %q has no managed files; missing %s", id, strings.Join(missing, ", "))
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
	configPath := filepath.Join(account.CodexHome, configFileName)
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
	info, err = os.Stat(configPath)
	if err == nil {
		view.ConfigPresent = true
		view.ConfigUpdated = info.ModTime()
	}
	return view
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
		LiveCodexHome: liveCodexHome,
		AccountsDir:   filepath.Join(home, defaultAccountsDirName),
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

	settings := defaults
	for lineNo, line := range strings.Split(string(data), "\n") {
		line = stripTomlComment(line)
		if strings.TrimSpace(line) == "" {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value, err := parseTomlString(parts[1])
		if err != nil {
			return Settings{}, fmt.Errorf("%s:%d: %w", path, lineNo+1, err)
		}
		switch key {
		case "live_codex_home":
			if strings.TrimSpace(value) != "" {
				settings.LiveCodexHome = expandPath(value, home)
			}
		case "accounts_dir":
			if strings.TrimSpace(value) != "" {
				settings.AccountsDir = expandPath(value, home)
			}
		}
	}
	return settings, nil
}

func writeSettings(path string, settings Settings) error {
	data := fmt.Sprintf(`# cube20 local settings
# Codex itself uses CODEX_HOME, auth.json, config.toml, and sessions/.
live_codex_home = %s
accounts_dir = %s
`, strconv.Quote(settings.LiveCodexHome), strconv.Quote(settings.AccountsDir))
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, []byte(data), 0o600); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func parseTomlString(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if strings.HasPrefix(value, `"`) {
		out, err := strconv.Unquote(value)
		if err != nil {
			return "", fmt.Errorf("invalid TOML string: %w", err)
		}
		return strings.TrimSpace(out), nil
	}
	return strings.Trim(strings.TrimSpace(value), `"`), nil
}

func stripTomlComment(line string) string {
	inString := false
	escaped := false
	for i, ch := range line {
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' && inString {
			escaped = true
			continue
		}
		if ch == '"' {
			inString = !inString
			continue
		}
		if ch == '#' && !inString {
			return strings.TrimSpace(line[:i])
		}
	}
	return strings.TrimSpace(line)
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
