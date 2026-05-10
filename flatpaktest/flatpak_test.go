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
	testRoot := filepath.Join(cacheRoot, "perfuncted-flatpak-test")
	sourceRoot := filepath.Join(testRoot, "source")
	manifestPath := filepath.Join(sourceRoot, "io.github.nskaggs.perfuncted.yml")
	appstreamPath := filepath.Join(sourceRoot, "flatpak", "io.github.nskaggs.perfuncted.metainfo.xml")
	bundlePath := filepath.Join(repoRoot, "dist", "flatpak", "perfuncted.flatpak")
	buildDir := filepath.Join(repoRoot, "builddir")
	defaultRepoDir := filepath.Join(repoRoot, "repo")
	stateDir := filepath.Join(testRoot, "flatpak-state")
	repoDir := filepath.Join(testRoot, "repo")
	buildHome := filepath.Join(testRoot, "build-home")
	bundleHome := filepath.Join(testRoot, "bundle-home")
	tmpRoot := filepath.Join(testRoot, "tmp")
	runRoot := filepath.Join(testRoot, "run")

	// Keep the workspace clean if the test is interrupted or fails part-way through.
	_ = os.RemoveAll(bundlePath)
	_ = os.RemoveAll(buildDir)
	_ = os.RemoveAll(defaultRepoDir)
	_ = os.RemoveAll(testRoot)
	_ = os.RemoveAll(filepath.Join(repoRoot, ".flatpak-builder"))
	_ = os.RemoveAll(sourceRoot)
	t.Cleanup(func() {
		_ = os.RemoveAll(buildDir)
		_ = os.RemoveAll(defaultRepoDir)
		_ = os.RemoveAll(testRoot)
		_ = os.RemoveAll(filepath.Join(repoRoot, ".flatpak-builder"))
		_ = mustRemoveWorktree(repoRoot, sourceRoot)
	})

	if err := os.MkdirAll(testRoot, 0o755); err != nil {
		t.Fatalf("create flatpak test root: %v", err)
	}
	if err := mustAddWorktree(repoRoot, sourceRoot); err != nil {
		t.Fatalf("create flatpak source worktree: %v", err)
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

	session := mustStartDBusSession(t)
	t.Cleanup(session.close)

	buildHarness := newHarness(t, sourceRoot, session.address, buildHome, tmpRoot, runRoot)
	bundleHarness := newHarness(t, sourceRoot, session.address, bundleHome, tmpRoot, runRoot)

	buildHarness.mustRun(t, sourceRoot, "remote-add", "--if-not-exists", "--user", "flathub", flathubRemoteURL)
	buildHarness.mustRun(t, sourceRoot, "install", "-y", "--user", "flathub", builderAppID)

	t.Run("manifest lint", func(t *testing.T) {
		buildHarness.mustRunLint(t, sourceRoot, "manifest", manifestPath)
		buildHarness.mustRun(t, sourceRoot, "run", "--command=flatpak-builder-lint", builderAppID, "appstream", appstreamPath)
	})

	buildHarness.mustRun(t, sourceRoot, "run", "--command=flathub-build", builderAppID,
		"--install",
		"--disable-rofiles-fuse",
		"--repo="+repoDir,
		"--state-dir="+stateDir,
		manifestPath,
	)

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

	if _, err := exec.LookPath(flatpakBinaryName); err != nil {
		t.Fatalf("required command %q not found in PATH: %v", flatpakBinaryName, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()

	fullArgs := append([]string{subcommand}, args...)
	t.Logf("$ flatpak %s", strings.Join(fullArgs, " "))

	cmd := exec.CommandContext(ctx, flatpakBinaryName, fullArgs...)
	cmd.Dir = dir
	cmd.Env = h.env

	var outBuf, errBuf bytes.Buffer
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

func mustStartDBusSession(t *testing.T) *dbusSession {
	t.Helper()

	if _, err := exec.LookPath("dbus-daemon"); err != nil {
		t.Fatalf("required command %q not found in PATH: %v", "dbus-daemon", err)
	}

	cmd := exec.Command("dbus-daemon", "--session", "--fork", "--print-address=1", "--print-pid=1", "--nopidfile")
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
	// Flathub treats the KDE window-management talk-name as a review exception.
	var report struct {
		Errors []string `json:"errors"`
	}
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		return false
	}
	return len(report.Errors) == 1 && report.Errors[0] == "finish-args-kwin-talk-name"
}

func assertVersionOutput(t *testing.T, stdout string) {
	t.Helper()

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

func mustAddWorktree(repoRoot, sourceRoot string) error {
	if _, err := os.Stat(sourceRoot); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}

	cmd := exec.Command("git", "-C", repoRoot, "worktree", "add", "--detach", sourceRoot, "HEAD")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git worktree add: %w: %s", err, out)
	}
	return nil
}

func mustRemoveWorktree(repoRoot, sourceRoot string) error {
	cmd := exec.Command("git", "-C", repoRoot, "worktree", "remove", "--force", sourceRoot)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// If the worktree is already gone, keep cleanup best-effort.
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("git worktree remove: %w: %s", err, out)
	}
	return nil
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
