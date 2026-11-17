//go:build linux

package clipboard

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestExternalClipboardCommandsPreferCallerCancellationAfterStart(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(context.Context, string) error
	}{
		{"get", func(ctx context.Context, command string) error {
			backend := &extCmdClipboard{getCmd: []string{"sh", "-c", command}}
			_, err := backend.Get(ctx)
			return err
		}},
		{"set", func(ctx context.Context, command string) error {
			backend := &extCmdClipboard{setCmd: []string{"sh", "-c", command}}
			return backend.Set(ctx, "private selection")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fifo := filepath.Join(t.TempDir(), "hold-command")
			if err := unix.Mkfifo(fifo, 0600); err != nil {
				t.Fatal(err)
			}
			command := "kill -USR1 \"$PPID\"; exec cat -- " + shellQuoteClipboardPath(fifo)
			started := make(chan os.Signal, 1)
			signal.Notify(started, syscall.SIGUSR1)
			defer signal.Stop(started)

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := make(chan error, 1)
			go func() { result <- tc.call(ctx, command) }()
			select {
			case <-started:
				cancel()
			case <-time.After(3 * time.Second):
				cancel()
				t.Fatal("external clipboard command did not announce startup")
			}
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "signal: killed") {
					t.Fatalf("%s cancellation returned %v, want caller cancellation", tc.name, err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("external clipboard command remained after cancellation")
			}
		})
	}
}

func shellQuoteClipboardPath(path string) string {
	return "'" + strings.ReplaceAll(path, "'", "'\\''") + "'"
}
