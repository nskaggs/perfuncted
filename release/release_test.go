//go:build release
// +build release

// Package release_test validates a built pf binary as a release smoke test.
//
// The binary under test is resolved in this order:
//  1. The path in the PF_BINARY environment variable.
//  2. A file named "pf" in the repository root (go build ./cmd/pf produces this).
//  3. "pf" on PATH (installed release binary).
//
// Static tests (TestBinaryStatic) require no display and run on every platform.
// Live tests (TestBinaryLive) require a running display server and are gated on
// PF_TEST_DISPLAY_SERVER being set (same values as the integration suite).
//
// Usage:
//
//	PF_BINARY=./pf go test -tags=release ./release -v
//	PF_BINARY=./dist/pf_linux_amd64/pf go test -tags=release ./release -v
package release_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted"
)

// binaryPath resolves the pf binary to test.
func binaryPath(t *testing.T) string {
	t.Helper()

	// runtime.Caller(0) gives us the compile-time path of this source file:
	// …/release/release_test.go  →  repo root is one directory up.
	_, thisFile, _, _ := runtime.Caller(0)
	repoRoot := filepath.Dir(filepath.Dir(thisFile))

	// 1. Explicit override via PF_BINARY.
	// Relative paths are resolved against the repo root (where `just build`
	// and `goreleaser` produce their artifacts), not the test package dir.
	if p := os.Getenv("PF_BINARY"); p != "" {
		if !filepath.IsAbs(p) {
			p = filepath.Join(repoRoot, p)
		}
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("PF_BINARY resolved to %q: stat failed: %v", p, err)
		}
		return p
	}

	// 2. Repo root "pf" (produced by `go build ./cmd/pf` or `just build`).
	candidate := filepath.Join(repoRoot, "pf")
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}

	// 3. PATH lookup.
	p, err := exec.LookPath("pf")
	if err != nil {
		t.Skip("no pf binary found; set PF_BINARY or run `go build ./cmd/pf` first")
	}
	return p
}

// run executes the binary with the given arguments and returns stdout, stderr,
// and the exit code. It never fatals on non-zero exit so callers can assert.
func run(t *testing.T, bin string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, args...)
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
	// context deadline or other OS error
	t.Logf("exec error (args=%v): %v\nstdout=%q\nstderr=%q", args, err, stdout, stderr)
	return stdout, stderr, 1
}

// ── static tests (no display required) ──────────────────────────────────────

// TestBinaryStatic validates CLI mechanics that require no display server.
// These always run and are the minimal gate for a release artifact.
func TestBinaryStatic(t *testing.T) {
	bin := binaryPath(t)
	t.Logf("testing binary: %s", bin)

	t.Run("exits_zero_on_help", func(t *testing.T) {
		stdout, stderr, code := run(t, bin, "--help")
		if code != 0 {
			t.Fatalf("--help exit code = %d, want 0\nstdout=%q\nstderr=%q", code, stdout, stderr)
		}
		if !strings.Contains(stdout, "pf") {
			t.Fatalf("--help output does not mention 'pf'\nstdout=%q", stdout)
		}
	})

	t.Run("version_output", func(t *testing.T) {
		stdout, stderr, code := run(t, bin, "version")
		if code != 0 {
			t.Fatalf("version exit code = %d, want 0\nstdout=%q\nstderr=%q", code, stdout, stderr)
		}
		if stderr != "" {
			t.Fatalf("version wrote to stderr: %q", stderr)
		}
		// Must include at least "pf " followed by some version string.
		if !strings.HasPrefix(stdout, "pf ") {
			t.Fatalf("version output does not start with 'pf ': %q", stdout)
		}
		// Must include commit and date lines.
		if !strings.Contains(stdout, "commit:") {
			t.Fatalf("version output missing 'commit:': %q", stdout)
		}
		if !strings.Contains(stdout, "date:") {
			t.Fatalf("version output missing 'date:': %q", stdout)
		}
	})

	t.Run("version_is_goreleaser_or_dev", func(t *testing.T) {
		// A proper release build should NOT say "dev" as the version.
		// We log the version but do not fail for dev builds (local testing).
		stdout, _, code := run(t, bin, "version")
		if code != 0 {
			t.Fatalf("version exit code = %d, want 0", code)
		}
		// Extract version token: first line is "pf <version>"
		line := strings.SplitN(stdout, "\n", 2)[0]
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			ver := fields[1]
			if ver == "dev" {
				t.Logf("WARNING: binary version is 'dev' — not a GoReleaser-stamped binary")
			} else {
				t.Logf("version: %s", ver)
			}
		}
	})

	t.Run("version_help_exits_zero", func(t *testing.T) {
		_, _, code := run(t, bin, "version", "--help")
		if code != 0 {
			t.Fatalf("version --help exit code = %d, want 0", code)
		}
	})

	t.Run("unknown_command_exits_nonzero", func(t *testing.T) {
		_, _, code := run(t, bin, "thisdoesnotexist")
		if code == 0 {
			t.Fatal("expected non-zero exit for unknown command, got 0")
		}
	})

	t.Run("help_lists_all_commands", func(t *testing.T) {
		stdout, _, code := run(t, bin, "--help")
		if code != 0 {
			t.Fatalf("--help exit code = %d, want 0", code)
		}
		for _, sub := range []string{"screen", "input", "window", "find", "clipboard", "info", "session", "docs", "version", "run", "output"} {
			if !strings.Contains(stdout, sub) {
				t.Errorf("--help output missing subcommand %q\nstdout=%q", sub, stdout)
			}
		}
	})

	t.Run("session_check_exits_zero", func(t *testing.T) {
		// session check prints environment status; it never requires a live display.
		stdout, stderr, code := run(t, bin, "session", "check")
		if code != 0 {
			t.Fatalf("session check exit code = %d, want 0\nstdout=%q\nstderr=%q", code, stdout, stderr)
		}
		// Output must contain the section header line.
		if !strings.Contains(stdout, "Environment Variable Checks") {
			t.Fatalf("session check output unexpected: %q", stdout)
		}
	})

	t.Run("session_type_exits_zero", func(t *testing.T) {
		stdout, stderr, code := run(t, bin, "session", "type")
		if code != 0 {
			t.Fatalf("session type exit code = %d, want 0\nstdout=%q\nstderr=%q", code, stdout, stderr)
		}
		if !strings.Contains(stdout, "session:") {
			t.Fatalf("session type output missing 'session:': %q", stdout)
		}
	})

	t.Run("info_json_valid", func(t *testing.T) {
		// pf info --output json should always produce valid JSON, even without a display.
		stdout, stderr, code := run(t, bin, "info", "--output", "json")
		if code != 0 {
			t.Fatalf("info --output json exit code = %d, want 0\nstdout=%q\nstderr=%q", code, stdout, stderr)
		}
		var report map[string]any
		if err := json.Unmarshal([]byte(stdout), &report); err != nil {
			t.Fatalf("info --output json produced invalid JSON: %v\nstdout=%q", err, stdout)
		}
		// Validate top-level keys are present.
		for _, key := range []string{"compositor", "environment", "probes", "capabilities"} {
			if _, ok := report[key]; !ok {
				t.Errorf("info JSON missing key %q; keys present: %v", key, mapKeys(report))
			}
		}
	})

	t.Run("binary_is_statically_linked", func(t *testing.T) {
		// The goreleaser config sets CGO_ENABLED=0.  On Linux we can check
		// that the binary does not reference libc via ldd.
		if runtime.GOOS != "linux" {
			t.Skip("static-link check only on Linux")
		}
		out, err := exec.Command("ldd", bin).CombinedOutput()
		if err != nil {
			// ldd returns non-zero for static binaries on some distros; the
			// stdout text is the reliable signal.
			t.Logf("ldd returned error (expected for static binaries): %v", err)
		}
		outStr := strings.ToLower(string(out))
		if strings.Contains(outStr, "linux-vdso") || strings.Contains(outStr, "libc") {
			t.Fatalf("binary appears dynamically linked (CGO may be enabled in this build): %s", out)
		}
		// If we reach here the binary looks static.
		t.Logf("binary is statically linked (CGO_ENABLED=0 confirmed)")
	})
}

// ── live tests (display server required) ────────────────────────────────────

// TestBinaryLive mirrors the integration suite's runScreenSmoke, driven by the
// pf binary rather than the Go API.  Requires a running display server.
//
// It spins up a headless Wayland session via pf session start (the same way
// the integration suite uses perfuncted.StartSession), runs a series of pf
// subcommands inside it, then tears the session down.
func TestBinaryLive(t *testing.T) {
	displayMode := strings.ToLower(os.Getenv("PF_TEST_DISPLAY_SERVER"))
	if displayMode == "" {
		t.Skip("PF_TEST_DISPLAY_SERVER not set; skipping live binary smoke test")
	}

	bin := binaryPath(t)
	t.Logf("testing binary: %s (display=%s)", bin, displayMode)

	var (
		xdgRuntimeDir  string
		waylandDisplay string
		dbusAddress    string
		pfEnv          []string
	)

	switch displayMode {
	case "headless-wayland", "wayland", "":
		// Start a headless sway session using the library directly, exactly as the
		// integration suite does.  We can't use `pf session start` here because it
		// blocks until interrupted — use the Go API to get the env vars and pass
		// them to subsequent `pf` invocations.
		sess, err := perfuncted.StartSession(perfuncted.SessionConfig{Resolution: image.Pt(1024, 768)})
		if err != nil {
			t.Skipf("could not start headless Wayland session: %v", err)
		}
		t.Cleanup(sess.Stop)
		xdgRuntimeDir = sess.XDGRuntimeDir()
		waylandDisplay = sess.WaylandDisplay()
		dbusAddress = sess.DBusAddress()

	case "headless-x11":
		// Caller must have set DISPLAY; we just propagate the existing env.
		if os.Getenv("DISPLAY") == "" {
			t.Skip("PF_TEST_DISPLAY_SERVER=headless-x11 requires DISPLAY to be set")
		}

	case "nested-wayland", "nested":
		// Caller provides existing WAYLAND_DISPLAY env.
		xdgRuntimeDir = os.Getenv("XDG_RUNTIME_DIR")
		waylandDisplay = os.Getenv("WAYLAND_DISPLAY")
		dbusAddress = os.Getenv("DBUS_SESSION_BUS_ADDRESS")
		if waylandDisplay == "" {
			t.Skip("PF_TEST_DISPLAY_SERVER=nested-wayland requires WAYLAND_DISPLAY to be set")
		}

	case "nested-x11":
		if os.Getenv("DISPLAY") == "" {
			t.Skip("PF_TEST_DISPLAY_SERVER=nested-x11 requires DISPLAY to be set")
		}

	default:
		t.Fatalf("unknown PF_TEST_DISPLAY_SERVER=%q", displayMode)
	}

	// Build the environment for pf subprocesses.
	pfEnv = append(os.Environ())
	if xdgRuntimeDir != "" {
		pfEnv = setEnv(pfEnv, "XDG_RUNTIME_DIR", xdgRuntimeDir)
	}
	if waylandDisplay != "" {
		pfEnv = setEnv(pfEnv, "WAYLAND_DISPLAY", waylandDisplay)
		pfEnv = delEnv(pfEnv, "DISPLAY") // prefer Wayland
	}
	if dbusAddress != "" {
		pfEnv = setEnv(pfEnv, "DBUS_SESSION_BUS_ADDRESS", dbusAddress)
	}

	runLive := func(t *testing.T, args ...string) (stdout, stderr string, code int) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Env = pfEnv
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
		return stdout, stderr, 1
	}

	// ── screen resolution ────────────────────────────────────────────────────

	t.Run("screen_resolution", func(t *testing.T) {
		stdout, stderr, code := runLive(t, "screen", "resolution")
		if code != 0 {
			t.Fatalf("screen resolution exit code = %d, want 0\nstdout=%q\nstderr=%q", code, stdout, stderr)
		}
		// Output must be WxH, e.g. "1024x768"
		stdout = strings.TrimSpace(stdout)
		parts := strings.SplitN(stdout, "x", 2)
		if len(parts) != 2 {
			t.Fatalf("screen resolution output %q does not match WxH format", stdout)
		}
		w, err1 := strconv.Atoi(parts[0])
		h, err2 := strconv.Atoi(parts[1])
		if err1 != nil || err2 != nil || w <= 0 || h <= 0 {
			t.Fatalf("screen resolution output %q: w=%d h=%d err1=%v err2=%v", stdout, w, h, err1, err2)
		}
		t.Logf("screen resolution: %dx%d", w, h)
	})

	// ── screen grab ─────────────────────────────────────────────────────────

	t.Run("screen_grab", func(t *testing.T) {
		out := filepath.Join(t.TempDir(), "smoke.png")
		stdout, stderr, code := runLive(t, "screen", "grab", "--rect", "0,0,200,200", "--out", out)
		if code != 0 {
			t.Fatalf("screen grab exit code = %d, want 0\nstdout=%q\nstderr=%q", code, stdout, stderr)
		}
		info, err := os.Stat(out)
		if err != nil {
			t.Fatalf("screen grab output file missing: %v", err)
		}
		if info.Size() == 0 {
			t.Fatal("screen grab output file is empty")
		}
		t.Logf("screen grab: %s (%d bytes)", out, info.Size())
	})

	// ── screen hash ─────────────────────────────────────────────────────────

	t.Run("screen_hash", func(t *testing.T) {
		stdout, stderr, code := runLive(t, "screen", "hash", "--rect", "0,0,100,100")
		if code != 0 {
			t.Fatalf("screen hash exit code = %d, want 0\nstdout=%q\nstderr=%q", code, stdout, stderr)
		}
		stdout = strings.TrimSpace(stdout)
		// Output is a decimal uint32.
		if _, err := strconv.ParseUint(stdout, 10, 32); err != nil {
			t.Fatalf("screen hash output %q is not a valid uint32: %v", stdout, err)
		}
		t.Logf("screen hash: %s", stdout)
	})

	// ── screen pixel ────────────────────────────────────────────────────────

	t.Run("screen_pixel", func(t *testing.T) {
		stdout, stderr, code := runLive(t, "screen", "pixel", "--x", "0", "--y", "0")
		if code != 0 {
			t.Fatalf("screen pixel exit code = %d, want 0\nstdout=%q\nstderr=%q", code, stdout, stderr)
		}
		if !strings.HasPrefix(stdout, "R=") {
			t.Fatalf("screen pixel output unexpected: %q", stdout)
		}
		t.Logf("screen pixel(0,0): %s", strings.TrimSpace(stdout))
	})

	// ── screen grab-region to stdout ─────────────────────────────────────────

	t.Run("screen_grab_region_stdout", func(t *testing.T) {
		stdout, stderr, code := runLive(t, "screen", "grab-region", "--rect", "0,0,100,100", "--out", "-")
		if code != 0 {
			t.Fatalf("screen grab-region exit code = %d, want 0\nstdout=%q\nstderr=%q", code, stdout, stderr)
		}
		// PNG magic bytes: 0x89 0x50 0x4E 0x47
		if len(stdout) < 4 || stdout[0] != 0x89 || stdout[1] != 0x50 || stdout[2] != 0x4E || stdout[3] != 0x47 {
			t.Fatalf("screen grab-region stdout is not a PNG (len=%d)", len(stdout))
		}
		t.Logf("screen grab-region: %d bytes PNG on stdout", len(stdout))
	})

	// ── info (live — compositor is now known) ────────────────────────────────

	t.Run("info_json_has_compositor", func(t *testing.T) {
		stdout, stderr, code := runLive(t, "info", "--output", "json")
		if code != 0 {
			t.Fatalf("info --output json exit code = %d, want 0\nstdout=%q\nstderr=%q", code, stdout, stderr)
		}
		var report map[string]any
		if err := json.Unmarshal([]byte(stdout), &report); err != nil {
			t.Fatalf("info JSON invalid: %v\nstdout=%q", err, stdout)
		}
		comp, _ := report["compositor"].(string)
		if comp == "" || comp == "unknown" {
			t.Logf("WARNING: compositor not detected: %q (may be expected in minimal CI)", comp)
		} else {
			t.Logf("compositor: %s", comp)
		}
	})

	// ── pf run script ────────────────────────────────────────────────────────
	// Mirrors the script runner; validates `pf run` works with the real binary.

	t.Run("run_script_screen_hash", func(t *testing.T) {
		script := "screen hash --rect 0,0,100,100\n"
		scriptFile := filepath.Join(t.TempDir(), "smoke.pf")
		if err := os.WriteFile(scriptFile, []byte(script), 0o644); err != nil {
			t.Fatalf("write script: %v", err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, "run", scriptFile)
		cmd.Env = pfEnv
		var outBuf, errBuf bytes.Buffer
		cmd.Stdout = &outBuf
		cmd.Stderr = &errBuf
		if err := cmd.Run(); err != nil {
			t.Fatalf("pf run script exit error: %v\nstdout=%q\nstderr=%q", err, outBuf.String(), errBuf.String())
		}
	})

	// ── input pointer location ───────────────────────────────────────────────
	// Validates the input backend connects; on headless wl-virtual this may
	// return "unsupported" — that is a valid capability gap, not a binary bug.

	t.Run("input_location", func(t *testing.T) {
		stdout, stderr, code := runLive(t, "input", "location")
		if code != 0 {
			// wl-virtual (headless Wayland) does not support pointer location.
			// Accept this as a known capability gap.
			if strings.Contains(stderr, "unsupported") {
				t.Skipf("input location unsupported by backend (headless): %s", strings.TrimSpace(stderr))
			}
			t.Fatalf("input location exit code = %d, want 0\nstdout=%q\nstderr=%q", code, stdout, stderr)
		}
		stdout = strings.TrimSpace(stdout)
		parts := strings.SplitN(stdout, ",", 2)
		if len(parts) != 2 {
			t.Fatalf("input location output %q does not match x,y format", stdout)
		}
		t.Logf("pointer location: %s", stdout)
	})

	// ── window list ──────────────────────────────────────────────────────────
	// In a bare headless session there may be no windows; we just check the
	// command succeeds and produces well-formed output.

	t.Run("window_list", func(t *testing.T) {
		stdout, stderr, code := runLive(t, "window", "list", "--output", "json")
		if code != 0 {
			t.Fatalf("window list exit code = %d, want 0\nstdout=%q\nstderr=%q", code, stdout, stderr)
		}
		var wins []map[string]any
		if err := json.Unmarshal([]byte(stdout), &wins); err != nil {
			// Empty window list might be "null" or "[]".
			if stdout != "null\n" && strings.TrimSpace(stdout) != "[]" {
				t.Fatalf("window list JSON invalid: %v\nstdout=%q", err, stdout)
			}
		}
		t.Logf("window list: %d windows", len(wins))
	})

	// ── clipboard set / get ──────────────────────────────────────────────────

	t.Run("clipboard_roundtrip", func(t *testing.T) {
		if os.Getenv("WAYLAND_DISPLAY") == "" && os.Getenv("DISPLAY") == "" {
			// If pfEnv has no display either, clipboard will fail.
			// We only skip if pfEnv also doesn't carry a display.
			hasWL := false
			hasX11 := false
			for _, e := range pfEnv {
				if strings.HasPrefix(e, "WAYLAND_DISPLAY=") && !strings.HasSuffix(e, "=") {
					hasWL = true
				}
				if strings.HasPrefix(e, "DISPLAY=") && !strings.HasSuffix(e, "=") {
					hasX11 = true
				}
			}
			if !hasWL && !hasX11 {
				t.Skip("no display in pfEnv; skipping clipboard test")
			}
		}

		marker := fmt.Sprintf("pf-release-smoke-%d", time.Now().UnixNano())

		_, stderr, code := runLive(t, "clipboard", "set", marker)
		if code != 0 {
			t.Fatalf("clipboard set exit code = %d, want 0\nstderr=%q", code, stderr)
		}

		stdout, stderr, code := runLive(t, "clipboard", "get")
		if code != 0 {
			t.Fatalf("clipboard get exit code = %d, want 0\nstderr=%q", code, stderr)
		}
		if got := strings.TrimSpace(stdout); got != marker {
			t.Fatalf("clipboard get = %q, want %q", got, marker)
		}
		t.Logf("clipboard roundtrip: OK")
	})
}

// ── subprocess helpers ───────────────────────────────────────────────────────

// setEnv replaces or appends KEY=VALUE in an env slice.
func setEnv(env []string, key, value string) []string {
	prefix := key + "="
	for i, e := range env {
		if strings.HasPrefix(e, prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

// delEnv removes KEY from an env slice.
func delEnv(env []string, key string) []string {
	prefix := key + "="
	var out []string
	for _, e := range env {
		if !strings.HasPrefix(e, prefix) {
			out = append(out, e)
		}
	}
	return out
}

// mapKeys returns the sorted keys of a map for error messages.
func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// terminateProcess sends SIGTERM and waits, then SIGKILL if it takes too long.
// Used by tests that start subprocesses.
func terminateProcess(cmd *exec.Cmd, timeout time.Duration) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case <-done:
	case <-t.C:
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		<-done
	}
}
