package manager

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func (m *Manager) AddAccount(id, label string) (Account, error) {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	return m.addAccountLocked(id, label)
}

// addAccountLocked is the unlocked core of AddAccount; the caller must hold
// stateMu (file mode). It is reused by other locked mutators (ImportLiveProfile,
// UpsertJSONProfile) so they do not re-enter the non-reentrant stateMu.
func (m *Manager) addAccountLocked(id, label string) (Account, error) {
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
	m.stateMu.Lock()
	defer m.stateMu.Unlock()

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

	account, err := m.addAccountLocked(id, label)
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

// IdentifyAuth returns the managed account whose auth identity matches authRaw.
// It is intentionally read-only: unlike UpsertProfileSnapshot it never writes
// account files, changes ownership, or creates a new account.
func (m *Manager) IdentifyAuth(authRaw json.RawMessage) (AccountView, bool, error) {
	if len(authRaw) == 0 || string(authRaw) == "null" {
		return AccountView{}, false, errors.New("auth is required")
	}
	var auth map[string]any
	if err := json.Unmarshal(authRaw, &auth); err != nil {
		return AccountView{}, false, fmt.Errorf("auth is not valid JSON: %w", err)
	}
	identity := authIdentity(auth)
	if identity == "" {
		return AccountView{}, false, nil
	}
	state, err := m.Load()
	if err != nil {
		return AccountView{}, false, err
	}
	for _, account := range state.Accounts {
		existing := readAuthMetadata(filepath.Join(account.CodexHome, authFileName))
		if authIdentity(existing) == identity {
			return m.accountView(account), true, nil
		}
	}
	return AccountView{}, false, nil
}

func (m *Manager) UpsertProfileSnapshot(snapshot ProfileSnapshot) (Account, error) {
	// Lock order: stateMu (outermost, intra-process) THEN the cross-process file
	// lock. UpsertJSONProfile's core is called via its unlocked variant because
	// stateMu is already held here.
	m.stateMu.Lock()
	defer m.stateMu.Unlock()

	lockPath := filepath.Join(m.StateDir, "run-round-robin.lock")
	unlock, err := m.acquireLock(lockPath)
	if err != nil {
		return Account{}, err
	}
	defer unlock()

	account, err := m.upsertJSONProfileLocked(JSONProfile{
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
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	return m.upsertJSONProfileLocked(profile)
}

// upsertJSONProfileLocked is the unlocked core of UpsertJSONProfile; the caller
// must hold stateMu (file mode). UpsertProfileSnapshot reuses it while also
// holding the cross-process file lock, so it must not re-enter stateMu.
func (m *Manager) upsertJSONProfileLocked(profile JSONProfile) (Account, error) {
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

	account, err := m.addAccountLocked(id, label)
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
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	return m.listAccountsLocked()
}

// listAccountsLocked is the unlocked core of ListAccounts; the caller must hold
// stateMu (file mode) because syncManagedAccounts may Save the whole state.
// SelectAccountForRun reuses it while also holding the cross-process file lock,
// so it must not re-enter the non-reentrant stateMu.
func (m *Manager) listAccountsLocked() ([]AccountView, error) {
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

// ErrAccountNotFound is returned by AccountViewByID when no account matches.
var ErrAccountNotFound = errors.New("account not found")

// AccountViewByID returns the view for a single account without the side
// effects of ListAccounts (which runs syncManagedAccounts and may rewrite the
// whole state). It is a plain read: load, find by id, build the same per-account
// view ListAccounts produces. Use this for single-account HTTP responses so a
// GET/PATCH on one account does not trigger an O(N) load-and-save of all state.
func (m *Manager) AccountViewByID(id string) (AccountView, error) {
	state, err := m.Load()
	if err != nil {
		return AccountView{}, err
	}
	for _, account := range state.Accounts {
		if account.ID == id {
			return m.accountView(account), nil
		}
	}
	return AccountView{}, fmt.Errorf("%w: %q", ErrAccountNotFound, id)
}
func (m *Manager) SetStatus(id string, status AccountStatus) error {
	if !validAccountStatus(status) {
		return fmt.Errorf("unknown status %q", status)
	}

	m.stateMu.Lock()
	defer m.stateMu.Unlock()
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

// SetAccountWorkspace moves an account into a different pool. The target
// workspace must exist; an empty id resolves to the default pool.
func (m *Manager) SetAccountWorkspace(id, workspaceID string) error {
	workspaceID = workspaceOrDefault(workspaceID)

	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	state, err := m.Load()
	if err != nil {
		return err
	}
	if !workspaceExists(state, workspaceID) {
		return fmt.Errorf("workspace %q not found", workspaceID)
	}
	for i := range state.Accounts {
		if state.Accounts[i].ID == id {
			state.Accounts[i].WorkspaceID = workspaceID
			state.Accounts[i].UpdatedAt = time.Now()
			return m.Save(state)
		}
	}
	return fmt.Errorf("account %q not found", id)
}
func (m *Manager) SetLabel(id, label string) error {
	label = strings.TrimSpace(label)
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
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
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
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
	m.stateMu.Lock()
	defer m.stateMu.Unlock()

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
func (m *Manager) accountAuthPresent(account Account) bool {
	_, err := os.Stat(filepath.Join(account.CodexHome, authFileName))
	return err == nil
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
