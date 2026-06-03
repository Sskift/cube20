package usage

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Summary struct {
	Status       string       `json:"status"`
	Detail       string       `json:"detail,omitempty"`
	FilesScanned int          `json:"filesScanned"`
	Events       int          `json:"events"`
	Today        Tokens       `json:"today"`
	SevenDays    Tokens       `json:"sevenDays"`
	AllTime      Tokens       `json:"allTime"`
	LatestAt     string       `json:"latestAt,omitempty"`
	LatestModel  string       `json:"latestModel,omitempty"`
	Models       []ModelUsage `json:"models,omitempty"`
	models       map[string]*ModelUsage
}

type ModelUsage struct {
	Model     string `json:"model"`
	Today     Tokens `json:"today"`
	SevenDays Tokens `json:"sevenDays"`
	AllTime   Tokens `json:"allTime"`
	LatestAt  string `json:"latestAt,omitempty"`
}

type Tokens struct {
	Input       int64 `json:"input"`
	CachedInput int64 `json:"cachedInput"`
	Output      int64 `json:"output"`
	Total       int64 `json:"total"`
}

type cumulativeTokens struct {
	input       int64
	cachedInput int64
	output      int64
}

type fileState struct {
	currentModel string
	prevTotal    *cumulativeTokens
}

func SummarizeCodexHome(codexHome string, now time.Time) Summary {
	files := collectFiles(codexHome)
	result := Summary{
		Status:       "ok",
		FilesScanned: len(files),
	}
	if len(files) == 0 {
		result.Detail = "no Codex session files"
		return result
	}

	startToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	startSevenDays := now.Add(-7 * 24 * time.Hour)

	for _, filePath := range files {
		parseFile(filePath, startToday, startSevenDays, &result)
	}
	if result.Events == 0 {
		result.Detail = "no token_count events"
	}
	result.finishModels()
	return result
}

func collectFiles(codexHome string) []string {
	files := []string{}
	for _, root := range []string{
		filepath.Join(codexHome, "sessions"),
		filepath.Join(codexHome, "archived_sessions"),
	} {
		_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry == nil || entry.IsDir() {
				return nil
			}
			if filepath.Ext(path) == ".jsonl" {
				files = append(files, path)
			}
			return nil
		})
	}
	sort.Strings(files)
	return files
}

func parseFile(filePath string, startToday, startSevenDays time.Time, result *Summary) {
	file, err := os.Open(filePath)
	if err != nil {
		return
	}
	defer file.Close()

	state := fileState{currentModel: "unknown"}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.Contains(line, "\"event_msg\"") &&
			!strings.Contains(line, "\"turn_context\"") &&
			!strings.Contains(line, "\"session_meta\"") {
			continue
		}
		if strings.Contains(line, "\"event_msg\"") && !strings.Contains(line, "\"token_count\"") {
			continue
		}

		var value map[string]any
		if err := json.Unmarshal([]byte(line), &value); err != nil {
			continue
		}

		eventType, _ := value["type"].(string)
		switch eventType {
		case "turn_context":
			if payload, ok := value["payload"].(map[string]any); ok {
				if model, ok := stringAt(payload, "model"); ok {
					state.currentModel = normalizeModel(model)
				} else if info, ok := payload["info"].(map[string]any); ok {
					if model, ok := stringAt(info, "model"); ok {
						state.currentModel = normalizeModel(model)
					}
				}
			}
		case "event_msg":
			parseTokenEvent(value, &state, startToday, startSevenDays, result)
		}
	}
}

func parseTokenEvent(value map[string]any, state *fileState, startToday, startSevenDays time.Time, result *Summary) {
	payload, ok := value["payload"].(map[string]any)
	if !ok {
		return
	}
	if payloadType, _ := payload["type"].(string); payloadType != "token_count" {
		return
	}
	info, ok := payload["info"].(map[string]any)
	if !ok {
		return
	}

	if model, ok := stringAt(info, "model"); ok {
		state.currentModel = normalizeModel(model)
	} else if model, ok := stringAt(info, "model_name"); ok {
		state.currentModel = normalizeModel(model)
	} else if model, ok := stringAt(payload, "model"); ok {
		state.currentModel = normalizeModel(model)
	}

	var delta cumulativeTokens
	if rawTotal, ok := info["total_token_usage"].(map[string]any); ok {
		current := parseTokens(rawTotal)
		if state.prevTotal == nil {
			delta = current
		} else {
			delta = cumulativeTokens{
				input:       maxInt64(0, current.input-state.prevTotal.input),
				cachedInput: maxInt64(0, current.cachedInput-state.prevTotal.cachedInput),
				output:      maxInt64(0, current.output-state.prevTotal.output),
			}
		}
		state.prevTotal = &current
	} else if rawLast, ok := info["last_token_usage"].(map[string]any); ok {
		delta = parseTokens(rawLast)
	} else {
		return
	}

	if delta.input == 0 && delta.cachedInput == 0 && delta.output == 0 {
		return
	}
	if delta.cachedInput > delta.input {
		delta.cachedInput = delta.input
	}

	eventTime := parseTime(value["timestamp"])
	addTokens(&result.AllTime, delta)
	if !eventTime.IsZero() {
		if eventTime.After(startSevenDays) || eventTime.Equal(startSevenDays) {
			addTokens(&result.SevenDays, delta)
		}
		if eventTime.After(startToday) || eventTime.Equal(startToday) {
			addTokens(&result.Today, delta)
		}
		if result.LatestAt == "" || eventTime.After(parseTime(result.LatestAt)) {
			result.LatestAt = eventTime.Format(time.RFC3339)
			result.LatestModel = state.currentModel
		}
	}
	addModelTokens(result, state.currentModel, delta, eventTime, startToday, startSevenDays)
	result.Events++
}

func parseTokens(value map[string]any) cumulativeTokens {
	return cumulativeTokens{
		input:       numberAt(value, "input_tokens"),
		cachedInput: firstNumberAt(value, "cached_input_tokens", "cache_read_input_tokens"),
		output:      numberAt(value, "output_tokens"),
	}
}

func addTokens(target *Tokens, delta cumulativeTokens) {
	target.Input += delta.input
	target.CachedInput += delta.cachedInput
	target.Output += delta.output
	target.Total += delta.input + delta.output
}

func addModelTokens(result *Summary, model string, delta cumulativeTokens, eventTime, startToday, startSevenDays time.Time) {
	model = normalizeModel(model)
	if model == "" {
		model = "unknown"
	}
	if result.models == nil {
		result.models = map[string]*ModelUsage{}
	}
	bucket := result.models[model]
	if bucket == nil {
		bucket = &ModelUsage{Model: model}
		result.models[model] = bucket
	}
	addTokens(&bucket.AllTime, delta)
	if !eventTime.IsZero() {
		if eventTime.After(startSevenDays) || eventTime.Equal(startSevenDays) {
			addTokens(&bucket.SevenDays, delta)
		}
		if eventTime.After(startToday) || eventTime.Equal(startToday) {
			addTokens(&bucket.Today, delta)
		}
		if bucket.LatestAt == "" || eventTime.After(parseTime(bucket.LatestAt)) {
			bucket.LatestAt = eventTime.Format(time.RFC3339)
		}
	}
}

func (s *Summary) finishModels() {
	if len(s.models) == 0 {
		return
	}
	s.Models = make([]ModelUsage, 0, len(s.models))
	for _, model := range s.models {
		s.Models = append(s.Models, *model)
	}
	sort.Slice(s.Models, func(i, j int) bool {
		if s.Models[i].SevenDays.Total != s.Models[j].SevenDays.Total {
			return s.Models[i].SevenDays.Total > s.Models[j].SevenDays.Total
		}
		return s.Models[i].Model < s.Models[j].Model
	})
	s.models = nil
}

func stringAt(value map[string]any, key string) (string, bool) {
	text, ok := value[key].(string)
	if !ok || strings.TrimSpace(text) == "" {
		return "", false
	}
	return text, true
}

func firstNumberAt(value map[string]any, keys ...string) int64 {
	for _, key := range keys {
		if n := numberAt(value, key); n > 0 {
			return n
		}
	}
	return 0
}

func numberAt(value map[string]any, key string) int64 {
	switch raw := value[key].(type) {
	case float64:
		if raw > 0 {
			return int64(raw)
		}
	case int64:
		if raw > 0 {
			return raw
		}
	case json.Number:
		if n, err := raw.Int64(); err == nil && n > 0 {
			return n
		}
	}
	return 0
}

func parseTime(value any) time.Time {
	text, ok := value.(string)
	if !ok || strings.TrimSpace(text) == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		return time.Time{}
	}
	return t
}

func normalizeModel(raw string) string {
	name := strings.ToLower(strings.TrimSpace(raw))
	if idx := strings.LastIndex(name, "/"); idx >= 0 {
		name = name[idx+1:]
	}
	return name
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
