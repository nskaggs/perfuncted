package runtime

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
	SessionLogPrefix     = "perfuncted-session-"
)

// CreateSessionLogDir creates a unique owner-private log directory.
func CreateSessionLogDir(configuredRoot string) (string, error) {
	root := configuredRoot
	if root == "" {
		root = filepath.Join(os.TempDir(), "perfuncted-logs")
		if err := ensureSessionLogRoot(root, true); err != nil {
			return "", err
		}
		CleanupSessionLogs(root, defaultSessionLogAge)
	} else {
		if err := ensureSessionLogRoot(root, false); err != nil {
			return "", err
		}
		CleanupSessionLogs(root, defaultSessionLogAge)
	}

	dir, err := os.MkdirTemp(root, SessionLogPrefix)
	if err != nil {
		return "", err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	return dir, nil
}

// OpenPrivateSessionLog creates a new private log without replacing an existing file.
func OpenPrivateSessionLog(path string) (*os.File, error) {
	// A session log is created exactly once in its fresh private directory.
	// Never follow or truncate a path that appeared there before this call.
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
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
	// An explicit, caller-selected log root must not redirect into another
	// tree through a symlinked ancestor. The default internally generated
	// log root may live beneath a short TMPDIR symlink used for Unix sockets.
	if !private && !sessionLogRootHasLiteralDirectories(root) {
		return fmt.Errorf("session log root contains a non-directory or symlink component: %s", root)
	}
	info, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("session log root is not a directory: %s", root)
	}
	if private {
		return os.Chmod(root, 0o700)
	}
	return nil
}

// CleanupSessionLogs removes expired private session logs from the specified root.
func CleanupSessionLogs(root string, maxAge time.Duration) {
	if root == "" || maxAge <= 0 {
		return
	}
	// The public cleanup entry point can be called without session startup's
	// directory validation. Never follow a root or ancestor alias into an
	// unrelated filesystem tree when deciding what to delete.
	if !sessionLogRootHasLiteralDirectories(root) {
		return
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	now := time.Now()
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), SessionLogPrefix) {
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

// sessionLogRootHasLiteralDirectories rejects symlinks in every path
// component, not just the final root. Lstat(root) alone follows a symlink in
// a parent directory, which could redirect retention into unrelated data.
func sessionLogRootHasLiteralDirectories(root string) bool {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	for dir := filepath.Clean(absolute); ; dir = filepath.Dir(dir) {
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() {
			return false
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return true
		}
	}
}

// CloseLogFile closes an acquired log, recording any failure.
func CloseLogFile(f *os.File) {
	if f != nil {
		if err := f.Close(); err != nil {
			slog.Debug("session: close log file", "error", err)
		}
	}
}
