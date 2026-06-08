package web

import (
	"context"
	"sync"
	"testing"
	"time"

	"cube20/internal/manager"
)

func TestQuotaWorkerStopsOnContextCancel(t *testing.T) {
	// An uninitialized manager makes RefreshQueue fail every tick, so the
	// logger fires deterministically on each loop iteration. We use that as an
	// observable heartbeat to prove the worker keeps running, then stops once
	// its context is cancelled (Fix #8).
	m := &manager.Manager{}

	var mu sync.Mutex
	calls := 0
	logf := func(string, ...any) {
		mu.Lock()
		calls++
		mu.Unlock()
	}

	ctx, cancel := context.WithCancel(context.Background())
	StartQuotaWorker(ctx, m, 5*time.Millisecond, logf)

	// Wait until the worker has clearly ticked several times.
	waitFor(t, time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return calls >= 3
	})

	cancel()

	// Give the goroutine time to observe ctx.Done() and any in-flight tick to
	// finish, then snapshot the count.
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	afterCancel := calls
	mu.Unlock()

	// The worker must stop calling logf after cancellation.
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	final := calls
	mu.Unlock()

	if final != afterCancel {
		t.Fatalf("worker kept running after cancel: calls went %d -> %d", afterCancel, final)
	}
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("condition not met within %v", timeout)
}
