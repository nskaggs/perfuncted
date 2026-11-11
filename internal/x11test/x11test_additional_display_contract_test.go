package x11test

import (
	"os"
	"path/filepath"
	"strings"
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
