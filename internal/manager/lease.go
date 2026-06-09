package manager

import (
	"context"
	"cube20/internal/quota"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func (m *Manager) SelectAccountForRun() (AccountView, error) {
	// Lock order: stateMu (outermost) THEN the cross-process file lock. We call
	// listAccountsLocked because stateMu is already held.
	m.stateMu.Lock()
	defer m.stateMu.Unlock()

	lockPath := filepath.Join(m.StateDir, "run-round-robin.lock")
	unlock, err := m.acquireLock(lockPath)
	if err != nil {
		return AccountView{}, err
	}
	defer unlock()

	accounts, err := m.listAccountsLocked()
	if err != nil {
		return AccountView{}, err
	}
	state, err := m.Load()
	if err != nil {
		return AccountView{}, err
	}

	type candidate struct {
		account AccountView
		score   float64
	}
	available := make([]candidate, 0, len(accounts))
	now := time.Now()
	for _, account := range accounts {
		evaluation := loadBalanceEligibility(account, state.QuotaCache[account.ID], now)
		if evaluation.Eligible {
			available = append(available, candidate{account: account, score: evaluation.QuotaScore})
		}
	}
	if len(available) == 0 {
		return AccountView{}, errors.New("no ready, unleased account with auth.json and available 5h quota is available")
	}
	sort.Slice(available, func(i, j int) bool {
		if !sameLoadBalanceScore(available[i].score, available[j].score) {
			return available[i].score > available[j].score
		}
		return available[i].account.ID < available[j].account.ID
	})

	roundRobin, err := m.loadRoundRobinState()
	if err != nil {
		return AccountView{}, err
	}
	selected := available[0]
	if roundRobin.LastAccountID != "" {
		topLen := loadBalanceTopGroupLen(len(available), func(i int) float64 { return available[i].score })
		for i := 0; i < topLen; i++ {
			if available[i].account.ID == roundRobin.LastAccountID {
				selected = available[(i+1)%topLen]
				break
			}
		}
	}

	if err := m.saveRoundRobinState(roundRobinState{LastAccountID: selected.account.ID}); err != nil {
		return AccountView{}, err
	}
	return selected.account, nil
}
func (m *Manager) ClaimLease(ctx context.Context, clientID, holder string, ttl time.Duration) (LeaseSnapshot, error) {
	// RecoverExpiredLeases takes stateMu itself and performs a network quota
	// re-probe with NO lock held, so it must run BEFORE we take stateMu here;
	// taking stateMu first would deadlock (stateMu is not reentrant) and would
	// also hold stateMu across the network call.
	if err := m.RecoverExpiredLeases(ctx); err != nil {
		return LeaseSnapshot{}, err
	}

	// Lock order: stateMu (outermost) THEN the cross-process file lock.
	m.stateMu.Lock()
	defer m.stateMu.Unlock()

	lockPath := filepath.Join(m.StateDir, "run-round-robin.lock")
	unlock, err := m.acquireLock(lockPath)
	if err != nil {
		return LeaseSnapshot{}, err
	}
	defer unlock()

	state, err := m.Load()
	if err != nil {
		return LeaseSnapshot{}, err
	}
	now := time.Now()
	state, _, _ = expireAccountLeases(state, now)

	type candidate struct {
		index   int
		account Account
		score   float64
	}
	available := []candidate{}
	for i, account := range state.Accounts {
		evaluation := loadBalanceEligibilityForAccount(account, m.accountAuthPresent(account), state.QuotaCache[account.ID], now)
		if !evaluation.Eligible {
			continue
		}
		available = append(available, candidate{index: i, account: account, score: evaluation.QuotaScore})
	}
	sort.Slice(available, func(i, j int) bool {
		if !sameLoadBalanceScore(available[i].score, available[j].score) {
			return available[i].score > available[j].score
		}
		return available[i].account.ID < available[j].account.ID
	})
	if len(available) == 0 {
		if err := m.Save(state); err != nil {
			return LeaseSnapshot{}, err
		}
		return LeaseSnapshot{}, errors.New("no ready, unleased account with auth.json and available 5h quota is available")
	}

	roundRobin, err := m.loadRoundRobinState()
	if err != nil {
		return LeaseSnapshot{}, err
	}
	selected := available[0]
	if roundRobin.LastAccountID != "" {
		topLen := loadBalanceTopGroupLen(len(available), func(i int) float64 { return available[i].score })
		for i, item := range available[:topLen] {
			if item.account.ID == roundRobin.LastAccountID {
				selected = available[(i+1)%topLen]
				break
			}
		}
	}

	leaseID, err := generateLeaseID()
	if err != nil {
		return LeaseSnapshot{}, err
	}
	ttl = normalizeLeaseTTL(ttl)
	account := state.Accounts[selected.index]
	account.LeaseID = leaseID
	account.LeaseClientID = strings.TrimSpace(clientID)
	account.LeaseHolder = strings.TrimSpace(holder)
	account.LeaseStartedAt = now
	account.LeaseHeartbeatAt = now
	account.LeaseExpiresAt = now.Add(ttl)
	account.UpdatedAt = now
	state.Accounts[selected.index] = account
	state.Dispatches = appendDispatchEvent(state.Dispatches, dispatchEventFromAccount(state, account, "claimed", now))

	if err := m.Save(state); err != nil {
		return LeaseSnapshot{}, err
	}
	_ = m.saveRoundRobinState(roundRobinState{LastAccountID: account.ID})

	snapshot, err := m.ExportProfileSnapshot(account.ID)
	if err != nil {
		return LeaseSnapshot{}, err
	}
	snapshot.LeaseID = leaseID
	snapshot.Generation = account.Generation
	snapshot.SourceClient = account.LeaseHolder

	lease := leaseFromAccount(account)
	return LeaseSnapshot{Lease: lease, Snapshot: snapshot}, nil
}
func (m *Manager) TouchLease(leaseID, accountID, clientID, holder string, ttl time.Duration) (Lease, error) {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()

	lockPath := filepath.Join(m.StateDir, "run-round-robin.lock")
	unlock, err := m.acquireLock(lockPath)
	if err != nil {
		return Lease{}, err
	}
	defer unlock()

	state, err := m.Load()
	if err != nil {
		return Lease{}, err
	}
	now := time.Now()
	state, _, _ = expireAccountLeases(state, now)
	index, account, err := findLeaseAccount(state, accountID, leaseID)
	if err != nil {
		_ = m.Save(state)
		return Lease{}, err
	}
	if err := validateLease(account, leaseID, clientID, now); err != nil {
		_ = m.Save(state)
		return Lease{}, err
	}
	ttl = normalizeLeaseTTL(ttl)
	account.LeaseHolder = firstNonEmpty(strings.TrimSpace(holder), account.LeaseHolder)
	account.LeaseHeartbeatAt = now
	account.LeaseExpiresAt = now.Add(ttl)
	account.UpdatedAt = now
	state.Accounts[index] = account
	if err := m.Save(state); err != nil {
		return Lease{}, err
	}
	return leaseFromAccount(account), nil
}
func (m *Manager) UpdateLeasedProfileSnapshot(snapshot ProfileSnapshot, clientID string, ttl time.Duration) (Account, error) {
	if strings.TrimSpace(snapshot.ID) == "" {
		return Account{}, errors.New("lease auth update needs account id")
	}
	if strings.TrimSpace(snapshot.LeaseID) == "" {
		return Account{}, errors.New("lease auth update needs lease id")
	}
	if len(snapshot.Auth) == 0 || string(snapshot.Auth) == "null" {
		return Account{}, errors.New("lease auth update needs auth")
	}
	if !json.Valid(snapshot.Auth) {
		return Account{}, errors.New("auth is not valid JSON")
	}
	if snapshot.Status != "" && !validAccountStatus(snapshot.Status) {
		return Account{}, fmt.Errorf("unknown status %q", snapshot.Status)
	}

	m.stateMu.Lock()
	defer m.stateMu.Unlock()

	lockPath := filepath.Join(m.StateDir, "run-round-robin.lock")
	unlock, err := m.acquireLock(lockPath)
	if err != nil {
		return Account{}, err
	}
	defer unlock()

	state, err := m.Load()
	if err != nil {
		return Account{}, err
	}
	now := time.Now()
	state, _, _ = expireAccountLeases(state, now)
	index, account, err := findLeaseAccount(state, snapshot.ID, snapshot.LeaseID)
	if err != nil {
		_ = m.Save(state)
		return Account{}, err
	}
	if err := validateLease(account, snapshot.LeaseID, clientID, now); err != nil {
		_ = m.Save(state)
		return Account{}, err
	}
	if snapshot.Generation != account.Generation {
		return Account{}, fmt.Errorf("auth generation conflict for %s: client has %d, server has %d", account.ID, snapshot.Generation, account.Generation)
	}
	if err := m.writeProfileFiles(account, snapshot.Auth); err != nil {
		return Account{}, err
	}
	if strings.TrimSpace(snapshot.Label) != "" {
		account.Label = strings.TrimSpace(snapshot.Label)
	}
	if strings.TrimSpace(snapshot.Plan) != "" {
		account.Plan = strings.TrimSpace(snapshot.Plan)
	}
	if snapshot.Status != "" {
		account.Status = snapshot.Status
	}
	account.Generation++
	account.LeaseHolder = firstNonEmpty(strings.TrimSpace(snapshot.SourceClient), account.LeaseHolder)
	account.LeaseHeartbeatAt = now
	account.LeaseExpiresAt = now.Add(normalizeLeaseTTL(ttl))
	account.UpdatedAt = now
	state.Accounts[index] = account
	if err := m.Save(state); err != nil {
		return Account{}, err
	}
	return account, nil
}

// ErrLeaseNotFound is returned by ReleaseLease when the (accountID, leaseID)
// pair does not match any current lease — typically because the lease already
// expired or was already released. Callers that treat release as idempotent
// (e.g. the sync API) can detect this with errors.Is and respond with success;
// the manager no longer silently masks it as nil, keeping the contract honest
// and consistent with TouchLease/UpdateLeasedProfileSnapshot.
var ErrLeaseNotFound = errors.New("lease not found")

func (m *Manager) ReleaseLease(accountID, leaseID, clientID string) error {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()

	lockPath := filepath.Join(m.StateDir, "run-round-robin.lock")
	unlock, err := m.acquireLock(lockPath)
	if err != nil {
		return err
	}
	defer unlock()

	state, err := m.Load()
	if err != nil {
		return err
	}
	now := time.Now()
	state, _, _ = expireAccountLeases(state, now)
	index, account, err := findLeaseAccount(state, accountID, leaseID)
	if err != nil {
		// Persist the expiry sweep above (it is legitimate state change), but
		// surface the not-found condition instead of returning nil success.
		if saveErr := m.Save(state); saveErr != nil {
			return saveErr
		}
		return fmt.Errorf("%w: %v", ErrLeaseNotFound, err)
	}
	if err := validateLease(account, leaseID, clientID, now); err != nil {
		return err
	}
	state.Dispatches = appendDispatchEvent(state.Dispatches, dispatchEventFromAccount(state, account, "released", now))
	clearAccountLease(&account)
	account.UpdatedAt = now
	state.Accounts[index] = account
	return m.Save(state)
}
func (m *Manager) AccountHasActiveLease(id string) (bool, error) {
	account, err := m.GetAccount(id)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return false, nil
		}
		return false, err
	}
	return accountLeaseActive(account, time.Now()), nil
}
func (m *Manager) RecoverExpiredLeases(ctx context.Context) error {
	// Lock order: stateMu (outermost) THEN the cross-process file lock. Both are
	// released BEFORE the per-account FetchQuota network re-probe below, because
	// (a) we must never hold a lock across a network call and (b) FetchQuota ->
	// recordQuotaResult re-acquires stateMu and the file lock, which would
	// deadlock the non-reentrant stateMu if we still held it here.
	m.stateMu.Lock()
	lockPath := filepath.Join(m.StateDir, "run-round-robin.lock")
	unlock, err := m.acquireLock(lockPath)
	if err != nil {
		m.stateMu.Unlock()
		return err
	}
	state, err := m.Load()
	if err != nil {
		unlock()
		m.stateMu.Unlock()
		return err
	}
	state, expired, changed := expireAccountLeases(state, time.Now())
	if changed {
		err = m.Save(state)
	}
	unlock()
	m.stateMu.Unlock()
	if err != nil {
		return err
	}
	for _, id := range expired {
		// Re-probe each recovered account's quota. FetchQuota persists the
		// outcome via recordQuotaResult (recovering -> ready on success, or
		// -> drain on an invalidated refresh token), so the return values are
		// intentionally discarded here. A per-account failure (e.g. a dead
		// refresh token) is a normal recovery result and must not fail the
		// whole batch, which would block ClaimLease for healthy accounts.
		_, _ = m.FetchQuota(ctx, id)
	}
	return nil
}
func accountLeaseActive(account Account, now time.Time) bool {
	return strings.TrimSpace(account.LeaseID) != "" && !account.LeaseExpiresAt.IsZero() && account.LeaseExpiresAt.After(now)
}
func leaseFromAccount(account Account) Lease {
	return Lease{
		ID:          account.LeaseID,
		AccountID:   account.ID,
		ClientID:    account.LeaseClientID,
		Holder:      account.LeaseHolder,
		Generation:  account.Generation,
		StartedAt:   account.LeaseStartedAt,
		HeartbeatAt: account.LeaseHeartbeatAt,
		ExpiresAt:   account.LeaseExpiresAt,
	}
}
func clearAccountLease(account *Account) {
	account.LeaseID = ""
	account.LeaseClientID = ""
	account.LeaseHolder = ""
	account.LeaseStartedAt = time.Time{}
	account.LeaseHeartbeatAt = time.Time{}
	account.LeaseExpiresAt = time.Time{}
}
func normalizeLeaseTTL(ttl time.Duration) time.Duration {
	if ttl < 30*time.Second {
		return 90 * time.Second
	}
	if ttl > 30*time.Minute {
		return 30 * time.Minute
	}
	return ttl
}
func expireAccountLeases(state State, now time.Time) (State, []string, bool) {
	expired := []string{}
	changed := false
	for i := range state.Accounts {
		account := state.Accounts[i]
		if strings.TrimSpace(account.LeaseID) == "" {
			continue
		}
		if account.LeaseExpiresAt.IsZero() || account.LeaseExpiresAt.After(now) {
			continue
		}
		leaseID := account.LeaseID
		state.Dispatches = appendDispatchEvent(state.Dispatches, dispatchEventFromAccount(state, account, "expired", now))
		clearAccountLease(&account)
		if account.Status == StatusReady {
			account.Status = StatusRecovering
		}
		account.LastError = fmt.Sprintf("lease %s expired at %s; recovery check pending", leaseID, now.Format(time.RFC3339))
		account.UpdatedAt = now
		state.Accounts[i] = account
		expired = append(expired, account.ID)
		changed = true
	}
	return state, expired, changed
}
func findLeaseAccount(state State, accountID, leaseID string) (int, Account, error) {
	accountID = strings.TrimSpace(accountID)
	leaseID = strings.TrimSpace(leaseID)
	for i, account := range state.Accounts {
		if accountID != "" && account.ID != accountID {
			continue
		}
		if leaseID != "" && account.LeaseID != leaseID {
			continue
		}
		return i, account, nil
	}
	if accountID != "" {
		return -1, Account{}, fmt.Errorf("lease %q for account %q not found", leaseID, accountID)
	}
	return -1, Account{}, fmt.Errorf("lease %q not found", leaseID)
}
func validateLease(account Account, leaseID, clientID string, now time.Time) error {
	leaseID = strings.TrimSpace(leaseID)
	clientID = strings.TrimSpace(clientID)
	if leaseID == "" || account.LeaseID != leaseID {
		return fmt.Errorf("account %s is not held by lease %q", account.ID, leaseID)
	}
	if !accountLeaseActive(account, now) {
		return fmt.Errorf("lease %s for account %s has expired", leaseID, account.ID)
	}
	if clientID != "" && account.LeaseClientID != "" && account.LeaseClientID != clientID {
		return fmt.Errorf("lease %s belongs to client %s", leaseID, account.LeaseClientID)
	}
	return nil
}

// verifyLeaseHolderLocked checks that accountID is currently leased by clientID
// under leaseID. The caller must already hold the round-robin lock (it reads
// state via Load without taking the lock).
func (m *Manager) verifyLeaseHolderLocked(accountID, leaseID, clientID string, now time.Time) error {
	state, err := m.Load()
	if err != nil {
		return err
	}
	for _, account := range state.Accounts {
		if account.ID != accountID {
			continue
		}
		if !accountLeaseActive(account, now) {
			return fmt.Errorf("account %s is not leased by client %s", accountID, clientID)
		}
		if strings.TrimSpace(account.LeaseID) != leaseID || strings.TrimSpace(account.LeaseClientID) != clientID {
			return fmt.Errorf("account %s is not leased by client %s", accountID, clientID)
		}
		return nil
	}
	return fmt.Errorf("account %q not found", accountID)
}

// verifyLeaseHolder is the Postgres-mode lease ownership check; it loads the
// account through the normal path (no file lock).
func (m *Manager) verifyLeaseHolder(accountID, leaseID, clientID string, now time.Time) error {
	account, err := m.GetAccount(accountID)
	if err != nil {
		return err
	}
	if !accountLeaseActive(account, now) || strings.TrimSpace(account.LeaseID) != leaseID || strings.TrimSpace(account.LeaseClientID) != clientID {
		return fmt.Errorf("account %s is not leased by client %s", accountID, clientID)
	}
	return nil
}

// ShouldSwapLease reports whether the most-constrained cached quota window (5h or
// 7d) for an account has dropped below swapRemainingThreshold, suggesting the
// holder should swap to a fresher account. Missing cache, missing windows, or an
// unsupported status returns false with a nil error.
func (m *Manager) ShouldSwapLease(accountID string) (bool, error) {
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return false, nil
	}
	state, err := m.Load()
	if err != nil {
		return false, err
	}
	cache, ok := state.QuotaCache[accountID]
	if !ok {
		return false, nil
	}
	window := cache.FiveHour
	if window == nil {
		if w := quotaFiveHour(cache.Result); w != nil {
			window = w
		}
	}
	if binding := bindingWindow(window, quotaSevenDay(cache.Result)); binding != nil {
		window = binding
	}
	if window == nil {
		return false, nil
	}
	if cache.Result.Status != "" && cache.Result.Status != quota.StatusSupported {
		return false, nil
	}
	return window.RemainingPercent < swapRemainingThreshold, nil
}
