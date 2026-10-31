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

func TestExternalClipboardGetReportsStderrAndExitFailure(t *testing.T) {
	c := &extCmdClipboard{getCmd: []string{"sh", "-c", "printf 'selection refused' >&2; exit 17"}}
	text, err := c.Get(context.Background())
	if text != "" || err == nil || !strings.Contains(err.Error(), "selection refused") || !strings.Contains(err.Error(), "exit status 17") {
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
	if err == nil || !strings.Contains(err.Error(), "selection denied") || !strings.Contains(err.Error(), "exit status 23") {
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
