package runtime

import (
	"os"
	"path/filepath"
	"strings"
)

// IsManagedSessionDir checks the exact managed runtime naming boundary.
func IsManagedSessionDir(dir string) bool {
	if !IsSafeToRemoveDir(dir) {
		return false
	}
	cleaned := filepath.Clean(dir)
	for _, root := range ManagedSessionTempRoots() {
		if matchesManagedSessionRoot(cleaned, root) {
			return true
		}
	}
	return false
}

// IsSafeToRemoveDir rejects broad or unrelated cleanup scopes.
func IsSafeToRemoveDir(dir string) bool {
	if dir == "" {
		return false
	}
	cleaned := filepath.Clean(dir)
	if cleaned == "/" || cleaned == "." {
		return false
	}
	tmp := filepath.Clean(os.TempDir())
	if cleaned == tmp {
		return false
	}
	if strings.HasPrefix(cleaned, tmp+string(os.PathSeparator)) {
		return true
	}
	// Only our exact managed pattern qualifies outside the caller's TMPDIR.
	return matchesManagedSessionRoot(cleaned, "/tmp")
}

// matchesManagedSessionRoot accepts only a directly nested managed runtime
// directory. Never broaden /tmp cleanup to arbitrary caller-owned paths.
func matchesManagedSessionRoot(dir, root string) bool {
	prefix := filepath.Join(root, "perfuncted-xdg-")
	if !strings.HasPrefix(dir, prefix) || len(dir) <= len(prefix) {
		return false
	}
	return !strings.Contains(dir[len(prefix):], string(os.PathSeparator))
}

// ManagedSessionTempRoots preserves the caller-selected temporary directory
// when safe, but can fall back to /tmp for Unix socket paths that are too long.
// The fallback must also be considered by stale-session cleanup.
func ManagedSessionTempRoots() []string {
	preferred := filepath.Clean(os.TempDir())
	const shortRoot = "/tmp"
	// A relative TMPDIR would produce relative managed runtime and socket
	// paths, which D-Bus cannot reliably bind and cleanup cannot own safely.
	if !filepath.IsAbs(preferred) {
		return []string{shortRoot}
	}
	if preferred == shortRoot {
		return []string{preferred}
	}
	return []string{preferred, shortRoot}
}
