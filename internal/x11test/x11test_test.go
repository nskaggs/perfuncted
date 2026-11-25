package x11test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The reported X11 display comes from an untrusted subprocess, never from
// a fixed display selected by the caller. Reject overflow and out-of-range
// values rather than returning a DISPLAY that cannot be represented safely.
func TestServerRejectsUnrepresentableDisplayFD(t *testing.T) {
	for _, identity := range []string{"65536", "99999999999999999999999999999999999999999"} {
		t.Run(identity, func(t *testing.T) {
			dir := t.TempDir()
			helper := filepath.Join(dir, "Xvfb")
			script := "#!/bin/sh\nprintf '%s\\n' '" + identity + "' >&3\nexec sleep 3\n"
			if err := os.WriteFile(helper, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			display, stop, err := startServerWithTimeout("Xvfb", "", func() []string { return nil }, time.Second)
			if display != "" || stop != nil || err == nil {
				t.Fatalf("accepted out-of-range display identity %q: display=%q stop=%v err=%v", identity, display, stop != nil, err)
			}
		})
	}
}

// Xephyr's nested-server mode must receive the explicitly selected parent
// display rather than inheriting the process's unrelated ambient DISPLAY.
func TestXephyrUsesExplicitHostDisplay(t *testing.T) {
	dir := t.TempDir()
	helper := filepath.Join(dir, "Xephyr")
	script := "#!/bin/sh\n[ \"$DISPLAY\" = ':284' ] || { echo 'wrong host display' >&2; exit 4; }\nprintf '284\\n' >&3\nexec sleep 10\n"
	if err := os.WriteFile(helper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DISPLAY", ":999")
	display, stop, err := StartXephyr(":284")
	if err != nil || display != ":284" || stop == nil {
		t.Fatalf("Xephyr did not receive host DISPLAY: display=%q stop=%v err=%v", display, stop != nil, err)
	}
	stop()
}

func TestMissingX11ServerReportsExecutableName(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	display, stop, err := startServerWithTimeout("not-a-real-x11-server", "", func() []string { return nil }, time.Second)
	if display != "" || stop != nil || err == nil || !strings.Contains(err.Error(), "not-a-real-x11-server") {
		t.Fatalf("unexpected missing server result: display=%q stop=%v err=%v", display, stop != nil, err)
	}
}

// Displayfd is an untrusted server response. A partially parsed or negative
// number must not be exposed as a usable X11 DISPLAY identifier.
func TestServerRejectsMalformedDisplayFDIdentity(t *testing.T) {
	for _, tc := range []struct{ name, response string }{
		{"negative", "-1\\n"},
		{"trailing_letters", "217junk\\n"},
		{"leading_junk", "x217\\n"},
		{"empty", "\\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			server := filepath.Join(dir, "Xvfb")
			script := "#!/bin/sh\\nprintf '%s' '" + strings.TrimSuffix(tc.response, "\\n") + "' >&3\\nprintf '\\n' >&3\\nexec sleep 2\\n"
			// The script itself is generated only from fixed, local test cases.
			script = strings.ReplaceAll(script, "\\n", "\n")
			if err := os.WriteFile(server, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			display, stop, err := StartXvfb()
			if display != "" || stop != nil || err == nil {
				t.Fatalf("invalid displayfd %q accepted: display=%q stop=%v err=%v", tc.response, display, stop != nil, err)
			}
		})
	}
}

// A server advertises its allocated display through the inherited displayfd
// pipe. Parsing that result and retiring the whole process group is part of
// the real X11 test harness contract, not merely an executable lookup.
func TestX11TestServerUsesDisplayFDAndStopsLiveProcess(t *testing.T) {
	directory := t.TempDir()
	server := filepath.Join(directory, "Xvfb")
	script := "#!/bin/sh\nprintf '217\\n' >&3\nexec sleep 30\n"
	if err := os.WriteFile(server, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	display, stop, err := StartXvfb()
	if err != nil {
		t.Fatalf("server did not publish displayfd: %v", err)
	}
	if display != ":217" || stop == nil {
		t.Fatalf("unexpected display %q, stop=%v", display, stop != nil)
	}
	stop()
}

func TestX11TestServerRejectsMissingDisplayFDAndRetainsDiagnostics(t *testing.T) {
	directory := t.TempDir()
	server := filepath.Join(directory, "Xvfb")
	script := "#!/bin/sh\nprintf 'synthetic server failed to claim display\\n' >&2\nexit 39\n"
	if err := os.WriteFile(server, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	display, stop, err := StartXvfb()
	if err == nil || stop != nil || display != "" || !strings.Contains(err.Error(), "synthetic server failed to claim display") {
		t.Fatalf("unreported display accepted: display=%q stop=%v err=%v", display, stop != nil, err)
	}
}

func TestXephyrRequiresExplicitHostDisplayBeforeStarting(t *testing.T) {
	if display, stop, err := StartXephyr(""); display != "" || stop != nil || err == nil || !strings.Contains(err.Error(), "host DISPLAY not set") {
		t.Fatalf("missing Xephyr host accepted: display=%q stop=%v err=%v", display, stop != nil, err)
	}
}

func TestDisplayServerWithMissingInterpreterFailsWithoutPublishingDisplay(t *testing.T) {
	directory := t.TempDir()
	server := filepath.Join(directory, "Xvfb")
	interpreter := filepath.Join(directory, "missing-interpreter")
	if err := os.WriteFile(server, []byte("#!"+interpreter+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory)

	display, stop, err := startServerWithTimeout("Xvfb", "", func() []string { return nil }, time.Second)
	if display != "" || stop != nil || !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), "x11test: start Xvfb") {
		t.Fatalf("unexecutable server returned display=%q stop=%v err=%v", display, stop != nil, err)
	}
}

// A server that sends a malformed displayfd response and keeps running must
// be terminated promptly rather than blocking on cmd.Wait indefinitely.
func TestMalformedDisplayResponseStopsLiveServer(t *testing.T) {
	dir := t.TempDir()
	server := filepath.Join(dir, "Xvfb")
	if err := os.WriteFile(server, []byte("#!/bin/sh\nprintf 'invalid-display\\n' >&3\nexec sleep 2\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	start := time.Now()
	display, stop, err := StartXvfb()
	if display != "" || stop != nil || err == nil || !strings.Contains(err.Error(), "did not report a display") {
		t.Fatalf("malformed displayfd accepted: display=%q stop=%v err=%v", display, stop != nil, err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("invalid displayfd waited %v for live server to exit", elapsed)
	}
}

// A display server that inherits displayfd but never publishes a display must
// be terminated at the deadline, not waited on until it exits voluntarily.
func TestDisplayStartupTimeoutTerminatesUnresponsiveServer(t *testing.T) {
	directory := t.TempDir()
	server := filepath.Join(directory, "Xvfb")
	if err := os.WriteFile(server, []byte("#!/bin/sh\nexec sleep 2\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	started := time.Now()
	display, stop, err := startServerWithTimeout("Xvfb", "", func() []string { return nil }, 50*time.Millisecond)
	elapsed := time.Since(started)
	if display != "" || stop != nil || err == nil || !strings.Contains(err.Error(), "did not become ready") {
		t.Fatalf("unexpected timeout result: display=%q stop=%v err=%v", display, stop != nil, err)
	}
	if elapsed > time.Second {
		t.Fatalf("startup timeout waited %s for an unresponsive server", elapsed)
	}
}

// A display server may ignore SIGTERM. Cleanup must escalate to SIGKILL
// against its process group and reap the original child.
func TestStopDisplayEscalatesForTermIgnoringServer(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")
	cmd := exec.Command("sh", "-c", "trap '' TERM; : > \"$1\"; while :; do sleep 0.2; done", "sh", ready)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			_ = cmd.Wait()
		}
	})
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("server failed to initialize")
		}
		time.Sleep(10 * time.Millisecond)
	}
	started := time.Now()
	stopDisplay(cmd)
	if cmd.ProcessState == nil {
		t.Fatal("server was not reaped")
	}
	status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
		t.Fatalf("expected SIGKILL escalation, got %v", cmd.ProcessState)
	}
	elapsed := time.Since(started)
	if elapsed < 1800*time.Millisecond || elapsed > 5*time.Second {
		t.Fatalf("unexpected termination escalation latency: %s", elapsed)
	}
}

func TestStopDisplayToleratesNoProcess(t *testing.T) {
	stopDisplay(nil)
	stopDisplay(&exec.Cmd{})
}

func TestHelpersCompile(t *testing.T) {
	_, stop, err := StartXvfb()
	if err != nil {
		t.Logf("StartXvfb expected to fail without display: %v", err)
	}
	if stop != nil {
		stop()
	}

	_, stop, err = StartXephyr(":99")
	if err != nil {
		t.Logf("StartXephyr expected to fail without display: %v", err)
	}
	if stop != nil {
		stop()
	}
}
