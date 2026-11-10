//go:build integration && linux
// +build integration,linux

package flatpaktest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	appID             = "io.github.nskaggs.perfuncted"
	branch            = "stable"
	flathubRemoteURL  = "https://dl.flathub.org/repo/flathub.flatpakrepo"
	commandTimeout    = 45 * time.Minute
	builderAppID      = "org.flatpak.Builder"
	flatpakBinaryName = "flatpak"
	flatpakService    = "org.freedesktop.Flatpak.service"
)

type harness struct {
	repoRoot string
	env      []string
}

type dbusSession struct {
	address string
	pid     int
}

func TestFlatpakBundleBuildInstallAndRun(t *testing.T) {
	t.Helper()

	if testing.Short() {
		t.Skip("skipping Flatpak integration test in short mode")
	}

	repoRoot := mustRepoRoot(t)
	cacheRoot, err := os.UserCacheDir()
	if err != nil {
		t.Fatalf("resolve user cache directory: %v", err)
	}
	// Each run owns a unique private root; never delete another active test's files.
	if err := os.MkdirAll(cacheRoot, 0o700); err != nil {
		t.Fatalf("create cache root: %v", err)
	}
	testRoot, err := os.MkdirTemp(cacheRoot, "perfuncted-flatpak-test-")
	if err != nil {
		t.Fatalf("create isolated test root: %v", err)
	}
	sourceRoot := filepath.Join(testRoot, "source")
	manifestPath := filepath.Join(sourceRoot, "io.github.nskaggs.perfuncted.yml")
	appstreamPath := filepath.Join(sourceRoot, "flatpak", "io.github.nskaggs.perfuncted.metainfo.xml")
	bundlePath := filepath.Join(testRoot, "bundle", "perfuncted.flatpak")
	stateDir := filepath.Join(testRoot, "flatpak-state")
	repoDir := filepath.Join(testRoot, "repo")
	buildHome := filepath.Join(testRoot, "build-home")
	bundleHome := filepath.Join(testRoot, "bundle-home")
	dbusHome := filepath.Join(testRoot, "dbus-home")
	tmpRoot := filepath.Join(testRoot, "tmp")
	runRoot := filepath.Join(testRoot, "run")

	// The harness owns only testRoot. Never delete another build, release
	// bundle, worktree, or cache belonging to the source checkout.
	t.Cleanup(func() { _ = os.RemoveAll(testRoot) })

	if err := os.MkdirAll(testRoot, 0o755); err != nil {
		t.Fatalf("create flatpak test root: %v", err)
	}
	if err := mustWriteFlatpakServiceFile(dbusHome); err != nil {
		t.Fatalf("write flatpak D-Bus service file: %v", err)
	}
	if err := snapshotFlatpakSource(repoRoot, sourceRoot); err != nil {
		t.Fatalf("create Flatpak source snapshot: %v", err)
	}
	if err := writeFlatpakBuildInfo(repoRoot, sourceRoot); err != nil {
		t.Fatalf("write stamped Flatpak build metadata: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(bundlePath), 0o755); err != nil {
		t.Fatalf("create bundle directory: %v", err)
	}
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatalf("create flatpak state directory: %v", err)
	}
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("create flatpak repo directory: %v", err)
	}
	if err := os.MkdirAll(tmpRoot, 0o755); err != nil {
		t.Fatalf("create flatpak tmp directory: %v", err)
	}
	if err := os.MkdirAll(runRoot, 0o755); err != nil {
		t.Fatalf("create flatpak run directory: %v", err)
	}

	session := mustStartDBusSession(t, dbusHome)
	t.Cleanup(session.close)

	buildHarness := newHarness(t, sourceRoot, session.address, buildHome, tmpRoot, runRoot)
	bundleHarness := newHarness(t, sourceRoot, session.address, bundleHome, tmpRoot, runRoot)

	buildHarness.mustRun(t, sourceRoot, "remote-add", "--if-not-exists", "--user", "flathub", flathubRemoteURL)
	// Use an already installed Builder when available (including system installs).
	// Otherwise install the Builder in this test's isolated user Flatpak home.
	_, _, builderAvailable := buildHarness.run(t, sourceRoot, "info", builderAppID)
	if builderAvailable != 0 {
		buildHarness.mustRun(t, sourceRoot, "install", "-y", "--user", "flathub", builderAppID)
	}

	t.Run("manifest lint", func(t *testing.T) {
		buildHarness.mustRunLint(t, sourceRoot, "manifest", manifestPath)
		buildHarness.mustRun(t, sourceRoot, "run", "--command=flatpak-builder-lint", builderAppID, "appstream", appstreamPath)
	})

	// Flathub Builder validates icons inside a nested sandbox on some host
	// Flatpak versions. GitHub runners may lack a compatible nested image
	// loader despite shipping a valid, decoded PNG. Retry ONLY that precise
	// export error using flatpak's own unsandboxed icon validator; malformed
	// icons are still rejected, and every subsequent release gate still runs.
	buildOut, buildErr, buildCode := buildHarness.run(t, sourceRoot, "run", "--command=flathub-build", builderAppID,
		"--install",
		"--disable-rofiles-fuse",
		"--repo="+repoDir,
		"--state-dir="+stateDir,
		manifestPath,
	)
	if buildCode != 0 {
		if !needsUnsandboxedIconExport(buildErr) {
			t.Fatalf("Flathub Builder failed (exit=%d): stdout=%q stderr=%q", buildCode, buildOut, buildErr)
		}
		t.Log("nested icon loader unavailable; retrying Flatpak icon validation without the nested sandbox")
		buildHarness.mustRun(t, sourceRoot, "build-export", "--disable-sandbox", "--update-appstream",
			repoDir, filepath.Join(sourceRoot, "builddir"), branch)
		buildHarness.mustRun(t, sourceRoot, "remote-add", "--if-not-exists", "--user", "--no-gpg-verify",
			"perfuncted-test-build", repoDir)
		buildHarness.mustRun(t, sourceRoot, "install", "-y", "--user", "perfuncted-test-build", appID)
	}

	t.Run("installed build", func(t *testing.T) {
		stdout, stderr, code := buildHarness.run(t, sourceRoot, "run", appID, "version")
		if code != 0 {
			t.Fatalf("flatpak run %s version exit code = %d, want 0\nstdout=%q\nstderr=%q", appID, code, stdout, stderr)
		}
		assertVersionOutput(t, stdout)
	})

	t.Run("repo lint", func(t *testing.T) {
		buildHarness.mustRunLint(t, sourceRoot, "repo", repoDir)
	})

	buildHarness.mustRun(t, sourceRoot, "build-bundle", repoDir, bundlePath, appID, branch)
	if info, err := os.Stat(bundlePath); err != nil {
		t.Fatalf("bundle missing after build: %v", err)
	} else if info.Size() == 0 {
		t.Fatalf("bundle is empty: %s", bundlePath)
	}

	bundleHarness.mustRun(t, sourceRoot, "remote-add", "--if-not-exists", "--user", "flathub", flathubRemoteURL)
	bundleHarness.mustRun(t, sourceRoot, "install", "--user", "-y", bundlePath)

	t.Run("bundle install", func(t *testing.T) {
		stdout, stderr, code := bundleHarness.run(t, sourceRoot, "run", appID, "version")
		if code != 0 {
			t.Fatalf("flatpak run %s version exit code = %d, want 0\nstdout=%q\nstderr=%q", appID, code, stdout, stderr)
		}
		assertVersionOutput(t, stdout)
	})
	t.Run("fresh download-only installation and launcher", func(t *testing.T) {
		testReleaseDownloadQuickstart(t, bundleHarness, session.address, sourceRoot, testRoot, bundlePath)
	})
	t.Run("upgrade from official v1.2.3 Flatpak", func(t *testing.T) {
		testRealFlatpakReleaseUpgrade(t, session.address, sourceRoot, testRoot, bundlePath)
	})
	t.Run("installed CLI and sandbox acceptance", func(t *testing.T) {
		testInstalledFlatpakCLI(t, bundleHarness, sourceRoot, testRoot)
	})
	t.Run("real GNOME and KDE Wayland compositor checks", func(t *testing.T) {
		testFlatpakWaylandMatrix(t, bundleHarness, sourceRoot, testRoot)
	})
}

func newHarness(t *testing.T, repoRoot, dbusAddress, home, tmpRoot, runRoot string) *harness {
	t.Helper()

	mustMkdirAll(t,
		home,
		filepath.Join(home, ".cache"),
		filepath.Join(home, ".config"),
		filepath.Join(home, ".local", "share"),
		tmpRoot,
		runRoot,
	)

	env := os.Environ()
	env = setEnv(env, "HOME", home)
	env = setEnv(env, "XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	env = setEnv(env, "XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	env = setEnv(env, "XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	env = setEnv(env, "XDG_DATA_DIRS", "/app/share:/usr/local/share:/usr/share")
	env = setEnv(env, "XDG_RUNTIME_DIR", runRoot)
	env = setEnv(env, "DBUS_SESSION_BUS_ADDRESS", dbusAddress)
	env = setEnv(env, "NO_AT_BRIDGE", "1")
	env = setEnv(env, "TMPDIR", tmpRoot)
	env = unsetEnv(env, "DISPLAY")
	env = unsetEnv(env, "WAYLAND_DISPLAY")
	env = unsetEnv(env, "SWAYSOCK")
	env = unsetEnv(env, "XDG_SESSION_TYPE")

	return &harness{
		repoRoot: repoRoot,
		env:      env,
	}
}

func (h *harness) mustRun(t *testing.T, dir, subcommand string, args ...string) {
	t.Helper()

	stdout, stderr, code := h.run(t, dir, subcommand, args...)
	if code != 0 {
		t.Fatalf("flatpak %s %s exit code = %d, want 0\nstdout=%q\nstderr=%q", subcommand, strings.Join(args, " "), code, stdout, stderr)
	}
}

func (h *harness) mustRunLint(t *testing.T, dir, lintMode string, args ...string) {
	t.Helper()

	stdout, stderr, code := h.run(t, dir, "run", append([]string{"--command=flatpak-builder-lint", builderAppID, lintMode}, args...)...)
	if code == 0 {
		return
	}
	if allowKnownManifestException(stdout) {
		t.Logf("flatpak-builder-lint %s reported known Flathub policy exception; continuing", lintMode)
		return
	}
	t.Fatalf("flatpak-builder-lint %s exit code = %d, want 0\nstdout=%q\nstderr=%q", lintMode, code, stdout, stderr)
}

func (h *harness) run(t *testing.T, dir, subcommand string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	return h.runWithInput(t, dir, subcommand, "", args...)
}

func (h *harness) runWithInput(t *testing.T, dir, subcommand, stdin string, args ...string) (stdout, stderr string, code int) {
	t.Helper()

	if _, err := exec.LookPath(flatpakBinaryName); err != nil {
		t.Fatalf("required command %q not found in PATH: %v", flatpakBinaryName, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()

	fullArgs := append([]string{subcommand}, args...)
	// The Builder Flatpak has its own sandbox: passing a source checkout path
	// without granting that path produces an ENOENT before lint/build starts.
	// Expose only this test's private root to Builder, not to the resulting app.
	if subcommand == "run" {
		for _, arg := range args {
			if arg == builderAppID {
				fullArgs = append([]string{"run", "--filesystem=" + filepath.Dir(h.repoRoot)}, args...)
				break
			}
		}
	}
	t.Logf("$ flatpak %s", strings.Join(fullArgs, " "))

	cmd := exec.CommandContext(ctx, flatpakBinaryName, fullArgs...)
	cmd.Dir = dir
	cmd.Env = h.env

	var outBuf, errBuf bytes.Buffer
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err := cmd.Run()
	stdout = outBuf.String()
	stderr = errBuf.String()

	if err == nil {
		return stdout, stderr, 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return stdout, stderr, exitErr.ExitCode()
	}
	if ctx.Err() == context.DeadlineExceeded {
		t.Logf("flatpak %s timed out after %s", strings.Join(fullArgs, " "), commandTimeout)
	}
	t.Logf("flatpak %s failed: %v\nstdout=%q\nstderr=%q", strings.Join(fullArgs, " "), err, stdout, stderr)
	return stdout, stderr, 1
}

func mustStartDBusSession(t *testing.T, dbusHome string) *dbusSession {
	t.Helper()

	if _, err := exec.LookPath("dbus-daemon"); err != nil {
		t.Fatalf("required command %q not found in PATH: %v", "dbus-daemon", err)
	}

	cmd := exec.Command("dbus-daemon", "--session", "--fork", "--print-address=1", "--print-pid=1", "--nopidfile")
	cmd.Env = os.Environ()
	cmd.Env = setEnv(cmd.Env, "HOME", dbusHome)
	cmd.Env = setEnv(cmd.Env, "XDG_DATA_HOME", filepath.Join(dbusHome, ".local", "share"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("start dbus session: %v\noutput=%s", err, out)
	}

	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		t.Fatalf("unexpected dbus-daemon output: %q", string(out))
	}
	pid, err := strconv.Atoi(strings.TrimSpace(lines[1]))
	if err != nil {
		t.Fatalf("parse dbus-daemon pid from %q: %v", lines[1], err)
	}

	return &dbusSession{
		address: strings.TrimSpace(lines[0]),
		pid:     pid,
	}
}

func (s *dbusSession) close() {
	if s == nil || s.pid == 0 {
		return
	}
	if proc, err := os.FindProcess(s.pid); err == nil {
		_ = proc.Kill()
	}
}

func allowKnownManifestException(stdout string) bool {
	// Flathub treats the KDE window-management and native bridge talk-names as
	// review exceptions. Both permissions are required by the supported runtime
	// capability paths.
	var report struct {
		Errors []string `json:"errors"`
	}
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		return false
	}
	known := map[string]struct{}{
		"finish-args-kwin-talk-name":              {},
		"finish-args-unnecessary-appid-talk-name": {},
	}
	if len(report.Errors) == 0 || len(report.Errors) > len(known) {
		return false
	}
	for _, issue := range report.Errors {
		if _, ok := known[issue]; !ok {
			return false
		}
	}
	return true
}

func assertVersionOutput(t *testing.T, stdout string) {
	t.Helper()
	if strings.HasPrefix(stdout, "pf \n") || strings.HasPrefix(stdout, "pf dev\n") {
		t.Fatalf("Flatpak version was not stamped from Git: %q", stdout)
	}
	if strings.Contains(stdout, "commit:  none") || strings.Contains(stdout, "date:    unknown") {
		t.Fatalf("Flatpak revision was not stamped from Git: %q", stdout)
	}

	if !strings.Contains(stdout, "builtBy: flatpak") {
		t.Fatalf("version output missing builtBy stamp: %q", stdout)
	}
	if !strings.Contains(stdout, "commit:") {
		t.Fatalf("version output missing commit line: %q", stdout)
	}
	if !strings.Contains(stdout, "date:") {
		t.Fatalf("version output missing date line: %q", stdout)
	}
}

// snapshotFlatpakSource extracts the committed tree without registering a Git
// worktree or reading in-flight edits. Build outputs remain confined to the
// caller-owned test root and may be safely cleaned up after the test.
func snapshotFlatpakSource(repoRoot, sourceRoot string) error {
	if err := os.Mkdir(sourceRoot, 0o700); err != nil {
		return fmt.Errorf("create snapshot directory: %w", err)
	}
	archive := exec.Command("git", "-C", repoRoot, "archive", "--format=tar", "HEAD")
	pipe, err := archive.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open Git archive pipe: %w", err)
	}
	var gitStderr bytes.Buffer
	archive.Stderr = &gitStderr
	if err := archive.Start(); err != nil {
		return fmt.Errorf("start Git archive: %w", err)
	}
	extract := exec.Command("tar", "-xf", "-", "-C", sourceRoot)
	extract.Stdin = pipe
	extractOut, extractErr := extract.CombinedOutput()
	gitErr := archive.Wait()
	if extractErr != nil {
		return fmt.Errorf("extract Git archive: %w: %s", extractErr, extractOut)
	}
	if gitErr != nil {
		return fmt.Errorf("Git archive failed: %w: %s", gitErr, gitStderr.String())
	}
	return nil
}

func mustWriteFlatpakServiceFile(dbusHome string) error {
	serviceDir := filepath.Join(dbusHome, ".local", "share", "dbus-1", "services")
	if err := os.MkdirAll(serviceDir, 0o755); err != nil {
		return fmt.Errorf("create flatpak D-Bus service directory: %w", err)
	}
	helperPath, err := resolveFlatpakSessionHelper()
	if err != nil {
		return err
	}

	servicePath := filepath.Join(serviceDir, flatpakService)
	service := fmt.Sprintf("[D-BUS Service]\nName=org.freedesktop.Flatpak\nExec=%s\nSystemdService=flatpak-session-helper.service\n", helperPath)
	if err := os.WriteFile(servicePath, []byte(service), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", servicePath, err)
	}
	return nil
}

func resolveFlatpakSessionHelper() (string, error) {
	if path, err := exec.LookPath("flatpak-session-helper"); err == nil {
		return path, nil
	}

	for _, candidate := range []string{
		"/usr/libexec/flatpak-session-helper",
		"/usr/lib/flatpak-session-helper",
		"/usr/libexec/flatpak-system-helper",
	} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("required Flatpak session helper not found in PATH or common install locations")
}

func mustRepoRoot(t *testing.T) string {
	t.Helper()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed while resolving repository root")
	}
	repoRoot := filepath.Dir(filepath.Dir(thisFile))
	absRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	return absRoot
}

func mustMkdirAll(t *testing.T, dirs ...string) {
	t.Helper()

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
}

func setEnv(env []string, key, value string) []string {
	prefix := key + "="
	for i, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

func unsetEnv(env []string, key string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// needsUnsandboxedIconExport is intentionally narrow. It must never turn
// compilation failures, manifest failures, or genuinely invalid icons into
// green builds: the fallback still runs Flatpak's actual icon validator.
func needsUnsandboxedIconExport(stderr string) bool {
	return strings.Contains(stderr, "is not a valid icon: Format not recognized") &&
		strings.Contains(stderr, "Export failed:")
}
