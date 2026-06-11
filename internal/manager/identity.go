package manager

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (m *Manager) uniqueAccountID(base string) string {
	base = sanitizeAccountID(base)
	if base == "" {
		base = "profile-" + time.Now().Format("20060102-150405")
	}
	state, err := m.Load()
	if err != nil {
		return base
	}
	used := map[string]bool{}
	for _, account := range state.Accounts {
		used[account.ID] = true
	}
	if !used[base] {
		return base
	}
	for i := 2; i < 1000; i++ {
		candidate := fmt.Sprintf("%s-%d", base, i)
		if len(candidate) > 64 {
			candidate = fmt.Sprintf("%s-%d", base[:64-len(fmt.Sprintf("-%d", i))], i)
		}
		if !used[candidate] {
			return candidate
		}
	}
	return "profile-" + time.Now().Format("20060102-150405")
}
func uniqueFromUsed(base string, used map[string]bool) string {
	base = sanitizeAccountID(base)
	if base == "" {
		base = "profile"
	}
	if !used[base] {
		return base
	}
	for i := 2; i < 1000; i++ {
		suffix := fmt.Sprintf("-%d", i)
		candidate := base
		if len(candidate)+len(suffix) > 64 {
			candidate = candidate[:64-len(suffix)]
		}
		candidate += suffix
		if !used[candidate] {
			return candidate
		}
	}
	return "profile-" + time.Now().Format("20060102-150405")
}
func accountIDs(state State) map[string]bool {
	used := map[string]bool{}
	for _, account := range state.Accounts {
		used[account.ID] = true
	}
	return used
}
func duplicateAccount(state State, id, identity string) (Account, bool) {
	for _, account := range state.Accounts {
		if account.ID == id {
			return account, true
		}
		if identity == "" {
			continue
		}
		existing := readAuthMetadata(filepath.Join(account.CodexHome, authFileName))
		if authIdentity(existing) == identity {
			return account, true
		}
	}
	return Account{}, false
}
func generatePAT() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "cube_pat_" + base64.RawURLEncoding.EncodeToString(raw), nil
}

// generateDeviceToken mints the per-device bearer the cube CLI uses. Same shape
// and entropy as a PAT — only the prefix differs so new and legacy tokens are
// distinguishable, though both validate identically by hash.
func generateDeviceToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "cube_dev_" + base64.RawURLEncoding.EncodeToString(raw), nil
}

// generateSessionToken mints the opaque value stored in the website session
// cookie. Only its sha256 hash is persisted server-side.
func generateSessionToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "cube_ses_" + base64.RawURLEncoding.EncodeToString(raw), nil
}

// pbkdf2 parameters for password hashing. Passwords are low-entropy and chosen
// by humans, so they need a slow KDF (unlike the high-entropy random tokens that
// hashToken/sha256 covers). Stored format: pbkdf2$<iter>$<saltB64>$<hashB64>.
const (
	pbkdf2Iterations = 210000
	pbkdf2KeyLen     = 32
	pbkdf2SaltLen    = 16
)

func hashPassword(password string) (string, error) {
	salt := make([]byte, pbkdf2SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, pbkdf2Iterations, pbkdf2KeyLen)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("pbkdf2$%d$%s$%s",
		pbkdf2Iterations,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// checkPassword verifies a plaintext password against a stored pbkdf2 hash in
// constant time. Returns false for any malformed/empty stored hash so a user
// with no password (login-disabled) can never be logged into.
func checkPassword(stored, password string) bool {
	if strings.TrimSpace(stored) == "" {
		return false
	}
	parts := strings.Split(stored, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2" {
		return false
	}
	var iter int
	if _, err := fmt.Sscanf(parts[1], "%d", &iter); err != nil || iter <= 0 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iter, len(want))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}

func generateLeaseID() (string, error) {
	raw := make([]byte, 18)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "lease_" + base64.RawURLEncoding.EncodeToString(raw), nil
}
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(token)))
	return fmt.Sprintf("%x", sum)
}
func subtleStringEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
func parseRFC3339(value string) time.Time {
	if strings.TrimSpace(value) == "" {
		return time.Time{}
	}
	out, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}
	}
	return out
}
func authIdentity(auth map[string]any) string {
	if tokens, ok := auth["tokens"].(map[string]any); ok {
		if accountID, ok := tokens["account_id"].(string); ok && strings.TrimSpace(accountID) != "" {
			return "account_id:" + strings.TrimSpace(accountID)
		}
		if idToken, ok := tokens["id_token"].(string); ok {
			claims := claimsFromIDToken(idToken)
			if sub, ok := claims["sub"].(string); ok && strings.TrimSpace(sub) != "" {
				return "sub:" + strings.TrimSpace(sub)
			}
			if email, ok := claims["email"].(string); ok && strings.TrimSpace(email) != "" {
				return "email:" + strings.ToLower(strings.TrimSpace(email))
			}
		}
	}
	if apiKey, ok := auth["OPENAI_API_KEY"].(string); ok && strings.TrimSpace(apiKey) != "" {
		sum := sha256.Sum256([]byte(strings.TrimSpace(apiKey)))
		return fmt.Sprintf("api_key:%x", sum)
	}
	return ""
}
func deriveIDFromAuth(auth map[string]any, label string) string {
	if tokens, ok := auth["tokens"].(map[string]any); ok {
		if accountID, ok := tokens["account_id"].(string); ok && strings.TrimSpace(accountID) != "" {
			return accountID
		}
		if idToken, ok := tokens["id_token"].(string); ok {
			claims := claimsFromIDToken(idToken)
			if sub, ok := claims["sub"].(string); ok && strings.TrimSpace(sub) != "" {
				return sub
			}
			if email, ok := claims["email"].(string); ok && strings.TrimSpace(email) != "" {
				return strings.Split(email, "@")[0]
			}
		}
	}
	if strings.TrimSpace(label) != "" {
		return label
	}
	if apiKey, ok := auth["OPENAI_API_KEY"].(string); ok && strings.TrimSpace(apiKey) != "" {
		return "api-key"
	}
	return ""
}
func deriveLabelFromAuth(auth map[string]any) string {
	if tokens, ok := auth["tokens"].(map[string]any); ok {
		if idToken, ok := tokens["id_token"].(string); ok {
			claims := claimsFromIDToken(idToken)
			for _, key := range []string{"email", "https://api.openai.com/profile_email", "sub"} {
				if value, ok := claims[key].(string); ok && strings.TrimSpace(value) != "" {
					return value
				}
			}
		}
		if accountID, ok := tokens["account_id"].(string); ok && strings.TrimSpace(accountID) != "" {
			return accountID
		}
	}
	return ""
}
func sanitizeAccountID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var builder strings.Builder
	for _, ch := range value {
		switch {
		case ch >= 'a' && ch <= 'z':
			builder.WriteRune(ch)
		case ch >= 'A' && ch <= 'Z':
			builder.WriteRune(ch)
		case ch >= '0' && ch <= '9':
			builder.WriteRune(ch)
		case ch == '-' || ch == '_':
			builder.WriteRune(ch)
		case ch == '.' || ch == '@' || ch == ' ':
			builder.WriteRune('-')
		}
		if builder.Len() >= 64 {
			break
		}
	}
	out := strings.Trim(builder.String(), "-_")
	if out == "" {
		return ""
	}
	if !((out[0] >= 'a' && out[0] <= 'z') || (out[0] >= 'A' && out[0] <= 'Z') || (out[0] >= '0' && out[0] <= '9')) {
		out = "profile-" + out
	}
	return out
}
func claimsFromIDToken(idToken string) map[string]any {
	parts := strings.Split(strings.TrimSpace(idToken), ".")
	if len(parts) < 2 {
		return map[string]any{}
	}
	payload := parts[1]
	payload = strings.ReplaceAll(payload, "-", "+")
	payload = strings.ReplaceAll(payload, "_", "/")
	for len(payload)%4 != 0 {
		payload += "="
	}
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return map[string]any{}
	}
	var claims map[string]any
	if err := json.Unmarshal(data, &claims); err != nil {
		return map[string]any{}
	}
	return claims
}
func readAuthMetadata(path string) map[string]any {
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string]any{}
	}
	var auth map[string]any
	if err := json.Unmarshal(data, &auth); err != nil {
		return map[string]any{}
	}
	return auth
}
func fileDigest(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum)
}
func prettyJSON(raw json.RawMessage) []byte {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return raw
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return raw
	}
	return append(data, '\n')
}
