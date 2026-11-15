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

// Production installation must run gsettings from the selected runtime,
// not the user's ambient desktop. Exercise the default runner against an
// isolated executable and user-data tree, never the host's GNOME settings.
func TestInstallRuntimeUsesIsolatedDefaultGSettingsRunner(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(bin, "gsettings")
	commands := filepath.Join(root, "commands.log")
	body := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$PF_TEST_COMMAND_LOG\"\nif [ \"$1\" = get ]; then\n case \"$3\" in\n allow-extension-installation) printf 'true\\n';;\n disable-user-extensions) printf 'false\\n';;\n disabled-extensions) printf '[]\\n';;\n enabled-extensions) printf \"['other@example']\\n\";;\n *) exit 4;;\n esac\nfi\n"
	if err := os.WriteFile(script, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	dataHome := filepath.Join(root, "isolated-user-data")
	rt := env.FromEnviron([]string{"PATH=" + os.Getenv("PATH"), "XDG_DATA_HOME=" + dataHome, "PF_TEST_COMMAND_LOG=" + commands})
	dest, err := InstallRuntime(context.Background(), rt)
	if err != nil {
		t.Fatalf("runtime installation failed: %v", err)
	}
	if dest != filepath.Join(dataHome, extensionDirectory) {
		t.Fatalf("wrong user-data installation: %q", dest)
	}
	if _, err := os.Stat(filepath.Join(dest, "metadata.json")); err != nil {
		t.Fatalf("bundled extension not published: %v", err)
	}
	data, err := os.ReadFile(commands)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, expected := range []string{"get org.gnome.shell allow-extension-installation", "get org.gnome.shell disable-user-extensions", "get org.gnome.shell disabled-extensions", "get org.gnome.shell enabled-extensions", "set org.gnome.shell enabled-extensions"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("missing isolated settings action %q from %q", expected, text)
		}
	}
	if strings.Count(text, "\n") != 5 {
		t.Fatalf("unexpected extra GNOME host commands: %q", text)
	}
}
