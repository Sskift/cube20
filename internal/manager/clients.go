package manager

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

func (m *Manager) CreateClient(label string) (ClientView, string, error) {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()

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
	m.stateMu.Lock()
	defer m.stateMu.Unlock()

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
	m.stateMu.Lock()
	defer m.stateMu.Unlock()

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
