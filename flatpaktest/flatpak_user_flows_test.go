//go:build integration && linux

package flatpaktest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/x11test"
)

const previousReleaseSHA256 = "5607b377e502c968db1458260d37afb6dd50afc30b6ad5a784c3f7741f844f87"

// A fresh user installs one standalone Flatpak bundle without a checkout,
// launcher script, Go toolchain or any default broad filesystem grant.
func testSingleBundleQuickstart(t *testing.T, sessionAddress, sourceRoot, testRoot, bundlePath string) {
	t.Helper()
	download := filepath.Join(testRoot, "fresh-downloads")
	if err := os.MkdirAll(download, 0700); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(download, "perfuncted.flatpak")
	raw, err := os.ReadFile(bundlePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bundle, raw, 0600); err != nil {
		t.Fatal(err)
	}

	home := filepath.Join(testRoot, "clean-user-home")
	fresh := newHarness(t, sourceRoot, sessionAddress, home,
		filepath.Join(testRoot, "tmp"), filepath.Join(testRoot, "run"))
	if _, _, code := fresh.run(t, download, "info", appID); code == 0 {
		t.Fatal("fresh user already has Perfuncted installed")
	}
	fresh.mustRun(t, download, "remote-add", "--if-not-exists", "--user", "flathub", flathubRemoteURL)
	fresh.mustRun(t, download, "install", "--user", "-y", bundle)

	t.Run("installed single bundle version and first-run diagnostics", func(t *testing.T) {
		stdout, stderr, code := fresh.run(t, download, "run", appID, "version")
		if code != 0 {
			t.Fatalf("flatpak run ... version exit=%d err=%q", code, stderr)
		}
		assertVersionOutput(t, stdout)
		info, stderr, code := fresh.run(t, download, "run", appID, "info", "--output=json")
		if code != 0 || !bytes.Contains([]byte(info), []byte(`"capabilities"`)) {
			t.Fatalf("flatpak run ... info exit=%d out=%q err=%q", code, info, stderr)
		}
		check, stderr, code := fresh.run(t, download, "run", appID, "session", "check")
		if code != 0 || !strings.Contains(check, "Flatpak CLI Notes") {
			t.Fatalf("flatpak session check exit=%d out=%q err=%q", code, check, stderr)
		}
	})
	t.Run("real X11 screenshots and explicit Flatpak filesystem grants", func(t *testing.T) {
		display, stop, err := x11test.StartXvfb()
		if err != nil {
			t.Fatal(err)
		}
		defer stop()
		original := fresh.env
		fresh.env = setEnv(fresh.env, "DISPLAY", display)
		fresh.env = setEnv(fresh.env, "XDG_SESSION_TYPE", "x11")
		defer func() { fresh.env = original }()

		stdout, stderr, code := fresh.run(t, download, "run", appID, "screen", "grab", "--rect", "0,0,64,48")
		if code != 0 {
			t.Fatalf("Flatpak stdout screenshot exit=%d err=%q", code, stderr)
		}
		img, err := png.DecodeConfig(bytes.NewReader([]byte(stdout)))
		if err != nil || img.Width != 64 || img.Height != 48 {
			t.Fatalf("Flatpak stdout screenshot not a 64x48 PNG: %+v err=%v", img, err)
		}

		scriptPath := filepath.Join(download, "task.pf")
		if err := os.WriteFile(scriptPath, []byte("info\n"), 0600); err != nil {
			t.Fatal(err)
		}
		unshared := filepath.Join(testRoot, "never-granted")
		if err := os.MkdirAll(unshared, 0700); err != nil {
			t.Fatal(err)
		}
		privateScript := filepath.Join(unshared, "private.pf")
		if err := os.WriteFile(privateScript, []byte("info\n"), 0600); err != nil {
			t.Fatal(err)
		}

		// Neither the bundle nor its manifest grants access to host folders.
		_, _, code = fresh.run(t, download, "run", appID, "run", scriptPath)
		if code == 0 {
			t.Fatal("Flatpak accessed ungranted host script")
		}
		_, _, code = fresh.run(t, download, "run", appID, "run", privateScript)
		if code == 0 {
			t.Fatal("Flatpak accessed private host script")
		}

		// --filesystem=PATH:ro is Flatpak's documented per-run permission,
		// rather than a homegrown launcher option or persistent broad grant.
		stdout, stderr, code = fresh.run(t, download, "run", "--filesystem="+download+":ro", appID, "run", scriptPath)
		if code != 0 || len(stdout) == 0 {
			t.Fatalf("Flatpak read-only host script exit=%d out=%q err=%q", code, stdout, stderr)
		}
		_, stderr, code = fresh.run(t, download, "run", "--filesystem="+download+":ro",
			"--command=sh", appID, "-c", "test -r \""+scriptPath+"\" && test ! -w \""+scriptPath+"\"")
		if code != 0 {
			t.Fatalf("Flatpak read-only grant accidentally writable: %q", stderr)
		}

		// The read-only grant doesn't allow writing back to the source dir.
		target := filepath.Join(download, "visible.png")
		_, _, code = fresh.run(t, download, "run", "--filesystem="+download+":ro", appID,
			"screen", "grab", "--rect", "0,0,64,48", "--out", target)
		if code == 0 {
			t.Fatal("Flatpak wrote PNG into read-only host filesystem")
		}
		// Explicit read/write grant applies to one invocation and one folder.
		_, stderr, code = fresh.run(t, download, "run", "--filesystem="+download,
			appID, "screen", "grab", "--rect", "0,0,64,48", "--out", target)
		if code != 0 {
			t.Fatalf("Flatpak per-run writable PNG failed: %s", stderr)
		}
		contents, err := os.ReadFile(target)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := png.DecodeConfig(bytes.NewReader(contents))
		if err != nil || decoded.Width != 64 || decoded.Height != 48 {
			t.Fatalf("host screenshot malformed: %+v %v", decoded, err)
		}
		// Grants are temporary; a later invocation must not inherit them.
		_, _, code = fresh.run(t, download, "run", appID, "run", scriptPath)
		if code == 0 {
			t.Fatal("temporary Flatpak grant persisted into later invocation")
		}
		_, _, code = fresh.run(t, download, "run", "--filesystem="+download+":ro", appID, "run", privateScript)
		if code == 0 {
			t.Fatal("scoped permission exposed another host directory")
		}
	})
}

// The v1.2.3 release contains an actual Flatpak bundle. Testing that exact
// published asset catches breaking upgrades that a synthetic revision misses.
func testRealFlatpakReleaseUpgrade(t *testing.T, sessionAddress, sourceRoot, testRoot, currentBundle string) {
	t.Helper()
	old := os.Getenv("PF_FLATPAK_PREVIOUS_BUNDLE")
	if old == "" {
		if os.Getenv("PF_FLATPAK_REQUIRE_UPGRADE") == "1" {
			t.Fatal("CI requires PF_FLATPAK_PREVIOUS_BUNDLE (the official v1.2.3 release asset)")
		}
		t.Skip("set PF_FLATPAK_PREVIOUS_BUNDLE to the pinned v1.2.3 bundle to test real upgrade")
	}
	data, err := os.ReadFile(old)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != previousReleaseSHA256 {
		t.Fatalf("untrusted legacy Flatpak digest %s (want %s)", got, previousReleaseSHA256)
	}
	upgradeHome := filepath.Join(testRoot, "upgrade-user-home")
	h := newHarness(t, sourceRoot, sessionAddress, upgradeHome, filepath.Join(testRoot, "tmp"), filepath.Join(testRoot, "run"))
	h.mustRun(t, sourceRoot, "remote-add", "--if-not-exists", "--user", "flathub", flathubRemoteURL)
	h.mustRun(t, sourceRoot, "install", "--user", "-y", old)
	oldVersion, oldStderr, code := h.run(t, sourceRoot, "run", appID, "version")
	if code != 0 {
		t.Fatalf("legacy v1.2.3 install unusable: %s; %s", oldVersion, oldStderr)
	}
	// The official v1.2.3 bundle predates working version stamping and
	// prints "pf " with no version. The pinned release SHA-256 above is
	// authoritative; requiring a version it never contained would make
	// genuine historical upgrade testing impossible.
	if !strings.Contains(oldVersion, "builtBy: flatpak") {
		t.Fatalf("legacy release does not contain the expected Flatpak CLI: %q", oldVersion)
	}
	t.Logf("upgrading official v1.2.3 SHA-256 %s (legacy CLI: %s)",
		previousReleaseSHA256, strings.TrimSpace(oldVersion))
	// Store a stable per-application marker, not a host filesystem path.
	persistent := "$XDG_DATA_HOME/pf-flatpak-upgrade-preserved"
	_, stderr, code := h.run(t, sourceRoot, "run", "--command=sh", appID, "-c",
		fmt.Sprintf("printf 'release-state-survived\\n' > %s", persistent))
	if code != 0 {
		t.Fatalf("legacy Flatpak could not save application state: %s", stderr)
	}
	grantDir := filepath.Join(testRoot, "upgrade-read-only-grant")
	privateDir := filepath.Join(testRoot, "upgrade-ungrouped-private")
	if err := os.MkdirAll(grantDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(privateDir, 0700); err != nil {
		t.Fatal(err)
	}
	grantedFile := filepath.Join(grantDir, "preserved.txt")
	privateFile := filepath.Join(privateDir, "not-granted.txt")
	if err := os.WriteFile(grantedFile, []byte("grant-survived"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(privateFile, []byte("not-exposed"), 0600); err != nil {
		t.Fatal(err)
	}
	h.mustRun(t, sourceRoot, "override", "--user", "--filesystem="+grantDir+":ro", appID)
	verifyGrants := func(t *testing.T) {
		t.Helper()
		_, stderr, rc := h.run(t, sourceRoot, "run", "--command=sh", appID, "-c",
			fmt.Sprintf("test -r %s && test ! -w %s && test ! -e %s",
				grantedFile, grantedFile, privateFile))
		if rc != 0 {
			t.Fatalf("upgrade changed scoped host read-only grant, or exposed ungranted file: %s", stderr)
		}
	}
	verifyGrants(t)
	h.mustRun(t, sourceRoot, "install", "--user", "-y", "--reinstall", currentBundle)
	version, stderr, code := h.run(t, sourceRoot, "run", appID, "version")
	if code != 0 {
		t.Fatalf("upgraded CLI unusable: %s", stderr)
	}
	if strings.Contains(version, "1.2.3") || !strings.Contains(version, "builtBy: flatpak") {
		t.Fatalf("installed upgrade did not deploy the new Flatpak binary: %q", version)
	}
	_, stderr, code = h.run(t, sourceRoot, "run", "--command=sh", appID, "-c",
		fmt.Sprintf("test \"$(cat %s)\" = release-state-survived", persistent))
	if code != 0 {
		t.Fatalf("Flatpak upgrade lost application data: %q", stderr)
	}
	verifyGrants(t)
	t.Logf("v1.2.3 to current release candidate: %s", strings.TrimSpace(version))
}
