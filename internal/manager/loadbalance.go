package manager

import (
	"cube20/internal/quota"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type LoadBalanceAccount struct {
	ID                            string           `json:"id"`
	Label                         string           `json:"label"`
	Status                        AccountStatus    `json:"status"`
	AuthPresent                   bool             `json:"authPresent"`
	ConfigPresent                 bool             `json:"configPresent"`
	Active                        bool             `json:"active"`
	CodexHome                     string           `json:"codexHome"`
	WorkspaceID                   string           `json:"workspaceId,omitempty"`
	OwnerMode                     AccountOwnerMode `json:"ownerMode"`
	OwnerClientID                 string           `json:"ownerClientId,omitempty"`
	Generation                    int64            `json:"generation"`
	LeaseActive                   bool             `json:"leaseActive"`
	LeaseKind                     string           `json:"leaseKind,omitempty"`
	LeaseClientID                 string           `json:"leaseClientId,omitempty"`
	LeaseHolder                   string           `json:"leaseHolder,omitempty"`
	LeaseExpiresAt                time.Time        `json:"leaseExpiresAt,omitempty"`
	Eligible                      bool             `json:"eligible"`
	Reason                        string           `json:"reason,omitempty"`
	RuntimeState                  RuntimeState     `json:"runtimeState,omitempty"`
	RuntimeReason                 string           `json:"runtimeReason,omitempty"`
	QuotaStatus                   quota.Status     `json:"quotaStatus,omitempty"`
	QuotaRemainingDisplay         string           `json:"quotaRemainingDisplay,omitempty"`
	QuotaRemainingPercent         float64          `json:"quotaRemainingPercent,omitempty"`
	QuotaUsedPercent              float64          `json:"quotaUsedPercent,omitempty"`
	QuotaResetsAt                 string           `json:"quotaResetsAt,omitempty"`
	QuotaUpdatedAt                time.Time        `json:"quotaUpdatedAt,omitempty"`
	QuotaScore                    float64          `json:"quotaScore,omitempty"`
	QuotaSevenDayRemainingDisplay string           `json:"quotaSevenDayRemainingDisplay,omitempty"`
	QuotaSevenDayRemainingPercent float64          `json:"quotaSevenDayRemainingPercent,omitempty"`
	QuotaSevenDayUsedPercent      float64          `json:"quotaSevenDayUsedPercent,omitempty"`
	QuotaSevenDayResetsAt         string           `json:"quotaSevenDayResetsAt,omitempty"`
	QuotaBindingWindow            string           `json:"quotaBindingWindow,omitempty"`
}
type LoadBalanceStatus struct {
	Policy        string               `json:"policy"`
	StatePath     string               `json:"statePath"`
	LastAccountID string               `json:"lastAccountId"`
	Eligible      []LoadBalanceAccount `json:"eligible"`
	Excluded      []LoadBalanceAccount `json:"excluded"`
}

type RuntimeState string

const (
	RuntimeAvailable             RuntimeState = "available"
	RuntimeLeased                RuntimeState = "leased"
	RuntimeQuotaCooldown         RuntimeState = "quota_cooldown"
	RuntimeRefreshNeeded         RuntimeState = "refresh_needed"
	RuntimeQuotaTelemetryMissing RuntimeState = "quota_telemetry_missing"
	RuntimeUnavailable           RuntimeState = "unavailable"
)

// LoadBalanceStatus reports pool eligibility. workspaceID scopes the view to a
// single pool; an empty workspaceID returns every account across all pools (the
// platform-wide admin view).
func (m *Manager) LoadBalanceStatus(workspaceID string) (LoadBalanceStatus, error) {
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
	workspaceID = strings.TrimSpace(workspaceID)
	for _, account := range accounts {
		if workspaceID != "" && workspaceOrDefault(account.WorkspaceID) != workspaceID {
			continue
		}
		entry := LoadBalanceAccount{
			ID:             account.ID,
			Label:          account.Label,
			Status:         account.Status,
			AuthPresent:    account.AuthPresent,
			ConfigPresent:  account.ConfigPresent,
			Active:         account.Active,
			CodexHome:      account.CodexHome,
			WorkspaceID:    workspaceOrDefault(account.WorkspaceID),
			OwnerMode:      account.OwnerMode,
			OwnerClientID:  account.OwnerClientID,
			Generation:     account.Generation,
			LeaseActive:    account.LeaseActive,
			LeaseKind:      account.LeaseKind,
			LeaseClientID:  account.LeaseClientID,
			LeaseHolder:    account.LeaseHolder,
			LeaseExpiresAt: account.LeaseExpiresAt,
		}
		evaluation := loadBalanceEligibility(account, state.QuotaCache[account.ID], now)
		entry.Eligible = evaluation.Eligible
		entry.Reason = evaluation.Reason
		entry.RuntimeState = evaluation.RuntimeState
		entry.RuntimeReason = evaluation.RuntimeReason
		entry.QuotaStatus = evaluation.QuotaStatus
		entry.QuotaRemainingDisplay = evaluation.QuotaRemainingDisplay
		entry.QuotaRemainingPercent = evaluation.QuotaRemainingPercent
		entry.QuotaUsedPercent = evaluation.QuotaUsedPercent
		entry.QuotaResetsAt = evaluation.QuotaResetsAt
		entry.QuotaUpdatedAt = evaluation.QuotaUpdatedAt
		entry.QuotaScore = evaluation.QuotaScore
		entry.QuotaSevenDayRemainingDisplay = evaluation.QuotaSevenDayRemainingDisplay
		entry.QuotaSevenDayRemainingPercent = evaluation.QuotaSevenDayRemainingPercent
		entry.QuotaSevenDayUsedPercent = evaluation.QuotaSevenDayUsedPercent
		entry.QuotaSevenDayResetsAt = evaluation.QuotaSevenDayResetsAt
		entry.QuotaBindingWindow = evaluation.QuotaBindingWindow
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

func decorateAccountRuntime(view *AccountView, account Account, state State, now time.Time) {
	if view == nil {
		return
	}
	evaluation := loadBalanceEligibilityForAccount(account, view.AuthPresent, state.QuotaCache[account.ID], now)
	view.RuntimeState = evaluation.RuntimeState
	view.RuntimeReason = evaluation.RuntimeReason
	view.LeaseKind = leaseKindForAccount(account, now)
}

type loadBalanceEvaluation struct {
	Eligible                      bool
	Reason                        string
	QuotaStatus                   quota.Status
	QuotaRemainingDisplay         string
	QuotaRemainingPercent         float64
	QuotaUsedPercent              float64
	QuotaResetsAt                 string
	QuotaUpdatedAt                time.Time
	QuotaScore                    float64
	QuotaSevenDayRemainingDisplay string
	QuotaSevenDayRemainingPercent float64
	QuotaSevenDayUsedPercent      float64
	QuotaSevenDayResetsAt         string
	QuotaBindingWindow            string
	RuntimeState                  RuntimeState
	RuntimeReason                 string
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
func evaluateLoadBalanceFields(ownerMode AccountOwnerMode, status AccountStatus, authPresent, leaseActive bool, leaseExpiresAt time.Time, cache QuotaCache, now time.Time) (evaluation loadBalanceEvaluation) {
	defer func() {
		evaluation.RuntimeState, evaluation.RuntimeReason = runtimeStateForEvaluation(evaluation)
	}()
	evaluation = loadBalanceQuotaEvaluation(cache, now)
	if ownerMode != OwnerCloud {
		evaluation.Reason = fmt.Sprintf("owner is %s", ownerMode)
		return
	}
	if status != StatusReady {
		evaluation.Reason = fmt.Sprintf("status is %s", status)
		return
	}
	if !authPresent {
		evaluation.Reason = "auth.json missing"
		return
	}
	if leaseActive {
		evaluation.Reason = fmt.Sprintf("leased until %s", leaseExpiresAt.Format(time.RFC3339))
		return
	}
	if evaluation.QuotaStatus == "" {
		evaluation.Reason = "quota not checked"
		return
	}
	if evaluation.QuotaStatus != quota.StatusSupported {
		evaluation.Reason = fmt.Sprintf("quota is %s", evaluation.QuotaStatus)
		return
	}
	if cache.FiveHour == nil {
		evaluation.Reason = "5h quota missing"
		return
	}
	resetAt := parseRFC3339(cache.FiveHour.ResetsAt)
	if resetAt.IsZero() {
		evaluation.Reason = "5h reset unknown"
		return
	}
	if !resetAt.After(now) {
		evaluation.Reason = "5h reset passed; refresh needed"
		return
	}
	if cache.FiveHour.RemainingPercent <= loadBalanceMinFiveHourRemaining {
		evaluation.Reason = fmt.Sprintf("5h quota exhausted until %s", resetAt.Format(time.RFC3339))
		return
	}
	// 7d window is present only for cloud-fetched accounts. When present and
	// exhausted (or its reset has passed) the account cannot run even though the
	// 5h window looks healthy. A missing 7d window means "no constraint".
	sevenDay := quotaSevenDay(cache.Result)
	if sevenDay != nil {
		sevenReset := parseRFC3339(sevenDay.ResetsAt)
		if !sevenReset.IsZero() && !sevenReset.After(now) {
			evaluation.Reason = "7d reset passed; refresh needed"
			return
		}
		if sevenDay.RemainingPercent <= loadBalanceMinFiveHourRemaining {
			if sevenReset.IsZero() {
				evaluation.Reason = "7d quota exhausted"
			} else {
				evaluation.Reason = fmt.Sprintf("7d quota exhausted until %s", sevenReset.Format(time.RFC3339))
			}
			return
		}
	}
	evaluation.Eligible = true
	evaluation.Reason = ""
	evaluation.QuotaScore = loadBalanceQuotaScore(*bindingWindow(cache.FiveHour, sevenDay), now)
	return
}

func runtimeStateForEvaluation(evaluation loadBalanceEvaluation) (RuntimeState, string) {
	if evaluation.Eligible {
		return RuntimeAvailable, "eligible for new leases"
	}
	reason := strings.TrimSpace(evaluation.Reason)
	switch {
	case strings.HasPrefix(reason, "leased until"):
		return RuntimeLeased, reason
	case strings.Contains(reason, "quota exhausted"):
		return RuntimeQuotaCooldown, reason
	case strings.Contains(reason, "reset passed; refresh needed"):
		return RuntimeRefreshNeeded, reason
	case reason == "quota not checked" || reason == "5h quota missing" || reason == "5h reset unknown":
		return RuntimeQuotaTelemetryMissing, reason
	default:
		return RuntimeUnavailable, reason
	}
}
func loadBalanceQuotaEvaluation(cache QuotaCache, now time.Time) loadBalanceEvaluation {
	evaluation := loadBalanceEvaluation{
		QuotaStatus:    cache.Result.Status,
		QuotaUpdatedAt: cache.UpdatedAt,
	}
	sevenDay := quotaSevenDay(cache.Result)
	if sevenDay != nil {
		evaluation.QuotaSevenDayRemainingDisplay = sevenDay.RemainingDisplay
		evaluation.QuotaSevenDayRemainingPercent = clampPercent(sevenDay.RemainingPercent)
		evaluation.QuotaSevenDayUsedPercent = clampPercent(sevenDay.UsedPercent)
		evaluation.QuotaSevenDayResetsAt = sevenDay.ResetsAt
	}
	binding := bindingWindow(cache.FiveHour, sevenDay)
	if binding == nil {
		return evaluation
	}
	evaluation.QuotaRemainingDisplay = binding.RemainingDisplay
	evaluation.QuotaRemainingPercent = clampPercent(binding.RemainingPercent)
	evaluation.QuotaUsedPercent = clampPercent(binding.UsedPercent)
	evaluation.QuotaResetsAt = binding.ResetsAt
	if binding == sevenDay {
		evaluation.QuotaBindingWindow = "7d"
	} else {
		evaluation.QuotaBindingWindow = "5h"
	}
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
