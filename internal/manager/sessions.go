package manager

import (
	"strings"
	"time"
)

// sessionTTL is the fixed absolute session lifetime. No sliding expiry — that
// would write to state on every request.
const sessionTTL = 30 * 24 * time.Hour

// CreateSession mints a website session for a user. Returns the plaintext cookie
// value (cube_ses_...) once; only its hash is stored.
func (m *Manager) CreateSession(userID string) (string, error) {
	token, err := generateSessionToken()
	if err != nil {
		return "", err
	}
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	state, err := m.Load()
	if err != nil {
		return "", err
	}
	used := map[string]bool{}
	for _, s := range state.Sessions {
		used[s.ID] = true
	}
	now := time.Now()
	session := Session{
		ID:         uniqueFromUsed("sess-"+strings.ToLower(userID), used),
		TokenHash:  hashToken(token),
		UserID:     userID,
		CreatedAt:  now,
		ExpiresAt:  now.Add(sessionTTL),
		LastSeenAt: now,
	}
	// Stamp last-login on the user.
	for i := range state.Users {
		if state.Users[i].ID == userID {
			state.Users[i].LastLoginAt = now
			break
		}
	}
	state.Sessions = append(state.Sessions, session)
	if err := m.Save(state); err != nil {
		return "", err
	}
	// On Postgres the generic Save does not write sessions (resurrection
	// safety), so insert the new session row directly.
	if strings.TrimSpace(m.DatabaseURL) != "" {
		if err := m.insertPostgresSession(session); err != nil {
			return "", err
		}
	}
	return token, nil
}

// ResolveSession validates a session cookie value and returns the live user. It
// rejects expired sessions and disabled users. Read-only (no write lock) — the
// hot auth path, like AuthenticateClientToken.
func (m *Manager) ResolveSession(token string) (UserView, string, bool) {
	token = strings.TrimSpace(token)
	if token == "" {
		return UserView{}, "", false
	}
	hash := hashToken(token)
	state, err := m.Load()
	if err != nil {
		return UserView{}, "", false
	}
	now := time.Now()
	for _, s := range state.Sessions {
		if !subtleStringEqual(s.TokenHash, hash) {
			continue
		}
		if !s.ExpiresAt.IsZero() && !s.ExpiresAt.After(now) {
			return UserView{}, "", false
		}
		for _, u := range state.Users {
			if u.ID == s.UserID {
				if u.DisabledAt != nil {
					return UserView{}, "", false
				}
				return userView(u, state), s.ID, true
			}
		}
		return UserView{}, "", false
	}
	return UserView{}, "", false
}

// DeleteSession removes one session (logout).
func (m *Manager) DeleteSession(id string) error {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	state, err := m.Load()
	if err != nil {
		return err
	}
	filtered := state.Sessions[:0]
	for _, s := range state.Sessions {
		if s.ID == id {
			continue
		}
		filtered = append(filtered, s)
	}
	state.Sessions = filtered
	if strings.TrimSpace(m.DatabaseURL) != "" {
		if err := m.deletePostgresSession(id); err != nil {
			return err
		}
	}
	return m.Save(state)
}
