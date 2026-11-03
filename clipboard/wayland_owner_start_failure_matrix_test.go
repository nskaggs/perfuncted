package clipboard

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// An external clipboard owner can fail to launch after staging its input.
// Neither the staged bytes nor its diagnostic capture may survive failure.
func TestWaylandOwnerStartFailureCleansPrivateStaging(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	backend := &extCmdClipboard{setCmd: []string{filepath.Join(dir, "missing-wl-copy")}}
	input, inputPath, err := waylandInput("private clipboard bytes")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(inputPath)
	defer input.Close()
	owner, done, err := backend.startWaylandOwner(context.Background(), input)
	if owner != nil || done != nil || err == nil || !strings.Contains(err.Error(), "clipboard set") {
		t.Fatalf("missing owner launched: owner=%v done=%v err=%v", owner, done, err)
	}
	captures, err := filepath.Glob(filepath.Join(dir, "perfuncted-clipboard-stderr-*"))
	if err != nil || len(captures) != 0 {
		t.Fatalf("failed owner leaked diagnostic files: %v %v", captures, err)
	}
	if content, err := os.ReadFile(inputPath); err != nil || string(content) != "private clipboard bytes" {
		t.Fatalf("staged bytes changed after failed start: %q %v", content, err)
	}
}

func TestWaylandOwnerStartFailsWhenPrivateStderrUnavailable(t *testing.T) {
	dir := t.TempDir()
	input, inputPath, err := waylandInput("private")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(inputPath)
	defer input.Close()
	t.Setenv("TMPDIR", filepath.Join(dir, "does-not-exist"))
	backend := &extCmdClipboard{setCmd: []string{"/bin/true"}}
	owner, done, err := backend.startWaylandOwner(context.Background(), input)
	if owner != nil || done != nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("invalid stderr staging: owner=%v done=%v err=%v", owner, done, err)
	}
}

func TestWaylandOwnerStartReapsExitedProcessAndDeletesDiagnosticFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	input, inputPath, err := waylandInput("private")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(inputPath)
	defer input.Close()
	backend := &extCmdClipboard{setCmd: []string{"/bin/true"}}
	owner, done, err := backend.startWaylandOwner(context.Background(), input)
	if err != nil || owner == nil || done == nil {
		t.Fatalf("expected synthetic owner: %v %v %v", owner, done, err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("finished owner not reaped")
	}
	if owner.ProcessState == nil {
		t.Fatal("owner exited but was not waited")
	}
	captures, err := filepath.Glob(filepath.Join(dir, "perfuncted-clipboard-stderr-*"))
	if err != nil || len(captures) != 0 {
		t.Fatalf("finished owner retained diagnostic files: %v %v", captures, err)
	}
}
