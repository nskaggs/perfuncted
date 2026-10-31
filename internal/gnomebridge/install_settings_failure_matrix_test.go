//go:build linux

package gnomebridge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
)

func TestGNOMEInstallerSettingsReadAndWriteFailuresPreservePublishedExtension(t *testing.T) {
	failure := errors.New("synthetic gsettings refused write or read")
	for _, tc := range []struct {
		name      string
		failAt    int
		disabled  string
		enabled   string
		wantStage string
	}{
		{"read installation policy", 1, "[]", "[]", "read extension-installation policy"},
		{"read user extension policy", 2, "[]", "[]", "read user-extension policy"},
		{"read disabled list", 3, "[]", "[]", "read disabled extensions"},
		{"clear disabled extension", 4, "['" + extensionUUID + "']", "[]", "clearing disabled state"},
		{"read enabled list", 4, "[]", "[]", "read enabled extensions"},
		{"write enabled list", 5, "[]", "[]", "enable extension for next session"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dataHome := t.TempDir()
			calls := 0
			runner := func(_ context.Context, _ env.Runtime, args ...string) ([]byte, error) {
				calls++
				if calls == tc.failAt {
					return nil, failure
				}
				if args[0] == "set" {
					return nil, nil
				}
				switch args[2] {
				case "allow-extension-installation":
					return []byte("true"), nil
				case "disable-user-extensions":
					return []byte("false"), nil
				case "disabled-extensions":
					return []byte(tc.disabled), nil
				case "enabled-extensions":
					return []byte(tc.enabled), nil
				default:
					t.Fatalf("unexpected settings key %q", args[2])
					return nil, nil
				}
			}
			installer := Installer{DataHome: dataHome, Run: runner}
			path, err := installer.Install(context.Background(), env.FromEnviron([]string{"HOME=" + dataHome}))
			if !errors.Is(err, failure) || !strings.Contains(err.Error(), tc.wantStage) {
				t.Fatalf("settings failure not surfaced: %v", err)
			}
			wantPath := filepath.Join(dataHome, extensionDirectory)
			if path != wantPath {
				t.Fatalf("already installed extension path not returned: %q want %q", path, wantPath)
			}
			if _, err := os.Stat(filepath.Join(path, "metadata.json")); err != nil {
				t.Fatalf("gsettings failure corrupted staged extension: %v", err)
			}
			if matches, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".perfuncted-extension-*")); len(matches) != 0 {
				t.Fatalf("gsettings failure leaked staging directories: %v", matches)
			}
		})
	}
}
