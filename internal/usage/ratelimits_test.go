package usage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeJSONL(t *testing.T, path string, lines []map[string]any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	var buf []byte
	for _, line := range lines {
		data, err := json.Marshal(line)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		buf = append(buf, data...)
		buf = append(buf, '\n')
	}
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func tokenCountLine(timestamp string, primary, secondary map[string]any, reached any) map[string]any {
	rateLimits := map[string]any{
		"rate_limit_reached_type": reached,
	}
	if primary != nil {
		rateLimits["primary"] = primary
	}
	if secondary != nil {
		rateLimits["secondary"] = secondary
	}
	return map[string]any{
		"timestamp": timestamp,
		"type":      "event_msg",
		"payload": map[string]any{
			"type":        "token_count",
			"rate_limits": rateLimits,
		},
	}
}

func TestLatestRateLimitsPicksNewest(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "sessions", "2026", "06", "07", "rollout-x-uuid.jsonl")

	older := tokenCountLine(
		"2026-06-07T10:00:00.000Z",
		map[string]any{"used_percent": 20.0, "window_minutes": 300, "resets_at": 1780000000},
		nil,
		nil,
	)
	newer := tokenCountLine(
		"2026-06-07T11:00:00.000Z",
		map[string]any{"used_percent": 80.0, "window_minutes": 300, "resets_at": 1780003600},
		map[string]any{"used_percent": 55.0, "window_minutes": 10080, "resets_at": 1781000000},
		"primary",
	)
	writeJSONL(t, path, []map[string]any{older, newer})

	got := LatestRateLimits(tmp)

	if !got.Found {
		t.Fatalf("expected Found=true")
	}
	if got.FiveHourUsedPercent != 80 {
		t.Errorf("FiveHourUsedPercent = %v, want 80", got.FiveHourUsedPercent)
	}
	if !got.FiveHourResetsAt.Equal(time.Unix(1780003600, 0)) {
		t.Errorf("FiveHourResetsAt = %v, want %v", got.FiveHourResetsAt, time.Unix(1780003600, 0))
	}
	if got.SevenDayUsedPercent != 55 {
		t.Errorf("SevenDayUsedPercent = %v, want 55", got.SevenDayUsedPercent)
	}
	if !got.SevenDayResetsAt.Equal(time.Unix(1781000000, 0)) {
		t.Errorf("SevenDayResetsAt = %v, want %v", got.SevenDayResetsAt, time.Unix(1781000000, 0))
	}
	if got.ReachedType != "primary" {
		t.Errorf("ReachedType = %q, want %q", got.ReachedType, "primary")
	}
	wantCaptured, err := time.Parse(time.RFC3339Nano, "2026-06-07T11:00:00.000Z")
	if err != nil {
		t.Fatalf("parse expected timestamp: %v", err)
	}
	if !got.CapturedAt.Equal(wantCaptured) {
		t.Errorf("CapturedAt = %v, want %v", got.CapturedAt, wantCaptured)
	}
}

func TestLatestRateLimitsEmpty(t *testing.T) {
	got := LatestRateLimits(t.TempDir())
	if got.Found {
		t.Errorf("expected Found=false on empty dir")
	}
	if got.ReachedType != "" {
		t.Errorf("ReachedType = %q, want empty", got.ReachedType)
	}
}

func TestLatestRateLimitsSkipsNonTokenCount(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "sessions", "2026", "06", "07", "rollout-y-uuid.jsonl")

	nonToken := map[string]any{
		"timestamp": "2026-06-07T09:00:00.000Z",
		"type":      "event_msg",
		"payload": map[string]any{
			"type":    "agent_message",
			"message": "hello",
		},
	}
	valid := tokenCountLine(
		"2026-06-07T09:30:00.000Z",
		map[string]any{"used_percent": 12.0, "window_minutes": 300, "resets_at": 1780005000},
		map[string]any{"used_percent": 34.0, "window_minutes": 10080, "resets_at": 1781005000},
		nil,
	)
	writeJSONL(t, path, []map[string]any{nonToken, valid})

	got := LatestRateLimits(tmp)

	if !got.Found {
		t.Fatalf("expected Found=true")
	}
	if got.FiveHourUsedPercent != 12 {
		t.Errorf("FiveHourUsedPercent = %v, want 12", got.FiveHourUsedPercent)
	}
	if got.SevenDayUsedPercent != 34 {
		t.Errorf("SevenDayUsedPercent = %v, want 34", got.SevenDayUsedPercent)
	}
	if got.ReachedType != "" {
		t.Errorf("ReachedType = %q, want empty", got.ReachedType)
	}
}

func TestLatestRateLimitsReachedNullStaysEmpty(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "sessions", "2026", "06", "07", "rollout-z-uuid.jsonl")

	line := tokenCountLine(
		"2026-06-07T08:00:00.000Z",
		map[string]any{"used_percent": 5.0, "window_minutes": 300, "resets_at": 1780002000},
		nil,
		nil,
	)
	writeJSONL(t, path, []map[string]any{line})

	got := LatestRateLimits(tmp)

	if !got.Found {
		t.Fatalf("expected Found=true")
	}
	if got.ReachedType != "" {
		t.Errorf("ReachedType = %q, want empty", got.ReachedType)
	}
}

// FIX 5: an empty rate_limits object must NOT be reported as Found. A phantom
// zero-percent snapshot would otherwise mislead swapDecision.
func TestLatestRateLimitsEmptyRateLimitsNotFound(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "sessions", "2026", "06", "07", "rollout-empty.jsonl")

	// token_count line that contains the rate_limits key but nothing parseable.
	line := map[string]any{
		"timestamp": "2026-06-07T08:00:00.000Z",
		"type":      "event_msg",
		"payload": map[string]any{
			"type":        "token_count",
			"rate_limits": map[string]any{},
		},
	}
	writeJSONL(t, path, []map[string]any{line})

	got := LatestRateLimits(tmp)
	if got.Found {
		t.Errorf("expected Found=false for empty rate_limits, got %+v", got)
	}
}

// FIX 5: rate_limit_reached_type alone (a real signal) counts as Found even when
// no primary/secondary block is present.
func TestLatestRateLimitsReachedOnlyIsFound(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "sessions", "2026", "06", "07", "rollout-reached.jsonl")

	line := map[string]any{
		"timestamp": "2026-06-07T08:00:00.000Z",
		"type":      "event_msg",
		"payload": map[string]any{
			"type": "token_count",
			"rate_limits": map[string]any{
				"rate_limit_reached_type": "primary",
			},
		},
	}
	writeJSONL(t, path, []map[string]any{line})

	got := LatestRateLimits(tmp)
	if !got.Found {
		t.Fatalf("expected Found=true when reached type is set")
	}
	if got.ReachedType != "primary" {
		t.Errorf("ReachedType = %q, want primary", got.ReachedType)
	}
}

// FIX 4: rate_limits numbers encoded as json.Number / int (not just float64)
// must still parse, matching codex.go's tolerant numberAt behavior. We build the
// record map directly because encoding/json yields float64 from text; the
// json.Number / int64 paths only arise from programmatically-built maps (and
// guard against float64-only helpers regressing).
func TestParseRateLimitsRecordParsesNonFloatNumbers(t *testing.T) {
	value := map[string]any{
		"timestamp": "2026-06-07T08:00:00.000Z",
		"payload": map[string]any{
			"type": "token_count",
			"rate_limits": map[string]any{
				"primary": map[string]any{
					"used_percent": json.Number("42"),
					"resets_at":    json.Number("1780003600"),
				},
				"secondary": map[string]any{
					"used_percent": int64(7),
					"resets_at":    int64(1781000000),
				},
			},
		},
	}

	got, ok := parseRateLimitsRecord(value)
	if !ok || !got.Found {
		t.Fatalf("expected parsed record, ok=%v found=%v", ok, got.Found)
	}
	if got.FiveHourUsedPercent != 42 {
		t.Errorf("FiveHourUsedPercent = %v, want 42", got.FiveHourUsedPercent)
	}
	if got.SevenDayUsedPercent != 7 {
		t.Errorf("SevenDayUsedPercent = %v, want 7", got.SevenDayUsedPercent)
	}
	if !got.FiveHourResetsAt.Equal(time.Unix(1780003600, 0)) {
		t.Errorf("FiveHourResetsAt = %v, want %v", got.FiveHourResetsAt, time.Unix(1780003600, 0))
	}
	if !got.SevenDayResetsAt.Equal(time.Unix(1781000000, 0)) {
		t.Errorf("SevenDayResetsAt = %v, want %v", got.SevenDayResetsAt, time.Unix(1781000000, 0))
	}
}
