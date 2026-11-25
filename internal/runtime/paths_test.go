package runtime

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func nestedSessionPrefix() string {
	return filepath.Join(os.TempDir(), "perfuncted-xdg-")
}
func TestReapSessionDirSkipsNonManagedPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "not-managed")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	sentinel := filepath.Join(dir, "sentinel")
	if err := os.WriteFile(sentinel, []byte("keep"), 0600); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}
	if err := reapSessionDir(context.Background(), dir); err != nil {
		t.Fatalf("reapSessionDir: %v", err)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("reapSessionDir removed non-managed dir: %v", err)
	}
}

func TestIsSafeToRemoveDir(t *testing.T) {
	tmp := filepath.Clean(os.TempDir())
	if !IsSafeToRemoveDir(filepath.Join(tmp, "perfuncted-xdg-ABC123")) {
		t.Fatal("IsSafeToRemoveDir for managed dir = false, want true")
	}
	if !IsSafeToRemoveDir(filepath.Join(tmp, "TestSomething", "001", "xdg")) {
		t.Fatal("IsSafeToRemoveDir for nested tmp dir = false, want true")
	}
	if IsSafeToRemoveDir(tmp) {
		t.Fatalf("IsSafeToRemoveDir(tmp) = true, want false")
	}
	if IsSafeToRemoveDir("/") {
		t.Fatalf("IsSafeToRemoveDir(/) = true, want false")
	}
	if IsSafeToRemoveDir("") {
		t.Fatalf("IsSafeToRemoveDir(empty) = true, want false")
	}
	if IsSafeToRemoveDir("/etc") {
		t.Fatalf("IsSafeToRemoveDir(/etc) = true, want false")
	}
}

func TestIsManagedSessionDir(t *testing.T) {
	prefix := nestedSessionPrefix()
	tmp := filepath.Clean(os.TempDir())
	managed := prefix + "ABC123"
	if !IsManagedSessionDir(managed) {
		t.Fatalf("IsManagedSessionDir(%q) = false, want true", managed)
	}
	if IsManagedSessionDir(prefix) {
		t.Fatalf("IsManagedSessionDir(%q) = true, want false (no suffix)", prefix)
	}
	if IsManagedSessionDir(tmp) {
		t.Fatalf("IsManagedSessionDir(%q) = true, want false (tmp itself)", tmp)
	}
	if IsManagedSessionDir(filepath.Join(tmp, "perfuncted-xdg-foo", "bar")) {
		t.Fatalf("IsManagedSessionDir with nested path = true, want false")
	}
	if IsManagedSessionDir("/etc/passwd") {
		t.Fatalf("IsManagedSessionDir(/etc/passwd) = true, want false")
	}
	if IsManagedSessionDir("") {
		t.Fatalf("IsManagedSessionDir(empty) = true, want false")
	}
	if IsManagedSessionDir(filepath.Join(tmp, "other-dir-123")) {
		t.Fatalf("IsManagedSessionDir(other pattern) = true, want false")
	}
}
