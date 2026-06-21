package quota

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
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
	primaryUsageURL         = "https://chatgpt.com/backend-api/wham/usage"
	fallbackUsageURL        = "https://chatgpt.com/api/codex/usage"
	primaryResetConsumeURL  = "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits/consume"
	fallbackResetConsumeURL = "https://chatgpt.com/api/codex/rate-limit-reset-credits/consume"
	refreshTokenURL         = "https://auth.openai.com/oauth/token"
	refreshClientID         = "app_EMoamEEZ73f0CkXaXp7hrann"
)

type Status string

const (
	StatusSupported      Status = "supported"
	StatusUnsupported    Status = "unsupported_api_key"
	StatusNotConfigured  Status = "not_configured"
	StatusRefreshInvalid Status = "refresh_token_invalidated"
	StatusError          Status = "error"
)

type Result struct {
	Status       Status        `json:"status"`
	Plan         string        `json:"plan,omitempty"`
	Account      string        `json:"account,omitempty"`
	Source       string        `json:"source,omitempty"`
	Detail       string        `json:"detail,omitempty"`
	ResetCredits *ResetCredits `json:"resetCredits,omitempty"`
	Quotas       []Window      `json:"quotas,omitempty"`
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

type ResetCredits struct {
	Available int           `json:"available"`
	Total     int           `json:"total,omitempty"`
	Credits   []ResetCredit `json:"credits,omitempty"`
}

type ResetCredit struct {
	GrantedAt string `json:"grantedAt,omitempty"`
	ExpiresAt string `json:"expiresAt,omitempty"`
	Status    string `json:"status,omitempty"`
}

func (credits *ResetCredits) UnmarshalJSON(data []byte) error {
	var raw struct {
		Available      *int          `json:"available"`
		AvailableCount *int          `json:"available_count"`
		Total          *int          `json:"total"`
		TotalCount     *int          `json:"total_count"`
		Credits        []ResetCredit `json:"credits"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.AvailableCount != nil {
		credits.Available = *raw.AvailableCount
	} else if raw.Available != nil {
		credits.Available = *raw.Available
	}
	if raw.TotalCount != nil {
		credits.Total = *raw.TotalCount
	} else if raw.Total != nil {
		credits.Total = *raw.Total
	} else if len(raw.Credits) > credits.Total {
		credits.Total = len(raw.Credits)
	}
	credits.Credits = raw.Credits
	if credits.Available < 0 {
		credits.Available = 0
	}
	if credits.Total < credits.Available {
		credits.Total = credits.Available
	}
	return nil
}

func (credit *ResetCredit) UnmarshalJSON(data []byte) error {
	var raw struct {
		GrantedAt      string `json:"grantedAt"`
		GrantedAtSnake string `json:"granted_at"`
		ExpiresAt      string `json:"expiresAt"`
		ExpiresAtSnake string `json:"expires_at"`
		Status         string `json:"status"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	credit.GrantedAt = firstNonBlank(raw.GrantedAt, raw.GrantedAtSnake)
	credit.ExpiresAt = firstNonBlank(raw.ExpiresAt, raw.ExpiresAtSnake)
	credit.Status = strings.TrimSpace(raw.Status)
	return nil
}

type authFileShape struct {
	OpenAIAPIKey string `json:"OPENAI_API_KEY"`
	Tokens       struct {
		AccessToken  string `json:"access_token"`
		IDToken      string `json:"id_token"`
		RefreshToken string `json:"refresh_token"`
		AccountID    string `json:"account_id"`
	} `json:"tokens"`
}

type refreshResponse struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
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
	ResetCredits        *ResetCredits `json:"rate_limit_reset_credits"`
	ResetCreditsCamel   *ResetCredits `json:"rateLimitResetCredits"`
	CodeReviewRateLimit struct {
		PrimaryWindow *usageWindow `json:"primary_window"`
	} `json:"code_review_rate_limit"`
}

var httpClient = &http.Client{
	Timeout: 8 * time.Second,
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ResponseHeaderTimeout: 6 * time.Second,
		TLSHandshakeTimeout:   4 * time.Second,
		IdleConnTimeout:       30 * time.Second,
		MaxIdleConnsPerHost:   2,
	},
}

var errNotFound = errors.New("codex usage endpoint returned 404")
var errUnauthorized = errors.New("unauthorized")

// Indirection seams so tests can stub the networked refresh/usage calls.
// They default to the real implementations.
var refreshAuthFileFn = refreshAuthFile
var fetchUsageFn = fetchUsage

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
	refreshToken := strings.TrimSpace(auth.Tokens.RefreshToken)

	if accessToken == "" {
		if refreshToken != "" {
			refreshed, refreshErr := refreshAuthFileFn(ctx, authPath, data, refreshToken)
			if refreshErr != nil {
				if isRefreshTokenInvalidated(refreshErr) {
					result.Status = StatusRefreshInvalid
				} else {
					result.Status = StatusError
				}
				result.Source = "auth.json refresh_token"
				result.Detail = sanitizeErr(refreshErr)
				return result, refreshErr
			}
			accessToken = strings.TrimSpace(refreshed.AccessToken)
			if refreshed.IDToken != "" {
				auth.Tokens.IDToken = refreshed.IDToken
			}
		}
		if accessToken == "" {
			if apiKey != "" {
				result.Status = StatusUnsupported
				result.Detail = "API-key Codex auth cannot expose ChatGPT subscription quota."
				return result, nil
			}
			result.Detail = "auth.json has no OAuth access token"
			return result, nil
		}
	}

	response, err := fetchUsageFn(ctx, accessToken, accountID)
	if errors.Is(err, errUnauthorized) && refreshToken != "" {
		refreshed, refreshErr := refreshAuthFileFn(ctx, authPath, data, refreshToken)
		if refreshErr != nil {
			if isRefreshTokenInvalidated(refreshErr) {
				result.Status = StatusRefreshInvalid
			} else {
				result.Status = StatusError
			}
			result.Source = "auth.json refresh_token"
			result.Detail = sanitizeErr(refreshErr)
			return result, refreshErr
		}
		if refreshed.AccessToken != "" {
			accessToken = refreshed.AccessToken
		}
		if refreshed.IDToken != "" {
			auth.Tokens.IDToken = refreshed.IDToken
		}
		response, err = fetchUsageFn(ctx, accessToken, accountID)
	}
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
	result.ResetCredits = resetCreditsFromResponse(response)
	return result, nil
}

func ConsumeRateLimitResetForCodexHome(ctx context.Context, codexHome string, now time.Time) (Result, error) {
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
	if accessToken == "" {
		if strings.TrimSpace(auth.OpenAIAPIKey) != "" {
			result.Status = StatusUnsupported
			result.Detail = "API-key Codex auth cannot consume ChatGPT Codex reset credits."
			return result, nil
		}
		result.Detail = "auth.json has no OAuth access token"
		return result, nil
	}
	accountID := strings.TrimSpace(auth.Tokens.AccountID)
	idempotencyKey, err := newRedeemRequestID()
	if err != nil {
		result.Status = StatusError
		result.Detail = "could not create reset request id"
		return result, err
	}
	if err := consumeResetCredit(ctx, accessToken, accountID, idempotencyKey); err != nil {
		result.Status = StatusError
		result.Source = "chatgpt.com/backend-api"
		result.Detail = sanitizeErr(err)
		return result, err
	}

	response, err := fetchUsageFn(ctx, accessToken, accountID)
	if err != nil {
		result.Status = StatusError
		result.Source = "chatgpt.com/backend-api"
		result.Detail = sanitizeErr(err)
		return result, err
	}
	result.Status = StatusSupported
	result.Plan = response.PlanType
	result.Account = accountFromIDToken(auth.Tokens.IDToken)
	result.Source = "chatgpt.com/backend-api"
	result.Quotas = windowsFromResponse(response, now)
	result.ResetCredits = resetCreditsFromResponse(response)
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

func consumeResetCredit(ctx context.Context, accessToken, accountID, redeemRequestID string) error {
	err := doConsumeResetCredit(ctx, primaryResetConsumeURL, accessToken, accountID, redeemRequestID)
	if err == nil {
		return nil
	}
	if errors.Is(err, errNotFound) {
		return doConsumeResetCredit(ctx, fallbackResetConsumeURL, accessToken, accountID, redeemRequestID)
	}
	return err
}

func doConsumeResetCredit(ctx context.Context, endpoint, accessToken, accountID, redeemRequestID string) error {
	payload, err := json.Marshal(map[string]string{
		"redeem_request_id": redeemRequestID,
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(string(payload)))
	if err != nil {
		return err
	}
	setCodexHeaders(req, accessToken, accountID)
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusNoContent:
	case http.StatusNotFound:
		return errNotFound
	case http.StatusUnauthorized:
		return errUnauthorized
	case http.StatusForbidden:
		return errors.New("forbidden")
	case http.StatusTooManyRequests:
		return errors.New("rate limited; try again later")
	default:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		detail := strings.TrimSpace(string(body))
		if detail == "" {
			return fmt.Errorf("codex reset returned HTTP %d", resp.StatusCode)
		}
		return fmt.Errorf("codex reset returned HTTP %d: %s", resp.StatusCode, detail)
	}

	if resp.StatusCode == http.StatusNoContent {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return nil
	}
	var out struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return fmt.Errorf("invalid reset response: %w", err)
	}
	switch strings.TrimSpace(out.Code) {
	case "", "reset", "already_redeemed":
		return nil
	case "nothing_to_reset":
		return errors.New("nothing to reset")
	case "no_credit":
		return errors.New("no reset credits available")
	default:
		return fmt.Errorf("unexpected reset response code %q", out.Code)
	}
}

func doUsage(ctx context.Context, endpoint, accessToken, accountID string) (*usageResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	setCodexHeaders(req, accessToken, accountID)

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
		return nil, errUnauthorized
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

func refreshAuthFile(ctx context.Context, authPath string, raw []byte, refreshToken string) (refreshResponse, error) {
	requestBody := map[string]string{
		"client_id":     refreshClientID,
		"grant_type":    "refresh_token",
		"refresh_token": refreshToken,
	}
	payload, err := json.Marshal(requestBody)
	if err != nil {
		return refreshResponse{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, refreshTokenURL, strings.NewReader(string(payload)))
	if err != nil {
		return refreshResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "cube20/0.1")

	resp, err := httpClient.Do(req)
	if err != nil {
		return refreshResponse{}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return refreshResponse{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return refreshResponse{}, fmt.Errorf("refresh token returned HTTP %d: %s", resp.StatusCode, parseRefreshError(body))
	}

	var refreshed refreshResponse
	if err := json.Unmarshal(body, &refreshed); err != nil {
		return refreshResponse{}, fmt.Errorf("invalid refresh response: %w", err)
	}
	if strings.TrimSpace(refreshed.AccessToken) == "" {
		return refreshResponse{}, errors.New("refresh response has no access token")
	}
	if err := persistRefreshedAuth(authPath, raw, refreshed); err != nil {
		return refreshResponse{}, err
	}
	return refreshed, nil
}

func persistRefreshedAuth(authPath string, raw []byte, refreshed refreshResponse) error {
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return err
	}
	tokens, ok := root["tokens"].(map[string]any)
	if !ok || tokens == nil {
		tokens = map[string]any{}
		root["tokens"] = tokens
	}
	if strings.TrimSpace(refreshed.IDToken) != "" {
		tokens["id_token"] = refreshed.IDToken
	}
	if strings.TrimSpace(refreshed.AccessToken) != "" {
		tokens["access_token"] = refreshed.AccessToken
	}
	if strings.TrimSpace(refreshed.RefreshToken) != "" {
		tokens["refresh_token"] = refreshed.RefreshToken
	}
	root["last_refresh"] = time.Now().UTC().Format(time.RFC3339)

	data, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmpPath := authPath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpPath, authPath)
}

func parseRefreshError(body []byte) string {
	var value map[string]any
	if err := json.Unmarshal(body, &value); err == nil {
		if errValue, ok := value["error"]; ok {
			if errMap, ok := errValue.(map[string]any); ok {
				if code, ok := errMap["code"].(string); ok && strings.TrimSpace(code) != "" {
					return code
				}
				if message, ok := errMap["message"].(string); ok && strings.TrimSpace(message) != "" {
					return message
				}
			}
			if text, ok := errValue.(string); ok && strings.TrimSpace(text) != "" {
				return text
			}
		}
		if code, ok := value["code"].(string); ok && strings.TrimSpace(code) != "" {
			return code
		}
	}
	text := strings.TrimSpace(string(body))
	if text == "" {
		return "empty response"
	}
	if len(text) > 240 {
		text = text[:240] + "..."
	}
	return text
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

func resetCreditsFromResponse(response *usageResponse) *ResetCredits {
	if response == nil {
		return nil
	}
	if response.ResetCredits != nil {
		return response.ResetCredits
	}
	return response.ResetCreditsCamel
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

func setCodexHeaders(req *http.Request, accessToken, accountID string) {
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("OpenAI-Beta", "codex-1")
	req.Header.Set("Originator", "Codex Desktop")
	req.Header.Set("User-Agent", "cube20/0.1")
	if accountID != "" {
		req.Header.Set("X-Account-Id", accountID)
		req.Header.Set("ChatClaude-Account-Id", accountID)
		req.Header.Set("ChatGPT-Account-Id", accountID)
		req.Header.Set("ChatGPT-Account-ID", accountID)
	}
}

func newRedeemRequestID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(b[:])
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32], nil
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

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if text := strings.TrimSpace(value); text != "" {
			return text
		}
	}
	return ""
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

func isRefreshTokenInvalidated(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, marker := range []string{
		"app_session_terminated",
		"refresh_token_reused",
		"invalid_grant",
		"invalid refresh",
		"token_revoked",
		"refresh token returned http 401",
		"refresh token returned http 403",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
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
