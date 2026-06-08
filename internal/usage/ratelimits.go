package usage

import (
	"strings"
	"time"
)

// RateLimits is a snapshot of the rate_limits object emitted by codex in a
// token_count event. Percentages are 0..100; reset times are zero when absent.
type RateLimits struct {
	FiveHourUsedPercent float64
	FiveHourResetsAt    time.Time // zero if absent
	SevenDayUsedPercent float64
	SevenDayResetsAt    time.Time // zero if absent
	ReachedType         string    // "" = no hard limit; non-empty = hard limit hit
	CapturedAt          time.Time // the event's top-level timestamp
	Found               bool      // true if any token_count.rate_limits was parsed
}

// LatestRateLimits scans all rollout *.jsonl under codexHome and returns the
// most-recent (by event timestamp) rate_limits snapshot. Found=false if none.
//
// The exported signature is intentionally errorless (callers in cmd/cube depend
// on it); a truncated scan of one file (bufio.ErrTooLong) is tolerated by
// continuing on to the remaining files and relying on newest-timestamp
// selection, rather than silently trusting a single truncated file.
func LatestRateLimits(codexHome string) RateLimits {
	var result RateLimits
	for _, filePath := range collectFiles(codexHome) {
		scanRateLimitsFile(filePath, &result)
	}
	return result
}

func scanRateLimitsFile(filePath string, result *RateLimits) {
	// If scanJSONL returns an error the scan of this file was truncated
	// (e.g. an oversized line). We can't surface it through LatestRateLimits'
	// errorless signature, so we keep whatever records were parsed before the
	// truncation and let the other files / newest-timestamp selection win.
	_ = scanJSONL(filePath,
		func(line string) bool { return strings.Contains(line, "rate_limits") },
		func(value map[string]any) {
			record, ok := parseRateLimitsRecord(value)
			if !ok {
				return
			}
			// Keep the record with the greatest CapturedAt. A zero-time best is
			// replaced by any parsed record so we never lose a found snapshot.
			if !result.Found || result.CapturedAt.IsZero() || record.CapturedAt.After(result.CapturedAt) {
				*result = record
			}
		},
	)
}

func parseRateLimitsRecord(value map[string]any) (RateLimits, bool) {
	payload, ok := value["payload"].(map[string]any)
	if !ok {
		return RateLimits{}, false
	}
	if payloadType, _ := payload["type"].(string); payloadType != "token_count" {
		return RateLimits{}, false
	}
	rateLimits, ok := payload["rate_limits"].(map[string]any)
	if !ok {
		return RateLimits{}, false
	}

	record := RateLimits{
		CapturedAt: parseTime(value["timestamp"]),
	}
	if reached, ok := rateLimits["rate_limit_reached_type"].(string); ok && reached != "" {
		record.ReachedType = reached
		record.Found = true
	}
	if primary, ok := rateLimits["primary"].(map[string]any); ok {
		if pct, ok := floatNumberAt(primary, "used_percent"); ok {
			record.FiveHourUsedPercent = pct
			record.Found = true
		}
		record.FiveHourResetsAt = unixSecondsAt(primary, "resets_at")
	}
	if secondary, ok := rateLimits["secondary"].(map[string]any); ok {
		if pct, ok := floatNumberAt(secondary, "used_percent"); ok {
			record.SevenDayUsedPercent = pct
			record.Found = true
		}
		record.SevenDayResetsAt = unixSecondsAt(secondary, "resets_at")
	}
	// Found only when at least one real signal parsed (a percentage or a
	// reached type); an empty rate_limits:{} must not masquerade as a snapshot.
	return record, record.Found
}

// unixSecondsAt reads a unix-seconds value (tolerating float64/int64/json.Number
// via numberAt) and returns the corresponding Time, or zero when absent.
func unixSecondsAt(value map[string]any, key string) time.Time {
	if n := numberAt(value, key); n > 0 {
		return time.Unix(n, 0)
	}
	return time.Time{}
}
