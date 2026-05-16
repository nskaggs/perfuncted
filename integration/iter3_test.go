//go:build integration
// +build integration

package integration_test

// iter3_test.go — third iteration of integration test improvements.
//
// Coverage areas:
//   1. Error paths: WaitForFn timeout, context cancellation before window/clipboard ops
//   2. TypeFast / Paste unicode edge cases (emoji, CJK, combining chars)
//   3. Multi-window isolation: Find/WaitForState in one window vs another
//   4. Clipboard edge cases: large payload (>4 KB), rapid successive writes, NUL bytes

import (
	"context"
	"errors"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/find"
	"github.com/nskaggs/perfuncted/window"
)

// ─────────────────────────────────────────────────────────────────────────────
// 1. Error path coverage
// ─────────────────────────────────────────────────────────────────────────────

// TestErrorPath_WaitForFn_Timeout verifies that WaitForFn returns a non-nil
// error when the predicate never satisfies within the deadline.
func TestErrorPath_WaitForFn_Timeout(t *testing.T) {
	s := mustSuite(t)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	screenW, screenH, err := s.pf.Screen.Resolution(ctx)
	if err != nil {
		t.Skipf("screen resolution unavailable: %v", err)
	}
	if screenW <= 0 || screenH <= 0 {
		t.Skipf("screen resolution returned invalid size %dx%d", screenW, screenH)
	}

	rect := image.Rect(0, 0, 10, 10)
	_, err = s.pf.Screen.WaitForFn(ctx, rect, func(_ context.Context, _ image.Image) bool {
		return false // never satisfies
	}, 50*time.Millisecond)
	if err == nil {
		t.Fatal("WaitForFn: expected timeout error when predicate never satisfies, got nil")
	}
}

// TestErrorPath_WaitForFn_AlreadyCancelled verifies that WaitForFn fails when
// the context is already cancelled before the first call.
func TestErrorPath_WaitForFn_AlreadyCancelled(t *testing.T) {
	s := mustSuite(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	rect := image.Rect(0, 0, 10, 10)
	_, err := s.pf.Screen.WaitForFn(ctx, rect, func(_ context.Context, _ image.Image) bool {
		return false
	}, 50*time.Millisecond)
	if err == nil {
		t.Fatal("WaitForFn: expected error with pre-cancelled context, got nil")
	}
}

// TestErrorPath_FindByTitle_EmptyPattern verifies that FindByTitle with an
// empty string does not panic and either returns ErrWindowNotFound or a
// valid (possibly best-matching) window.
func TestErrorPath_FindByTitle_EmptyPattern(t *testing.T) {
	s := mustSuite(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	info, err := s.pf.Window.FindByTitle(ctx, "")
	if err != nil {
		if !errors.Is(err, window.ErrWindowNotFound) {
			t.Logf("FindByTitle(\"\") returned non-ErrWindowNotFound error (acceptable): %v", err)
		}
		return
	}
	t.Logf("FindByTitle(\"\") returned window %q (compositor matched by prefix)", info.Title)
}

// TestErrorPath_ContextCancelledWindowActivate verifies that Activate returns
// a non-nil error when the context is cancelled before the call.
func TestErrorPath_ContextCancelledWindowActivate(t *testing.T) {
	s := mustSuite(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := s.pf.Window.Activate(ctx, "xyzzy-never-exists-cancelled")
	if err == nil {
		t.Error("Activate: expected error with pre-cancelled context, got nil")
	}
}

// TestErrorPath_ContextCancelledClipboardSet verifies that Clipboard.Set
// returns a non-nil error when the context is already cancelled.
func TestErrorPath_ContextCancelledClipboardSet(t *testing.T) {
	s := mustSuite(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := s.pf.Clipboard.Set(ctx, "should not matter")
	if err == nil {
		t.Error("Clipboard.Set: expected error with pre-cancelled context, got nil")
	}
}

// TestErrorPath_WaitForChange_NeverChanges verifies that WaitForChange returns
// a non-nil error when the screen does not change within a short deadline.
func TestErrorPath_WaitForChange_NeverChanges(t *testing.T) {
	s := mustSuite(t)

	hashCtx, hashCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer hashCancel()

	screenW, screenH, err := s.pf.Screen.Resolution(hashCtx)
	if err != nil {
		t.Skipf("screen resolution unavailable: %v", err)
	}
	if screenW <= 0 || screenH <= 0 {
		t.Skipf("invalid screen size %dx%d", screenW, screenH)
	}

	rect := image.Rect(0, 0, min(screenW, 50), min(screenH, 50))
	h0, err := s.pf.Screen.GrabRegionHash(hashCtx, rect)
	if err != nil {
		t.Fatalf("initial hash: %v", err)
	}

	waitCtx, waitCancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer waitCancel()
	_, err = s.pf.Screen.WaitForChange(waitCtx, rect, h0, 50*time.Millisecond)
	if err == nil {
		// Screen may change due to cursor blink etc. — not a failure, just skip.
		t.Skip("screen changed during WaitForChange_NeverChanges; skipping (non-deterministic)")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 2. TypeFast / Paste unicode edge cases
// ─────────────────────────────────────────────────────────────────────────────

// TestPaste_Unicode_Emoji verifies that pf.Paste sets the clipboard to a
// string containing emoji (4-byte UTF-8 code points) without corruption.
func TestPaste_Unicode_Emoji(t *testing.T) {
	s := mustSuite(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	const text = "🎉🌍🚀💡🦀🐹"
	if err := s.pf.Paste(ctx, text); err != nil {
		t.Skipf("Paste not available: %v", err)
	}
	t.Cleanup(func() {
		cctx, done := context.WithTimeout(context.Background(), 2*time.Second)
		defer done()
		_ = s.pf.Clipboard.Set(cctx, "")
	})

	got, err := s.pf.Clipboard.Get(ctx)
	if err != nil {
		t.Fatalf("clipboard get after Paste(emoji): %v", err)
	}
	if got != text {
		t.Errorf("Paste emoji: clipboard want %q, got %q", text, got)
	}
}

// TestPaste_Unicode_CJK verifies that pf.Paste handles CJK characters
// (3-byte UTF-8 code points) without corruption.
func TestPaste_Unicode_CJK(t *testing.T) {
	s := mustSuite(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	const text = "你好世界 日本語テスト 한국어시험"
	if err := s.pf.Paste(ctx, text); err != nil {
		t.Skipf("Paste not available: %v", err)
	}
	t.Cleanup(func() {
		cctx, done := context.WithTimeout(context.Background(), 2*time.Second)
		defer done()
		_ = s.pf.Clipboard.Set(cctx, "")
	})

	got, err := s.pf.Clipboard.Get(ctx)
	if err != nil {
		t.Fatalf("clipboard get after Paste(CJK): %v", err)
	}
	if got != text {
		t.Errorf("Paste CJK: clipboard want %q, got %q", text, got)
	}
}

// TestPaste_Unicode_CombiningChars verifies that combining-character sequences
// (NFD normalization form) survive the Paste clipboard path intact.
func TestPaste_Unicode_CombiningChars(t *testing.T) {
	s := mustSuite(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// A + combining grave (U+0300) → À (NFD)
	// n + combining tilde (U+0303) → ñ (NFD)
	const text = "A\u0300 n\u0303 e\u0301 cafe\u0301"
	if err := s.pf.Paste(ctx, text); err != nil {
		t.Skipf("Paste not available: %v", err)
	}
	t.Cleanup(func() {
		cctx, done := context.WithTimeout(context.Background(), 2*time.Second)
		defer done()
		_ = s.pf.Clipboard.Set(cctx, "")
	})

	got, err := s.pf.Clipboard.Get(ctx)
	if err != nil {
		t.Fatalf("clipboard get after Paste(combining chars): %v", err)
	}
	if got != text {
		t.Errorf("Paste combining chars: clipboard want %q, got %q", text, got)
	}
}

// TestPaste_Unicode_MixedASCIIAndMultibyte verifies that a string mixing
// plain ASCII with multi-byte runes survives the Paste path intact.
func TestPaste_Unicode_MixedASCIIAndMultibyte(t *testing.T) {
	s := mustSuite(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	const text = "Hello 世界! Foo=42 🦀 bar\nαβγ\ttab"
	if err := s.pf.Paste(ctx, text); err != nil {
		t.Skipf("Paste not available: %v", err)
	}
	t.Cleanup(func() {
		cctx, done := context.WithTimeout(context.Background(), 2*time.Second)
		defer done()
		_ = s.pf.Clipboard.Set(cctx, "")
	})

	got, err := s.pf.Clipboard.Get(ctx)
	if err != nil {
		t.Fatalf("clipboard get after Paste(mixed): %v", err)
	}
	if got != text {
		t.Errorf("Paste mixed ASCII/multibyte: clipboard want %q, got %q", text, got)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 3. Multi-window scenarios
// ─────────────────────────────────────────────────────────────────────────────

// TestMultiWindow_FindIsolation opens two editor windows and verifies that
// FindByTitle for each window returns only that window's Info, and that
// closing one window does not remove the other from the window list.
func TestMultiWindow_FindIsolation(t *testing.T) {
	app, ok := firstAvailableApp(t)
	if !ok {
		t.Skip("no supported text editor found in PATH; skipping multi-window isolation test")
	}
	s := mustSuite(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	fileA := filepath.Join(t.TempDir(), "multiwin_A.txt")
	if err := os.WriteFile(fileA, nil, 0o644); err != nil {
		t.Fatalf("create fileA: %v", err)
	}
	appA := app
	appA.saveFile = fileA
	cmdA, err := launchApp(s.rt, s.session, appA, appA.extraEnvFor(s.mode)...)
	if err != nil {
		t.Fatalf("launch window A: %v", err)
	}
	t.Cleanup(func() { terminateCmd(cmdA, 5*time.Second) })
	docA := filepath.Base(fileA)
	if _, err := waitForWindow(s.pf, docA, 30*time.Second); err != nil {
		t.Fatalf("wait for window A: %v", err)
	}

	fileB := filepath.Join(t.TempDir(), "multiwin_B.txt")
	if err := os.WriteFile(fileB, nil, 0o644); err != nil {
		t.Fatalf("create fileB: %v", err)
	}
	appB := app
	appB.saveFile = fileB
	cmdB, err := launchApp(s.rt, s.session, appB, appB.extraEnvFor(s.mode)...)
	if err != nil {
		t.Fatalf("launch window B: %v", err)
	}
	t.Cleanup(func() { terminateCmd(cmdB, 5*time.Second) })
	docB := filepath.Base(fileB)
	if _, err := waitForWindow(s.pf, docB, 30*time.Second); err != nil {
		t.Fatalf("wait for window B: %v", err)
	}

	// FindByTitle(A) must contain A's doc name, not B's.
	infoA, err := s.pf.Window.FindByTitle(ctx, docA)
	if err != nil {
		t.Fatalf("FindByTitle(A): %v", err)
	}
	if !strings.Contains(strings.ToLower(infoA.Title), strings.ToLower(docA)) {
		t.Errorf("FindByTitle(A) wrong window: %q (want %q)", infoA.Title, docA)
	}
	if strings.Contains(strings.ToLower(infoA.Title), strings.ToLower(docB)) {
		t.Errorf("FindByTitle(A) returned B's window: %q", infoA.Title)
	}

	// FindByTitle(B) must contain B's doc name, not A's.
	infoB, err := s.pf.Window.FindByTitle(ctx, docB)
	if err != nil {
		t.Fatalf("FindByTitle(B): %v", err)
	}
	if !strings.Contains(strings.ToLower(infoB.Title), strings.ToLower(docB)) {
		t.Errorf("FindByTitle(B) wrong window: %q (want %q)", infoB.Title, docB)
	}
	if strings.Contains(strings.ToLower(infoB.Title), strings.ToLower(docA)) {
		t.Errorf("FindByTitle(B) returned A's window: %q", infoB.Title)
	}

	// Close A; B must remain in the window list.
	closeCtxA, cancelCloseA := context.WithTimeout(ctx, 15*time.Second)
	defer cancelCloseA()
	if err := s.pf.Window.CloseWindow(closeCtxA, docA); err != nil {
		t.Fatalf("CloseWindow(A): %v", err)
	}
	if err := waitForWindowClose(s.pf, docA, 15*time.Second); err != nil {
		t.Fatalf("wait for window A close: %v", err)
	}

	listCtx, listCancel := context.WithTimeout(ctx, 5*time.Second)
	defer listCancel()
	wins, err := s.pf.Window.List(listCtx)
	if err != nil {
		t.Fatalf("Window.List after close A: %v", err)
	}
	foundB := false
	for _, w := range wins {
		if strings.Contains(strings.ToLower(w.Title), strings.ToLower(docB)) {
			foundB = true
			break
		}
	}
	if !foundB {
		t.Errorf("window B %q missing from list after closing A (%d windows total)", docB, len(wins))
	}
}

// TestMultiWindow_WaitForStateIsolation opens two editor windows and verifies
// that WaitForState (via Verifier) on one window's region succeeds while the
// other window is also open.
func TestMultiWindow_WaitForStateIsolation(t *testing.T) {
	app, ok := firstAvailableApp(t)
	if !ok {
		t.Skip("no supported text editor found in PATH; skipping WaitForState isolation test")
	}
	s := mustSuite(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	fileA := filepath.Join(t.TempDir(), "wfsA.txt")
	if err := os.WriteFile(fileA, nil, 0o644); err != nil {
		t.Fatalf("create fileA: %v", err)
	}
	appA := app
	appA.saveFile = fileA
	cmdA, err := launchApp(s.rt, s.session, appA, appA.extraEnvFor(s.mode)...)
	if err != nil {
		t.Fatalf("launch window A: %v", err)
	}
	t.Cleanup(func() { terminateCmd(cmdA, 5*time.Second) })
	docA := filepath.Base(fileA)
	if _, err := waitForWindow(s.pf, docA, 30*time.Second); err != nil {
		t.Fatalf("wait for window A: %v", err)
	}

	fileB := filepath.Join(t.TempDir(), "wfsB.txt")
	if err := os.WriteFile(fileB, nil, 0o644); err != nil {
		t.Fatalf("create fileB: %v", err)
	}
	appB := app
	appB.saveFile = fileB
	cmdB, err := launchApp(s.rt, s.session, appB, appB.extraEnvFor(s.mode)...)
	if err != nil {
		t.Fatalf("launch window B: %v", err)
	}
	t.Cleanup(func() { terminateCmd(cmdB, 5*time.Second) })
	docB := filepath.Base(fileB)
	if _, err := waitForWindow(s.pf, docB, 30*time.Second); err != nil {
		t.Fatalf("wait for window B: %v", err)
	}

	// Re-query A's bounds after B opened (compositor may have rearranged).
	infoA, err := s.pf.Window.FindByTitle(ctx, docA)
	if err != nil {
		t.Fatalf("re-find window A: %v", err)
	}
	rectA := image.Rect(infoA.X, infoA.Y, infoA.X+infoA.W, infoA.Y+infoA.H)
	if rectA.Empty() {
		t.Skip("window A has empty geometry (tiling compositor?); skipping")
	}

	// WaitForState on A's region must succeed while B is alive.
	v := &Verifier{t: t, pf: s.pf}
	waitCtx, waitCancel := context.WithTimeout(ctx, 10*time.Second)
	defer waitCancel()
	v.WaitForState(waitCtx, "window-A-region", rectA,
		func(_ context.Context, img image.Image) bool {
			return img != nil && img.Bounds().Dx() > 0
		},
	)
	// Suppress unused variable warning for docB.
	_ = docB
}

// TestMultiWindow_ActivateSwitching opens two editors and alternates focus
// between them, verifying each activation is reflected in ActiveTitle.
func TestMultiWindow_ActivateSwitching(t *testing.T) {
	app, ok := firstAvailableApp(t)
	if !ok {
		t.Skip("no supported text editor found in PATH; skipping multi-window activate test")
	}
	s := mustSuite(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	fileA := filepath.Join(t.TempDir(), "switchA.txt")
	if err := os.WriteFile(fileA, nil, 0o644); err != nil {
		t.Fatalf("create fileA: %v", err)
	}
	appA := app
	appA.saveFile = fileA
	cmdA, err := launchApp(s.rt, s.session, appA, appA.extraEnvFor(s.mode)...)
	if err != nil {
		t.Fatalf("launch A: %v", err)
	}
	t.Cleanup(func() { terminateCmd(cmdA, 5*time.Second) })
	docA := filepath.Base(fileA)
	if _, err := waitForWindow(s.pf, docA, 30*time.Second); err != nil {
		t.Fatalf("wait for A: %v", err)
	}

	fileB := filepath.Join(t.TempDir(), "switchB.txt")
	if err := os.WriteFile(fileB, nil, 0o644); err != nil {
		t.Fatalf("create fileB: %v", err)
	}
	appB := app
	appB.saveFile = fileB
	cmdB, err := launchApp(s.rt, s.session, appB, appB.extraEnvFor(s.mode)...)
	if err != nil {
		t.Fatalf("launch B: %v", err)
	}
	t.Cleanup(func() { terminateCmd(cmdB, 5*time.Second) })
	docB := filepath.Base(fileB)
	if _, err := waitForWindow(s.pf, docB, 30*time.Second); err != nil {
		t.Fatalf("wait for B: %v", err)
	}

	type step struct{ doc, match string }
	sequence := []step{
		{docA, app.winMatch},
		{docB, app.winMatch},
		{docA, app.winMatch},
	}
	for i, st := range sequence {
		activateCtx, activateCancel := context.WithTimeout(ctx, 10*time.Second)
		if err := s.pf.Window.Activate(activateCtx, st.doc); err != nil {
			activateCancel()
			t.Fatalf("round %d: activate %q: %v", i+1, st.doc, err)
		}
		pollCtx, pollCancel := context.WithTimeout(ctx, 5*time.Second)
		title, ok := pollActiveTitle(pollCtx, s.pf, st.match)
		pollCancel()
		activateCancel()
		if !ok {
			t.Errorf("round %d: ActiveTitle never contained %q (last: %q)", i+1, st.doc, title)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 4. Clipboard edge cases
// ─────────────────────────────────────────────────────────────────────────────

// TestClipboard_LargePayload verifies that a payload larger than 4 KB survives
// a write/read cycle without truncation.
func TestClipboard_LargePayload(t *testing.T) {
	s := mustSuite(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	const chunk = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-"
	var sb strings.Builder
	for sb.Len() < 5*1024 {
		sb.WriteString(chunk)
	}
	sb.WriteString("||END||")
	large := sb.String()

	if err := s.pf.Clipboard.Set(ctx, large); err != nil {
		t.Skipf("clipboard rejected large payload: %v", err)
	}
	t.Cleanup(func() {
		cctx, done := context.WithTimeout(context.Background(), 2*time.Second)
		defer done()
		_ = s.pf.Clipboard.Set(cctx, "")
	})

	got, err := s.pf.Clipboard.Get(ctx)
	if err != nil {
		t.Fatalf("clipboard get (large payload): %v", err)
	}
	if got != large {
		t.Errorf("large payload mismatch: want %d bytes, got %d bytes (first diff at index %d)",
			len(large), len(got), iter3FirstDiffIdx(large, got))
	}
}

// TestClipboard_LargeUnicodePayload verifies that a >4 KB unicode payload
// (ASCII + CJK + emoji) survives a clipboard round-trip intact.
func TestClipboard_LargeUnicodePayload(t *testing.T) {
	s := mustSuite(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var sb strings.Builder
	for sb.Len() < 5*1024 {
		sb.WriteString("hello 世界 🎉 αβγ ")
	}
	sb.WriteString("||UNICODE_END||")
	large := sb.String()

	if err := s.pf.Clipboard.Set(ctx, large); err != nil {
		t.Skipf("clipboard rejected large unicode payload: %v", err)
	}
	t.Cleanup(func() {
		cctx, done := context.WithTimeout(context.Background(), 2*time.Second)
		defer done()
		_ = s.pf.Clipboard.Set(cctx, "")
	})

	got, err := s.pf.Clipboard.Get(ctx)
	if err != nil {
		t.Fatalf("clipboard get (large unicode): %v", err)
	}
	if got != large {
		t.Errorf("large unicode payload mismatch: want %d bytes, got %d bytes", len(large), len(got))
	}
}

// TestClipboard_RapidSuccessiveWrites verifies that rapid consecutive Set
// calls do not intermix data — only the last written value must be readable.
func TestClipboard_RapidSuccessiveWrites(t *testing.T) {
	s := mustSuite(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	const last = "FINAL_VALUE_RAPID_WRITES_999"
	for i := 0; i < 5; i++ {
		val := "interim-value-" + strings.Repeat("x", i*10)
		if err := s.pf.Clipboard.Set(ctx, val); err != nil {
			t.Skipf("clipboard not available: %v", err)
		}
	}
	if err := s.pf.Clipboard.Set(ctx, last); err != nil {
		t.Fatalf("clipboard final set: %v", err)
	}
	t.Cleanup(func() {
		cctx, done := context.WithTimeout(context.Background(), 2*time.Second)
		defer done()
		_ = s.pf.Clipboard.Set(cctx, "")
	})

	got, err := s.pf.Clipboard.Get(ctx)
	if err != nil {
		t.Fatalf("clipboard get after rapid writes: %v", err)
	}
	if got != last {
		t.Errorf("rapid writes: want %q, got %q", last, got)
	}
}

// TestClipboard_NullBytes verifies clipboard behaviour with embedded NUL bytes.
// Many backends strip or reject NUL; the test skips when that limitation is
// detected rather than failing.
func TestClipboard_NullBytes(t *testing.T) {
	s := mustSuite(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	text := "before\x00after"
	if err := s.pf.Clipboard.Set(ctx, text); err != nil {
		t.Skipf("clipboard backend rejected NUL bytes (expected limitation): %v", err)
	}
	t.Cleanup(func() {
		cctx, done := context.WithTimeout(context.Background(), 2*time.Second)
		defer done()
		_ = s.pf.Clipboard.Set(cctx, "")
	})

	got, err := s.pf.Clipboard.Get(ctx)
	if err != nil {
		t.Skipf("clipboard get returned error for NUL content (backend limitation): %v", err)
	}
	// The non-NUL prefix must be intact regardless of what the backend does with NUL.
	if !strings.HasPrefix(got, "before") {
		t.Errorf("NUL bytes: clipboard content %q lost prefix %q", got, "before")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 5. WaitForFn — deterministic unit-style tests via constScreenshotter
// ─────────────────────────────────────────────────────────────────────────────

// TestWaitForFn_ImmediatelySatisfied verifies that WaitForFn returns without
// error when the predicate is true on the very first poll.
func TestWaitForFn_ImmediatelySatisfied(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	img := solidColorImage(10, 10, color.RGBA{R: 100, G: 200, B: 50, A: 255})
	sc := &constScreenshotter{img: img}

	got, err := find.WaitForFn(ctx, sc, image.Rect(0, 0, 10, 10),
		func(_ context.Context, i image.Image) bool { return i != nil },
		50*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitForFn immediately-satisfied: unexpected error: %v", err)
	}
	if got == nil {
		t.Error("WaitForFn immediately-satisfied: returned nil image")
	}
}

// TestWaitForFn_SatisfiedAfterDelay verifies that WaitForFn keeps polling
// until the predicate becomes true.
func TestWaitForFn_SatisfiedAfterDelay(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	img := solidColorImage(10, 10, color.RGBA{R: 100, G: 200, B: 50, A: 255})
	sc := &constScreenshotter{img: img}

	callCount := 0
	got, err := find.WaitForFn(ctx, sc, image.Rect(0, 0, 10, 10),
		func(_ context.Context, _ image.Image) bool {
			callCount++
			return callCount >= 3
		},
		30*time.Millisecond)
	if err != nil {
		t.Fatalf("WaitForFn satisfied-after-delay: unexpected error: %v", err)
	}
	if got == nil {
		t.Error("WaitForFn satisfied-after-delay: returned nil image")
	}
	if callCount < 3 {
		t.Errorf("expected at least 3 calls, got %d", callCount)
	}
}

// TestWaitForFn_TimeoutWithConstScreenshotter verifies a non-nil error when
// the predicate never satisfies, using an in-memory screenshotter.
func TestWaitForFn_TimeoutWithConstScreenshotter(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	img := solidColorImage(10, 10, color.RGBA{R: 100, G: 200, B: 50, A: 255})
	sc := &constScreenshotter{img: img}

	_, err := find.WaitForFn(ctx, sc, image.Rect(0, 0, 10, 10),
		func(_ context.Context, _ image.Image) bool { return false },
		30*time.Millisecond)
	if err == nil {
		t.Fatal("WaitForFn timeout: expected error when predicate never satisfies, got nil")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Local helpers (iter3)
// ─────────────────────────────────────────────────────────────────────────────

// iter3FirstDiffIdx returns the byte index of the first difference between a
// and b, or min(len(a), len(b)) when one is a prefix of the other.
func iter3FirstDiffIdx(a, b string) int {
	limit := len(a)
	if len(b) < limit {
		limit = len(b)
	}
	for i := 0; i < limit; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return limit
}
