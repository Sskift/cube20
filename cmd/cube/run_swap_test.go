package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cube20/internal/usage"
)

func TestNewestSessionID(t *testing.T) {
	home := t.TempDir()

	older := filepath.Join(home, "sessions", "2026", "06", "06", "rollout-2026-06-06T10-00-00-aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee.jsonl")
	newer := filepath.Join(home, "sessions", "2026", "06", "07", "rollout-2026-06-07T11-00-00-11111111-2222-3333-4444-555555555555.jsonl")
	for _, p := range []string{older, newer} {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte("{}\n"), 0o600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	id, err := newestSessionID(home)
	if err != nil {
		t.Fatalf("newestSessionID: %v", err)
	}
	if want := "11111111-2222-3333-4444-555555555555"; id != want {
		t.Fatalf("session id = %q, want %q", id, want)
	}

	if _, err := newestSessionID(t.TempDir()); err == nil {
		t.Fatalf("expected error for empty codex home")
	}
}

func TestSwapDecision(t *testing.T) {
	t.Run("reached", func(t *testing.T) {
		swap, reason := swapDecision(usage.RateLimits{ReachedType: "primary", Found: true}, false)
		if !swap {
			t.Fatalf("expected swap=true for reached limit")
		}
		if !strings.Contains(reason, "hard limit") {
			t.Fatalf("reason = %q, want it to contain %q", reason, "hard limit")
		}
	})

	t.Run("cloud advise", func(t *testing.T) {
		swap, reason := swapDecision(usage.RateLimits{Found: true, FiveHourUsedPercent: 10}, true)
		if !swap {
			t.Fatalf("expected swap=true for cloud advise")
		}
		if reason != "cloud advised swap" {
			t.Fatalf("reason = %q, want %q", reason, "cloud advised swap")
		}
	})

	t.Run("local low", func(t *testing.T) {
		swap, _ := swapDecision(usage.RateLimits{Found: true, FiveHourUsedPercent: 95}, false)
		if !swap {
			t.Fatalf("expected swap=true when remaining (5) < threshold")
		}
	})

	t.Run("healthy", func(t *testing.T) {
		swap, reason := swapDecision(usage.RateLimits{Found: true, FiveHourUsedPercent: 10}, false)
		if swap {
			t.Fatalf("expected swap=false for healthy account")
		}
		if reason != "" {
			t.Fatalf("reason = %q, want empty", reason)
		}
	})

	t.Run("not found no cloud", func(t *testing.T) {
		swap, _ := swapDecision(usage.RateLimits{Found: false}, false)
		if swap {
			t.Fatalf("expected swap=false when nothing found and no cloud advise")
		}
	})
}

func TestRateLimitsToWindow(t *testing.T) {
	if w := rateLimitsToWindow(usage.RateLimits{Found: false}); w != nil {
		t.Fatalf("expected nil window when Found=false, got %+v", w)
	}

	resets := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
	w := rateLimitsToWindow(usage.RateLimits{Found: true, FiveHourUsedPercent: 30, FiveHourResetsAt: resets})
	if w == nil {
		t.Fatalf("expected non-nil window when Found=true")
	}
	if w.Key != "five_hour" {
		t.Fatalf("Key = %q, want %q", w.Key, "five_hour")
	}
	if w.Label != "5h" {
		t.Fatalf("Label = %q, want %q", w.Label, "5h")
	}
	if w.RemainingPercent != 70 {
		t.Fatalf("RemainingPercent = %v, want 70", w.RemainingPercent)
	}
	parsed, err := time.Parse(time.RFC3339, w.ResetsAt)
	if err != nil {
		t.Fatalf("ResetsAt %q not RFC3339: %v", w.ResetsAt, err)
	}
	if !parsed.Equal(resets) {
		t.Fatalf("ResetsAt parsed = %v, want %v", parsed, resets)
	}
}

func TestRateLimitsToWindowZeroReset(t *testing.T) {
	w := rateLimitsToWindow(usage.RateLimits{Found: true, FiveHourUsedPercent: 0})
	if w == nil {
		t.Fatalf("expected non-nil window")
	}
	if w.ResetsAt != "" {
		t.Fatalf("ResetsAt = %q, want empty for zero reset time", w.ResetsAt)
	}
}

func TestScrubAuthKeepsSessions(t *testing.T) {
	home := t.TempDir()
	authPath := filepath.Join(home, "auth.json")
	configPath := filepath.Join(home, "config.toml")
	sessionPath := filepath.Join(home, "sessions", "2026", "06", "07", "rollout-2026-06-07T11-00-00-11111111-2222-3333-4444-555555555555.jsonl")

	if err := os.WriteFile(authPath, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write auth: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("x"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(sessionPath), 0o700); err != nil {
		t.Fatalf("mkdir sessions: %v", err)
	}
	if err := os.WriteFile(sessionPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write session: %v", err)
	}

	if err := scrubAuth(home); err != nil {
		t.Fatalf("scrubAuth: %v", err)
	}

	if _, err := os.Stat(authPath); !os.IsNotExist(err) {
		t.Fatalf("auth.json should be removed, stat err = %v", err)
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("config.toml should be removed, stat err = %v", err)
	}
	if _, err := os.Stat(sessionPath); err != nil {
		t.Fatalf("session file should survive scrub, stat err = %v", err)
	}

	// Idempotent: scrubbing again must not error.
	if err := scrubAuth(home); err != nil {
		t.Fatalf("scrubAuth (second call): %v", err)
	}
}

func TestStableRunHomeCreates(t *testing.T) {
	base := t.TempDir()

	first, err := stableRunHome(base)
	if err != nil {
		t.Fatalf("stableRunHome: %v", err)
	}
	info, err := os.Stat(first)
	if err != nil {
		t.Fatalf("stat run home: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("run home %q is not a directory", first)
	}
	if parent := filepath.Dir(first); parent != filepath.Join(base, "runs") {
		t.Fatalf("run home parent = %q, want %q", parent, filepath.Join(base, "runs"))
	}
	if name := filepath.Base(first); len(name) != 32 {
		t.Fatalf("run id %q has length %d, want 32 hex chars", name, len(name))
	}

	second, err := stableRunHome(base)
	if err != nil {
		t.Fatalf("stableRunHome (second): %v", err)
	}
	if first == second {
		t.Fatalf("expected distinct run homes, got %q twice", first)
	}
}

func TestRateLimitsToWindowsBothWindows(t *testing.T) {
	if ws := rateLimitsToWindows(usage.RateLimits{Found: false}); ws != nil {
		t.Fatalf("expected nil when Found=false, got %+v", ws)
	}

	fiveReset := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)
	sevenReset := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	ws := rateLimitsToWindows(usage.RateLimits{
		Found:               true,
		FiveHourUsedPercent: 30,
		FiveHourResetsAt:    fiveReset,
		SevenDayUsedPercent: 10,
		SevenDayResetsAt:    sevenReset,
	})
	if len(ws) != 2 {
		t.Fatalf("expected 2 windows (5h+7d), got %d: %+v", len(ws), ws)
	}
	byKey := map[string]float64{}
	for _, w := range ws {
		byKey[w.Key] = w.RemainingPercent
	}
	if byKey["five_hour"] != 70 {
		t.Errorf("5h remaining = %v, want 70", byKey["five_hour"])
	}
	if byKey["seven_day"] != 90 {
		t.Errorf("7d remaining = %v, want 90", byKey["seven_day"])
	}
}

func TestRateLimitsToWindowsFiveHourOnly(t *testing.T) {
	// No 7d data (zero used + zero reset) -> only the 5h window is emitted.
	ws := rateLimitsToWindows(usage.RateLimits{Found: true, FiveHourUsedPercent: 20})
	if len(ws) != 1 || ws[0].Key != "five_hour" {
		t.Fatalf("expected only the 5h window, got %+v", ws)
	}
}
