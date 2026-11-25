package clipboard

import (
	"bytes"
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/internal/executil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
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
	paste := filepath.Join(dir, "wl-paste")
	// Readiness must be observed from a separate paste command, not inferred
	// merely because the foreground clipboard owner has started.
	pasteScript := "#!/bin/sh\n[ -e \"$PF_TEST_CLIPBOARD_HOLDING\" ] || exit 1\ncat \"$PF_TEST_CLIPBOARD_OUTPUT\"\n"
	if err := os.WriteFile(paste, []byte(pasteScript), 0o700); err != nil {
		t.Fatalf("write fake wl-paste: %v", err)
	}
	// The owner consumes the clipboard content, announces that it is holding
	// the selection, and then stays alive well past any bound this test uses.
	script := "#!/bin/sh\ncat > \"$PF_TEST_CLIPBOARD_OUTPUT\"\n: > \"$PF_TEST_CLIPBOARD_HOLDING\"\nsleep 30\n"
	if err := os.WriteFile(tool, []byte(script), 0o700); err != nil {
		t.Fatalf("write fake wl-copy: %v", err)
	}

	cb := &extCmdClipboard{
		setCmd: []string{tool, "--foreground"},
		getCmd: []string{paste, "--no-newline"},
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

// A previously populated selection may already have the exact requested text.
// That must not make Set succeed if the foreground wl-copy owner died before
// it took ownership: no new selection was established by this invocation.
func TestWaylandSetDoesNotAcceptStaleMatchingSelectionAfterOwnerExit(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	owner := filepath.Join(dir, "wl-copy")
	paste := filepath.Join(dir, "wl-paste")
	if err := os.WriteFile(owner, []byte("#!/bin/sh\nexit 23\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paste, []byte("#!/bin/sh\nsleep 0.15\nprintf 'old-selection'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	cb := &extCmdClipboard{setCmd: []string{owner, "--foreground"}, getCmd: []string{paste, "--no-newline"}}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := cb.Set(ctx, "old-selection"); err == nil || !strings.Contains(err.Error(), "owner") {
		t.Fatalf("Set falsely reported readiness from stale matching selection: %v", err)
	}
	for _, glob := range []string{"perfuncted-clipboard-input-*", "perfuncted-clipboard-stderr-*"} {
		files, err := filepath.Glob(filepath.Join(dir, glob))
		if err != nil || len(files) > 0 {
			t.Fatalf("temporary clipboard files leaked for %s: %v %v", glob, files, err)
		}
	}
}

// A still-living foreground owner with an observable selection must continue
// to satisfy the ordinary Set path, despite explicit liveness checking.
func TestWaylandSetAcceptsMatchingSelectionFromLiveOwner(t *testing.T) {
	dir := t.TempDir()
	owner := filepath.Join(dir, "wl-copy")
	paste := filepath.Join(dir, "wl-paste")
	if err := os.WriteFile(owner, []byte("#!/bin/sh\nexec sleep 30\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paste, []byte("#!/bin/sh\nprintf 'new-selection'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	cb := &extCmdClipboard{setCmd: []string{owner, "--foreground"}, getCmd: []string{paste, "--no-newline"}}
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	if err := cb.Set(ctx, "new-selection"); err != nil {
		t.Fatalf("live foreground owner rejected: %v", err)
	}
}

func TestWaylandInputPreservesExactBytesAndRewinds(t *testing.T) {
	text := "first\x00line\nπ\n" + strings.Repeat("x", 128*1024)
	f, path, err := waylandInput(text)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	defer os.Remove(path)
	if !strings.HasPrefix(filepath.Base(path), "perfuncted-clipboard-input-") {
		t.Fatalf("unexpected temporary name: %q", path)
	}
	if pos, err := f.Seek(0, 1); err != nil || pos != 0 {
		t.Fatalf("input not rewound: offset=%d err=%v", pos, err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != text {
		t.Fatalf("clipboard input bytes differ: got %d bytes, want %d", len(got), len(text))
	}
}

func TestWaylandInputReturnsCreateFailureWithoutFile(t *testing.T) {
	root := t.TempDir()
	absent := filepath.Join(root, "no-such-directory")
	t.Setenv("TMPDIR", absent)
	f, path, err := waylandInput("secret")
	if f != nil || path != "" || err == nil || !strings.Contains(err.Error(), "clipboard set: create input:") || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("create failure = (%v, %q, %v)", f, path, err)
	}
}

// Clipboard contents must remain private even if the caller uses a permissive
// process umask. Each active owner also needs a distinct staging file.
func TestWaylandInputPrivateModeAndIndependentOwnership(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	previous := syscall.Umask(0)
	defer syscall.Umask(previous)

	first, firstPath, err := waylandInput("first private selection")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	defer os.Remove(firstPath)
	second, secondPath, err := waylandInput("second private selection")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	defer os.Remove(secondPath)

	if firstPath == secondPath {
		t.Fatal("independent owners reused a staging file")
	}
	for _, path := range []string{firstPath, secondPath} {
		if filepath.Dir(path) != root {
			t.Fatalf("staging path outside controlled directory: %q", path)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if mode := info.Mode().Perm(); mode != 0600 {
			t.Fatalf("staging file %q has mode %04o; want 0600", path, mode)
		}
	}
	firstBytes, err := os.ReadFile(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := os.ReadFile(secondPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(firstBytes) != "first private selection" || string(secondBytes) != "second private selection" {
		t.Fatal("clipboard staging files mixed owner contents")
	}
}

// A Wayland clipboard owner alone is not proof that the compositor accepted
// the new selection. Without a paste probe the result cannot be verified, and
// the owner must not be started for an incomplete backend.
func TestWaylandClipboardSetRejectsMissingReadinessCommandBeforeOwnerSpawn(t *testing.T) {
	dir := t.TempDir()
	owner := filepath.Join(dir, "wl-copy")
	marker := filepath.Join(dir, "launched")
	if err := os.WriteFile(owner, []byte("#!/bin/sh\nprintf started > \""+marker+"\"\nexec sleep 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	backend := &extCmdClipboard{setCmd: []string{owner, "--foreground"}}
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	err := backend.Set(ctx, "private clipboard value")
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("Wayland clipboard accepted unobservable selection: %v", err)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("unverifiable clipboard owner was launched: %v", statErr)
	}
}

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

// The public Set entry point, not merely the owner helper, must fail closed
// when a configured Wayland owner executable disappears after discovery.
func TestWaylandSetMissingOwnerLeavesNoPrivateArtifacts(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	backend := &extCmdClipboard{setCmd: []string{filepath.Join(dir, "missing", "wl-copy"), "--foreground"}, getCmd: []string{"/bin/false"}}
	const payload = "private clipboard selection"
	err := backend.Set(context.Background(), payload)
	if !errors.Is(err, os.ErrNotExist) || strings.Contains(err.Error(), payload) {
		t.Fatalf("missing owner returned unexpected error: %v", err)
	}
	for _, pattern := range []string{"perfuncted-clipboard-input-*", "perfuncted-clipboard-stderr-*"} {
		matches, globErr := filepath.Glob(filepath.Join(dir, pattern))
		if globErr != nil || len(matches) != 0 {
			t.Fatalf("staging leaked for %s: %v %v", pattern, matches, globErr)
		}
	}
}

// No clipboard command may run after cancellation, even if the caller invokes
// the readiness function directly with a previous read configuration.
func TestWaylandReadinessPreCanceledNeverObservesSelection(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	tool := filepath.Join(dir, "wl-paste")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\nprintf 'ran' > '"+marker+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	backend := &extCmdClipboard{getCmd: []string{tool}}
	if err := backend.waitWaylandContent(ctx, "sensitive"); !errors.Is(err, context.Canceled) {
		t.Fatalf("readiness cancellation = %v", err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled readiness executed paste: %v", err)
	}
}

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

// A compositor observation can race caller cancellation. A successful paste
// result from a subprocess that disregards its context must not authorize
// Set's success after its caller has already canceled.
func TestWaylandReadinessRejectsCanceledContextAfterMatchingPaste(t *testing.T) {
	original := executil.CommandContext
	t.Cleanup(func() { executil.CommandContext = original })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	executil.CommandContext = func(context.Context, string, ...string) *exec.Cmd {
		cancel()
		return exec.Command("sh", "-c", "printf 'synthetic-selection'")
	}
	cb := &extCmdClipboard{getCmd: []string{"wl-paste"}}
	if err := cb.waitWaylandContent(ctx, "synthetic-selection"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled compositor observation reported selection ready: %v", err)
	}
}

// The private foreground owner must be reaped if the read that proved
// clipboard readiness was concurrently canceled. Otherwise a canceled
// clipboard request can retain the selection for the full owner timeout.
func TestWaylandSetLateCancellationReapsForegroundOwner(t *testing.T) {
	original := executil.CommandContext
	t.Cleanup(func() { executil.CommandContext = original })
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var owner *exec.Cmd
	executil.CommandContext = func(_ context.Context, name string, _ ...string) *exec.Cmd {
		if name == "wl-copy" {
			owner = exec.Command("sleep", "30")
			return owner
		}
		cancel()
		return exec.Command("sh", "-c", "printf 'synthetic-selection'")
	}
	cb := &extCmdClipboard{setCmd: []string{"wl-copy", "--foreground"}, getCmd: []string{"wl-paste", "--no-newline"}}
	if err := cb.Set(ctx, "synthetic-selection"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Set reported success: %v", err)
	}
	if owner == nil || owner.ProcessState == nil {
		// A reaped process killed by a signal reports Exited() == false;
		// ProcessState publication by Wait is the relevant lifecycle proof.
		t.Fatalf("canceled Set did not reap foreground owner: %+v", owner)
	}
	for _, pattern := range []string{"perfuncted-clipboard-input-*", "perfuncted-clipboard-stderr-*"} {
		found, err := filepath.Glob(filepath.Join(dir, pattern))
		if err != nil || len(found) != 0 {
			t.Fatalf("clipboard tmpfiles leaked (%s): %v %v", pattern, found, err)
		}
	}
}

// An absent reader cannot establish that the compositor serves the selection.
// Readiness is a positive protocol observation, not merely absence of failure.
func TestWaylandReadinessRefusesMissingReader(t *testing.T) {
	for _, command := range [][]string{nil, {}, {""}} {
		backend := &extCmdClipboard{getCmd: command}
		err := backend.waitWaylandContent(context.Background(), "private clipboard selection")
		if err == nil || !strings.Contains(err.Error(), "readiness") {
			t.Fatalf("missing reader %q accepted as ready: %v", command, err)
		}
	}
}

// A foreground owner can remain alive without the compositor serving the
// requested selection. A successful launch is not a successful clipboard
// write: readiness mismatch must revoke and reap that owner.
func TestWaylandSetLiveOwnerWithUnmatchedSelectionFailsAndReaps(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", dir)
	owner := filepath.Join(dir, "wl-copy")
	paste := filepath.Join(dir, "wl-paste")
	if err := os.WriteFile(owner, []byte("#!/bin/sh\nexec sleep 30\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paste, []byte("#!/bin/sh\nprintf 'stale-selection'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	cb := &extCmdClipboard{setCmd: []string{owner, "--foreground"}, getCmd: []string{paste, "--no-newline"}}
	err := cb.Set(context.Background(), "private-new-selection")
	if err == nil || !strings.Contains(err.Error(), "clipboard set") {
		t.Fatalf("unverified selection accepted: %v", err)
	}
	for _, pattern := range []string{"perfuncted-clipboard-input-*", "perfuncted-clipboard-stderr-*"} {
		matches, globErr := filepath.Glob(filepath.Join(dir, pattern))
		if globErr != nil || len(matches) != 0 {
			t.Fatalf("unverified owner leaked %s: %v %v", pattern, matches, globErr)
		}
	}
}

// A previous selection can be arbitrarily larger than the requested text.
// The readiness comparison must not buffer it in memory merely to reject it.
func TestWaylandReadinessStreamingComparisonBoundsUnrelatedSelections(t *testing.T) {
	const secret = "synthetic-private-selection-value"
	for _, tc := range []struct {
		name, output, expected string
		match                  bool
	}{
		{"matching", secret, secret, true},
		{"mismatching", "different", secret, false},
		{"extra bytes", secret + "-suffix", secret, false},
		{"truncated", secret[:10], secret, false},
		{"large unrelated", strings.Repeat("x", 4<<20), secret, false},
		{"empty", "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			content := filepath.Join(dir, "selection")
			if err := os.WriteFile(content, []byte(tc.output), 0600); err != nil {
				t.Fatal(err)
			}
			backend := &extCmdClipboard{getCmd: []string{"cat", content}}
			if got := backend.waylandContentMatches(context.Background(), tc.expected); got != tc.match {
				t.Fatalf("comparison=%v, want %v", got, tc.match)
			}
			// The streaming comparator must retain only constant-size state,
			// independent of the prior clipboard selection length.
			comparator := &clipboardContentMatcher{want: tc.expected}
			for offset := 0; offset < len(tc.output); offset += 8192 {
				end := offset + 8192
				if end > len(tc.output) {
					end = len(tc.output)
				}
				if _, err := comparator.Write([]byte(tc.output[offset:end])); err != nil {
					t.Fatal(err)
				}
			}
			if got := comparator.matches(); got != tc.match {
				t.Fatalf("streaming match=%v want=%v", got, tc.match)
			}
		})
	}
}
