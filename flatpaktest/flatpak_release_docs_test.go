//go:build integration && linux

package flatpaktest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Guard the release journey independently of a Git checkout; these examples
// are copied by users directly from the README.
func TestFlatpakReleaseInstructionsUseOnlyPublishedAssets(t *testing.T) {
	repo := mustRepoRoot(t)
	read := func(name string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(repo, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	readme := read("README.md")
	guide := read("FLATPAK.md")
	release := read(".github/workflows/release.yml")
	for _, target := range []struct{ name, doc string }{
		{"README", readme},
		{"Flatpak guide", guide},
	} {
		for _, required := range []string{
			"flatpak install --user -y ./perfuncted.flatpak",
			"install -Dm755 ./pf-flatpak",
			"pf session check",
			"pf info",
		} {
			if !strings.Contains(target.doc, required) {
				t.Errorf("%s missing download-only first-run command %q", target.name, required)
			}
		}
		if strings.Contains(target.doc, "install -Dm755 scripts/pf-flatpak") ||
			strings.Contains(target.doc, "flatpak install --user -y ./dist/flatpak/perfuncted.flatpak") {
			t.Errorf("%s still requires an undeclared repository checkout", target.name)
		}
	}
	if strings.Contains(readme, `(FLATPAK.md)\n`) {
		t.Fatal("README contains literal newline escapes instead of rendered paragraphs")
	}
	if !strings.Contains(release, "gh release upload") ||
		!strings.Contains(release, "dist/flatpak/perfuncted.flatpak scripts/pf-flatpak") {
		t.Fatal("release workflow does not publish the bundle and host launcher as separate assets")
	}
	if !strings.Contains(guide, "RemoteDesktop") ||
		!strings.Contains(guide, "attended") {
		t.Fatal("guide must distinguish actual Wayland consent testing from unattended capability probes")
	}
}
