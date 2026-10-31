package clipboard

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestWaylandOwnerCleanupReapsSyntheticSelectionOwner(t *testing.T) {
	for _, tc := range []struct {
		name        string
		alreadyDone bool
	}{
		{"stop active owner", false}, {"stop already reaped owner", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			// A direct sleep process models a foreground wl-copy owner without touching
			// a real Wayland compositor or leaving a shell subprocess behind.
			executable, err := exec.LookPath("sleep")
			if err != nil {
				t.Skipf("sleep unavailable: %v", err)
			}
			cmd := exec.Command(executable, "30")
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			done := make(chan struct{})
			go func() { _ = cmd.Wait(); close(done) }()
			if tc.alreadyDone {
				_ = cmd.Process.Kill()
				<-done
			}
			stopWaylandOwner(cmd, done)
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("clipboard process was not reaped")
			}
			if tc.alreadyDone && cmd.ProcessState == nil {
				t.Fatal("process did not finish")
			}
			if _, err := os.Stat(filepath.Join(dir, "clipboard-owned")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("synthetic cleanup mutated unrelated path: %v", err)
			}
		})
	}
	stopWaylandOwner(nil, nil)
	stopWaylandOwner(&exec.Cmd{}, nil)
}

func TestWaylandClipboardInputIsPrivateAndComplete(t *testing.T) {
	for _, content := range []string{"", "hello\n\n", "É😀\x00end"} {
		t.Run(content, func(t *testing.T) {
			file, path, err := waylandInput(content)
			if err != nil {
				t.Fatal(err)
			}
			defer os.Remove(path)
			defer file.Close()
			if info, lintShadow65 := os.Stat(path); lintShadow65 != nil || info.Mode().Perm()&0077 != 0 {
				t.Fatalf("clipboard input is not private: %v %v", info, lintShadow65)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != content {
				t.Fatalf("input content changed: got %q want %q", data, content)
			}
			if offset, err := file.Seek(0, 1); err != nil || offset != 0 {
				t.Fatalf("input stream not rewound: %d %v", offset, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cb := &extCmdClipboard{setCmd: []string{"wl-copy"}}
	if err := cb.setWayland(ctx, "secret"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled write launched clipboard owner: %v", err)
	}
}
