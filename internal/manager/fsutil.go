package manager

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func samePath(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" || b == "" {
		return false
	}
	aAbs, errA := filepath.Abs(a)
	bAbs, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	aReal, errA := filepath.EvalSymlinks(aAbs)
	bReal, errB := filepath.EvalSymlinks(bAbs)
	if errA == nil {
		aAbs = aReal
	}
	if errB == nil {
		bAbs = bReal
	}
	return filepath.Clean(aAbs) == filepath.Clean(bAbs)
}
func pathWithin(child, parent string) bool {
	childAbs, err := filepath.Abs(child)
	if err != nil {
		return false
	}
	parentAbs, err := filepath.Abs(parent)
	if err != nil {
		return false
	}
	childReal, err := filepath.EvalSymlinks(childAbs)
	if err == nil {
		childAbs = childReal
	}
	parentReal, err := filepath.EvalSymlinks(parentAbs)
	if err == nil {
		parentAbs = parentReal
	}
	rel, err := filepath.Rel(filepath.Clean(parentAbs), filepath.Clean(childAbs))
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return false
	}
	return true
}
func (m *Manager) acquireLock(lockPath string) (func(), error) {
	if strings.TrimSpace(m.DatabaseURL) != "" {
		return m.acquirePostgresLock(filepath.Base(lockPath))
	}
	// Cross-process coordination via an advisory file lock (flock) on a stable
	// lock file. Unlike the previous O_EXCL sentinel scheme, the file is never
	// deleted and its existence carries no meaning: flock coordinates via the
	// open fd. The kernel releases a flock automatically when the holding
	// process dies (SIGKILL/panic), so a crash while the lock is held can no
	// longer wedge the selector with a stale lock file. We keep the lock fd open
	// for the whole critical section and release it (LOCK_UN + close) in the
	// returned closure.
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	start := time.Now()
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
				_ = f.Close()
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			_ = f.Close()
			return nil, err
		}
		if time.Since(start) > 2*time.Second {
			_ = f.Close()
			return nil, errors.New("timeout acquiring lock for round-robin selector")
		}
		time.Sleep(50 * time.Millisecond)
	}
}
func expandPath(value, home string) string {
	value = strings.TrimSpace(value)
	if value == "~" {
		return home
	}
	if strings.HasPrefix(value, "~/") {
		return filepath.Join(home, value[2:])
	}
	return filepath.Clean(value)
}
func fileModeFor(fileName string) os.FileMode {
	return secretFileMode
}
func copyFile(source, target string, mode os.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
func copyFileWithBackup(source, target string, mode os.FileMode) error {
	if samePath(source, target) {
		return nil
	}
	if _, err := os.Stat(target); err == nil {
		backup := target + ".backup-" + time.Now().Format("20060102-150405")
		if err := copyFile(target, backup, mode); err != nil {
			return fmt.Errorf("backup existing %s: %w", filepath.Base(target), err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return copyFile(source, target, mode)
}
func withEnv(env []string, key, value string) []string {
	prefix := key + "="
	next := make([]string, 0, len(env)+1)
	replaced := false
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			next = append(next, prefix+value)
			replaced = true
		} else {
			next = append(next, item)
		}
	}
	if !replaced {
		next = append(next, prefix+value)
	}
	return next
}
