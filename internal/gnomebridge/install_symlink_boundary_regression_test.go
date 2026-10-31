//go:build linux

package gnomebridge

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
)

func TestGnomeExtensionInstallerRejectsSymlinkedDestinationAncestors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		linkAt string
	}{
		{"data home", "data-home"},
		{"gnome shell parent", "gnome-shell"},
		{"extension parent", "extensions"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			destination := filepath.Join(root, "data")
			outside := filepath.Join(root, "unrelated-directory")
			if err := os.MkdirAll(outside, 0700); err != nil {
				t.Fatal(err)
			}
			if tc.linkAt == "data-home" {
				if err := os.Symlink(outside, destination); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Mkdir(destination, 0700); err != nil {
					t.Fatal(err)
				}
				if tc.linkAt == "gnome-shell" {
					if err := os.Symlink(outside, filepath.Join(destination, "gnome-shell")); err != nil {
						t.Fatal(err)
					}
				} else {
					shell := filepath.Join(destination, "gnome-shell")
					if err := os.Mkdir(shell, 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(outside, filepath.Join(shell, "extensions")); err != nil {
						t.Fatal(err)
					}
				}
			}
			var invoked int
			installer := Installer{DataHome: destination, Run: func(context.Context, env.Runtime, ...string) ([]byte, error) { invoked++; return []byte("true"), nil }}
			path, err := installer.Install(context.Background(), env.FromEnviron(nil))
			if err == nil || path != "" || invoked != 0 {
				t.Fatalf("extension install followed symlink ancestor: path=%q err=%v gsettings-calls=%d", path, err, invoked)
			}
			if _, err := os.Stat(filepath.Join(outside, extensionUUID)); !os.IsNotExist(err) {
				t.Fatalf("unrelated directory was modified through symlink: %v", err)
			}
			if tc.linkAt == "data-home" {
				if _, err := os.Stat(filepath.Join(outside, "gnome-shell")); !os.IsNotExist(err) {
					t.Fatalf("unrelated data home was modified: %v", err)
				}
			}
		})
	}
}
