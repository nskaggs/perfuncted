//go:build integration && linux

package flatpaktest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/x11test"
)

// testInstalledFlatpakCLI validates the distributed bundle, not the host Go
// binary. The harness keeps Flatpak's HOME and D-Bus isolated from real users.
func testInstalledFlatpakCLI(t *testing.T, h *harness, sourceRoot, testRoot string) {
	t.Helper()
	run := func(t *testing.T, args ...string) (string, string) {
		t.Helper()
		stdout, stderr, code := h.run(t, sourceRoot, "run", args...)
		if code != 0 {
			t.Fatalf("flatpak run %v returned %d: stdout=%q stderr=%q", args, code, stdout, stderr)
		}
		return stdout, stderr
	}
	t.Run("help and command discovery", func(t *testing.T) {
		out, _ := run(t, appID, "--help")
		for _, cmd := range []string{"a11y", "clipboard", "completion", "docs", "find", "info", "input", "output", "run", "screen", "session", "window"} {
			if !strings.Contains(out, cmd+" ") {
				t.Errorf("packaged CLI help missing %s: %s", cmd, out)
			}
		}
		out, _ = run(t, appID, "screen", "--help")
		if !strings.Contains(out, "grab-region") {
			t.Fatalf("packaged screen help missing grab-region: %q", out)
		}
	})
	t.Run("argument error code", func(t *testing.T) {
		out, stderr, code := h.run(t, sourceRoot, "run", appID, "not-a-pf-command")
		if code == 0 || !strings.Contains(stderr, "unknown command") {
			t.Fatalf("invalid command exit=%d out=%q err=%q", code, out, stderr)
		}
	})
	t.Run("machine-readable info", func(t *testing.T) {
		out, _ := run(t, appID, "info", "--output=json")
		var report map[string]json.RawMessage
		if err := json.Unmarshal([]byte(out), &report); err != nil {
			t.Fatalf("info --output=json must return pure JSON: %v; output=%q", err, out)
		}
		if len(report["capabilities"]) == 0 {
			t.Fatalf("info JSON missing capabilities: %q", out)
		}
	})
	t.Run("info capabilities and diagnostics without display", func(t *testing.T) {
		raw, _ := run(t, appID, "info", "--output=json")
		var report struct {
			Compositor   string `json:"compositor"`
			Capabilities map[string]struct {
				Supported bool   `json:"supported"`
				Backend   string `json:"backend"`
				Reason    string `json:"reason"`
			} `json:"capabilities"`
		}
		if err := json.Unmarshal([]byte(raw), &report); err != nil {
			t.Fatal(err)
		}
		if len(report.Capabilities) < 3 {
			t.Fatalf("installed Flatpak info omitted capabilities: %s", raw)
		}
		plain, _ := run(t, appID, "info")
		for _, want := range []string{"Environment", "Capabilities", "input", "screen"} {
			if !strings.Contains(plain, want) {
				t.Errorf("installed Flatpak info missing %s: %s", want, plain)
			}
		}
	})
	t.Run("diagnostics without display", func(t *testing.T) {
		out, _ := run(t, appID, "session", "check")
		if !strings.Contains(out, "XDG_RUNTIME_DIR") {
			t.Fatalf("session check missing environment diagnosis: %q", out)
		}
		for _, want := range []string{"Flatpak CLI Notes", "grab-region --out -", "pf run -", "--host-dir-ro"} {
			if !strings.Contains(out, want) {
				t.Fatalf("packaged first-run diagnostics missing %q: %q", want, out)
			}
		}
	})
	fixture := filepath.Join(testRoot, "host-fixture")
	if err := os.MkdirAll(fixture, 0o700); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(fixture, "marker.txt")
	if err := os.WriteFile(secret, []byte("test-host-file-not-auto-exposed"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Run("sandbox denies ungranted host files", func(t *testing.T) {
		// Check from inside the *installed application's* sandbox; the
		// Flatpak Builder's source grant must not leak into the app.
		out, stderr, code := h.run(t, sourceRoot, "run", "--command=sh", appID, "-c", "test ! -e "+secret)
		if code != 0 {
			t.Fatalf("ungranted host file leaked into app sandbox: exit=%d out=%q err=%q", code, out, stderr)
		}
	})
	t.Run("explicit read-only file access", func(t *testing.T) {
		out, stderr, code := h.run(t, sourceRoot, "run", "--filesystem="+fixture+":ro", "--command=sh", appID, "-c", "test -r "+secret)
		if code != 0 {
			t.Fatalf("scoped read grant failed: exit=%d out=%q err=%q", code, out, stderr)
		}
	})
	t.Run("host file output requires deliberate grant", func(t *testing.T) {
		docs := filepath.Join(fixture, "docs")
		_, stderr, code := h.run(t, sourceRoot, "run", appID, "docs", "--dir="+docs)
		if _, err := os.Stat(docs); !os.IsNotExist(err) {
			t.Fatalf("ungranted host docs directory was written: code=%d err=%v stderr=%q", code, err, stderr)
		}
		// A Flatpak can successfully write to its *private* view of a path
		// even when the host cannot see it. The CLI must explain this.
		if code == 0 && !strings.Contains(stderr, "Flatpak sandbox") {
			t.Fatalf("sandbox-only output was reported without a host visibility hint: %q", stderr)
		}
		run(t, "--filesystem="+fixture, appID, "docs", "--dir="+docs)
		p, err := os.ReadFile(filepath.Join(docs, "pf.md"))
		if err != nil || !bytes.Contains(p, []byte("pf")) {
			t.Fatalf("scoped docs output missing: %v", err)
		}
	})
	t.Run("real X11 screen and input", func(t *testing.T) {
		display, stop, err := x11test.StartXvfb()
		if err != nil {
			t.Fatalf("Xvfb required for packaged desktop smoke: %v", err)
		}
		defer stop()
		origEnv := h.env
		h.env = setEnv(h.env, "DISPLAY", display)
		h.env = setEnv(h.env, "XDG_SESSION_TYPE", "x11")
		defer func() { h.env = origEnv }()
		t.Run("actual X11 info reports working capabilities", func(t *testing.T) {
			raw, _ := run(t, "--socket=x11", appID, "info", "--output=json")
			var report struct {
				Compositor   string `json:"compositor"`
				Capabilities map[string]struct {
					Supported bool   `json:"supported"`
					Backend   string `json:"backend"`
				} `json:"capabilities"`
			}
			if err := json.Unmarshal([]byte(raw), &report); err != nil {
				t.Fatal(err)
			}
			for _, cap := range []string{"screen", "input", "windows"} {
				if !report.Capabilities[cap].Supported {
					t.Errorf("installed Flatpak on working X11 desktop reports unavailable %s: %s", cap, raw)
				}
			}
			if strings.Contains(strings.ToLower(report.Compositor), "wayland") {
				t.Fatalf("installed Flatpak misidentified Xvfb as Wayland: %s", raw)
			}
		})
		out, _ := run(t, "--socket=x11", appID, "screen", "resolution")
		if !strings.Contains(out, "1024") || !strings.Contains(out, "768") {
			t.Fatalf("packaged X11 screen resolution incorrect: %q", out)
		}
		out, _ = run(t, "--socket=x11", appID, "screen", "grab-region", "--rect=0,0,64,48", "--out=-")
		info, err := png.DecodeConfig(bytes.NewReader([]byte(out)))
		if err != nil || info.Width != 64 || info.Height != 48 {
			t.Fatalf("packaged screenshot invalid: image=%+v err=%v output-bytes=%d", info, err, len(out))
		}
		out, _ = run(t, "--socket=x11", appID, "screen", "hash", "--rect=0,0,64,48")
		if !regexp.MustCompile("^[0-9a-fA-F]{8}\\n$").MatchString(out) {
			t.Fatalf("packaged screenshot hash malformed: %q", out)
		}
		run(t, "--socket=x11", appID, "input", "click", "--x=14", "--y=25")
		run(t, "--socket=x11", appID, "output", "list")
		t.Run("real GUI window, click, type and screenshot", func(t *testing.T) {
			testInstalledFlatpakRealX11Application(t, h, sourceRoot, testRoot)
		})
		t.Run("stdin scripts work without host file grants", func(t *testing.T) {
			stdout, stderr, code := h.runWithInput(t, sourceRoot, "run", "# packaged stdin demo\ninfo\n", "--socket=x11", appID, "run", "-")
			if code != 0 {
				t.Fatalf("packaged script through stdin failed: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
			if strings.TrimSpace(stdout) == "" {
				t.Fatalf("packaged stdin script produced no information")
			}
		})
		t.Run("explicit read-only script input", func(t *testing.T) {
			script := filepath.Join(fixture, "task.pf")
			if err := os.WriteFile(script, []byte("info\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			stdout, stderr, code := h.run(t, sourceRoot, "run", "--socket=x11", "--filesystem="+fixture+":ro", appID, "run", script)
			if code != 0 || strings.TrimSpace(stdout) == "" {
				t.Fatalf("scoped script failed: exit=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
		})
	})
}

// An explicit, narrow user override must never mutate or widen the app's
// committed manifest privileges.
func TestFlatpakManifestContract(t *testing.T) {
	repo := mustRepoRoot(t)
	manifest, err := os.ReadFile(filepath.Join(repo, "io.github.nskaggs.perfuncted.yml"))
	if err != nil {
		t.Fatal(err)
	}
	m := string(manifest)
	for _, want := range []string{
		"app-id: " + appID,
		"command: pf",
		"runtime: org.freedesktop.Platform",
		"runtime-version: '26.08'",
		"org.freedesktop.Sdk.Extension.golang",
		"--socket=wayland",
		"--socket=fallback-x11",
	} {
		if !strings.Contains(m, want) {
			t.Errorf("manifest missing %q", want)
		}
	}
	for _, forbidden := range []string{"--filesystem=host", "--filesystem=home", "--filesystem=host-os", "--device=all"} {
		if strings.Contains(m, forbidden) {
			t.Errorf("manifest unexpectedly grants broad file access %q", forbidden)
		}
	}
	appstream, err := os.ReadFile(filepath.Join(repo, "flatpak", "io.github.nskaggs.perfuncted.metainfo.xml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{fmt.Sprintf("<id>%s</id>", appID), "<binary>pf</binary>"} {
		if !bytes.Contains(appstream, []byte(want)) {
			t.Errorf("AppStream missing %q", want)
		}
	}
}
