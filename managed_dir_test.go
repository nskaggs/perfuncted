package perfuncted

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsManagedSessionDir(t *testing.T) {
	prefix := nestedSessionPrefix()
	tmp := filepath.Clean(os.TempDir())
	managed := prefix + "ABC123"
	if !isManagedSessionDir(managed) {
		t.Fatalf("isManagedSessionDir(%q) = false, want true", managed)
	}
	if isManagedSessionDir(prefix) {
		t.Fatalf("isManagedSessionDir(%q) = true, want false (no suffix)", prefix)
	}
	if isManagedSessionDir(tmp) {
		t.Fatalf("isManagedSessionDir(%q) = true, want false (tmp itself)", tmp)
	}
	if isManagedSessionDir(filepath.Join(tmp, "perfuncted-xdg-foo", "bar")) {
		t.Fatalf("isManagedSessionDir with nested path = true, want false")
	}
	if isManagedSessionDir("/etc/passwd") {
		t.Fatalf("isManagedSessionDir(/etc/passwd) = true, want false")
	}
	if isManagedSessionDir("") {
		t.Fatalf("isManagedSessionDir(empty) = true, want false")
	}
	if isManagedSessionDir(filepath.Join(tmp, "other-dir-123")) {
		t.Fatalf("isManagedSessionDir(other pattern) = true, want false")
	}
}

func TestIsSafeToRemoveDir(t *testing.T) {
	tmp := filepath.Clean(os.TempDir())
	if !isSafeToRemoveDir(filepath.Join(tmp, "perfuncted-xdg-ABC123")) {
		t.Fatal("isSafeToRemoveDir for managed dir = false, want true")
	}
	if !isSafeToRemoveDir(filepath.Join(tmp, "TestSomething", "001", "xdg")) {
		t.Fatal("isSafeToRemoveDir for nested tmp dir = false, want true")
	}
	if isSafeToRemoveDir(tmp) {
		t.Fatalf("isSafeToRemoveDir(tmp) = true, want false")
	}
	if isSafeToRemoveDir("/") {
		t.Fatalf("isSafeToRemoveDir(/) = true, want false")
	}
	if isSafeToRemoveDir("") {
		t.Fatalf("isSafeToRemoveDir(empty) = true, want false")
	}
	if isSafeToRemoveDir("/etc") {
		t.Fatalf("isSafeToRemoveDir(/etc) = true, want false")
	}
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
	reapSessionDir(dir)
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatalf("reapSessionDir removed non-managed dir: %v", err)
	}
}

func TestStopSkipsNonSafeDir(t *testing.T) {
	dir := t.TempDir()
	infra := &sessionInfra{xdgDir: "/"}
	infra.stop()
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("tmp dir stat: %v", err)
	}
	infra2Dir, err := os.MkdirTemp("", "perfuncted-xdg-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	infra2 := &sessionInfra{xdgDir: infra2Dir}
	sentinel := filepath.Join(infra2.xdgDir, "keep")
	if err := os.WriteFile(sentinel, []byte("x"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	infra2.stop()
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("expected managed dir to be removed, sentinel stat = %v", err)
	}
	if _, err := os.Stat(infra2Dir); !os.IsNotExist(err) {
		t.Fatalf("expected managed dir to be removed, dir stat = %v", err)
	}
}
