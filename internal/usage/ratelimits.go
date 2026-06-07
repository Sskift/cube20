package usage

import (
	"bufio"
	"encoding/json"
	"os"
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
func LatestRateLimits(codexHome string) RateLimits {
	var result RateLimits
	for _, filePath := range collectFiles(codexHome) {
		scanRateLimitsFile(filePath, &result)
	}
	return result
}

func scanRateLimitsFile(filePath string, result *RateLimits) {
	file, err := os.Open(filePath)
	if err != nil {
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.Contains(line, "rate_limits") {
			continue
		}

		var value map[string]any
		if err := json.Unmarshal([]byte(line), &value); err != nil {
			continue
		}

		record, ok := parseRateLimitsRecord(value)
		if !ok {
			continue
		}

		// Keep the record with the greatest CapturedAt. A zero-time best is
		// replaced by any parsed record so we never lose a found snapshot.
		if !result.Found || result.CapturedAt.IsZero() || record.CapturedAt.After(result.CapturedAt) {
			*result = record
		}
	}
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
		Found:      true,
		CapturedAt: parseTime(value["timestamp"]),
	}
	if reached, ok := rateLimits["rate_limit_reached_type"].(string); ok {
		record.ReachedType = reached
	}
	if primary, ok := rateLimits["primary"].(map[string]any); ok {
		record.FiveHourUsedPercent = floatAt(primary, "used_percent")
		record.FiveHourResetsAt = unixSecondsAt(primary, "resets_at")
	}
	if secondary, ok := rateLimits["secondary"].(map[string]any); ok {
		record.SevenDayUsedPercent = floatAt(secondary, "used_percent")
		record.SevenDayResetsAt = unixSecondsAt(secondary, "resets_at")
	}
	return record, true
}

func floatAt(value map[string]any, key string) float64 {
	if f, ok := value[key].(float64); ok {
		return f
	}
	return 0
}

func unixSecondsAt(value map[string]any, key string) time.Time {
	if f, ok := value[key].(float64); ok && f > 0 {
		return time.Unix(int64(f), 0)
	}
	return time.Time{}
}
