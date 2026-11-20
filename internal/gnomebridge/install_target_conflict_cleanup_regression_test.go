//go:build linux

package gnomebridge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
)

func TestInstallerConflictingExtensionTargetCleansStagingWithoutChangingSettings(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "gnome-shell", "extensions")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, extensionUUID)
	const original = "user-owned non-extension file"
	if err := os.WriteFile(target, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	installer := Installer{DataHome: home, Run: func(context.Context, env.Runtime, ...string) ([]byte, error) {
		calls++
		return nil, nil
	}}
	path, err := installer.Install(context.Background(), env.FromEnviron(nil))
	if path != "" || err == nil || !strings.Contains(err.Error(), "existing extension target is not a directory") {
		t.Fatalf("conflicting destination = (%q, %v)", path, err)
	}
	if calls != 0 {
		t.Fatalf("failed installation changed settings %d times", calls)
	}
	if contents, err := os.ReadFile(target); err != nil || string(contents) != original {
		t.Fatalf("conflicting target changed: %q, %v", contents, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 || entries[0].Name() != extensionUUID {
		t.Fatalf("failed installation left staging artifacts: %v, %v", entries, err)
	}
}
