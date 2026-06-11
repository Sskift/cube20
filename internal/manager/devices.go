package manager

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// CreateDevice mints a per-machine bearer token owned by a user. The plaintext
// token (cube_dev_...) is returned ONCE; only its hash is stored. A device is a
// Client row with UserID set, so the existing token-auth path validates it
// unchanged.
func (m *Manager) CreateDevice(userID, label string) (DeviceView, string, error) {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return DeviceView{}, "", fmt.Errorf("device needs an owning user")
	}
	label = strings.TrimSpace(label)
	if label == "" {
		label = "device"
	}
	token, err := generateDeviceToken()
	if err != nil {
		return DeviceView{}, "", err
	}

	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	state, err := m.Load()
	if err != nil {
		return DeviceView{}, "", err
	}
	if !userExists(state, userID) {
		return DeviceView{}, "", fmt.Errorf("user %q not found", userID)
	}
	used := map[string]bool{}
	for _, c := range state.Clients {
		used[c.ID] = true
	}
	id := uniqueFromUsed("device-"+label, used)
	now := time.Now()
	device := Device{
		ID:        id,
		UserID:    userID,
		Label:     label,
		TokenHash: hashToken(token),
		CreatedAt: now,
	}
	state.Clients = append(state.Clients, device)
	if err := m.Save(state); err != nil {
		return DeviceView{}, "", err
	}
	return clientView(device), token, nil
}

// ListDevices returns a user's devices (or all devices when userID is empty —
// admin view), sorted active-first then by id.
func (m *Manager) ListDevices(userID string) ([]DeviceView, error) {
	state, err := m.Load()
	if err != nil {
		return nil, err
	}
	userID = strings.TrimSpace(userID)
	out := []DeviceView{}
	for _, c := range state.Clients {
		if userID != "" && c.UserID != userID {
			continue
		}
		out = append(out, clientView(c))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Active != out[j].Active {
			return out[i].Active
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// DeviceOwner returns the user id that owns a device, and whether it exists.
func (m *Manager) DeviceOwner(deviceID string) (string, bool) {
	state, err := m.Load()
	if err != nil {
		return "", false
	}
	for _, c := range state.Clients {
		if c.ID == deviceID {
			return c.UserID, true
		}
	}
	return "", false
}

// RevokeDevice soft-revokes a device (sets RevokedAt). The row is retained for
// audit. On Postgres the targeted update is issued directly.
func (m *Manager) RevokeDevice(id string) error {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	state, err := m.Load()
	if err != nil {
		return err
	}
	found := false
	now := time.Now()
	for i := range state.Clients {
		if state.Clients[i].ID == id {
			state.Clients[i].RevokedAt = &now
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("device %q not found", id)
	}
	if strings.TrimSpace(m.DatabaseURL) != "" {
		if err := m.deletePostgresDevice(id); err != nil {
			return err
		}
	}
	return m.Save(state)
}

// AuthenticateDeviceToken resolves a bearer token to its device (the underlying
// client). Returns the device view + owning user id. Mirrors
// AuthenticateClientToken; both cube_pat_ and cube_dev_ tokens validate here.
func (m *Manager) AuthenticateDeviceToken(token string) (DeviceView, string, bool) {
	token = strings.TrimSpace(token)
	if token == "" {
		return DeviceView{}, "", false
	}
	hash := hashToken(token)
	state, err := m.Load()
	if err != nil {
		return DeviceView{}, "", false
	}
	for _, c := range state.Clients {
		if c.RevokedAt != nil || c.TokenHash == "" {
			continue
		}
		if subtleStringEqual(c.TokenHash, hash) {
			return clientView(c), c.UserID, true
		}
	}
	return DeviceView{}, "", false
}

func userExists(state State, id string) bool {
	for _, u := range state.Users {
		if u.ID == id {
			return true
		}
	}
	return false
}
