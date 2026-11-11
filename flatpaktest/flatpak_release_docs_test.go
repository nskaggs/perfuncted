//go:build integration && linux

package flatpaktest

import (
	"bytes"
	"os"
	"os/exec"
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
			"flatpak run io.github.nskaggs.perfuncted session check",
			"flatpak run io.github.nskaggs.perfuncted info",
			"flatpak run --filesystem=",
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
		!strings.Contains(release, "\"$RUNNER_TEMP/perfuncted.flatpak\" --clobber") {
		t.Fatal("release workflow must publish exactly one verified Flatpak bundle")
	}
	for _, document := range []struct{ name, content string }{
		{"release workflow", release},
		{"release notes", read(".github/release-notes/v1.3.0.md")},
		{"README", readme},
		{"Flatpak guide", guide},
	} {
		if strings.Contains(document.content, "scripts/pf-flatpak ") ||
			strings.Contains(document.content, "install -Dm755 ./pf-flatpak") ||
			strings.Contains(document.content, "--host-dir-ro") {
			t.Errorf("%s refers to an obsolete launcher", document.name)
		}
	}
	if _, err := os.Stat(filepath.Join(repo, "scripts", "pf-flatpak")); !os.IsNotExist(err) {
		t.Fatalf("obsolete host launcher must not exist: %v", err)
	}
	flatpakCI := read(".github/workflows/flatpak.yml")
	for _, workflow := range []struct{ name, content string }{
		{"Flatpak", flatpakCI},
		{"Release", release},
	} {
		if !strings.Contains(workflow.content, "PF_FLATPAK_EXPORT_BUNDLE: ${{ runner.temp }}/perfuncted.flatpak") {
			t.Fatalf("%s workflow must export the verified bundle outside isolated test workspace", workflow.name)
		}
	}
	if !strings.Contains(flatpakCI, "PF_FLATPAK_WLROOTS_TEST: \"1\"") ||
		!strings.Contains(release, "PF_FLATPAK_WLROOTS_TEST: \"1\"") {
		t.Fatal("installed Flatpak native Wayland capability and authority checks must run in CI and before release")
	}
	if !strings.Contains(flatpakCI, "path: ${{ runner.temp }}/perfuncted.flatpak") {
		t.Fatal("Flatpak CI artifact upload references the old unpopulated checkout path")
	}
	if strings.Index(release, "      - name: Run GoReleaser") <= strings.Index(release, "      - name: Build Flatpak bundle") {
		t.Fatal("release must pass installed Flatpak acceptance before publishing via GoReleaser")
	}
	if !strings.Contains(guide, "RemoteDesktop") ||
		!strings.Contains(guide, "attended") {
		t.Fatal("guide must distinguish actual Wayland consent testing from unattended capability probes")
	}
	if strings.Contains(release, "pf-flatpak-attended-check") {
		t.Fatal("attended desktop tests belong in the repository, not release downloads")
	}
	if strings.Contains(read(".github/release-notes/v1.3.0.md"), "pf-flatpak-attended-check") {
		t.Fatal("release notes must describe user features, not a developer-only attended helper")
	}
}

func TestFlatpakAttendedScreenshotRequiresAttendedSession(t *testing.T) {
	script := filepath.Join(mustRepoRoot(t), "scripts", "pf-flatpak-attended-check")
	help := exec.Command("bash", script, "--help")
	out, err := help.CombinedOutput()
	if err != nil || !bytes.Contains(out, []byte("capture")) || !bytes.Contains(out, []byte("capture-deny")) || !bytes.Contains(out, []byte("capture-cancel")) || !bytes.Contains(out, []byte("screenshot")) {
		t.Fatalf("attended screenshot mode is missing from help: %v %s", err, out)
	}
	cmd := exec.Command("bash", script, "capture")
	cmd.Stdin = bytes.NewBuffer(nil)
	cmd.Env = append(os.Environ(), "XDG_SESSION_TYPE=wayland", "WAYLAND_DISPLAY=wayland-test")
	out, err = cmd.CombinedOutput()
	if err == nil || !bytes.Contains(out, []byte("attended")) {
		t.Fatalf("screenshot consent check ran unattended: %v %s", err, out)
	}
}

func TestFlatpakAttendedValidatorRejectsUnattendedExecution(t *testing.T) {
	script := filepath.Join(mustRepoRoot(t), "scripts", "pf-flatpak-attended-check")
	help := exec.Command("bash", script, "--help")
	out, err := help.CombinedOutput()
	if err != nil || !bytes.Contains(out, []byte("allow")) || !bytes.Contains(out, []byte("revoke")) {
		t.Fatalf("attended validator lacks usable help: %v %s", err, out)
	}
	// No TTY is provided here, regardless of the developer's own login.
	cmd := exec.Command("bash", script, "allow")
	cmd.Stdin = bytes.NewBuffer(nil)
	cmd.Env = append(os.Environ(), "XDG_SESSION_TYPE=wayland", "WAYLAND_DISPLAY=wayland-test")
	out, err = cmd.CombinedOutput()
	if err == nil || !bytes.Contains(out, []byte("attended")) {
		t.Fatalf("interactive-only portal validator ran without consent: %v %s", err, out)
	}
}
