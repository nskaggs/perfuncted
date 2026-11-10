//go:build integration && linux

package flatpaktest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/x11test"
)

const previousReleaseSHA256 = "5607b377e502c968db1458260d37afb6dd50afc30b6ad5a784c3f7741f844f87"

// A developer should need only the two assets from a GitHub release, not
// the source checkout. This fresh isolated Flatpak user home starts empty.
func testReleaseDownloadQuickstart(t *testing.T, base *harness, sessionAddress, sourceRoot, testRoot, bundlePath string) {
	t.Helper()
	download := filepath.Join(testRoot, "fresh-downloads")
	if err := os.MkdirAll(download, 0o700); err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(download, "perfuncted.flatpak")
	launcher := filepath.Join(download, "pf-flatpak")
	for _, pair := range [][3]string{
		{bundlePath, bundle, "0600"},
		{filepath.Join(sourceRoot, "scripts", "pf-flatpak"), launcher, "0755"},
	} {
		if err := copyStandaloneAsset(pair[0], pair[1], pair[2]); err != nil {
			t.Fatal(err)
		}
	}
	home := filepath.Join(testRoot, "clean-user-home")
	fresh := newHarness(t, sourceRoot, sessionAddress, home, filepath.Join(testRoot, "tmp"), filepath.Join(testRoot, "run"))
	if _, _, code := fresh.run(t, download, "info", appID); code == 0 {
		t.Fatal("fresh user already has Perfuncted installed")
	}
	fresh.mustRun(t, download, "remote-add", "--if-not-exists", "--user", "flathub", flathubRemoteURL)
	fresh.mustRun(t, download, "install", "--user", "-y", bundle)

	run := func(t *testing.T, args ...string) ([]byte, string, int) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(ctx, launcher, args...)
		cmd.Dir = download
		cmd.Env = fresh.env
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		if ctx.Err() != nil {
			t.Fatalf("real released launcher timed out: args=%q error=%v", args, ctx.Err())
		}
		if err == nil {
			return stdout.Bytes(), stderr.String(), 0
		}
		if exit, ok := err.(*exec.ExitError); ok {
			return stdout.Bytes(), stderr.String(), exit.ExitCode()
		}
		t.Fatalf("could not execute release launcher without source checkout: %v", err)
		return nil, "", 1
	}
	t.Run("launcher version without checkout", func(t *testing.T) {
		stdout, stderr, code := run(t, "version")
		if code != 0 {
			t.Fatalf("released pf launcher version exit=%d stderr=%q", code, stderr)
		}
		assertVersionOutput(t, string(stdout))
	})
	t.Run("launcher actionable first-run diagnostics", func(t *testing.T) {
		stdout, stderr, code := run(t, "session", "check")
		if code != 0 || !bytes.Contains(stdout, []byte("Flatpak CLI Notes")) {
			t.Fatalf("packaged first-run check exit=%d stdout=%q stderr=%q", code, stdout, stderr)
		}
	})
	t.Run("launcher host file and PNG work end to end", func(t *testing.T) {
		display, stop, err := x11test.StartXvfb()
		if err != nil {
			t.Fatal(err)
		}
		defer stop()
		previous := fresh.env
		fresh.env = setEnv(fresh.env, "DISPLAY", display)
		fresh.env = setEnv(fresh.env, "XDG_SESSION_TYPE", "x11")
		defer func() { fresh.env = previous }()
		stdout, stderr, code := run(t, "screen", "grab", "--rect", "0,0,64,48")
		if code != 0 {
			t.Fatalf("default screenshot failed exit=%d stderr=%q", code, stderr)
		}
		image, err := png.DecodeConfig(bytes.NewReader(stdout))
		if err != nil || image.Width != 64 || image.Height != 48 {
			t.Fatalf("default screenshot is not a 64x48 PNG: image=%+v err=%v", image, err)
		}
		script := filepath.Join(download, "task.pf")
		if err := os.WriteFile(script, []byte("info\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		stdout, stderr, code = run(t, "--host-dir-ro", download, "run", script)
		if code != 0 || len(stdout) == 0 {
			t.Fatalf("released launcher cannot read granted host script: code=%d out=%q err=%q", code, stdout, stderr)
		}
		target := filepath.Join(download, "visible.png")
		_, stderr, code = run(t, "--host-dir", download, "screen", "grab", "--rect", "0,0,64,48", "--out", target)
		if code != 0 {
			t.Fatalf("released launcher cannot write granted host PNG: code=%d err=%q", code, stderr)
		}
		raw, err := os.ReadFile(target)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := png.DecodeConfig(bytes.NewReader(raw)); err != nil {
			t.Fatalf("host PNG invalid: %v", err)
		}
	})
}

func copyStandaloneAsset(src, dst, mode string) error {
	raw, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	perm := os.FileMode(0600)
	if mode == "0755" {
		perm = 0755
	}
	return os.WriteFile(dst, raw, perm)
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
