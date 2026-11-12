package clipboard

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
)

// Clipboard commands must use the desktop session captured when the backend was
// opened, not a later ambient DISPLAY or unrelated environment credentials.
func TestClipboardExternalCommandsRetainSessionSnapshotAfterParentChanges(t *testing.T) {
	bin := t.TempDir()
	capture := filepath.Join(bin, "written-session")
	program := filepath.Join(bin, "xclip")
	script := `#!/bin/sh
if [ "$3" = "-o" ]; then
  printf '%s|%s|%s' "$DISPLAY" "${PERFUNCTED_AMBIENT_ONLY-unset}" "${PERFUNCTED_SESSION_TAG-unset}"
else
  printf '%s|%s|%s' "$DISPLAY" "${PERFUNCTED_AMBIENT_ONLY-unset}" "${PERFUNCTED_SESSION_TAG-unset}" > ` + capture + `
fi
`
	if err := os.WriteFile(program, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("DISPLAY", ":ambient-before")
	t.Setenv("PERFUNCTED_AMBIENT_ONLY", "ambient-secret")
	rt := env.FromEnviron([]string{
		"PATH=" + bin,
		"DISPLAY=:isolated-session",
		"XDG_SESSION_TYPE=x11",
		"XDG_CURRENT_DESKTOP=XFCE",
		"PERFUNCTED_SESSION_TAG=original",
	})
	cb, err := OpenRuntimeContext(context.Background(), rt)
	if err != nil {
		t.Fatal(err)
	}
	defer cb.Close()
	t.Setenv("DISPLAY", ":ambient-after")
	t.Setenv("PERFUNCTED_SESSION_TAG", "mutated")
	const want = ":isolated-session|unset|original"
	got, err := cb.Get(context.Background())
	if err != nil || got != want {
		t.Fatalf("read used wrong session: %q, err=%v", got, err)
	}
	if err := cb.Set(context.Background(), "private payload"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(capture)
	if err != nil || strings.TrimSpace(string(data)) != want {
		t.Fatalf("write used wrong session: %q, err=%v", data, err)
	}
}
