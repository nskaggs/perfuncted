package perfuncted

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	defaultSessionLogAge = 7 * 24 * time.Hour
	sessionLogPrefix     = "perfuncted-session-"
)

func createSessionLogDir(configuredRoot string) (string, error) {
	root := configuredRoot
	if root == "" {
		root = filepath.Join(os.TempDir(), "perfuncted-logs")
		if err := ensureSessionLogRoot(root, true); err != nil {
			return "", err
		}
		cleanupSessionLogRoot(root, defaultSessionLogAge)
	} else {
		if err := ensureSessionLogRoot(root, false); err != nil {
			return "", err
		}
		cleanupSessionLogRoot(root, defaultSessionLogAge)
	}

	dir, err := os.MkdirTemp(root, sessionLogPrefix)
	if err != nil {
		return "", err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	return dir, nil
}

func openPrivateSessionLog(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func ensureSessionLogRoot(root string, private bool) error {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("session log root is not a directory: %s", root)
	}
	if private {
		return os.Chmod(root, 0o700)
	}
	return nil
}

// CleanupSessionLogs removes only expired, uniquely-created session log
// directories from root. It never scans or removes arbitrary files in the
// configured parent directory.
func CleanupSessionLogs(root string, maxAge time.Duration) {
	cleanupSessionLogRoot(root, maxAge)
}

func cleanupSessionLogRoot(root string, maxAge time.Duration) {
	if root == "" || maxAge <= 0 {
		return
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	now := time.Now()
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), sessionLogPrefix) {
			continue
		}
		path := filepath.Join(root, entry.Name())
		info, err := entry.Info()
		if err != nil || now.Sub(info.ModTime()) <= maxAge {
			continue
		}
		if err := os.RemoveAll(path); err != nil {
			slog.Debug("session: remove expired log directory", "path", path, "error", err)
		}
	}
}

func logFileClose(f *os.File) {
	if f != nil {
		if err := f.Close(); err != nil {
			slog.Debug("session: close log file", "error", err)
		}
	}
}
