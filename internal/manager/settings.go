package manager

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

type Settings struct {
	LiveCodexHome    string `json:"liveCodexHome" toml:"live_codex_home"`
	AccountsDir      string `json:"accountsDir" toml:"accounts_dir"`
	SharedConfigPath string `json:"sharedConfigPath" toml:"-"`
	CloudURL         string `json:"cloudUrl" toml:"cloud_url"`
	CloudToken       string `json:"cloudToken" toml:"cloud_token"`
	DatabaseURL      string `json:"databaseUrl" toml:"database_url"`
}

func applyEnvironmentOverrides(settings Settings) Settings {
	if value := strings.TrimSpace(os.Getenv("CUBE_CLOUD_URL")); value != "" {
		settings.CloudURL = value
	}
	if value := strings.TrimSpace(os.Getenv("CUBE_CLOUD_TOKEN")); value != "" {
		settings.CloudToken = value
	}
	if value := firstNonEmpty(os.Getenv("CUBE_DATABASE_URL"), os.Getenv("DATABASE_URL")); value != "" {
		settings.DatabaseURL = value
	}
	return settings
}
func (m *Manager) UpdateSettings(liveCodexHome, accountsDir, sharedConfigPath string) (Settings, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Settings{}, err
	}

	settings := Settings{
		LiveCodexHome:    m.LiveCodexHome,
		AccountsDir:      m.AccountsDir,
		SharedConfigPath: m.SharedConfigPath,
		CloudURL:         m.CloudURL,
		CloudToken:       m.CloudToken,
		DatabaseURL:      m.DatabaseURL,
	}
	if strings.TrimSpace(liveCodexHome) != "" {
		settings.LiveCodexHome = expandPath(liveCodexHome, home)
	}
	if strings.TrimSpace(accountsDir) != "" {
		settings.AccountsDir = expandPath(accountsDir, home)
	}
	if settings.LiveCodexHome == "" || settings.AccountsDir == "" {
		return Settings{}, errors.New("settings paths cannot be empty")
	}

	if err := writeSettings(m.SettingsPath, settings); err != nil {
		return Settings{}, err
	}
	m.LiveCodexHome = settings.LiveCodexHome
	m.AccountsDir = settings.AccountsDir
	m.SharedConfigPath = settings.SharedConfigPath
	m.CloudURL = settings.CloudURL
	m.CloudToken = settings.CloudToken
	m.DatabaseURL = settings.DatabaseURL
	if err := m.Ensure(); err != nil {
		return Settings{}, err
	}
	return settings, nil
}
func (m *Manager) UpdateCloudSettings(cloudURL, cloudToken string) (Settings, error) {
	settings := Settings{
		LiveCodexHome:    m.LiveCodexHome,
		AccountsDir:      m.AccountsDir,
		SharedConfigPath: m.SharedConfigPath,
		CloudURL:         m.CloudURL,
		CloudToken:       m.CloudToken,
		DatabaseURL:      m.DatabaseURL,
	}
	if strings.TrimSpace(cloudURL) != "" {
		settings.CloudURL = strings.TrimSpace(cloudURL)
	}
	if strings.TrimSpace(cloudToken) != "" {
		settings.CloudToken = strings.TrimSpace(cloudToken)
	}
	if settings.LiveCodexHome == "" || settings.AccountsDir == "" {
		return Settings{}, errors.New("settings paths cannot be empty")
	}
	if err := writeSettings(m.SettingsPath, settings); err != nil {
		return Settings{}, err
	}
	m.CloudURL = settings.CloudURL
	m.CloudToken = settings.CloudToken
	return settings, nil
}
func (m *Manager) ReadSettingsText() (string, error) {
	if err := m.Ensure(); err != nil {
		return "", err
	}
	data, err := os.ReadFile(m.SettingsPath)
	if errors.Is(err, os.ErrNotExist) {
		settings := Settings{
			LiveCodexHome:    m.LiveCodexHome,
			AccountsDir:      m.AccountsDir,
			SharedConfigPath: m.SharedConfigPath,
			CloudURL:         m.CloudURL,
			CloudToken:       m.CloudToken,
			DatabaseURL:      m.DatabaseURL,
		}
		if err := writeSettings(m.SettingsPath, settings); err != nil {
			return "", err
		}
		data, err = os.ReadFile(m.SettingsPath)
	}
	if err != nil {
		return "", err
	}
	return string(data), nil
}
func (m *Manager) WriteSettingsText(raw string) (Settings, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Settings{}, err
	}

	settings, _, err := parseSettingsData([]byte(raw), Settings{
		LiveCodexHome:    m.LiveCodexHome,
		AccountsDir:      m.AccountsDir,
		SharedConfigPath: m.SharedConfigPath,
		CloudURL:         m.CloudURL,
		CloudToken:       m.CloudToken,
		DatabaseURL:      m.DatabaseURL,
	}, home)
	if err != nil {
		return Settings{}, err
	}
	if settings.LiveCodexHome == "" || settings.AccountsDir == "" {
		return Settings{}, errors.New("settings.toml must include live_codex_home and accounts_dir")
	}

	if err := writeSettings(m.SettingsPath, settings); err != nil {
		return Settings{}, err
	}
	m.LiveCodexHome = settings.LiveCodexHome
	m.AccountsDir = settings.AccountsDir
	m.SharedConfigPath = settings.SharedConfigPath
	m.CloudURL = settings.CloudURL
	m.CloudToken = settings.CloudToken
	m.DatabaseURL = settings.DatabaseURL
	if err := m.Ensure(); err != nil {
		return Settings{}, err
	}
	return settings, nil
}
func defaultSettings(home string) Settings {
	liveCodexHome := filepath.Join(home, ".codex")
	if value := strings.TrimSpace(os.Getenv("CODEX_HOME")); value != "" {
		liveCodexHome = expandPath(value, home)
	}
	return Settings{
		LiveCodexHome: liveCodexHome,
		AccountsDir:   filepath.Join(home, defaultAccountsDirName),
		CloudURL:      strings.TrimSpace(os.Getenv("CUBE_CLOUD_URL")),
		CloudToken:    strings.TrimSpace(os.Getenv("CUBE_CLOUD_TOKEN")),
		DatabaseURL:   firstNonEmpty(os.Getenv("CUBE_DATABASE_URL"), os.Getenv("DATABASE_URL")),
	}
}
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
func loadSettings(path string, defaults Settings, home string) (Settings, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if err := writeSettings(path, defaults); err != nil {
			return Settings{}, err
		}
		return defaults, nil
	}
	if err != nil {
		return Settings{}, err
	}

	settings, changed, err := parseSettingsData(data, defaults, home)
	if err != nil {
		return Settings{}, err
	}
	if changed {
		if err := writeSettings(path, settings); err != nil {
			return Settings{}, err
		}
	}
	return settings, nil
}
func parseSettingsData(data []byte, defaults Settings, home string) (Settings, bool, error) {
	settings := defaults
	if err := toml.Unmarshal(data, &settings); err != nil {
		return Settings{}, false, err
	}

	rawText := string(data)
	changed := strings.Contains(rawText, "shared_settings_path") || strings.Contains(rawText, "shared_config_path")

	settings.LiveCodexHome = expandPath(settings.LiveCodexHome, home)
	settings.AccountsDir = expandPath(settings.AccountsDir, home)
	settings.CloudURL = strings.TrimSpace(settings.CloudURL)
	settings.CloudToken = strings.TrimSpace(settings.CloudToken)
	settings.DatabaseURL = strings.TrimSpace(settings.DatabaseURL)
	return settings, changed, nil
}
func writeSettings(path string, settings Settings) error {
	data, err := toml.Marshal(settings)
	if err != nil {
		return err
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
