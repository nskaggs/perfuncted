package clipboard

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWaylandReadinessContextHonorsCallerDeadline(t *testing.T) {
	deadline := time.Now().Add(time.Minute)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()

	readyCtx, readyCancel := waylandReadinessContext(ctx)
	defer readyCancel()
	got, ok := readyCtx.Deadline()
	if !ok || !got.Equal(deadline) {
		t.Fatalf("readiness deadline = %v, present=%v, want caller policy deadline %v", got, ok, deadline)
	}
}

func TestWaylandReadinessContextKeepsDirectCallerFallback(t *testing.T) {
	readyCtx, cancel := waylandReadinessContext(context.Background())
	defer cancel()

	deadline, ok := readyCtx.Deadline()
	remaining := time.Until(deadline)
	if !ok || remaining <= 0 || remaining > time.Second {
		t.Fatalf("readiness deadline remaining = %v, present=%v, want positive and <= 1s", remaining, ok)
	}
}

// A real wl-copy holds the selection open until the clipboard is replaced, so
// the owner outlives the Set call that started it. Set must return on its own
// readiness rather than by waiting the owner out.
//
// The two outcomes are separated by orders of magnitude, not by a margin that a
// loaded machine can eat. Set spawns the owner and returns; its own work is one
// fork/exec, so it completes in about a millisecond. An implementation that
// waits instead blocks until the bounded owner lifetime elapses, which is
// capped at 5s, and then kills the owner before returning nil. The caller
// deadline is therefore 10s, well past that cap, so a waiting implementation is
// reported by the 2s bound instead of by a deadline error. The bound sits
// 2000x above the real cost and 2.5x below the failure it must catch.
func TestWaylandSetDoesNotWaitForPasteConsumer(t *testing.T) {
	dir := t.TempDir()
	output := filepath.Join(dir, "clipboard.txt")
	holding := filepath.Join(dir, "holding")
	tool := filepath.Join(dir, "wl-copy")
	// The owner consumes the clipboard content, announces that it is holding
	// the selection, and then stays alive well past any bound this test uses.
	script := "#!/bin/sh\ncat > \"$PF_TEST_CLIPBOARD_OUTPUT\"\n: > \"$PF_TEST_CLIPBOARD_HOLDING\"\nsleep 30\n"
	if err := os.WriteFile(tool, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake wl-copy: %v", err)
	}

	cb := &extCmdClipboard{
		setCmd: []string{tool, "--foreground"},
		env:    append(os.Environ(), "PF_TEST_CLIPBOARD_OUTPUT="+output, "PF_TEST_CLIPBOARD_HOLDING="+holding),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	started := time.Now()
	if err := cb.Set(ctx, "browser console command"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	elapsed := time.Since(started)
	if elapsed >= 2*time.Second {
		t.Fatalf("Set took %s: it waited out the paste consumer instead of returning on readiness", elapsed)
	}

	// The owner must have received the clipboard content and reached the point
	// where it is holding the selection, all without Set having waited for it.
	var delivered, holdingNow bool
	deadline := time.Now().Add(5 * time.Second)
	for !delivered || !holdingNow {
		if data, err := os.ReadFile(output); err == nil {
			delivered = string(data) == "browser console command"
		} else if !os.IsNotExist(err) {
			t.Fatalf("read clipboard output: %v", err)
		}
		if _, err := os.Stat(holding); err == nil {
			holdingNow = true
		} else if !os.IsNotExist(err) {
			t.Fatalf("stat owner state: %v", err)
		}
		if delivered && holdingNow {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("fake wl-copy state: content delivered=%t holding selection=%t, want both", delivered, holdingNow)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Every read of the compositor's answer forks a process, so the interval at
// which readiness is observed is the fork rate. A compositor that never
// answers must cost a bounded number of forks inside the readiness budget
// rather than one per poll tick.
func TestWaylandContentReadinessBoundsProcessCreations(t *testing.T) {
	dir := t.TempDir()
	count := filepath.Join(dir, "paste-count")
	paste := filepath.Join(dir, "wl-paste")
	// The paste tool records each invocation and never reports the wanted text,
	// which is the compositor that never becomes ready.
	script := "#!/bin/sh\necho x >> \"$PF_TEST_PASTE_COUNT\"\nprintf 'not-the-wanted-text'\n"
	if err := os.WriteFile(paste, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake wl-paste: %v", err)
	}

	cb := &extCmdClipboard{
		getCmd: []string{paste, "--no-newline"},
		env:    append(os.Environ(), "PF_TEST_PASTE_COUNT="+count),
	}
	// The readiness window is long enough that a fixed short interval would
	// produce a count that is orders of magnitude above the bounded one.
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()

	if err := cb.waitWaylandContent(ctx, "wanted"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waitWaylandContent error = %v, want context.DeadlineExceeded", err)
	}

	data, err := os.ReadFile(count)
	if err != nil {
		t.Fatalf("read paste count: %v", err)
	}
	calls := bytes.Count(data, []byte("x"))
	// A fixed 10ms interval over this window would fork roughly 60 times. The
	// backed-off schedule answers about seven times, so this bound separates the
	// two designs by a wide margin rather than by a timing threshold.
	if calls == 0 {
		t.Fatal("no paste attempt was made; readiness was never observed")
	}
	if calls > 12 {
		t.Fatalf("paste attempts = %d, want a bounded count well below one per 10ms tick", calls)
	}
}

// A compositor that answers must still be recognized on the first read, so the
// backing off schedule costs nothing on the ordinary path.
func TestWaylandContentReadinessAcceptsImmediateAnswer(t *testing.T) {
	dir := t.TempDir()
	paste := filepath.Join(dir, "wl-paste")
	if err := os.WriteFile(paste, []byte("#!/bin/sh\nprintf 'wanted'\n"), 0o700); err != nil {
		t.Fatalf("write fake wl-paste: %v", err)
	}

	cb := &extCmdClipboard{getCmd: []string{paste, "--no-newline"}}
	if err := cb.waitWaylandContent(context.Background(), "wanted"); err != nil {
		t.Fatalf("waitWaylandContent: %v", err)
	}
}
