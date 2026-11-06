package x11test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
