package manager

import (
	"cube20/internal/quota"
	"fmt"
	"path/filepath"
	"sort"
	"time"
)

type LoadBalanceAccount struct {
	ID                    string           `json:"id"`
	Label                 string           `json:"label"`
	Status                AccountStatus    `json:"status"`
	AuthPresent           bool             `json:"authPresent"`
	ConfigPresent         bool             `json:"configPresent"`
	Active                bool             `json:"active"`
	CodexHome             string           `json:"codexHome"`
	OwnerMode             AccountOwnerMode `json:"ownerMode"`
	OwnerClientID         string           `json:"ownerClientId,omitempty"`
	Generation            int64            `json:"generation"`
	LeaseActive           bool             `json:"leaseActive"`
	LeaseClientID         string           `json:"leaseClientId,omitempty"`
	LeaseHolder           string           `json:"leaseHolder,omitempty"`
	LeaseExpiresAt        time.Time        `json:"leaseExpiresAt,omitempty"`
	Eligible              bool             `json:"eligible"`
	Reason                string           `json:"reason,omitempty"`
	QuotaStatus           quota.Status     `json:"quotaStatus,omitempty"`
	QuotaRemainingDisplay string           `json:"quotaRemainingDisplay,omitempty"`
	QuotaRemainingPercent float64          `json:"quotaRemainingPercent,omitempty"`
	QuotaUsedPercent      float64          `json:"quotaUsedPercent,omitempty"`
	QuotaResetsAt         string           `json:"quotaResetsAt,omitempty"`
	QuotaUpdatedAt        time.Time        `json:"quotaUpdatedAt,omitempty"`
	QuotaScore            float64          `json:"quotaScore,omitempty"`
}
type LoadBalanceStatus struct {
	Policy        string               `json:"policy"`
	StatePath     string               `json:"statePath"`
	LastAccountID string               `json:"lastAccountId"`
	Eligible      []LoadBalanceAccount `json:"eligible"`
	Excluded      []LoadBalanceAccount `json:"excluded"`
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
	state, err := m.Load()
	if err != nil {
		return LoadBalanceStatus{}, err
	}
	now := time.Now()

	status := LoadBalanceStatus{
		Policy:        "quota-aware weighted round-robin",
		StatePath:     filepath.Join(m.StateDir, roundRobinFileName),
		LastAccountID: roundRobin.LastAccountID,
		Eligible:      []LoadBalanceAccount{},
		Excluded:      []LoadBalanceAccount{},
	}
	for _, account := range accounts {
		entry := LoadBalanceAccount{
			ID:             account.ID,
			Label:          account.Label,
			Status:         account.Status,
			AuthPresent:    account.AuthPresent,
			ConfigPresent:  account.ConfigPresent,
			Active:         account.Active,
			CodexHome:      account.CodexHome,
			OwnerMode:      account.OwnerMode,
			OwnerClientID:  account.OwnerClientID,
			Generation:     account.Generation,
			LeaseActive:    account.LeaseActive,
			LeaseClientID:  account.LeaseClientID,
			LeaseHolder:    account.LeaseHolder,
			LeaseExpiresAt: account.LeaseExpiresAt,
		}
		evaluation := loadBalanceEligibility(account, state.QuotaCache[account.ID], now)
		entry.Eligible = evaluation.Eligible
		entry.Reason = evaluation.Reason
		entry.QuotaStatus = evaluation.QuotaStatus
		entry.QuotaRemainingDisplay = evaluation.QuotaRemainingDisplay
		entry.QuotaRemainingPercent = evaluation.QuotaRemainingPercent
		entry.QuotaUsedPercent = evaluation.QuotaUsedPercent
		entry.QuotaResetsAt = evaluation.QuotaResetsAt
		entry.QuotaUpdatedAt = evaluation.QuotaUpdatedAt
		entry.QuotaScore = evaluation.QuotaScore
		if entry.Eligible {
			status.Eligible = append(status.Eligible, entry)
		} else {
			status.Excluded = append(status.Excluded, entry)
		}
	}
	sort.Slice(status.Eligible, func(i, j int) bool {
		if !sameLoadBalanceScore(status.Eligible[i].QuotaScore, status.Eligible[j].QuotaScore) {
			return status.Eligible[i].QuotaScore > status.Eligible[j].QuotaScore
		}
		return status.Eligible[i].ID < status.Eligible[j].ID
	})
	sort.Slice(status.Excluded, func(i, j int) bool {
		return status.Excluded[i].ID < status.Excluded[j].ID
	})
	return status, nil
}

type loadBalanceEvaluation struct {
	Eligible              bool
	Reason                string
	QuotaStatus           quota.Status
	QuotaRemainingDisplay string
	QuotaRemainingPercent float64
	QuotaUsedPercent      float64
	QuotaResetsAt         string
	QuotaUpdatedAt        time.Time
	QuotaScore            float64
}

func loadBalanceEligibility(account AccountView, cache QuotaCache, now time.Time) loadBalanceEvaluation {
	return evaluateLoadBalanceFields(
		account.OwnerMode,
		account.Status,
		account.AuthPresent,
		account.LeaseActive,
		account.LeaseExpiresAt,
		cache,
		now,
	)
}
func loadBalanceEligibilityForAccount(account Account, authPresent bool, cache QuotaCache, now time.Time) loadBalanceEvaluation {
	return evaluateLoadBalanceFields(
		account.OwnerMode,
		account.Status,
		authPresent,
		accountLeaseActive(account, now),
		account.LeaseExpiresAt,
		cache,
		now,
	)
}
func evaluateLoadBalanceFields(ownerMode AccountOwnerMode, status AccountStatus, authPresent, leaseActive bool, leaseExpiresAt time.Time, cache QuotaCache, now time.Time) loadBalanceEvaluation {
	evaluation := loadBalanceQuotaEvaluation(cache, now)
	if ownerMode != OwnerCloud {
		evaluation.Reason = fmt.Sprintf("owner is %s", ownerMode)
		return evaluation
	}
	if status != StatusReady {
		evaluation.Reason = fmt.Sprintf("status is %s", status)
		return evaluation
	}
	if !authPresent {
		evaluation.Reason = "auth.json missing"
		return evaluation
	}
	if leaseActive {
		evaluation.Reason = fmt.Sprintf("leased until %s", leaseExpiresAt.Format(time.RFC3339))
		return evaluation
	}
	if evaluation.QuotaStatus == "" {
		evaluation.Reason = "quota not checked"
		return evaluation
	}
	if evaluation.QuotaStatus != quota.StatusSupported {
		evaluation.Reason = fmt.Sprintf("quota is %s", evaluation.QuotaStatus)
		return evaluation
	}
	if cache.FiveHour == nil {
		evaluation.Reason = "5h quota missing"
		return evaluation
	}
	resetAt := parseRFC3339(cache.FiveHour.ResetsAt)
	if resetAt.IsZero() {
		evaluation.Reason = "5h reset unknown"
		return evaluation
	}
	if !resetAt.After(now) {
		evaluation.Reason = "5h reset passed; refresh needed"
		return evaluation
	}
	if cache.FiveHour.RemainingPercent <= loadBalanceMinFiveHourRemaining {
		evaluation.Reason = fmt.Sprintf("5h quota exhausted until %s", resetAt.Format(time.RFC3339))
		return evaluation
	}
	evaluation.Eligible = true
	evaluation.Reason = ""
	evaluation.QuotaScore = loadBalanceQuotaScore(*cache.FiveHour, now)
	return evaluation
}
func loadBalanceQuotaEvaluation(cache QuotaCache, now time.Time) loadBalanceEvaluation {
	evaluation := loadBalanceEvaluation{
		QuotaStatus:    cache.Result.Status,
		QuotaUpdatedAt: cache.UpdatedAt,
	}
	if cache.FiveHour == nil {
		return evaluation
	}
	evaluation.QuotaRemainingDisplay = cache.FiveHour.RemainingDisplay
	evaluation.QuotaRemainingPercent = clampPercent(cache.FiveHour.RemainingPercent)
	evaluation.QuotaUsedPercent = clampPercent(cache.FiveHour.UsedPercent)
	evaluation.QuotaResetsAt = cache.FiveHour.ResetsAt
	return evaluation
}
func loadBalanceQuotaScore(window quota.Window, now time.Time) float64 {
	remaining := clampPercent(window.RemainingPercent)
	if remaining <= loadBalanceMinFiveHourRemaining {
		return 0
	}
	score := remaining
	resetAt := parseRFC3339(window.ResetsAt)
	if resetAt.IsZero() {
		return score
	}
	untilReset := resetAt.Sub(now)
	if untilReset <= 0 {
		return score
	}
	if untilReset < loadBalanceNearResetWindow {
		pressure := 1 - (float64(untilReset) / float64(loadBalanceNearResetWindow))
		score += pressure * loadBalanceNearResetBonus
	}
	return score
}
func loadBalanceTopGroupLen(length int, scoreAt func(int) float64) int {
	if length == 0 {
		return 0
	}
	topScore := scoreAt(0)
	out := 1
	for out < length && sameLoadBalanceScore(topScore, scoreAt(out)) {
		out++
	}
	return out
}
func sameLoadBalanceScore(a, b float64) bool {
	diff := a - b
	return diff >= -loadBalanceScoreEpsilon && diff <= loadBalanceScoreEpsilon
}
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
func clampPercent(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}
