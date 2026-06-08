package manager

import (
	"cube20/internal/usage"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

func (m *Manager) RecordUsage(accountID, clientID string, summary usage.Summary) error {
	return m.RecordUsageWithContext(accountID, clientID, "", "", summary)
}
func (m *Manager) RecordUsageWithContext(accountID, clientID, leaseID, runID string, summary usage.Summary) error {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return errors.New("usage account id is required")
	}
	if strings.TrimSpace(m.DatabaseURL) != "" {
		return m.recordPostgresUsage(accountID, strings.TrimSpace(clientID), strings.TrimSpace(leaseID), strings.TrimSpace(runID), summary)
	}
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	state, err := m.Load()
	if err != nil {
		return err
	}
	if state.Usage == nil {
		state.Usage = map[string]AccountUsage{}
	}
	state.Usage[accountID] = AccountUsage{
		AccountID:   accountID,
		ClientID:    strings.TrimSpace(clientID),
		UpdatedAt:   time.Now(),
		LatestAt:    summary.LatestAt,
		LatestModel: summary.LatestModel,
		Today:       summary.Today,
		SevenDays:   summary.SevenDays,
		AllTime:     summary.AllTime,
		Models:      summary.Models,
	}
	return m.Save(state)
}
func (m *Manager) UsageStats() (map[string]AccountUsage, error) {
	state, err := m.Load()
	if err != nil {
		return nil, err
	}
	out := map[string]AccountUsage{}
	for id, stat := range state.Usage {
		out[id] = stat
	}
	return out, nil
}
func (m *Manager) DispatchHistory(limit int, clientID string) ([]DispatchEvent, error) {
	if err := m.Ensure(); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > maxDispatchHistory {
		limit = 50
	}
	clientID = strings.TrimSpace(clientID)
	if strings.TrimSpace(m.DatabaseURL) != "" {
		return m.postgresDispatchHistory(limit, clientID)
	}
	state, err := m.Load()
	if err != nil {
		return nil, err
	}
	out := make([]DispatchEvent, 0, minInt(limit, len(state.Dispatches)))
	for _, event := range state.Dispatches {
		if clientID != "" && event.ClientID != clientID {
			continue
		}
		event = enrichDispatchEvent(state, event)
		out = append(out, event)
		if len(out) >= limit {
			break
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}
func enrichDispatchEvent(state State, event DispatchEvent) DispatchEvent {
	if strings.TrimSpace(event.AccountLabel) == "" {
		for _, account := range state.Accounts {
			if account.ID == event.AccountID {
				event.AccountLabel = account.Label
				break
			}
		}
	}
	if strings.TrimSpace(event.ClientLabel) == "" {
		event.ClientLabel = clientLabelFromState(state, event.ClientID)
	}
	return event
}
func (m *Manager) RefreshQueue() ([]RefreshQueueItem, error) {
	accounts, err := m.ListAccounts()
	if err != nil {
		return nil, err
	}
	state, err := m.Load()
	if err != nil {
		return nil, err
	}
	items := make([]RefreshQueueItem, 0, len(accounts))
	for _, account := range accounts {
		cache := state.QuotaCache[account.ID]
		item := RefreshQueueItem{
			AccountID:             account.ID,
			Label:                 account.Label,
			Status:                account.Status,
			AuthPresent:           account.AuthPresent,
			UpdatedAt:             cache.UpdatedAt,
			QuotaStatus:           cache.Result.Status,
			OwnerMode:             account.OwnerMode,
			OwnerClientID:         account.OwnerClientID,
			QuotaSource:           cache.Source,
			QuotaReporterClientID: cache.ReporterClientID,
			LeaseActive:           account.LeaseActive,
			LeaseClientID:         account.LeaseClientID,
			LeaseExpiresAt:        account.LeaseExpiresAt,
		}
		if cache.FiveHour != nil {
			item.ResetsAt = cache.FiveHour.ResetsAt
			item.RemainingDisplay = cache.FiveHour.RemainingDisplay
			item.RemainingPercent = cache.FiveHour.RemainingPercent
			item.UsedPercent = cache.FiveHour.UsedPercent
		}
		switch {
		case !account.AuthPresent:
			item.RefreshOrderReason = "auth missing"
		case account.OwnerMode == OwnerClient:
			if cache.Result.Status == "" {
				item.RefreshOrderReason = "waiting for client report"
			} else {
				item.RefreshOrderReason = "client reported"
			}
		case account.LeaseActive:
			item.RefreshOrderReason = "leased"
		case cache.FiveHour == nil:
			item.RefreshOrderReason = "quota not checked"
		case cache.FiveHour.ResetsAt == "":
			item.RefreshOrderReason = "reset unknown"
		default:
			item.RefreshOrderReason = "5h reset order"
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		ti := parseRFC3339(items[i].ResetsAt)
		tj := parseRFC3339(items[j].ResetsAt)
		if !ti.IsZero() && !tj.IsZero() {
			return ti.Before(tj)
		}
		if !ti.IsZero() {
			return true
		}
		if !tj.IsZero() {
			return false
		}
		return items[i].AccountID < items[j].AccountID
	})
	return items, nil
}
func dispatchEventFromAccount(state State, account Account, event string, now time.Time) DispatchEvent {
	return DispatchEvent{
		ID:           dispatchEventID(account, event, now),
		LeaseID:      account.LeaseID,
		AccountID:    account.ID,
		AccountLabel: account.Label,
		ClientID:     account.LeaseClientID,
		ClientLabel:  clientLabelFromState(state, account.LeaseClientID),
		Holder:       account.LeaseHolder,
		Event:        event,
		Generation:   account.Generation,
		CreatedAt:    now,
		StartedAt:    account.LeaseStartedAt,
		ExpiresAt:    account.LeaseExpiresAt,
	}
}
func dispatchEventID(account Account, event string, now time.Time) string {
	leaseID := strings.TrimSpace(account.LeaseID)
	if leaseID != "" {
		return leaseID + ":" + event
	}
	return fmt.Sprintf("%s:%s:%d", strings.TrimSpace(account.ID), event, now.UnixNano())
}
func appendDispatchEvent(events []DispatchEvent, event DispatchEvent) []DispatchEvent {
	if strings.TrimSpace(event.ID) == "" {
		return events
	}
	next := make([]DispatchEvent, 0, minInt(len(events)+1, maxDispatchHistory))
	next = append(next, event)
	for _, existing := range events {
		if existing.ID == event.ID {
			continue
		}
		next = append(next, existing)
		if len(next) >= maxDispatchHistory {
			break
		}
	}
	return next
}
func clientLabelFromState(state State, clientID string) string {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return ""
	}
	for _, client := range state.Clients {
		if client.ID == clientID {
			return client.Label
		}
	}
	return ""
}
func (m *Manager) FetchUsage(id string) (usage.Summary, error) {
	account, err := m.GetAccount(id)
	if err != nil {
		return usage.Summary{}, err
	}
	return usage.SummarizeCodexHome(account.CodexHome, time.Now()), nil
}
