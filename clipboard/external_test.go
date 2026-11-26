package clipboard

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/v2/internal/env"
	"github.com/nskaggs/perfuncted/v2/internal/executil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// An empty clipboard write does not make the previous selection public.
// Some failing clipboard tools include the old selection in stderr.
func TestClipboardSetEmptyDoesNotLeakPreviousSelectionFromOwnerStderr(t *testing.T) {
	const previousSelection = "previous-secret-selection-f7d2"
	for _, name := range []string{"synthetic-owner", "xclip"} {
		t.Run(name, func(t *testing.T) {
			executable := filepath.Join(t.TempDir(), name)
			script := "#!/bin/sh\nprintf 'previous clipboard: previous-secret-selection-f7d2\\n' >&2\nexit 49\n"
			if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			backend := &extCmdClipboard{setCmd: []string{executable}}
			err := backend.Set(context.Background(), "")
			if err == nil || !strings.Contains(err.Error(), "exit status 49") || strings.Contains(err.Error(), previousSelection) {
				t.Fatalf("previous selection leaked through failed empty write: %v", err)
			}
		})
	}
}

// Clipboard readers can put the current selection in stderr when failing.
// That data is unknown to Get, so returning unsanitized stderr leaks it into
// callers' errors and logs.
func TestExternalClipboardGetDoesNotExposeSelectionFromFailureStderr(t *testing.T) {
	const selection = "secret-clipboard-selection-6f4a"
	script := filepath.Join(t.TempDir(), "reader")
	content := "#!/bin/sh\nprintf 'reader failed while accessing %s\\n' '" + selection + "' >&2\nexit 27\n"
	if err := os.WriteFile(script, []byte(content), 0700); err != nil {
		t.Fatal(err)
	}
	cb := &extCmdClipboard{getCmd: []string{script}}
	got, err := cb.Get(context.Background())
	if err == nil || got != "" || !strings.Contains(err.Error(), "exit status 27") {
		t.Fatalf("failed reader not reported: result=%q err=%v", got, err)
	}
	if strings.Contains(err.Error(), selection) {
		t.Fatalf("clipboard selection leaked through stderr in returned error: %v", err)
	}
}

// A failed owner can print only a *part* of its stdin. Substituting the exact
// clipboard text in stderr does not sanitize these private fragments.
func TestClipboardSetDoesNotLeakPartialContentFromFailedOwner(t *testing.T) {
	const private = "private-selection-very-secret-42"
	const fragment = "selection-very-secret"
	for _, name := range []string{"generic-owner", "xclip"} {
		t.Run(name, func(t *testing.T) {
			executable := filepath.Join(t.TempDir(), name)
			if err := os.WriteFile(executable, []byte("#!/bin/sh\nprintf 'owner failed on: selection-very-secret\\n' >&2\nexit 47\n"), 0700); err != nil {
				t.Fatal(err)
			}
			backend := &extCmdClipboard{setCmd: []string{executable}}
			err := backend.Set(context.Background(), private)
			if err == nil || !strings.Contains(err.Error(), "exit status 47") || strings.Contains(err.Error(), fragment) {
				t.Fatalf("private clipboard fragment disclosed: err=%v", err)
			}
		})
	}
}

// A failed external clipboard tool can echo its stdin to stderr. Command
// diagnostics must explain the failure without putting the attempted private
// selection in a returned error or session-level logs.
func TestClipboardSetRedactsEchoedSelectionFromToolDiagnostics(t *testing.T) {
	const secret = "private-selection-9f3cc7"
	for _, tc := range []struct{ name, executable string }{
		{name: "generic owner", executable: "synthetic-owner"},
		{name: "xclip owner", executable: "xclip"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), tc.executable)
			script := "#!/bin/sh\nIFS= read -r value\nprintf 'owner rejected selection: %s; retry later\\n' \"$value\" >&2\nexit 43\n"
			if err := os.WriteFile(path, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			backend := &extCmdClipboard{setCmd: []string{path}}
			err := backend.Set(context.Background(), secret)
			if err == nil || !strings.Contains(err.Error(), "exit status 43") || strings.Contains(err.Error(), "retry later") {
				t.Fatalf("command failure diagnostics lost: %v", err)
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("clipboard content leaked through tool stderr: %v", err)
			}
		})
	}
}

// Failure messages must distinguish a failed external program from an empty
// selection, without publishing the attempted clipboard payload as diagnostics.
func TestExternalClipboardFailureDiagnosticMatrix(t *testing.T) {
	const secret = "private-selection-payload"
	for _, tc := range []struct{ name, script, suffix string }{
		{"silent failure", "exit 29", "exit status 29"},
		{"diagnostic failure", "printf 'backend unavailable\\n' >&2; exit 30", "exit status 30"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := &extCmdClipboard{setCmd: []string{"sh", "-c", tc.script}}
			err := backend.Set(context.Background(), secret)
			if err == nil || !strings.Contains(err.Error(), tc.suffix) || strings.Contains(err.Error(), secret) {
				t.Fatalf("set failure was not safely diagnosed: %v", err)
			}
		})
	}
}

func TestXclipCaptureTemporaryFileIsRemovedOnSuccessAndFailure(t *testing.T) {
	temp := t.TempDir()
	t.Setenv("TMPDIR", temp)
	for _, tc := range []struct {
		name, body string
		wantErr    bool
	}{
		{"success", "cat >/dev/null", false},
		{"failure", "printf 'backend rejected request' >&2; exit 31", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			program := filepath.Join(t.TempDir(), "xclip")
			if err := os.WriteFile(program, []byte("#!/bin/sh\n"+tc.body+"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			backend := &extCmdClipboard{setCmd: []string{program}}
			err := backend.Set(context.Background(), "confidential-text")
			if (err != nil) != tc.wantErr {
				t.Fatalf("Set error = %v, want error %v", err, tc.wantErr)
			}
			if tc.wantErr && (!strings.Contains(err.Error(), "exit status 31") || strings.Contains(err.Error(), "backend rejected request") || strings.Contains(err.Error(), "confidential-text")) {
				t.Fatalf("unsafe diagnostics: %v", err)
			}
			entries, err := os.ReadDir(temp)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), "perfuncted-clipboard-stderr-") {
					t.Fatalf("clipboard stderr capture was not removed: %s", entry.Name())
				}
			}
		})
	}
}

func TestExternalClipboardGetReportsExitFailureWithoutExposingStderr(t *testing.T) {
	c := &extCmdClipboard{getCmd: []string{"sh", "-c", "printf 'selection refused' >&2; exit 17"}}
	text, err := c.Get(context.Background())
	if text != "" || err == nil || strings.Contains(err.Error(), "selection refused") || !strings.Contains(err.Error(), "exit status 17") {
		t.Fatalf("Get = %q, %v", text, err)
	}
}

func TestExternalClipboardSetReportsXclipErrorAndCleansTemporaryCapture(t *testing.T) {
	script := filepath.Join(t.TempDir(), "xclip")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf 'selection denied' >&2\nexit 23\n"), 0700); err != nil {
		t.Fatal(err)
	}
	c := &extCmdClipboard{setCmd: []string{script}}
	err := c.Set(context.Background(), "payload")
	if err == nil || strings.Contains(err.Error(), "selection denied") || !strings.Contains(err.Error(), "exit status 23") {
		t.Fatalf("Set error = %v", err)
	}
}

func TestExternalClipboardGetHonorsExpiredDeadline(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	c := &extCmdClipboard{getCmd: []string{"sh", "-c", "sleep 1"}}
	if _, err := c.Get(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Get deadline = %v", err)
	}
}

func TestExternalClipboardSetHonorsExpiredDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := &extCmdClipboard{setCmd: []string{"sh", "-c", "exit 0"}}
	if err := c.Set(ctx, "payload"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Set cancellation = %v", err)
	}
}

// A partially initialized command backend must return an error rather than
// panic when a session attempts to read or set a selection.
func TestClipboardIncompleteCommandBackendFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name string
		clip *extCmdClipboard
	}{
		{name: "nil backend", clip: nil},
		{name: "zero value", clip: &extCmdClipboard{}},
		{name: "empty read executable", clip: &extCmdClipboard{getCmd: []string{""}, setCmd: []string{"/bin/true"}}},
		{name: "empty write executable", clip: &extCmdClipboard{getCmd: []string{"/bin/true"}, setCmd: []string{""}}},
		{name: "only read command", clip: &extCmdClipboard{getCmd: []string{"/bin/true"}}},
		{name: "only write command", clip: &extCmdClipboard{setCmd: []string{"/bin/true"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.clip == nil || len(tc.clip.getCmd) == 0 || tc.clip.getCmd[0] == "" {
				if got, err := tc.clip.Get(context.Background()); got != "" || err == nil || !strings.Contains(err.Error(), "not configured") {
					t.Fatalf("Get on incomplete backend: text=%q error=%v", got, err)
				}
			}
			if tc.clip == nil || len(tc.clip.setCmd) == 0 || tc.clip.setCmd[0] == "" {
				if err := tc.clip.Set(context.Background(), "sensitive clipboard value"); err == nil || !strings.Contains(err.Error(), "not configured") {
					t.Fatalf("Set on incomplete backend: %v", err)
				}
			}
		})
	}
}

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

// An external clipboard tool may finish successfully while the caller
// cancels its request. The caller's cancellation must win over the exit
// status: returning success could publish stale clipboard contents.
func TestClipboardGetRejectsCanceledContextAfterExternalSuccess(t *testing.T) {
	original := executil.CommandContext
	t.Cleanup(func() { executil.CommandContext = original })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	executil.CommandContext = func(context.Context, string, ...string) *exec.Cmd {
		cancel()
		return exec.Command("sh", "-c", "printf 'private clipboard bytes'")
	}
	cb := &extCmdClipboard{getCmd: []string{"synthetic-get"}}
	got, err := cb.Get(ctx)
	if !errors.Is(err, context.Canceled) || got != "" {
		t.Fatalf("canceled clipboard read published %q err=%v", got, err)
	}
}

func TestClipboardSetRejectsCanceledContextAfterExternalSuccess(t *testing.T) {
	original := executil.CommandContext
	t.Cleanup(func() { executil.CommandContext = original })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	executil.CommandContext = func(context.Context, string, ...string) *exec.Cmd {
		cancel()
		return exec.Command("sh", "-c", "cat >/dev/null")
	}
	cb := &extCmdClipboard{setCmd: []string{"synthetic-set"}}
	if err := cb.Set(ctx, "private clipboard bytes"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled clipboard write reported success: %v", err)
	}
}

// A failed temporary-file allocation must not execute a clipboard owner or
// disclose the selection bytes in diagnostics, regardless of backend mode.
func TestClipboardTempFileFailureNeverRunsSelectionOwner(t *testing.T) {
	for _, tc := range []struct{ name, tool string }{
		{name: "xclip diagnostic capture", tool: "xclip"},
		{name: "wayland input staging", tool: "wl-copy"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			marker := filepath.Join(root, "owner-executed")
			bin := filepath.Join(root, tc.tool)
			if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf ran >"+marker+"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("TMPDIR", filepath.Join(root, "nonexistent-temporary-directory"))
			backend := &extCmdClipboard{setCmd: []string{bin}, getCmd: []string{"false"}}
			const privateContent = "confidential clipboard contents"
			err := backend.Set(context.Background(), privateContent)
			if !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), "clipboard set:") {
				t.Fatalf("temporary capture error = %v", err)
			}
			if strings.Contains(err.Error(), privateContent) {
				t.Fatalf("error disclosed clipboard contents: %v", err)
			}
			if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("clipboard owner unexpectedly executed: %v", statErr)
			}
		})
	}
}
