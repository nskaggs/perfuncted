package clipboard

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Open uses the caller's actual runtime, but a private PATH and fake DISPLAY
// must be enough for a fully synthetic selection; no host desktop is contacted.
func TestClipboardOpenCurrentRuntimeUsesOnlyControlledSyntheticXclip(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "xclip")
	body := "#!/bin/sh\ncase \" $* \" in\n  *' -o '*) printf 'synthetic clipboard\\n' ;;\n  *) /bin/cat >/dev/null ;;\nesac\n"
	if err := os.WriteFile(script, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("DISPLAY", ":298")
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("XDG_SESSION_TYPE", "x11")
	t.Setenv("XDG_CURRENT_DESKTOP", "XFCE")
	t.Setenv("XDG_SESSION_DESKTOP", "XFCE")
	t.Setenv("DESKTOP_SESSION", "XFCE")
	cb, err := Open()
	if err != nil {
		t.Fatalf("Open rejected controlled synthetic X11 tool: %v", err)
	}
	defer cb.Close()
	got, err := cb.Get(context.Background())
	if err != nil || got != "synthetic clipboard\n" {
		t.Fatalf("synthetic clipboard read=%q err=%v", got, err)
	}
	if err := cb.Set(context.Background(), "private test content"); err != nil {
		t.Fatalf("synthetic clipboard write: %v", err)
	}
	selected, ok := cb.(*extCmdClipboard)
	if !ok || len(selected.getCmd) == 0 || selected.getCmd[0] != "xclip" {
		t.Fatalf("wrong clipboard backend selected: %T", cb)
	}
	if !strings.Contains(strings.Join(selected.env, " "), "DISPLAY=:298") {
		t.Fatalf("runtime DISPLAY missing from subprocess environment: %v", selected.env)
	}
}

func TestClipboardOpenCurrentRuntimeWithoutDisplayOrToolsFailsClosed(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	t.Setenv("DISPLAY", "")
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("XDG_SESSION_TYPE", "x11")
	t.Setenv("XDG_CURRENT_DESKTOP", "XFCE")
	t.Setenv("XDG_SESSION_DESKTOP", "XFCE")
	t.Setenv("DESKTOP_SESSION", "XFCE")
	got, err := Open()
	if got != nil || !errors.Is(err, ErrNoClipboardTool) {
		t.Fatalf("clipboard selected without display or tools: %T err=%v", got, err)
	}
}
