//go:build linux

package gnomebridge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
)

// A user may revoke write permission on an existing extension directory.
// Provisioning must not overwrite an existing extension or run gsettings when
// it cannot stage an atomic replacement in that directory.
func TestGNOMEInstallerRejectsUnwritableExtensionRootBeforeSettingsMutation(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory write permissions")
	}
	home := t.TempDir()
	root := filepath.Join(home, "gnome-shell", "extensions")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(root, extensionUUID)
	if err := os.Mkdir(existing, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(existing, "private-user-data")
	if err := os.WriteFile(marker, []byte("keep this content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o500); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(root, 0o700) }()
	calls := 0
	installer := Installer{DataHome: home, Run: func(context.Context, env.Runtime, ...string) ([]byte, error) {
		calls++
		return nil, errors.New("gsettings must not run after failed staging")
	}}
	path, err := installer.Install(context.Background(), env.FromEnviron(nil))
	if path != "" || !errors.Is(err, os.ErrPermission) {
		t.Fatalf("unwritable extension staging accepted: path=%q err=%v", path, err)
	}
	if calls != 0 {
		t.Fatalf("failed extension staging invoked gsettings %d times", calls)
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "keep this content" {
		t.Fatalf("failed staging changed the existing extension: data=%q err=%v", data, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 || entries[0].Name() != extensionUUID {
		t.Fatalf("failed staging left unexpected artifacts: entries=%v err=%v", entries, err)
	}
}

// Atomic replacement can succeed while removing the displaced extension
// fails. Report that cleanup failure without deleting the old inaccessible
// directory or mistaking it for the newly installed version.
func TestGNOMEAtomicReplacementReportsDisplacedExtensionCleanupDenial(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory write permissions")
	}
	parent := t.TempDir()
	stage := filepath.Join(parent, "stage")
	dest := filepath.Join(parent, extensionUUID)
	if err := os.Mkdir(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dest, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "installed"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "previous"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dest, 0o500); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(stage, 0o700) }()

	err := atomicReplaceDirectory(stage, dest)
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("failed old-extension cleanup ignored: %v", err)
	}
	if contents, err := os.ReadFile(filepath.Join(dest, "installed")); err != nil || string(contents) != "new" {
		t.Fatalf("installed content not published despite directory swap: %q %v", contents, err)
	}
	if contents, err := os.ReadFile(filepath.Join(stage, "previous")); err != nil || string(contents) != "old" {
		t.Fatalf("displaced extension lost or altered after cleanup denial: %q %v", contents, err)
	}
}
