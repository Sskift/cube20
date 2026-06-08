package quota

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeAuthJSON writes an auth.json into a temp CODEX_HOME and returns the home dir.
func writeAuthJSON(t *testing.T, auth authFileShape) string {
	t.Helper()
	home := t.TempDir()
	data, err := json.Marshal(auth)
	if err != nil {
		t.Fatalf("marshal auth: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "auth.json"), data, 0o600); err != nil {
		t.Fatalf("write auth.json: %v", err)
	}
	return home
}

// stubSeams temporarily replaces the networked refresh/usage seams and restores
// them when the test completes.
func stubSeams(t *testing.T, refresh func(context.Context, string, []byte, string) (refreshResponse, error), usage func(context.Context, string, string) (*usageResponse, error)) {
	t.Helper()
	origRefresh := refreshAuthFileFn
	origUsage := fetchUsageFn
	t.Cleanup(func() {
		refreshAuthFileFn = origRefresh
		fetchUsageFn = origUsage
	})
	if refresh != nil {
		refreshAuthFileFn = refresh
	}
	if usage != nil {
		fetchUsageFn = usage
	}
}

// TestFetchForCodexHome_RefreshedOAuthWithAPIKeyIsNotUnsupported reproduces the
// bug: an auth.json with an empty access token, a valid refresh token, AND an
// OpenAI API key should refresh the OAuth token and reach the usage path — it
// must NOT be classified as unsupported_api_key.
func TestFetchForCodexHome_RefreshedOAuthWithAPIKeyIsNotUnsupported(t *testing.T) {
	var auth authFileShape
	auth.OpenAIAPIKey = "sk-test-key"
	auth.Tokens.RefreshToken = "valid-refresh-token"
	auth.Tokens.AccountID = "acct-123"
	// access_token deliberately empty to force the refresh path.
	home := writeAuthJSON(t, auth)

	refreshCalled := false
	usageCalled := false
	stubSeams(t,
		func(ctx context.Context, authPath string, raw []byte, refreshToken string) (refreshResponse, error) {
			refreshCalled = true
			return refreshResponse{AccessToken: "fresh-access-token"}, nil
		},
		func(ctx context.Context, accessToken, accountID string) (*usageResponse, error) {
			usageCalled = true
			if accessToken != "fresh-access-token" {
				t.Errorf("fetchUsage got accessToken %q, want refreshed token", accessToken)
			}
			out := &usageResponse{PlanType: "pro"}
			return out, nil
		},
	)

	result, err := FetchForCodexHome(context.Background(), home, time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !refreshCalled {
		t.Fatalf("expected refresh to be attempted")
	}
	if result.Status == StatusUnsupported {
		t.Fatalf("refreshed OAuth account wrongly reported as unsupported_api_key (detail=%q)", result.Detail)
	}
	if result.Status != StatusSupported {
		t.Fatalf("expected StatusSupported, got %q (detail=%q)", result.Status, result.Detail)
	}
	if !usageCalled {
		t.Fatalf("expected fetchUsage to be reached after successful refresh")
	}
	if result.Plan != "pro" {
		t.Errorf("expected plan from usage response, got %q", result.Plan)
	}
}

// TestFetchForCodexHome_APIKeyOnlyIsUnsupported: empty access token, no refresh
// token, but an API key present → still unsupported_api_key (preserved).
func TestFetchForCodexHome_APIKeyOnlyIsUnsupported(t *testing.T) {
	var auth authFileShape
	auth.OpenAIAPIKey = "sk-test-key"
	home := writeAuthJSON(t, auth)

	stubSeams(t,
		func(ctx context.Context, authPath string, raw []byte, refreshToken string) (refreshResponse, error) {
			t.Fatalf("refresh must not be called when there is no refresh token")
			return refreshResponse{}, nil
		},
		func(ctx context.Context, accessToken, accountID string) (*usageResponse, error) {
			t.Fatalf("fetchUsage must not be called for api-key-only auth")
			return nil, nil
		},
	)

	result, err := FetchForCodexHome(context.Background(), home, time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusUnsupported {
		t.Fatalf("expected StatusUnsupported, got %q (detail=%q)", result.Status, result.Detail)
	}
}

// TestFetchForCodexHome_NoTokenNoAPIKey: empty access token, no refresh token,
// no API key → "auth.json has no OAuth access token" (preserved).
func TestFetchForCodexHome_NoTokenNoAPIKey(t *testing.T) {
	var auth authFileShape // everything empty
	home := writeAuthJSON(t, auth)

	stubSeams(t,
		func(ctx context.Context, authPath string, raw []byte, refreshToken string) (refreshResponse, error) {
			t.Fatalf("refresh must not be called with no refresh token")
			return refreshResponse{}, nil
		},
		func(ctx context.Context, accessToken, accountID string) (*usageResponse, error) {
			t.Fatalf("fetchUsage must not be called with no access token")
			return nil, nil
		},
	)

	result, err := FetchForCodexHome(context.Background(), home, time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status == StatusUnsupported {
		t.Fatalf("no-token/no-api-key must not be unsupported_api_key")
	}
	if result.Detail != "auth.json has no OAuth access token" {
		t.Fatalf("expected no-OAuth-token detail, got %q", result.Detail)
	}
}

// TestFetchForCodexHome_RefreshFailsReturnsEarly: empty access token, refresh
// token present but refresh FAILS → error/refresh-invalid handling returns
// early; must not reach usage and must not become unsupported (preserved).
func TestFetchForCodexHome_RefreshFailsReturnsEarly(t *testing.T) {
	var auth authFileShape
	auth.OpenAIAPIKey = "sk-test-key" // also has api key, but refresh path runs first
	auth.Tokens.RefreshToken = "bad-refresh-token"
	home := writeAuthJSON(t, auth)

	stubSeams(t,
		func(ctx context.Context, authPath string, raw []byte, refreshToken string) (refreshResponse, error) {
			return refreshResponse{}, errUnauthorized
		},
		func(ctx context.Context, accessToken, accountID string) (*usageResponse, error) {
			t.Fatalf("fetchUsage must not be called after a failed refresh")
			return nil, nil
		},
	)

	result, err := FetchForCodexHome(context.Background(), home, time.Unix(0, 0).UTC())
	if err == nil {
		t.Fatalf("expected an error when refresh fails")
	}
	if result.Status == StatusUnsupported {
		t.Fatalf("failed-refresh must not be classified as unsupported_api_key")
	}
	if result.Status != StatusError {
		t.Fatalf("expected StatusError on generic refresh failure, got %q", result.Status)
	}
}

// TestFetchForCodexHome_DirectAccessTokenSkipsRefresh: non-empty access token
// from the start goes straight to fetchUsage (preserved).
func TestFetchForCodexHome_DirectAccessTokenSkipsRefresh(t *testing.T) {
	var auth authFileShape
	auth.Tokens.AccessToken = "already-valid-token"
	auth.Tokens.AccountID = "acct-9"
	home := writeAuthJSON(t, auth)

	usageCalled := false
	stubSeams(t,
		func(ctx context.Context, authPath string, raw []byte, refreshToken string) (refreshResponse, error) {
			t.Fatalf("refresh must not be called when access token is present")
			return refreshResponse{}, nil
		},
		func(ctx context.Context, accessToken, accountID string) (*usageResponse, error) {
			usageCalled = true
			if accessToken != "already-valid-token" {
				t.Errorf("fetchUsage got %q, want existing token", accessToken)
			}
			return &usageResponse{PlanType: "plus"}, nil
		},
	)

	result, err := FetchForCodexHome(context.Background(), home, time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !usageCalled {
		t.Fatalf("expected fetchUsage to be called directly")
	}
	if result.Status != StatusSupported {
		t.Fatalf("expected StatusSupported, got %q (detail=%q)", result.Status, result.Detail)
	}
}

// TestFetchForCodexHome_MissingAuthFile: no auth.json → not_configured.
func TestFetchForCodexHome_MissingAuthFile(t *testing.T) {
	home := t.TempDir() // no auth.json written
	result, err := FetchForCodexHome(context.Background(), home, time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Status != StatusNotConfigured {
		t.Fatalf("expected StatusNotConfigured, got %q", result.Status)
	}
}
