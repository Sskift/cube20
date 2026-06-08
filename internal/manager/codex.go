package manager

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func (m *Manager) LoginCommand(id string) (*exec.Cmd, error) {
	account, err := m.GetAccount(id)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(account.CodexHome, 0o700); err != nil {
		return nil, err
	}
	if err := m.ensureLocalConfigLink(account.CodexHome); err != nil {
		return nil, err
	}

	cmd := exec.Command("codex", "login", "--device-auth")
	cmd.Env = withEnv(os.Environ(), "CODEX_HOME", account.CodexHome)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd, nil
}
func (m *Manager) CodexCommand(id string, args []string) (*exec.Cmd, error) {
	account, err := m.GetAccount(id)
	if err != nil {
		return nil, err
	}
	if account.Status == StatusDisabled {
		return nil, fmt.Errorf("account %q is disabled", id)
	}
	if err := os.MkdirAll(account.CodexHome, 0o700); err != nil {
		return nil, err
	}
	if err := m.ensureLocalConfigLink(account.CodexHome); err != nil {
		return nil, err
	}

	cmd := exec.Command("codex", args...)
	cmd.Env = withEnv(os.Environ(), "CODEX_HOME", account.CodexHome)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd, nil
}
func (m *Manager) syncLiveAuthToManaged(account Account) error {
	liveAuth := filepath.Join(m.LiveCodexHome, authFileName)
	managedAuth := filepath.Join(account.CodexHome, authFileName)
	if samePath(liveAuth, managedAuth) {
		return nil
	}

	liveInfo, err := os.Stat(liveAuth)
	if err != nil {
		return nil
	}
	managedInfo, err := os.Stat(managedAuth)
	if err != nil {
		return nil
	}
	if !liveInfo.ModTime().After(managedInfo.ModTime()) {
		return nil
	}

	liveIdentity := authIdentity(readAuthMetadata(liveAuth))
	managedIdentity := authIdentity(readAuthMetadata(managedAuth))
	if liveIdentity == "" || liveIdentity != managedIdentity {
		return nil
	}
	return copyFile(liveAuth, managedAuth, fileModeFor(authFileName))
}
func (m *Manager) ensureLocalConfigLink(codexHome string) error {
	if strings.TrimSpace(codexHome) == "" {
		return nil
	}
	source := CodexConfigPath(m.LiveCodexHome)
	target := filepath.Join(codexHome, configFileName)
	if samePath(source, target) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(source), 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(codexHome, 0o700); err != nil {
		return err
	}
	if _, err := os.Stat(source); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(source, []byte{}, fileModeFor(configFileName)); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	info, err := os.Lstat(target)
	if err == nil {
		if samePath(target, source) {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			if err := os.Remove(target); err != nil {
				return err
			}
		} else {
			backup, err := nextBackupPath(target)
			if err != nil {
				return err
			}
			if err := os.Rename(target, backup); err != nil {
				return err
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Symlink(source, target)
}
func nextBackupPath(path string) (string, error) {
	base := path + ".cube20.bak"
	if _, err := os.Lstat(base); errors.Is(err, os.ErrNotExist) {
		return base, nil
	} else if err != nil {
		return "", err
	}
	for i := 2; i < 1000; i++ {
		candidate := fmt.Sprintf("%s.%d", base, i)
		if _, err := os.Lstat(candidate); errors.Is(err, os.ErrNotExist) {
			return candidate, nil
		} else if err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("could not find backup path for %s", path)
}
func (m *Manager) DeployProfile(id, targetCodexHome string) ([]string, error) {
	account, err := m.GetAccount(id)
	if err != nil {
		return nil, err
	}

	if strings.TrimSpace(targetCodexHome) == "" {
		targetCodexHome = m.LiveCodexHome
	}
	if strings.TrimSpace(targetCodexHome) == "" {
		targetCodexHome, err = defaultCodexHome()
		if err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(targetCodexHome, 0o700); err != nil {
		return nil, err
	}

	written := []string{}
	authSource := filepath.Join(account.CodexHome, authFileName)
	if _, err := os.Stat(authSource); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("account %q has no auth.json", id)
		}
		return nil, err
	}
	authTarget := filepath.Join(targetCodexHome, authFileName)
	if err := copyFileWithBackup(authSource, authTarget, fileModeFor(authFileName)); err != nil {
		return nil, err
	}
	written = append(written, authTarget)

	return written, nil
}
func (m *Manager) DeployAuth(id, targetCodexHome string) (string, error) {
	written, err := m.DeployProfile(id, targetCodexHome)
	if err != nil {
		return "", err
	}
	return strings.Join(written, ", "), nil
}
func defaultCodexHome() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if value := strings.TrimSpace(os.Getenv("CODEX_HOME")); value != "" {
		return expandPath(value, home), nil
	}
	return filepath.Join(home, ".codex"), nil
}
func CodexConfigPath(codexHome string) string {
	codexHome = strings.TrimSpace(codexHome)
	if codexHome == "" {
		if value, err := defaultCodexHome(); err == nil {
			codexHome = value
		}
	}
	return filepath.Join(codexHome, configFileName)
}
