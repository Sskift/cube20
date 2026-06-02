package quota

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	primaryUsageURL  = "https://chatgpt.com/backend-api/wham/usage"
	fallbackUsageURL = "https://chatgpt.com/api/codex/usage"
)

type Status string

const (
	StatusSupported     Status = "supported"
	StatusUnsupported   Status = "unsupported_api_key"
	StatusNotConfigured Status = "not_configured"
	StatusError         Status = "error"
)

type Result struct {
	Status  Status   `json:"status"`
	Plan    string   `json:"plan,omitempty"`
	Account string   `json:"account,omitempty"`
	Source  string   `json:"source,omitempty"`
	Detail  string   `json:"detail,omitempty"`
	Quotas  []Window `json:"quotas,omitempty"`
}

type Window struct {
	Key              string  `json:"key"`
	Label            string  `json:"label"`
	UsedPercent      float64 `json:"usedPercent"`
	RemainingPercent float64 `json:"remainingPercent"`
	ResetsAt         string  `json:"resetsAt,omitempty"`
	UsedDisplay      string  `json:"usedDisplay,omitempty"`
	RemainingDisplay string  `json:"remainingDisplay,omitempty"`
	Stale            bool    `json:"stale,omitempty"`
}

type authFileShape struct {
	OpenAIAPIKey string `json:"OPENAI_API_KEY"`
	Tokens       struct {
		AccessToken string `json:"access_token"`
		IDToken     string `json:"id_token"`
		AccountID   string `json:"account_id"`
	} `json:"tokens"`
}

type usageWindow struct {
	UsedPercent        float64 `json:"used_percent"`
	ResetAtUnix        int64   `json:"reset_at"`
	LimitWindowSeconds int64   `json:"limit_window_seconds"`
}

type usageResponse struct {
	PlanType  string `json:"plan_type"`
	RateLimit struct {
		PrimaryWindow   *usageWindow `json:"primary_window"`
		SecondaryWindow *usageWindow `json:"secondary_window"`
	} `json:"rate_limit"`
	CodeReviewRateLimit struct {
		PrimaryWindow *usageWindow `json:"primary_window"`
	} `json:"code_review_rate_limit"`
}

var httpClient = &http.Client{
	Timeout: 8 * time.Second,
	Transport: &http.Transport{
		Proxy:                 nil,
		ResponseHeaderTimeout: 6 * time.Second,
		TLSHandshakeTimeout:   4 * time.Second,
		IdleConnTimeout:       30 * time.Second,
		MaxIdleConnsPerHost:   2,
	},
}

var errNotFound = errors.New("codex usage endpoint returned 404")

func FetchForCodexHome(ctx context.Context, codexHome string, now time.Time) (Result, error) {
	result := Result{
		Status: StatusNotConfigured,
		Detail: "auth.json is missing",
	}

	authPath := filepath.Join(codexHome, "auth.json")
	data, err := os.ReadFile(authPath)
	if errors.Is(err, os.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		result.Status = StatusError
		result.Detail = "could not read auth.json"
		return result, err
	}

	var auth authFileShape
	if err := json.Unmarshal(data, &auth); err != nil {
		result.Status = StatusError
		result.Detail = "auth.json is not valid JSON"
		return result, err
	}

	accessToken := strings.TrimSpace(auth.Tokens.AccessToken)
	apiKey := strings.TrimSpace(auth.OpenAIAPIKey)
	accountID := strings.TrimSpace(auth.Tokens.AccountID)

	if accessToken == "" {
		if apiKey != "" {
			result.Status = StatusUnsupported
			result.Detail = "API-key Codex auth cannot expose ChatGPT subscription quota."
			return result, nil
		}
		result.Detail = "auth.json has no OAuth access token"
		return result, nil
	}

	response, err := fetchUsage(ctx, accessToken, accountID)
	if err != nil {
		result.Status = StatusError
		result.Source = "auth.json"
		result.Detail = sanitizeErr(err)
		return result, err
	}

	result.Status = StatusSupported
	result.Detail = ""
	result.Plan = response.PlanType
	result.Account = accountFromIDToken(auth.Tokens.IDToken)
	result.Source = "chatgpt.com/backend-api"
	result.Quotas = windowsFromResponse(response, now)
	return result, nil
}

func fetchUsage(ctx context.Context, accessToken, accountID string) (*usageResponse, error) {
	out, err := doUsage(ctx, primaryUsageURL, accessToken, accountID)
	if err == nil {
		return out, nil
	}
	if errors.Is(err, errNotFound) {
		return doUsage(ctx, fallbackUsageURL, accessToken, accountID)
	}
	return nil, err
}

func doUsage(ctx context.Context, endpoint, accessToken, accountID string) (*usageResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "cube20/0.1")
	if accountID != "" {
		req.Header.Set("X-Account-Id", accountID)
		req.Header.Set("ChatClaude-Account-Id", accountID)
		req.Header.Set("ChatGPT-Account-Id", accountID)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, errNotFound
	case http.StatusUnauthorized:
		return nil, errors.New("unauthorized; re-run codex login")
	case http.StatusForbidden:
		return nil, errors.New("forbidden")
	case http.StatusTooManyRequests:
		return nil, errors.New("rate limited; try again later")
	default:
		return nil, fmt.Errorf("codex returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return nil, err
	}
	var out usageResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("invalid usage response: %w", err)
	}
	return &out, nil
}

func windowsFromResponse(response *usageResponse, now time.Time) []Window {
	if response == nil {
		return nil
	}
	windows := []Window{}
	if response.RateLimit.PrimaryWindow != nil {
		windows = append(windows, normalizeWindow("five_hour", "5h", response.RateLimit.PrimaryWindow, now))
	}
	if response.RateLimit.SecondaryWindow != nil {
		windows = append(windows, normalizeWindow("seven_day", "7d", response.RateLimit.SecondaryWindow, now))
	}
	if response.CodeReviewRateLimit.PrimaryWindow != nil {
		windows = append(windows, normalizeWindow("code_review", "Review", response.CodeReviewRateLimit.PrimaryWindow, now))
	}
	return windows
}

func normalizeWindow(key, label string, input *usageWindow, now time.Time) Window {
	used := clamp(input.UsedPercent)
	remaining := clamp(100 - used)
	window := Window{
		Key:              key,
		Label:            label,
		UsedPercent:      used,
		RemainingPercent: remaining,
		UsedDisplay:      fmt.Sprintf("%d%%", int(math.Round(used))),
		RemainingDisplay: fmt.Sprintf("%d%%", int(math.Round(remaining))),
	}
	if input.ResetAtUnix > 0 {
		reset := time.Unix(input.ResetAtUnix, 0).UTC()
		window.ResetsAt = reset.Format(time.RFC3339)
		window.Stale = now.After(reset.Add(time.Minute))
	}
	return window
}

func clamp(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 100 {
		return 100
	}
	return value
}

func sanitizeErr(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	for _, prefix := range []string{"Bearer ", "bearer ", "sk-"} {
		if i := strings.Index(msg, prefix); i >= 0 {
			return msg[:i] + "[redacted]"
		}
	}
	return msg
}

func accountFromIDToken(idToken string) string {
	idToken = strings.TrimSpace(idToken)
	if idToken == "" {
		return ""
	}
	parts := strings.Split(idToken, ".")
	if len(parts) < 2 {
		return ""
	}
	payload := parts[1]
	payload = strings.ReplaceAll(payload, "-", "+")
	payload = strings.ReplaceAll(payload, "_", "/")
	for len(payload)%4 != 0 {
		payload += "="
	}
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return ""
	}
	var claims map[string]any
	if err := json.Unmarshal(data, &claims); err != nil {
		return ""
	}
	if sub, ok := claims["sub"].(string); ok && strings.TrimSpace(sub) != "" {
		return sub
	}
	for _, key := range []string{"email", "https://api.openai.com/profile_email"} {
		if email, ok := claims[key].(string); ok && strings.TrimSpace(email) != "" {
			return redactEmail(email)
		}
	}
	return ""
}

func redactEmail(email string) string {
	at := strings.IndexByte(email, '@')
	if at <= 1 {
		return "***"
	}
	return email[:1] + "***" + email[at:]
}
