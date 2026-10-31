package main

import (
	"bytes"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type diagnosticFailOnWrite struct {
	count, limit int
	failure      error
}

func (w *diagnosticFailOnWrite) Write(p []byte) (int, error) {
	w.count++
	if w.count == w.limit {
		return 0, w.failure
	}
	return len(p), nil
}

func TestSessionCheckDiagnosticsRealSocketAndMissingEnvironment(t *testing.T) {
	runtime := t.TempDir()
	listener, err := net.Listen("unix", filepath.Join(runtime, "wayland-fixture"))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	t.Setenv("WAYLAND_DISPLAY", "wayland-fixture")
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/synthetic-bus")
	var output bytes.Buffer
	if err := writeSessionCheck(&output); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"XDG_RUNTIME_DIR=<set>", "WAYLAND_DISPLAY=wayland-fixture (socket reachable)", "DBUS_SESSION_BUS_ADDRESS=<set>", "/dev/uinput"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("session diagnostics missed %q: %s", want, output.String())
		}
	}

	t.Setenv("XDG_RUNTIME_DIR", "")
	t.Setenv("WAYLAND_DISPLAY", "nonexistent-wayland")
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "")
	output.Reset()
	if err := writeSessionCheck(&output); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"XDG_RUNTIME_DIR is not set", "socket unresolved", "DBUS_SESSION_BUS_ADDRESS is not set"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("missing environment not explained: %q in %s", want, output.String())
		}
	}

	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(runtime, "missing"))
	output.Reset()
	if err := writeSessionCheck(&output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "not found") || !strings.Contains(output.String(), "socket missing") {
		t.Fatalf("unreachable paths reported available: %s", output.String())
	}
}
func TestSessionCheckEveryOutputStagePropagatesWriterFailure(t *testing.T) {
	runtime := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	t.Setenv("WAYLAND_DISPLAY", "wayland-missing")
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/nonexistent/synthetic")
	var reference bytes.Buffer
	if err := writeSessionCheck(&reference); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("simulated closed diagnostics output")
	for nth := 1; nth <= 9; nth++ {
		w := &diagnosticFailOnWrite{limit: nth, failure: failure}
		err := writeSessionCheck(w)
		if w.count < nth {
			break
		}
		if !errors.Is(err, failure) {
			t.Fatalf("write failure %d swallowed after %d writes: %v", nth, w.count, err)
		}
	}
	for _, tc := range []struct {
		name  string
		write func(*diagnosticFailOnWrite) error
	}{
		{"runtime-dir", func(w *diagnosticFailOnWrite) error { return writeRuntimeDirCheck(w, runtime) }},
		{"missing-runtime-dir", func(w *diagnosticFailOnWrite) error { return writeRuntimeDirCheck(w, "") }},
		{"wayland-unset", func(w *diagnosticFailOnWrite) error { return writeWaylandDisplayCheck(w, "", runtime) }},
		{"wayland-missing", func(w *diagnosticFailOnWrite) error { return writeWaylandDisplayCheck(w, "missing", runtime) }},
		{"DBus-enabled", func(w *diagnosticFailOnWrite) error { return writeDBusCheck(w, "unix:path=/private-bus") }},
		{"DBus-disabled", func(w *diagnosticFailOnWrite) error { return writeDBusCheck(w, "") }},
		{"uinput", func(w *diagnosticFailOnWrite) error { return writeUinputCheck(w) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := &diagnosticFailOnWrite{limit: 1, failure: failure}
			if err := tc.write(w); !errors.Is(err, failure) {
				t.Fatalf("diagnostics writer error not propagated: %v", err)
			}
		})
	}
}
func TestSessionCheckRejectsRegularFileMisrepresentedAsWaylandSocket(t *testing.T) {
	runtime := t.TempDir()
	regular := filepath.Join(runtime, "wayland-regular")
	if err := os.WriteFile(regular, []byte("not a socket"), 0600); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := writeWaylandCheck(&buf, regular, runtime); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "socket reachable") || !strings.Contains(buf.String(), "socket missing") {
		t.Fatalf("regular file accepted as compositor socket: %s", buf.String())
	}
}
