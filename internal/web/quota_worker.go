package web

import (
	"context"
	"time"

	"cube20/internal/manager"
)

type quotaWorkerLogger func(format string, args ...any)

func StartQuotaWorker(ctx context.Context, m *manager.Manager, interval time.Duration, logf quotaWorkerLogger) {
	if ctx == nil || m == nil || interval <= 0 {
		return
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	go func() {
		runQuotaWorkerOnce(ctx, m, logf)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runQuotaWorkerOnce(ctx, m, logf)
			}
		}
	}()
}

func runQuotaWorkerOnce(ctx context.Context, m *manager.Manager, logf quotaWorkerLogger) {
	queue, err := m.RefreshQueue()
	if err != nil {
		logf("cube quota worker: refresh queue failed: %v", err)
		return
	}
	now := time.Now()
	for _, item := range queue {
		if !quotaWorkerShouldRefresh(item, now) {
			continue
		}
		if _, err := m.FetchQuota(ctx, item.AccountID); err != nil {
			logf("cube quota worker: refresh %s failed: %v", item.AccountID, err)
		}
	}
}

func quotaWorkerShouldRefresh(item manager.RefreshQueueItem, now time.Time) bool {
	if item.OwnerMode != manager.OwnerCloud {
		return false
	}
	if item.Status != manager.StatusReady {
		return false
	}
	if !item.AuthPresent || item.LeaseActive {
		return false
	}
	if item.QuotaStatus == "" {
		return true
	}
	if item.ResetsAt == "" {
		return true
	}
	resetAt, err := time.Parse(time.RFC3339, item.ResetsAt)
	if err != nil {
		return true
	}
	return !resetAt.After(now)
}
