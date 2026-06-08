package manager

import (
	"context"
	"cube20/internal/quota"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

func (m *Manager) FetchQuota(ctx context.Context, id string) (quota.Result, error) {
	account, err := m.GetAccount(id)
	if err != nil {
		return quota.Result{}, err
	}
	now := time.Now()
	if account.OwnerMode == OwnerClient {
		state, loadErr := m.Load()
		if loadErr == nil {
			if cache, ok := state.QuotaCache[id]; ok && cache.Result.Status != "" {
				result := cache.Result
				result.Source = quotaSourceLabel(cache)
				if cache.ReporterClientID != "" {
					result.Detail = firstNonEmpty(result.Detail, fmt.Sprintf("client-owned account; returning quota reported by %s at %s", cache.ReporterClientID, cache.UpdatedAt.Format(time.RFC3339)))
				} else {
					result.Detail = firstNonEmpty(result.Detail, fmt.Sprintf("client-owned account; returning client-reported quota from %s", cache.UpdatedAt.Format(time.RFC3339)))
				}
				return result, nil
			}
		}
		return quota.Result{
			Status: quota.StatusNotConfigured,
			Source: "client report",
			Detail: "client-owned account; waiting for local cube report",
		}, nil
	}
	if accountLeaseActive(account, now) {
		return m.leasedQuotaResponse(account), nil
	}
	// Close the snapshot/network race: the `account` above was loaded before
	// any lease writer could have claimed it. Re-check lease-active state from
	// fresh state right before the network fetch. In file mode this re-check
	// runs under the same round-robin lock the lease writers hold, so a lease
	// that lands concurrently is observed here instead of being clobbered by a
	// cloud quota refresh. The lock is released before the network call and
	// before recordQuotaResult (which acquires the same lock) to avoid a
	// deadlock. In Postgres mode there is no file lock here (lease writers use
	// advisory locks elsewhere); a fresh reload re-check is the intended scope.
	if fresh, leased := m.leaseActiveFresh(id); leased {
		return m.leasedQuotaResponse(fresh), nil
	}
	_ = m.syncLiveAuthToManaged(account)
	authPath := filepath.Join(account.CodexHome, authFileName)
	beforeDigest := fileDigest(authPath)
	result, err := quota.FetchForCodexHome(ctx, account.CodexHome, now)
	afterDigest := fileDigest(authPath)
	authChanged := beforeDigest != "" && afterDigest != "" && beforeDigest != afterDigest
	_ = m.recordQuotaResult(id, result, authChanged, QuotaSourceCloud, "", false)
	return result, err
}

// leaseActiveFresh reloads the account and reports whether it is currently
// lease-active. In file mode the reload runs under the round-robin lock so a
// concurrent lease write is observed; the lock is always released before the
// function returns so callers may proceed to the network/recordQuotaResult
// path (which acquires the same lock) without deadlocking. Errors and missing
// accounts are treated as "not leased" so the caller falls through to its
// normal path.
func (m *Manager) leaseActiveFresh(id string) (Account, bool) {
	if strings.TrimSpace(m.DatabaseURL) == "" {
		lockPath := filepath.Join(m.StateDir, "run-round-robin.lock")
		unlock, err := m.acquireLock(lockPath)
		if err != nil {
			return Account{}, false
		}
		defer unlock()
	}
	state, err := m.Load()
	if err != nil {
		return Account{}, false
	}
	for _, account := range state.Accounts {
		if account.ID != id {
			continue
		}
		return account, accountLeaseActive(account, time.Now())
	}
	return Account{}, false
}

// leasedQuotaResponse builds the FetchQuota response for an account that is
// currently leased: the cached quota (returned verbatim with a leased-by note)
// when present, otherwise a paused-refresh marker. It never performs a network
// fetch.
func (m *Manager) leasedQuotaResponse(account Account) quota.Result {
	if state, loadErr := m.Load(); loadErr == nil {
		if cache, ok := state.QuotaCache[account.ID]; ok && cache.Result.Status != "" {
			result := cache.Result
			result.Source = quotaSourceLabel(cache)
			result.Detail = firstNonEmpty(result.Detail, fmt.Sprintf("account is leased by %s until %s; returning cached quota", account.LeaseClientID, account.LeaseExpiresAt.Format(time.RFC3339)))
			return result
		}
	}
	return quota.Result{
		Status: quota.StatusError,
		Source: "cube lease",
		Detail: fmt.Sprintf("account is leased by %s until %s; quota refresh is paused", account.LeaseClientID, account.LeaseExpiresAt.Format(time.RFC3339)),
	}
}

// RecordLeasedQuota stores a client-reported quota for an account the client
// currently leases. Unlike RecordQuotaReport it must NOT flip the account into
// client ownership: a leased cloud account stays cloud-owned so it returns to
// the load balancer when the lease ends. The caller must hold the lease
// (matching LeaseID and LeaseClientID, lease still active); otherwise an error
// is returned and the cache is left unchanged.
func (m *Manager) RecordLeasedQuota(accountID, leaseID, clientID string, result quota.Result, now time.Time) error {
	accountID = strings.TrimSpace(accountID)
	leaseID = strings.TrimSpace(leaseID)
	clientID = strings.TrimSpace(clientID)
	if accountID == "" {
		return fmt.Errorf("account id is required")
	}
	if now.IsZero() {
		now = time.Now()
	}
	if strings.TrimSpace(m.DatabaseURL) != "" {
		if err := m.verifyLeaseHolder(accountID, leaseID, clientID, now); err != nil {
			return err
		}
		return m.recordPostgresQuotaResult(accountID, result, false, QuotaSourceClient, clientID, false)
	}
	// File mode: take stateMu (outermost) then the round-robin lock exactly
	// once, validate lease ownership, and write the cache atomically so a
	// concurrent release cannot slip between the check and the write.
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	lockPath := filepath.Join(m.StateDir, "run-round-robin.lock")
	unlock, err := m.acquireLock(lockPath)
	if err != nil {
		return err
	}
	defer unlock()
	if err := m.verifyLeaseHolderLocked(accountID, leaseID, clientID, now); err != nil {
		return err
	}
	return m.writeQuotaResultLocked(accountID, result, false, QuotaSourceClient, clientID, false)
}
func (m *Manager) RecordQuotaReport(id string, result quota.Result, clientID string) error {
	return m.recordQuotaResult(id, result, false, QuotaSourceClient, strings.TrimSpace(clientID), true)
}
func (m *Manager) recordQuotaResult(id string, result quota.Result, authChanged bool, source QuotaSource, reporterClientID string, flipOwner bool) error {
	if strings.TrimSpace(id) == "" {
		return nil
	}
	if strings.TrimSpace(m.DatabaseURL) != "" {
		return m.recordPostgresQuotaResult(id, result, authChanged, source, strings.TrimSpace(reporterClientID), flipOwner)
	}
	// File mode shares state.json with the lease writers (ClaimLease,
	// TouchLease, ReleaseLease, RecoverExpiredLeases). Take stateMu (outermost,
	// intra-process) then the cross-process round-robin lock so this
	// Load->modify->Save cannot clobber a concurrent lease change and resurrect
	// a released lease (which would let the same account be dispatched twice).
	// No caller holds stateMu or this file lock when reaching recordQuotaResult
	// (FetchQuota and RecoverExpiredLeases release both before getting here), so
	// acquiring them does not deadlock.
	m.stateMu.Lock()
	defer m.stateMu.Unlock()
	lockPath := filepath.Join(m.StateDir, "run-round-robin.lock")
	unlock, err := m.acquireLock(lockPath)
	if err != nil {
		return err
	}
	defer unlock()
	return m.writeQuotaResultLocked(id, result, authChanged, source, reporterClientID, flipOwner)
}
func (m *Manager) writeQuotaResultLocked(id string, result quota.Result, authChanged bool, source QuotaSource, reporterClientID string, flipOwner bool) error {
	state, err := m.Load()
	if err != nil {
		return err
	}
	if state.QuotaCache == nil {
		state.QuotaCache = map[string]QuotaCache{}
	}
	fiveHour := quotaFiveHour(result)
	if source == "" {
		source = QuotaSourceCloud
	}
	state.QuotaCache[id] = QuotaCache{
		AccountID:        id,
		UpdatedAt:        time.Now(),
		Result:           result,
		FiveHour:         fiveHour,
		Source:           source,
		ReporterClientID: strings.TrimSpace(reporterClientID),
	}
	if flipOwner && source == QuotaSourceClient {
		for i := range state.Accounts {
			if state.Accounts[i].ID != id {
				continue
			}
			state.Accounts[i].OwnerMode = OwnerClient
			if strings.TrimSpace(reporterClientID) != "" {
				state.Accounts[i].OwnerClientID = strings.TrimSpace(reporterClientID)
			}
			state.Accounts[i].UpdatedAt = time.Now()
			break
		}
	}
	if result.Status == quota.StatusRefreshInvalid {
		for i := range state.Accounts {
			if state.Accounts[i].ID != id {
				continue
			}
			if state.Accounts[i].Status == StatusReady || state.Accounts[i].Status == StatusRecovering {
				state.Accounts[i].Status = StatusDrain
			}
			state.Accounts[i].LastError = result.Detail
			state.Accounts[i].UpdatedAt = time.Now()
			break
		}
	} else if result.Status == quota.StatusSupported {
		for i := range state.Accounts {
			if state.Accounts[i].ID != id {
				continue
			}
			if strings.TrimSpace(result.Plan) != "" {
				state.Accounts[i].Plan = result.Plan
			}
			if state.Accounts[i].Status == StatusRecovering {
				state.Accounts[i].Status = StatusReady
			}
			state.Accounts[i].LastError = ""
			if authChanged {
				state.Accounts[i].Generation++
			}
			state.Accounts[i].UpdatedAt = time.Now()
			break
		}
	} else if authChanged {
		for i := range state.Accounts {
			if state.Accounts[i].ID != id {
				continue
			}
			state.Accounts[i].Generation++
			state.Accounts[i].UpdatedAt = time.Now()
			break
		}
	}
	return m.Save(state)
}
func quotaFiveHour(result quota.Result) *quota.Window {
	for _, window := range result.Quotas {
		if window.Key == "five_hour" || strings.EqualFold(window.Label, "5h") {
			copy := window
			return &copy
		}
	}
	return nil
}
func quotaSourceLabel(cache QuotaCache) string {
	switch cache.Source {
	case QuotaSourceClient:
		if cache.ReporterClientID != "" {
			return "client report:" + cache.ReporterClientID
		}
		return "client report"
	case QuotaSourceCloud:
		return "cloud refresh"
	default:
		return string(cache.Source)
	}
}
