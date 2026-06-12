package manager

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// CreateUser registers a website identity: a username + password. The password
// is pbkdf2-hashed; the plaintext is never stored. Username is unique
// (case-insensitive).
func (m *Manager) CreateUser(username, password string) (UserView, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	if username == "" {
		return UserView{}, fmt.Errorf("username is required")
	}
	if len(password) < 6 {
		return UserView{}, fmt.Errorf("password must be at least 6 characters")
	}
	// Hash outside the state lock — pbkdf2 is deliberately slow.
	hash, err := hashPassword(password)
	if err != nil {
		return UserView{}, err
	}

	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	state, err := m.Load()
	if err != nil {
		return UserView{}, err
	}
	used := map[string]bool{}
	for _, u := range state.Users {
		if strings.EqualFold(u.Username, username) {
			return UserView{}, fmt.Errorf("username %q is taken", username)
		}
		used[u.ID] = true
	}
	now := time.Now()
	user := User{
		ID:           uniqueFromUsed("user-"+username, used),
		Username:     username,
		PasswordHash: hash,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	state.Users = append(state.Users, user)
	if err := m.Save(state); err != nil {
		return UserView{}, err
	}
	return userView(user, state), nil
}

// AuthenticateUser verifies username + password and returns the user. Returns
// false for unknown user, disabled user, or bad password — callers must give a
// single generic error so usernames can't be enumerated.
func (m *Manager) AuthenticateUser(username, password string) (UserView, bool) {
	username = strings.ToLower(strings.TrimSpace(username))
	if username == "" {
		return UserView{}, false
	}
	state, err := m.Load()
	if err != nil {
		return UserView{}, false
	}
	// dummyHash is a well-formed pbkdf2 hash of an unguessable value. Every
	// failure path runs a verify against it so login timing does not reveal
	// whether a username is unknown, disabled, or has no password set.
	const dummyHash = "pbkdf2$210000$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	for _, u := range state.Users {
		if !strings.EqualFold(u.Username, username) {
			continue
		}
		// Always run a real verification so disabled / passwordless accounts take
		// the same time as a normal failed login.
		ok := checkPassword(u.PasswordHash, password)
		if u.DisabledAt != nil || !ok {
			return UserView{}, false
		}
		return userView(u, state), true
	}
	// Unknown user: spend equivalent work before returning.
	_ = checkPassword(dummyHash, password)
	return UserView{}, false
}

// GetUser returns a user view by id.
func (m *Manager) GetUser(id string) (UserView, bool) {
	state, err := m.Load()
	if err != nil {
		return UserView{}, false
	}
	for _, u := range state.Users {
		if u.ID == id {
			return userView(u, state), true
		}
	}
	return UserView{}, false
}

// ListUsers returns all users (admin view), sorted by username.
func (m *Manager) ListUsers() ([]UserView, error) {
	state, err := m.Load()
	if err != nil {
		return nil, err
	}
	out := make([]UserView, 0, len(state.Users))
	for _, u := range state.Users {
		out = append(out, userView(u, state))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Username < out[j].Username })
	return out, nil
}

// SetUserPassword updates a user's password and revokes all their sessions
// (so a password change logs out every browser).
func (m *Manager) SetUserPassword(id, password string) error {
	if len(password) < 6 {
		return fmt.Errorf("password must be at least 6 characters")
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	state, err := m.Load()
	if err != nil {
		return err
	}
	found := false
	for i := range state.Users {
		if state.Users[i].ID == id {
			state.Users[i].PasswordHash = hash
			state.Users[i].UpdatedAt = time.Now()
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("user %q not found", id)
	}
	state.Sessions = dropUserSessions(state.Sessions, id)
	if err := m.persistAfterSessionDelete(state, id); err != nil {
		return err
	}
	return nil
}

// SetUserDisabled enables/disables a user. Disabling also drops their sessions.
func (m *Manager) SetUserDisabled(id string, disabled bool) error {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	state, err := m.Load()
	if err != nil {
		return err
	}
	found := false
	for i := range state.Users {
		if state.Users[i].ID == id {
			if disabled {
				now := time.Now()
				state.Users[i].DisabledAt = &now
			} else {
				state.Users[i].DisabledAt = nil
			}
			state.Users[i].UpdatedAt = time.Now()
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("user %q not found", id)
	}
	if disabled {
		state.Sessions = dropUserSessions(state.Sessions, id)
		return m.persistAfterSessionDelete(state, id)
	}
	// Re-enable: the PG generic Save uses a monotonic COALESCE for disabled_at
	// (so a stale save can't re-enable), so clearing it requires the dedicated
	// path.
	if strings.TrimSpace(m.DatabaseURL) != "" {
		if err := m.clearPostgresUserDisabled(id); err != nil {
			return err
		}
	}
	return m.Save(state)
}

// persistAfterSessionDelete saves state and, on Postgres, issues the targeted
// session delete the upsert-only Save cannot do.
func (m *Manager) persistAfterSessionDelete(state State, userID string) error {
	if strings.TrimSpace(m.DatabaseURL) != "" {
		if err := m.deletePostgresUserSessions(userID); err != nil {
			return err
		}
	}
	return m.Save(state)
}

func dropUserSessions(sessions []Session, userID string) []Session {
	filtered := sessions[:0]
	for _, s := range sessions {
		if s.UserID == userID {
			continue
		}
		filtered = append(filtered, s)
	}
	return filtered
}

func userView(user User, state State) UserView {
	count := 0
	for _, c := range state.Clients {
		if c.UserID == user.ID && c.RevokedAt == nil {
			count++
		}
	}
	return UserView{
		ID:          user.ID,
		Username:    user.Username,
		CreatedAt:   user.CreatedAt,
		LastLoginAt: user.LastLoginAt,
		Disabled:    user.DisabledAt != nil,
		DeviceCount: count,
	}
}
