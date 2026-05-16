//go:build integration
// +build integration

package integration_test

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

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/find"
	"github.com/nskaggs/perfuncted/internal/executil"
	"github.com/nskaggs/perfuncted/window"
)

// ─────────────────────────────────────────────────────────────────────────────
// Verifier – structured assertion helper for integration tests.
// All visual and behavioral assertions must use Verifier so failure messages
// are consistent and carry enough context to debug failures without a re-run.
// ─────────────────────────────────────────────────────────────────────────────

// Verifier wraps *testing.T and exposes assertion helpers for pixel content,
// image location, window presence, and basic value equality.
type chk struct {
	t  *testing.T
	sc find.Screenshotter // nil when pixel/image assertions are not needed
}

func newChk(t *testing.T, sc find.Screenshotter) *chk {
	t.Helper()
	return &chk{t: t, sc: sc}
}

// NoError fatally fails the test if err is non-nil.
func (v *chk) NoError(err error, msg string) {
	v.t.Helper()
	if err != nil {
		v.t.Fatalf("%s: %v", msg, err)
	}
}

// Equal fails the test if want != got.
func (v *chk) Equal(want, got, msg string) {
	v.t.Helper()
	if want != got {
		v.t.Errorf("%s: want %q, got %q", msg, want, got)
	}
}

// ErrorIs fails the test if err does not wrap target.
func (v *chk) ErrorIs(err, target error, msg string) {
	v.t.Helper()
	if !errors.Is(err, target) {
		v.t.Errorf("%s: want error wrapping %v, got %v", msg, target, err)
	}
}

// PixelPresent grabs rect from the screenshotter and asserts at least one
// pixel matches target within tolerance. rect must be in screen coordinates.
func (v *chk) PixelPresent(ctx context.Context, rect image.Rectangle, target color.RGBA, tolerance int) {
	v.t.Helper()
	if v.sc == nil {
		v.t.Fatal("PixelPresent: Verifier has no screenshotter")
	}
	img, err := v.sc.Grab(ctx, rect)
	if err != nil {
		v.t.Fatalf("PixelPresent: grab %v: %v", rect, err)
	}
	if _, ok := find.PixelFound(img, rect, target, tolerance); !ok {
		v.t.Errorf("PixelPresent: pixel rgb(%d,%d,%d) (tol=%d) not found in %v",
			target.R, target.G, target.B, tolerance, rect)
	}
}

// ImageLocated asserts reference can be found (exact match) within searchArea
// using the Verifier's screenshotter and returns the matched rectangle in
// screen coordinates.
func (v *chk) ImageLocated(ctx context.Context, searchArea image.Rectangle, reference image.Image, msg string) image.Rectangle {
	v.t.Helper()
	if v.sc == nil {
		v.t.Fatal("ImageLocated: Verifier has no screenshotter")
	}
	r, err := find.LocateExact(ctx, v.sc, searchArea, reference)
	if err != nil {
		v.t.Fatalf("ImageLocated (%s): %v", msg, err)
	}
	return r
}

// ImageNotLocated asserts reference cannot be found within searchArea and
// that the error wraps find.ErrNotFound.
func (v *chk) ImageNotLocated(ctx context.Context, searchArea image.Rectangle, reference image.Image, msg string) {
	v.t.Helper()
	if v.sc == nil {
		v.t.Fatal("ImageNotLocated: Verifier has no screenshotter")
	}
	_, err := find.LocateExact(ctx, v.sc, searchArea, reference)
	if err == nil {
		v.t.Errorf("ImageNotLocated (%s): expected error, got nil", msg)
		return
	}
	if !errors.Is(err, find.ErrNotFound) {
		v.t.Errorf("ImageNotLocated (%s): want ErrNotFound, got: %v", msg, err)
	}
}

// WindowExists asserts a window matching pattern is currently visible and
// returns its Info.
func (v *chk) WindowExists(ctx context.Context, pf *perfuncted.Perfuncted, pattern string) window.Info {
	v.t.Helper()
	info, err := pf.Window.FindByTitle(ctx, pattern)
	if err != nil {
		v.t.Fatalf("WindowExists %q: %v", pattern, err)
	}
	return info
}

// WindowAbsent asserts no window matching pattern is currently visible.
func (v *chk) WindowAbsent(ctx context.Context, pf *perfuncted.Perfuncted, pattern string) {
	v.t.Helper()
	_, err := pf.Window.FindByTitle(ctx, pattern)
	if err == nil {
		v.t.Errorf("WindowAbsent: window %q still visible", pattern)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Test-local helpers
// ─────────────────────────────────────────────────────────────────────────────

// firstAvailableApp returns the first supported text editor found in PATH.
// Tests that require launching a GUI application should call t.Skip when ok
// is false.
func firstAvailableApp(t *testing.T) (appSpec, bool) {
	t.Helper()
	for _, spec := range []appSpec{
		{name: "kwrite", launch: []string{"kwrite"}, winMatch: "kwrite"},
		{name: "featherpad", launch: []string{"featherpad"}, winMatch: "featherpad"},
	} {
		if _, err := executil.LookPath(spec.launch[0]); err == nil {
			return spec, true
		}
	}
	return appSpec{}, false
}

// solidColorImage returns a w×h *image.RGBA uniformly filled with c.
func solidColorImage(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

// pollActiveTitle polls pf.Window.ActiveTitle until it contains substr
// (case-insensitive) or ctx expires. Returns the last observed title and
// whether the match succeeded.
func pollActiveTitle(ctx context.Context, pf *perfuncted.Perfuncted, substr string) (string, bool) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var last string
	for {
		title, err := pf.Window.ActiveTitle(ctx)
		if err == nil {
			last = title
			if strings.Contains(strings.ToLower(title), strings.ToLower(substr)) {
				return title, true
			}
		}
		select {
		case <-ctx.Done():
			return last, false
		case <-ticker.C:
		}
	}
}

// constScreenshotter is a minimal find.Screenshotter that always returns the
// same image regardless of the requested rect. Used for deterministic
// unit-style sub-tests inside integration test functions.
type constScreenshotter struct{ img *image.RGBA }

func (c *constScreenshotter) Grab(_ context.Context, _ image.Rectangle) (image.Image, error) {
	return c.img, nil
}
func (c *constScreenshotter) GrabFullHash(_ context.Context) (uint32, error) {
	return find.PixelHash(c.img, nil), nil
}
func (c *constScreenshotter) GrabRegionHash(_ context.Context, _ image.Rectangle) (uint32, error) {
	return find.PixelHash(c.img, nil), nil
}

// ─────────────────────────────────────────────────────────────────────────────
// 1. Clipboard round-trip verification
// ─────────────────────────────────────────────────────────────────────────────

// TestClipboardRoundTrip_Unicode verifies that multi-byte Unicode text is
// stored and retrieved without corruption.
func TestClipboardRoundTrip_Unicode(t *testing.T) {
	s := mustSuite(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	v := newChk(t, s.pf.Screen.Screenshotter)

	const text = "Hello 世界 🌍 Ñoño αβγδ ∑∫∂"
	v.NoError(s.pf.Clipboard.Set(ctx, text), "clipboard set unicode")
	t.Cleanup(func() {
		cleanCtx, done := context.WithTimeout(context.Background(), 2*time.Second)
		defer done()
		_ = s.pf.Clipboard.Set(cleanCtx, "")
	})

	got, err := s.pf.Clipboard.Get(ctx)
	v.NoError(err, "clipboard get unicode")
	v.Equal(text, got, "clipboard unicode round-trip")
}

// TestClipboardRoundTrip_Whitespace verifies that leading/trailing and
// internal whitespace (spaces, tabs) survives a clipboard round-trip intact.
func TestClipboardRoundTrip_Whitespace(t *testing.T) {
	s := mustSuite(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	v := newChk(t, s.pf.Screen.Screenshotter)

	const text = "  leading  \t\ttabs\t  nested   trailing  "
	v.NoError(s.pf.Clipboard.Set(ctx, text), "clipboard set whitespace")
	t.Cleanup(func() {
		cleanCtx, done := context.WithTimeout(context.Background(), 2*time.Second)
		defer done()
		_ = s.pf.Clipboard.Set(cleanCtx, "")
	})

	got, err := s.pf.Clipboard.Get(ctx)
	v.NoError(err, "clipboard get whitespace")
	v.Equal(text, got, "clipboard whitespace round-trip")
}

// TestClipboardRoundTrip_Newlines verifies that embedded newline characters
// survive a clipboard round-trip without truncation or modification.
func TestClipboardRoundTrip_Newlines(t *testing.T) {
	s := mustSuite(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	v := newChk(t, s.pf.Screen.Screenshotter)

	const text = "line one\nline two\nline three\nfinal"
	v.NoError(s.pf.Clipboard.Set(ctx, text), "clipboard set newlines")
	t.Cleanup(func() {
		cleanCtx, done := context.WithTimeout(context.Background(), 2*time.Second)
		defer done()
		_ = s.pf.Clipboard.Set(cleanCtx, "")
	})

	got, err := s.pf.Clipboard.Get(ctx)
	v.NoError(err, "clipboard get newlines")
	v.Equal(text, got, "clipboard newlines round-trip")
}

// TestClipboardRoundTrip_Empty verifies clipboard behaviour with an empty
// string. Some backends do not support empty content; the test skips rather
// than failing when that limitation is detected.
func TestClipboardRoundTrip_Empty(t *testing.T) {
	s := mustSuite(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Prime the clipboard so we can confirm that Set("") actually clears it.
	if err := s.pf.Clipboard.Set(ctx, "prime"); err != nil {
		t.Skipf("clipboard not available: %v", err)
	}
	if err := s.pf.Clipboard.Set(ctx, ""); err != nil {
		// wl-copy and xclip may refuse an empty write.
		t.Skipf("clipboard backend does not support empty string: %v", err)
	}
	t.Cleanup(func() {
		cleanCtx, done := context.WithTimeout(context.Background(), 2*time.Second)
		defer done()
		_ = s.pf.Clipboard.Set(cleanCtx, "")
	})

	got, err := s.pf.Clipboard.Get(ctx)
	if err != nil {
		// Some backends return an error on empty-clipboard Get; that is acceptable.
		t.Skipf("clipboard get on empty content returned error (backend limitation): %v", err)
	}
	if got != "" {
		t.Errorf("clipboard empty round-trip: want %q, got %q", "", got)
	}
}

// TestClipboard_NoContamination verifies that writing a second value fully
// replaces the first — successive writes must not bleed into each other.
func TestClipboard_NoContamination(t *testing.T) {
	s := mustSuite(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	v := newChk(t, s.pf.Screen.Screenshotter)

	const first = "first-clipboard-value-abc"
	const second = "second-clipboard-value-xyz"

	v.NoError(s.pf.Clipboard.Set(ctx, first), "clipboard set first")
	got1, err := s.pf.Clipboard.Get(ctx)
	v.NoError(err, "clipboard get first")
	v.Equal(first, got1, "first value round-trip")

	v.NoError(s.pf.Clipboard.Set(ctx, second), "clipboard set second")
	got2, err := s.pf.Clipboard.Get(ctx)
	v.NoError(err, "clipboard get second")
	v.Equal(second, got2, "second value fully replaces first")

	t.Cleanup(func() {
		cleanCtx, done := context.WithTimeout(context.Background(), 2*time.Second)
		defer done()
		_ = s.pf.Clipboard.Set(cleanCtx, "")
	})
}

// ─────────────────────────────────────────────────────────────────────────────
// 2. TypeFast (clipboard paste) vs Type (keyboard) verification
// ─────────────────────────────────────────────────────────────────────────────

// TestTypeFast_SetsClipboard verifies that pf.Paste (the clipboard-based
// "TypeFast" path) updates the clipboard to the pasted text, which is the key
// behavioural difference between pf.Paste and pf.Input.Type.
func TestTypeFast_SetsClipboard(t *testing.T) {
	s := mustSuite(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	v := newChk(t, s.pf.Screen.Screenshotter)

	// Establish a known initial clipboard state.
	const sentinel = "SENTINEL_BEFORE_PASTE"
	v.NoError(s.pf.Clipboard.Set(ctx, sentinel), "set sentinel")

	got0, err := s.pf.Clipboard.Get(ctx)
	v.NoError(err, "get sentinel")
	v.Equal(sentinel, got0, "sentinel round-trip")

	// pf.Paste must write the pasted text into the clipboard.
	const pasteText = "PASTE_UPDATES_CLIPBOARD_42"
	v.NoError(s.pf.Paste(ctx, pasteText), "paste text")

	got1, err := s.pf.Clipboard.Get(ctx)
	v.NoError(err, "get clipboard after paste")
	v.Equal(pasteText, got1, "clipboard updated by Paste")

	t.Cleanup(func() {
		cleanCtx, done := context.WithTimeout(context.Background(), 2*time.Second)
		defer done()
		_ = s.pf.Clipboard.Set(cleanCtx, "")
	})
}

// TestTypeFast_MatchesType opens two editor instances (same application,
// different save files), types the same string into each using a different
// input method — pf.Input.Type (key-by-key) vs pf.Paste (clipboard paste) —
// and verifies that both saved files contain identical content.
func TestTypeFast_MatchesType(t *testing.T) {
	app, ok := firstAvailableApp(t)
	if !ok {
		t.Skip("no supported text editor found in PATH; skipping TypeFast comparison")
	}
	s := mustSuite(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	v := newChk(t, s.pf.Screen.Screenshotter)

	const typeText = "TypeFastMatchesType"

	// ── keyboard path ─────────────────────────────────────────────────────────
	kbFile := filepath.Join(t.TempDir(), "typekb.txt")
	if err := os.WriteFile(kbFile, nil, 0o644); err != nil {
		t.Fatalf("create kbFile: %v", err)
	}
	kbApp := app
	kbApp.saveFile = kbFile
	cmdKB, err := launchApp(s.rt, s.session, kbApp, kbApp.extraEnvFor(s.mode)...)
	v.NoError(err, "launch keyboard editor")
	t.Cleanup(func() { terminateCmd(cmdKB, 5*time.Second) })

	kbDocName := filepath.Base(kbFile)
	_, err = waitForWindow(s.pf, kbDocName, 30*time.Second)
	v.NoError(err, "wait for keyboard editor window")

	kbCtx, kbCancel := context.WithTimeout(ctx, 30*time.Second)
	defer kbCancel()
	v.NoError(s.pf.Window.Activate(kbCtx, kbDocName), "activate keyboard editor")
	time.Sleep(500 * time.Millisecond)
	v.NoError(s.pf.Input.Type(kbCtx, typeText), "type via keyboard")
	time.Sleep(300 * time.Millisecond)
	v.NoError(s.pf.Input.Type(kbCtx, "{ctrl+s}"), "save keyboard file")

	kbContent, err := waitForFileContains(kbCtx, kbFile, typeText, 10*time.Second)
	v.NoError(err, "wait for keyboard file content")
	v.NoError(s.pf.Window.CloseWindow(kbCtx, kbDocName), "close keyboard editor")
	time.Sleep(500 * time.Millisecond)

	// ── clipboard-paste path ──────────────────────────────────────────────────
	cbFile := filepath.Join(t.TempDir(), "typecb.txt")
	if err := os.WriteFile(cbFile, nil, 0o644); err != nil {
		t.Fatalf("create cbFile: %v", err)
	}
	cbApp := app
	cbApp.saveFile = cbFile
	cmdCB, err := launchApp(s.rt, s.session, cbApp, cbApp.extraEnvFor(s.mode)...)
	v.NoError(err, "launch clipboard editor")
	t.Cleanup(func() { terminateCmd(cmdCB, 5*time.Second) })

	cbDocName := filepath.Base(cbFile)
	_, err = waitForWindow(s.pf, cbDocName, 30*time.Second)
	v.NoError(err, "wait for clipboard editor window")

	cbCtx, cbCancel := context.WithTimeout(ctx, 30*time.Second)
	defer cbCancel()
	v.NoError(s.pf.Window.Activate(cbCtx, cbDocName), "activate clipboard editor")
	time.Sleep(500 * time.Millisecond)
	v.NoError(s.pf.Paste(cbCtx, typeText), "paste via clipboard")
	time.Sleep(300 * time.Millisecond)
	v.NoError(s.pf.Input.Type(cbCtx, "{ctrl+s}"), "save clipboard file")

	cbContent, err := waitForFileContains(cbCtx, cbFile, typeText, 10*time.Second)
	v.NoError(err, "wait for clipboard file content")
	v.NoError(s.pf.Window.CloseWindow(cbCtx, cbDocName), "close clipboard editor")

	// Both methods must produce files that contain the same typed text.
	kbTrimmed := strings.TrimSpace(kbContent)
	cbTrimmed := strings.TrimSpace(cbContent)
	if !strings.Contains(kbTrimmed, typeText) {
		t.Errorf("keyboard output %q does not contain expected text %q", kbTrimmed, typeText)
	}
	if !strings.Contains(cbTrimmed, typeText) {
		t.Errorf("clipboard output %q does not contain expected text %q", cbTrimmed, typeText)
	}
	if kbTrimmed != cbTrimmed {
		t.Errorf("Type and Paste produced different file contents:\n  keyboard: %q\n  paste:    %q",
			kbTrimmed, cbTrimmed)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 3. Window focus and raise verification
// ─────────────────────────────────────────────────────────────────────────────

// TestWindowFocus_SwitchBetweenWindows opens two editor instances and verifies
// that Activate transfers keyboard focus between them, as reported by
// pf.Window.ActiveTitle.
func TestWindowFocus_SwitchBetweenWindows(t *testing.T) {
	app, ok := firstAvailableApp(t)
	if !ok {
		t.Skip("no supported text editor found in PATH; skipping window-focus test")
	}
	s := mustSuite(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	v := newChk(t, s.pf.Screen.Screenshotter)

	// ── open window A ─────────────────────────────────────────────────────────
	fileA := filepath.Join(t.TempDir(), "focusA.txt")
	if err := os.WriteFile(fileA, nil, 0o644); err != nil {
		t.Fatalf("create fileA: %v", err)
	}
	appA := app
	appA.saveFile = fileA
	cmdA, err := launchApp(s.rt, s.session, appA, appA.extraEnvFor(s.mode)...)
	v.NoError(err, "launch window A")
	t.Cleanup(func() { terminateCmd(cmdA, 5*time.Second) })

	docA := filepath.Base(fileA) // "focusA.txt"
	_, err = waitForWindow(s.pf, docA, 30*time.Second)
	v.NoError(err, "wait for window A")

	// ── open window B ─────────────────────────────────────────────────────────
	fileB := filepath.Join(t.TempDir(), "focusB.txt")
	if err := os.WriteFile(fileB, nil, 0o644); err != nil {
		t.Fatalf("create fileB: %v", err)
	}
	appB := app
	appB.saveFile = fileB
	cmdB, err := launchApp(s.rt, s.session, appB, appB.extraEnvFor(s.mode)...)
	v.NoError(err, "launch window B")
	t.Cleanup(func() { terminateCmd(cmdB, 5*time.Second) })

	docB := filepath.Base(fileB) // "focusB.txt"
	_, err = waitForWindow(s.pf, docB, 30*time.Second)
	v.NoError(err, "wait for window B")

	// ── activate A; confirm title reflects it ─────────────────────────────────
	v.NoError(s.pf.Window.Activate(ctx, docA), "activate window A")
	focusCtxA, cancelFocusA := context.WithTimeout(ctx, 5*time.Second)
	defer cancelFocusA()
	titleA, gotA := pollActiveTitle(focusCtxA, s.pf, docA)
	if !gotA {
		t.Errorf("window A focus: ActiveTitle %q never contained %q", titleA, docA)
	}

	// ── activate B; confirm title changed ─────────────────────────────────────
	v.NoError(s.pf.Window.Activate(ctx, docB), "activate window B")
	focusCtxB, cancelFocusB := context.WithTimeout(ctx, 5*time.Second)
	defer cancelFocusB()
	titleB, gotB := pollActiveTitle(focusCtxB, s.pf, docB)
	if !gotB {
		t.Errorf("window B focus: ActiveTitle %q never contained %q", titleB, docB)
	}

	// Both windows must still be listed after the focus switch.
	v.WindowExists(ctx, s.pf, docA)
	v.WindowExists(ctx, s.pf, docB)
}

// TestWindowClose_VerifiedGoneFromList opens a text editor, closes it via
// pf.Window.CloseWindow, waits for it to disappear, and verifies it is absent
// from the live window list.
func TestWindowClose_VerifiedGoneFromList(t *testing.T) {
	app, ok := firstAvailableApp(t)
	if !ok {
		t.Skip("no supported text editor found in PATH; skipping window-close test")
	}
	s := mustSuite(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	v := newChk(t, s.pf.Screen.Screenshotter)

	closeFile := filepath.Join(t.TempDir(), "closetest.txt")
	if err := os.WriteFile(closeFile, nil, 0o644); err != nil {
		t.Fatalf("create closeFile: %v", err)
	}
	closeApp := app
	closeApp.saveFile = closeFile
	cmd, err := launchApp(s.rt, s.session, closeApp, closeApp.extraEnvFor(s.mode)...)
	v.NoError(err, "launch editor")
	t.Cleanup(func() { terminateCmd(cmd, 5*time.Second) })

	docName := filepath.Base(closeFile) // "closetest.txt"
	_, err = waitForWindow(s.pf, docName, 30*time.Second)
	v.NoError(err, "wait for editor window")

	// Confirm window is visible before closing.
	v.WindowExists(ctx, s.pf, docName)

	// Close the unmodified window — no save dialog is expected.
	v.NoError(s.pf.Window.Activate(ctx, docName), "activate before close")
	time.Sleep(300 * time.Millisecond)
	v.NoError(s.pf.Window.CloseWindow(ctx, docName), "close window")

	// Wait until the window manager reports the window is gone.
	closeCtx, cancelClose := context.WithTimeout(ctx, 15*time.Second)
	defer cancelClose()
	v.NoError(s.pf.Window.WaitForClose(closeCtx, docName, 200*time.Millisecond), "wait for window close")

	// Final confirmation: FindByTitle must now return an error.
	v.WindowAbsent(ctx, s.pf, docName)
}

// ─────────────────────────────────────────────────────────────────────────────
// 4. Screen capture with known-content pixel assertions
// ─────────────────────────────────────────────────────────────────────────────

// TestScreenCapture_SelfLocate grabs a region of the screen and then uses
// find.LocateExact to relocate that same region within a slightly larger
// search area. This exercises the full capture + exact-match pipeline together.
func TestScreenCapture_SelfLocate(t *testing.T) {
	s := mustSuite(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	v := newChk(t, s.pf.Screen.Screenshotter)

	screenW, screenH, err := s.pf.Screen.Resolution(ctx)
	v.NoError(err, "screen resolution")
	if screenW <= 0 || screenH <= 0 {
		t.Skipf("screen resolution unavailable (%dx%d)", screenW, screenH)
	}

	// Choose a stable 20×20 region at the centre of the screen.
	cx, cy := screenW/2, screenH/2
	refRect := image.Rect(cx-10, cy-10, cx+10, cy+10)
	searchArea := image.Rect(
		max(0, cx-60), max(0, cy-60),
		min(screenW, cx+60), min(screenH, cy+60),
	)

	// Wait for the desktop to settle before capturing the reference.
	settleCtx, settleCancel := context.WithTimeout(ctx, 3*time.Second)
	defer settleCancel()
	_, _ = s.pf.Screen.WaitForNoChange(settleCtx, searchArea, 3, 50*time.Millisecond)

	ref, err := s.pf.Screen.Grab(ctx, refRect)
	v.NoError(err, "grab reference region")

	// The reference must be locatable within the search area that contains it.
	found := v.ImageLocated(ctx, searchArea, ref, "self-locate")
	if found.Empty() {
		t.Error("LocateExact returned empty rectangle")
	}
	if !found.Overlaps(searchArea) {
		t.Errorf("found rect %v does not overlap search area %v", found, searchArea)
	}
}

// TestScreenCapture_PixelPresent verifies find.PixelFound in two stages:
// first against a known in-memory image (deterministic), then against a live
// screen region (genuine integration). Both stages must correctly detect the
// expected colour within the given tolerance.
func TestScreenCapture_PixelPresent(t *testing.T) {
	// ── in-memory stage ───────────────────────────────────────────────────────
	red := color.RGBA{R: 255, A: 255}
	img := solidColorImage(50, 50, red)
	rect := image.Rect(0, 0, 50, 50)

	pt, ok := find.PixelFound(img, rect, red, 0)
	if !ok {
		t.Error("PixelFound: failed to find known red pixel in solid-red image")
	}
	if !pt.In(rect) {
		t.Errorf("PixelFound: returned point %v outside image rect %v", pt, rect)
	}

	// A completely different colour must not be found (zero tolerance).
	blue := color.RGBA{B: 255, A: 255}
	if _, found := find.PixelFound(img, rect, blue, 0); found {
		t.Error("PixelFound: incorrectly found blue in a solid-red image")
	}

	// Near-red should be found with a permissive tolerance.
	nearRed := color.RGBA{R: 250, A: 255}
	if _, found := find.PixelFound(img, rect, nearRed, 10); !found {
		t.Error("PixelFound: failed to find near-red in solid-red image with tolerance=10")
	}

	// ── live screen stage ─────────────────────────────────────────────────────
	s := mustSuite(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	v := newChk(t, s.pf.Screen.Screenshotter)

	screenW, screenH, err := s.pf.Screen.Resolution(ctx)
	v.NoError(err, "live screen resolution")
	if screenW <= 0 || screenH <= 0 {
		t.Skipf("screen resolution unavailable (%dx%d)", screenW, screenH)
	}

	// Grab the top-left corner, read its pixel, then verify PixelPresent finds it.
	liveRect := image.Rect(0, 0, min(screenW, 100), min(screenH, 100))
	liveImg, err := s.pf.Screen.Grab(ctx, liveRect)
	v.NoError(err, "grab live region")

	b := liveImg.Bounds()
	pxColor := color.RGBAModel.Convert(liveImg.At(b.Min.X, b.Min.Y)).(color.RGBA)
	// Allow tolerance=5 to absorb minor compositor compositing variations.
	v.PixelPresent(ctx, liveRect, pxColor, 5)
}

// ─────────────────────────────────────────────────────────────────────────────
// 5. Find functions: known positions and error cases
// ─────────────────────────────────────────────────────────────────────────────

// TestFind_LocateExact_NotFound verifies that LocateExact returns ErrNotFound
// when the reference colour pattern does not appear anywhere in the search
// area. Uses a constant screenshotter for deterministic, display-independent
// results.
func TestFind_LocateExact_NotFound(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Screenshotter always returns a solid blue image.
	bg := solidColorImage(200, 200, color.RGBA{B: 200, A: 255})
	sc := &constScreenshotter{img: bg}
	v := newChk(t, sc)

	// A solid red reference must not match inside the blue search area.
	ref := solidColorImage(10, 10, color.RGBA{R: 200, A: 255})
	v.ImageNotLocated(ctx, image.Rect(0, 0, 200, 200), ref, "red in blue background")
}

// TestFind_LocateAll_KnownPosition verifies that LocateExact returns the
// correct screen-coordinate rectangle when the reference patch is embedded at
// a known position in a larger canvas.
func TestFind_LocateAll_KnownPosition(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Build a 100×100 blue canvas with a 10×10 red patch at (30, 40).
	canvas := solidColorImage(100, 100, color.RGBA{B: 200, A: 255})
	red := color.RGBA{R: 200, A: 255}
	for y := 40; y < 50; y++ {
		for x := 30; x < 40; x++ {
			canvas.SetRGBA(x, y, red)
		}
	}
	sc := &constScreenshotter{img: canvas}
	v := newChk(t, sc)

	ref := solidColorImage(10, 10, red)
	// Search the entire canvas (zero-origin to match constScreenshotter output).
	found := v.ImageLocated(ctx, image.Rect(0, 0, 100, 100), ref, "red patch in blue canvas")

	// The found rectangle must cover exactly the embedded patch.
	want := image.Rect(30, 40, 40, 50)
	if found != want {
		t.Errorf("LocateExact position: want %v, got %v", want, found)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// 6. Error path verification
// ─────────────────────────────────────────────────────────────────────────────

// TestErrorPath_NilClipboard verifies that Get and Set on a nil ClipboardBundle
// return a meaningful error rather than panicking.
func TestErrorPath_NilClipboard(t *testing.T) {
	ctx := context.Background()
	pf := &perfuncted.Perfuncted{}

	err := pf.Clipboard.Set(ctx, "test")
	if err == nil {
		t.Error("Set on nil ClipboardBundle should return an error")
	}

	_, err = pf.Clipboard.Get(ctx)
	if err == nil {
		t.Error("Get on nil ClipboardBundle should return an error")
	}
}

// TestErrorPath_NilInput verifies that pf.Input.Type on a nil InputBundle
// returns an error rather than panicking.
func TestErrorPath_NilInput(t *testing.T) {
	ctx := context.Background()
	pf := &perfuncted.Perfuncted{}

	err := pf.Input.Type(ctx, "hello")
	if err == nil {
		t.Error("Type on nil InputBundle should return an error")
	}
}

// TestErrorPath_WindowNotFound verifies that FindByTitle returns
// window.ErrWindowNotFound for a pattern that matches no open window.
func TestErrorPath_WindowNotFound(t *testing.T) {
	s := mustSuite(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	v := newChk(t, s.pf.Screen.Screenshotter)

	_, err := s.pf.Window.FindByTitle(ctx, "xyzzy-nonexistent-window-pfinttest-8675309")
	if err == nil {
		t.Fatal("FindByTitle for nonexistent window should return an error")
	}
	v.ErrorIs(err, window.ErrWindowNotFound, "FindByTitle nonexistent window")
}

// TestErrorPath_ContextCancelled verifies that perfuncted.Retry stops
// promptly and returns a non-nil error when the context is already cancelled
// before the call.
func TestErrorPath_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before the first call

	calls := 0
	err := perfuncted.Retry(ctx, 0, func() error {
		calls++
		return errors.New("always fails")
	})
	if err == nil {
		t.Error("Retry should return an error when context is already cancelled")
	}
	if calls == 0 {
		t.Error("Retry should call the function at least once before giving up")
	}
}
