package manager

import (
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
)

const (
	defaultStateDirName    = ".cube20"
	defaultAccountsDirName = ".codex-accounts"
	authFileName           = "auth.json"
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
	AuthPresent bool      `json:"authPresent"`
	AuthPath    string    `json:"authPath"`
	AuthUpdated time.Time `json:"authUpdated,omitempty"`
}

type State struct {
	Version  int       `json:"version"`
	Accounts []Account `json:"accounts"`
}

type Manager struct {
	StateDir    string
	StatePath   string
	AccountsDir string
}

func New() (*Manager, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	stateDir := filepath.Join(home, defaultStateDirName)
	accountsDir := filepath.Join(home, defaultAccountsDirName)

	return &Manager{
		StateDir:    stateDir,
		StatePath:   filepath.Join(stateDir, "state.json"),
		AccountsDir: accountsDir,
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

func (m *Manager) ListAccounts() ([]AccountView, error) {
	state, err := m.Load()
	if err != nil {
		return nil, err
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

func (m *Manager) DeployAuth(id, targetCodexHome string) (string, error) {
	account, err := m.GetAccount(id)
	if err != nil {
		return "", err
	}
	source := filepath.Join(account.CodexHome, authFileName)
	if _, err := os.Stat(source); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("account %q has no auth.json yet", id)
		}
		return "", err
	}

	if strings.TrimSpace(targetCodexHome) == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		targetCodexHome = filepath.Join(home, ".codex")
	}
	if err := os.MkdirAll(targetCodexHome, 0o700); err != nil {
		return "", err
	}

	target := filepath.Join(targetCodexHome, authFileName)
	if _, err := os.Stat(target); err == nil {
		backup := target + ".backup-" + time.Now().Format("20060102-150405")
		if err := copyFile(target, backup, 0o600); err != nil {
			return "", fmt.Errorf("backup existing auth: %w", err)
		}
	}

	if err := copyFile(source, target, 0o600); err != nil {
		return "", err
	}
	return target, nil
}

func (m *Manager) accountView(account Account) AccountView {
	authPath := filepath.Join(account.CodexHome, authFileName)
	view := AccountView{
		Account:  account,
		AuthPath: authPath,
	}

	info, err := os.Stat(authPath)
	if err == nil {
		view.AuthPresent = true
		view.AuthUpdated = info.ModTime()
	}
	return view
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
