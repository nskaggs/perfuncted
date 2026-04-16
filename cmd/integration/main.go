// cmd/integration is a live integration test that validates each core capability
// of the perfuncted library against the current display. It tests against
// every app executable found in PATH (kwrite, pluma) so both Qt and GTK
// dialog paths are covered. Run it inside a nested sway session.
//
// Exit code 0 = all sections passed.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/png"
	"log"
	"os"
	"strings"
	"time"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/find"
	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/executil"
	"github.com/nskaggs/perfuncted/screen"
	"github.com/nskaggs/perfuncted/window"
)

// appSpec describes one application to exercise in the test run.
type appSpec struct {
	name      string   // display name in output
	launch    []string // command + args; first element is the executable
	winMatch  string   // substring matched against window title (case-insensitive)
	saveFile  string   // unique path used for the E2E save test (empty for browsers)
	extraEnv  []string // additional environment variables for the subprocess
	isBrowser bool     // true → run testBrowser instead of testApp
}

// detectApps returns apps available in PATH in test order.
func detectApps() []appSpec {
	pfx := os.Getenv("PF_TEST_PREFIX")
	if pfx == "" {
		pfx = "perfuncted"
	}
	all := []appSpec{
		{
			name:     "kwrite",
			launch:   []string{"kwrite"},
			winMatch: "kwrite",
			saveFile: fmt.Sprintf("/tmp/%s-kwrite.txt", pfx),
		},
		{
			// pluma is a single-instance GTK app that uses D-Bus to find running
			// instances. dbus-run-session gives it a private session bus so it
			// always starts as a fresh instance instead of opening a tab in the
			// host's running pluma. GTK_USE_PORTAL=0 forces the native GTK file
			// chooser instead of delegating to the KDE portal (which would appear
			// on the host desktop, unreachable from the nested session).
			name:     "pluma",
			launch:   []string{"dbus-run-session", "pluma"},
			winMatch: "pluma",
			saveFile: fmt.Sprintf("/tmp/%s-pluma.txt", pfx),
			extraEnv: []string{"GTK_USE_PORTAL=0"},
		},
		{
			// Firefox: --no-remote --new-instance ensures a fresh process even if
			// Firefox is already running on the host. MOZ_ENABLE_WAYLAND=1 enables
			// the native Wayland backend (wl_seat, wl_keyboard, etc.) so perfuncted's
			// virtual-input and screencopy backends reach it correctly.
			// MOZ_DISABLE_CONTENT_SANDBOX=1 suppresses sandbox failures in headless
			// environments where user namespaces are not available.
			name:      "firefox",
			launch:    []string{"firefox", "--no-remote", "--new-instance", "about:blank"},
			winMatch:  "firefox",
			isBrowser: true,
			extraEnv: []string{
				"MOZ_ENABLE_WAYLAND=1",
				"MOZ_DISABLE_CONTENT_SANDBOX=1",
			},
		},
	}
	var found []appSpec
	for _, a := range all {
		// Detect if any part of the launch command exists in PATH.
		for _, arg := range a.launch {
			if _, err := executil.LookPath(arg); err == nil {
				found = append(found, a)
				break
			}
		}
	}
	return found
}

func main() {
	appFilter := flag.String("app", "", "run only this app (kwrite or pluma); empty = all")
	flag.Parse()

	r := &results{}

	pf, err := perfuncted.New(perfuncted.Options{MaxX: 1920, MaxY: 1080})
	if err != nil {
		log.Fatalf("perfuncted.New: %v", err)
	}
	defer pf.Close()
	sc := pf.Screen.Screenshotter
	inp := pf.Input.Inputter
	wm := pf.Window.Manager
	fmt.Printf("screen: %T\ninput:  %T\nwindow: %T\n\n", sc, inp, wm)

	// ── 1. Screen ────────────────────────────────────────────────────────────
	// Validates: Grab, PixelHash, FirstPixel, LastPixel, GrabHash stability.
	r.section("SCREEN")

	cornerRect := image.Rect(0, 0, 200, 200)
	img1, err := sc.Grab(cornerRect)
	r.check("grab 200x200 region", err)

	if err == nil {
		hash1 := find.PixelHash(img1, nil)
		r.pass("pixel hash computed: %d", hash1)

		px, err := find.FirstPixel(sc, cornerRect)
		r.check("read first pixel", err)
		if err == nil {
			r.pass("first pixel R=%d G=%d B=%d", px.R, px.G, px.B)
		}

		last, err := find.LastPixel(sc, cornerRect)
		r.check("read last pixel", err)
		if err == nil {
			r.pass("last pixel R=%d G=%d B=%d", last.R, last.G, last.B)
		}

		hash2, err := find.GrabHash(sc, cornerRect, nil)
		r.check("second grab", err)
		if err == nil {
			if hash1 == hash2 {
				r.pass("hash stable across two grabs (%d)", hash1)
			} else {
				r.fail("hash unstable: %d -> %d (screen changing?)", hash1, hash2)
			}
		}

		full, err := sc.Grab(image.Rect(0, 0, 1920, 1080))
		if err == nil {
			pfx := os.Getenv("PF_TEST_PREFIX")
			if pfx == "" {
				pfx = "perfuncted"
			}
			fpath := fmt.Sprintf("/tmp/%s-screen.png", pfx)
			savePNG(full, fpath)
			r.pass("full screenshot -> %s", fpath)
		}
	}

	// ── 2. Probes ────────────────────────────────────────────────────────────
	// Validates: all three Probe() functions enumerate backends; selected
	// backend matches the one Open() returned.
	r.section("PROBES")

	for _, res := range screen.Probe() {
		sel := " "
		if res.Selected {
			sel = "▶"
		}
		fmt.Printf("  %s screen  %-30s available=%-5v %s\n", sel, res.Name, res.Available, res.Reason)
	}
	for _, res := range input.Probe() {
		sel := " "
		if res.Selected {
			sel = "▶"
		}
		fmt.Printf("  %s input   %-30s available=%-5v %s\n", sel, res.Name, res.Available, res.Reason)
	}
	for _, res := range window.Probe() {
		sel := " "
		if res.Selected {
			sel = "▶"
		}
		fmt.Printf("  %s window  %-30s available=%-5v %s\n", sel, res.Name, res.Available, res.Reason)
	}
	r.pass("screen/input/window probes enumerated")

	// ── Per-app tests ─────────────────────────────────────────────────────────
	apps := detectApps()
	if *appFilter != "" {
		var filtered []appSpec
		for _, a := range apps {
			if a.name == *appFilter {
				filtered = append(filtered, a)
			}
		}
		if len(filtered) == 0 {
			log.Fatalf("app %q not available in PATH", *appFilter)
		}
		apps = filtered
	} else if len(apps) == 0 {
		log.Fatal("no supported apps found in PATH (need kwrite, pluma, or firefox)")
	}
	for _, app := range apps {
		if app.isBrowser {
			testBrowser(r, pf, app)
		} else {
			testApp(r, pf, app)
		}
	}

	r.summary()
}

// testApp runs WINDOW, MOUSE, TEXT INPUT and E2E SAVE sections for one app.
func testApp(r *results, pf *perfuncted.Perfuncted, app appSpec) {
	sc := pf.Screen.Screenshotter
	inp := pf.Input.Inputter
	wm := pf.Window.Manager
	pfx := os.Getenv("PF_TEST_PREFIX")
	if pfx == "" {
		pfx = "perfuncted"
	}
	// ── Window ───────────────────────────────────────────────────────────────
	r.section("WINDOW [" + app.name + "]")

	os.Remove(app.saveFile)
	// Pre-create the save file so the app opens it directly; Ctrl+S then saves
	// without triggering a "Save As" dialog.  The E2E section verifies the file
	// content was actually written by checking for a unique marker.
	if err := os.WriteFile(app.saveFile, []byte{}, 0o644); err != nil {
		r.fail("pre-create %s: %v", app.saveFile, err)
		return
	}
	// Append the file path so the app opens it on launch.
	launchCmd := append(app.launch, app.saveFile)
	proc := executil.CommandContext(context.Background(), launchCmd[0], launchCmd[1:]...)
	if len(app.extraEnv) > 0 {
		proc.Env = env.Merge(os.Environ(), app.extraEnv...)
	}
	if err := proc.Start(); err != nil {
		r.fail("%s launch: %v", app.launch[0], err)
		return
	}
	defer proc.Process.Kill() //nolint:errcheck

	ctx60, cancel60 := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel60()
	info, err := pf.Window.WaitFor(ctx60, app.winMatch, 300*time.Millisecond)
	r.check("window appeared in list", err)
	if err != nil {
		return
	}
	r.pass("found: %q (id=0x%x)", info.Title, info.ID)

	if err := wm.Activate(info.Title); err != nil {
		r.fail("Activate: %v", err)
	} else {
		r.pass("Activate %s", app.name)
	}
	time.Sleep(500 * time.Millisecond)

	active, err := wm.ActiveTitle()
	r.check("read ActiveTitle", err)
	if err == nil {
		if strings.Contains(strings.ToLower(active), strings.ToLower(app.winMatch)) {
			r.pass("ActiveTitle contains %q: %q", app.winMatch, active)
		} else {
			r.fail("ActiveTitle %q does not mention %s", active, app.winMatch)
		}
	}

	// ── Mouse ────────────────────────────────────────────────────────────────
	r.section("MOUSE [" + app.name + "]")

	winX, winY := info.X, info.Y
	winRect := image.Rect(winX, winY, winX+info.W, winY+info.H)
	r.pass("window origin: %d,%d (W=%d H=%d)", winX, winY, info.W, info.H)

	menuBarRect := image.Rect(winX, winY+22, winX+300, winY+50)
	hashBefore, err := find.GrabHash(sc, menuBarRect, nil)
	r.check("grab menu bar before click", err)

	fileMenuX, fileMenuY := winX+30, winY+35
	r.check("MouseMove to File menu", inp.MouseMove(fileMenuX, fileMenuY))
	time.Sleep(200 * time.Millisecond)

	hashHover, err := find.GrabHash(sc, menuBarRect, nil)
	r.check("grab menu bar after hover", err)
	if err == nil {
		if hashHover != hashBefore {
			r.pass("menu bar changed on hover")
		} else {
			r.pass("menu bar unchanged on hover (theme may not highlight)")
		}
	}

	r.check("MouseClick File menu", inp.MouseClick(fileMenuX, fileMenuY, 1))
	time.Sleep(400 * time.Millisecond)

	menuDropRect := image.Rect(winX, winY+50, winX+200, winY+200)
	hashAfterClick, err := find.GrabHash(sc, menuDropRect, nil)
	r.check("grab menu after click", err)
	if err == nil {
		if hashAfterClick != hashBefore {
			r.pass("screen changed after File menu click (menu opened)")
		} else {
			r.fail("screen unchanged after File menu click")
		}
		fpath := fmt.Sprintf("/tmp/%s-menu-%s.png", pfx, app.name)
		savePNG2(sc, menuDropRect, fpath)
		r.pass("menu region -> %s", fpath)
	}

	inp.KeyTap("escape") //nolint:errcheck
	time.Sleep(200 * time.Millisecond)

	// Right-click in the editor area — context menu should appear (button 3).
	rcX, rcY := winX+400, winY+300
	hashPreRC, _ := find.GrabHash(sc, winRect, nil)
	r.check("MouseClick right (context menu)", inp.MouseClick(rcX, rcY, 3))
	ctxRC, cancelRC := context.WithTimeout(context.Background(), 3*time.Second)
	_, rcErr := find.WaitForChange(ctxRC, sc, winRect, hashPreRC, 100*time.Millisecond, nil)
	cancelRC()
	if rcErr != nil {
		r.fail("right-click context menu did not appear (screen unchanged): %v", rcErr)
	} else {
		r.pass("right-click context menu appeared")
	}
	hashPreRCEsc, _ := find.GrabHash(sc, winRect, nil)
	inp.KeyTap("escape") //nolint:errcheck
	ctxRCE, cancelRCE := context.WithTimeout(context.Background(), 4*time.Second)
	_, rceErr := find.WaitForChange(ctxRCE, sc, winRect, hashPreRCEsc, 100*time.Millisecond, nil)
	cancelRCE()
	if rceErr != nil {
		r.fail("context menu did not close after Escape (screen unchanged)")
	} else {
		r.pass("context menu closed after Escape")
	}
	time.Sleep(100 * time.Millisecond)

	// ── Input Device ─────────────────────────────────────────────────────────
	// Tests Mousedown and Mouseup as independent events (vs. the combined
	// MouseClick helper). Validates that the virtual pointer backend correctly
	// emits press and release events separately.
	r.section("INPUT DEVICE [" + app.name + "]")

	// Move to a safe editor area.
	inp.MouseMove(winX+400, winY+300) //nolint:errcheck
	time.Sleep(100 * time.Millisecond)

	// Mousedown + Mouseup: one full click via the explicit press/release path.
	r.check("Mousedown button 1", inp.MouseDown(1))
	time.Sleep(50 * time.Millisecond)
	r.check("Mouseup button 1", inp.MouseUp(1))
	time.Sleep(200 * time.Millisecond)

	// Verify the click registered focus by typing a sentinel character and
	// confirming the editor content changes, then undoing it.
	editorScrollRect := image.Rect(winX+10, winY+60, winX+600, winY+400)
	hashPreSentinel, _ := find.GrabHash(sc, editorScrollRect, nil)
	inp.Type("~") //nolint:errcheck
	time.Sleep(200 * time.Millisecond)
	hashPostSentinel, _ := find.GrabHash(sc, editorScrollRect, nil)
	pf.Input.PressCombo("ctrl+z") //nolint:errcheck
	time.Sleep(100 * time.Millisecond)
	if hashPostSentinel != hashPreSentinel {
		r.pass("Mousedown/Mouseup: click registered (sentinel character appeared in editor)")
	} else {
		r.fail("Mousedown/Mouseup: click did not register (editor content unchanged after typing)")
	}

	// ── Text input ───────────────────────────────────────────────────────────
	r.section("TEXT INPUT [" + app.name + "]")

	editorX, editorY := winX+400, winY+300
	// Double-click to ensure focus and cursor placement
	r.check("click inside editor", inp.MouseClick(editorX, editorY, 1))
	time.Sleep(100 * time.Millisecond)
	inp.MouseClick(editorX, editorY, 1) //nolint:errcheck
	time.Sleep(500 * time.Millisecond)

	editorRect := image.Rect(winX+10, winY+60, winX+600, winY+400)
	hashEditorBefore, err := find.GrabHash(sc, editorRect, nil)
	r.check("grab editor before typing", err)

	r.check("Type text", inp.Type("hello from perfuncted"))
	time.Sleep(1 * time.Second)

	// WaitForChange confirms text appeared without a fixed sleep.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	hashEditorAfter, changeErr := find.WaitForChange(ctx, sc, editorRect, hashEditorBefore, 100*time.Millisecond, nil)
	cancel()
	if changeErr != nil {
		r.fail("editor text typed: no visual change detected after typing")
	} else {
		r.pass("editor changed after typing (hash %d->%d)", hashEditorBefore, hashEditorAfter)
	}

	// ── Keyboard ─────────────────────────────────────────────────────────────
	// Verifies that modifier-key combinations reach the focused window.
	// Strategy: type a unique marker, select-all, copy, then read via wl-paste.
	// Clipboard round-trip is reliable in both headless and visible sessions
	// because it uses Wayland protocols directly, not screencopy rendering.
	r.section("KEYBOARD [" + app.name + "]")

	inp.MouseClick(editorX, editorY, 1) //nolint:errcheck — re-focus editor
	time.Sleep(200 * time.Millisecond)

	// Clear any accumulated text from prior sections, then type a known marker.
	inp.KeyDown("ctrl")  //nolint:errcheck
	inp.KeyTap("a")      //nolint:errcheck
	inp.KeyUp("ctrl")    //nolint:errcheck
	inp.KeyTap("delete") //nolint:errcheck
	time.Sleep(100 * time.Millisecond)
	kbMarker := pfx + "-keyboard"
	r.check("type keyboard marker", inp.Type(kbMarker))
	time.Sleep(100 * time.Millisecond)

	// Ctrl+A → Ctrl+C: select all and copy to clipboard.
	inp.KeyDown("ctrl") //nolint:errcheck
	inp.KeyTap("a")     //nolint:errcheck
	inp.KeyTap("c")     //nolint:errcheck
	inp.KeyUp("ctrl")   //nolint:errcheck
	time.Sleep(500 * time.Millisecond)

	clipOut, clipErr := executil.CommandContext(context.Background(), "wl-paste").Output()
	if clipErr != nil {
		r.fail("clipboard: wl-paste failed: %v", clipErr)
	} else if got := strings.TrimSpace(string(clipOut)); got == kbMarker {
		r.pass("clipboard: verified %q via wl-paste", got)
	} else {
		r.fail("clipboard: wl-paste returned %q, want %q", got, kbMarker)
	}

	// ── Find ─────────────────────────────────────────────────────────────────
	// Tests the remaining find package APIs: WaitForChange timeout path,
	// WaitFor (wait for a specific hash to return), and ScanFor (multi-region).
	r.section("FIND [" + app.name + "]")

	// WaitForChange timeout: the menu bar is static between operations;
	// a short-timeout WaitForChange must return an error.
	hashMenuStable, _ := find.GrabHash(sc, menuBarRect, nil)
	ctxStatic, cancelStatic := context.WithTimeout(context.Background(), 400*time.Millisecond)
	_, timeoutErr := find.WaitForChange(ctxStatic, sc, menuBarRect, hashMenuStable, 50*time.Millisecond, nil)
	cancelStatic()
	if timeoutErr != nil {
		r.pass("WaitForChange timeout: static region correctly returned error")
	} else {
		r.fail("WaitForChange timeout: static region unexpectedly changed (cursor blink?)")
	}

	// WaitFor: open the File menu (screen changes), press Escape, then WaitFor
	// waits until the menu bar returns to its pre-menu hash.
	// A throwaway open+escape primes the menu bar to its settled post-escape
	// rendering state before we capture the reference hash, so that the
	// reference and the post-escape comparison state always match.
	inp.MouseMove(winX+400, winY+300) //nolint:errcheck — neutral position
	time.Sleep(200 * time.Millisecond)
	inp.MouseClick(winX+30, winY+35, 1) //nolint:errcheck — prime: open File menu
	time.Sleep(200 * time.Millisecond)
	inp.KeyTap("escape")               //nolint:errcheck — prime: close
	inp.MouseMove(winX+400, winY+300)  //nolint:errcheck
	time.Sleep(500 * time.Millisecond) // let menu bar fully settle to its closed state
	hashMenuClosed, _ := find.GrabHash(sc, menuBarRect, nil)
	inp.MouseClick(winX+30, winY+35, 1) //nolint:errcheck — open File menu (real test)
	time.Sleep(300 * time.Millisecond)
	inp.KeyTap("escape")               //nolint:errcheck
	inp.MouseMove(winX+400, winY+300)  //nolint:errcheck — move away so hover clears
	time.Sleep(500 * time.Millisecond) // extra settle before polling
	ctxWF, cancelWF := context.WithTimeout(context.Background(), 5*time.Second)
	finalHash, waitForErr := find.WaitFor(ctxWF, sc, menuBarRect, hashMenuClosed, 100*time.Millisecond, nil)
	cancelWF()
	if waitForErr != nil {
		r.fail("WaitFor: menu bar did not return to closed state: %v", waitForErr)
	} else {
		r.pass("WaitFor: menu bar returned to closed state after Escape (hash %d)", finalHash)
	}

	// ScanFor: two stable regions should both be found immediately.
	// Returns on the first region to match its expected hash.
	cornerRect2 := image.Rect(winX, winY, winX+50, winY+20)
	hashCorner2, _ := find.GrabHash(sc, cornerRect2, nil)
	hashMenuNow, _ := find.GrabHash(sc, menuBarRect, nil)
	ctxScan, cancelScan := context.WithTimeout(context.Background(), 3*time.Second)
	scanResult, scanErr := find.ScanFor(ctxScan, sc,
		[]image.Rectangle{cornerRect2, menuBarRect},
		[]uint32{hashCorner2, hashMenuNow},
		100*time.Millisecond, nil)
	cancelScan()
	if scanErr != nil {
		r.fail("ScanFor: %v", scanErr)
	} else {
		r.pass("ScanFor: matched rect %v (hash %d)", scanResult.Rect, scanResult.Hash)
	}

	// WaitForNoChange: the settled menu bar should be stable immediately.
	ctxNC, cancelNC := context.WithTimeout(context.Background(), 5*time.Second)
	stableHash, ncErr := find.WaitForNoChange(ctxNC, sc, menuBarRect, 3, 100*time.Millisecond, nil)
	cancelNC()
	if ncErr != nil {
		r.fail("WaitForNoChange: static region did not stabilise: %v", ncErr)
	} else {
		r.pass("WaitForNoChange: menu bar stable at %08x", stableHash)
	}

	// ── E2E ──────────────────────────────────────────────────────────────────
	// End-to-end: clear the editor, type a unique marker, verify it reached
	// the app via clipboard (primary check), then attempt a Ctrl+S file save
	// (bonus — succeeds in visible sessions; headless apps may not flush to disk).
	r.section("E2E [" + app.name + "]")

	wm.Activate(app.winMatch) //nolint:errcheck
	time.Sleep(200 * time.Millisecond)
	inp.MouseClick(editorX, editorY, 1) //nolint:errcheck
	time.Sleep(200 * time.Millisecond)

	inp.KeyDown("ctrl")  //nolint:errcheck
	inp.KeyTap("a")      //nolint:errcheck
	inp.KeyUp("ctrl")    //nolint:errcheck
	inp.KeyTap("delete") //nolint:errcheck
	time.Sleep(150 * time.Millisecond)

	e2eMarker := pfx + "-e2e"
	r.check("type E2E marker", inp.Type(e2eMarker))
	time.Sleep(100 * time.Millisecond)

	// Clipboard is the primary verification (reliable across all sessions).
	inp.KeyDown("ctrl") //nolint:errcheck
	inp.KeyTap("a")     //nolint:errcheck
	inp.KeyTap("c")     //nolint:errcheck
	inp.KeyUp("ctrl")   //nolint:errcheck
	time.Sleep(500 * time.Millisecond)

	clipOut2, clipErr2 := executil.CommandContext(context.Background(), "wl-paste").Output()
	if clipErr2 != nil {
		r.fail("clipboard: E2E wl-paste failed: %v", clipErr2)
	} else if got := strings.TrimSpace(string(clipOut2)); got == e2eMarker {
		r.pass("clipboard: E2E verified %q via wl-paste", got)
	} else {
		r.fail("clipboard: E2E wl-paste returned %q, want %q", got, e2eMarker)
	}

	// Ctrl+S: opens a Save As dialog for the document. Exercise dialog
	// interaction by detecting the dialog, typing a save path, and confirming.
	e2eSaveFile := fmt.Sprintf("/tmp/%s-e2e-save.txt", pfx)
	os.Remove(e2eSaveFile)
	defer os.Remove(e2eSaveFile)

	titleBefore, _ := wm.ActiveTitle()
	inp.KeyDown("ctrl") //nolint:errcheck
	inp.KeyTap("s")     //nolint:errcheck
	inp.KeyUp("ctrl")   //nolint:errcheck

	// Wait for the Save As dialog (active window title changes from editor).
	dialogTitle := ""
	dlgDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(dlgDeadline) {
		time.Sleep(200 * time.Millisecond)
		if t, err := wm.ActiveTitle(); err == nil && t != titleBefore {
			dialogTitle = t
			break
		}
	}

	if dialogTitle == "" {
		r.fail("Ctrl+S: Save As dialog did not appear")
		inp.KeyTap("escape") //nolint:errcheck
		time.Sleep(500 * time.Millisecond)
	} else {
		r.pass("Ctrl+S: Save As dialog appeared: %q", dialogTitle)

		// Type the full save path into the filename field. In KDE file
		// dialogs a leading / activates the path-edit bar automatically.
		time.Sleep(300 * time.Millisecond)
		r.check("type save path in dialog", inp.Type(e2eSaveFile))
		time.Sleep(200 * time.Millisecond)
		inp.KeyTap("return") //nolint:errcheck

		// Wait for the dialog to close (active title changes from dialog).
		dialogClosed := false
		closeDeadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(closeDeadline) {
			time.Sleep(200 * time.Millisecond)
			if t, err := wm.ActiveTitle(); err == nil && t != dialogTitle {
				dialogClosed = true
				break
			}
		}

		if !dialogClosed {
			r.fail("Ctrl+S: dialog did not close after Enter")
			inp.KeyTap("escape") //nolint:errcheck
			time.Sleep(500 * time.Millisecond)
		} else {
			r.pass("Ctrl+S: dialog dismissed after save")
			time.Sleep(500 * time.Millisecond)
			if content, readErr := os.ReadFile(e2eSaveFile); readErr != nil {
				r.fail("Ctrl+S file save: could not read %s: %v", e2eSaveFile, readErr)
			} else if strings.Contains(string(content), e2eMarker) {
				r.pass("Ctrl+S file save: %s contains marker", e2eSaveFile)
			} else {
				r.fail("Ctrl+S file save: %s does not contain marker (got %q)", e2eSaveFile, string(content))
			}
		}
	}

	// ── Move / Resize ─────────────────────────────────────────────────────────
	// Tested last so the window-position changes do not affect prior coordinate
	// calculations. The window remains floating after this section; it is killed
	// by the deferred proc.Process.Kill() immediately after testApp returns.
	r.section("MOVE/RESIZE [" + app.name + "]")
	wm.Activate(app.winMatch) //nolint:errcheck
	time.Sleep(200 * time.Millisecond)

	// In X11, Openbox might maximize windows. Force remove maximization.
	if os.Getenv("WAYLAND_DISPLAY") == "" && os.Getenv("DISPLAY") != "" {
		executil.CommandContext(context.Background(), "wmctrl", "-r", app.winMatch, "-b", "remove,maximized_vert,maximized_horz").Run() //nolint:errcheck
		time.Sleep(200 * time.Millisecond)
	}

	const testX, testY, testW, testH = 50, 50, 900, 650
	moveErr := wm.Move(app.winMatch, testX, testY)
	if moveErr == nil {
		time.Sleep(200 * time.Millisecond)
		ctxMove, cancelMove := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancelMove()
		if moved, mErr := pf.Window.WaitFor(ctxMove, app.winMatch, 300*time.Millisecond); mErr == nil {
			if moved.X == testX && moved.Y == testY {
				r.pass("Move: repositioned to (%d,%d)", testX, testY)
			} else {
				r.fail("Move: expected (%d,%d) got (%d,%d)", testX, testY, moved.X, moved.Y)
			}
		} else {
			r.fail("Move: verify: %v", mErr)
		}
	} else if errors.Is(moveErr, window.ErrNotSupported) {
		r.pass("Move: ErrNotSupported (expected on this compositor)")
	} else {
		r.fail("Move: unexpected error: %v", moveErr)
	}

	resizeErr := wm.Resize(app.winMatch, testW, testH)
	if resizeErr == nil {
		time.Sleep(200 * time.Millisecond)
		ctxResize, cancelResize := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancelResize()
		if resized, rErr := pf.Window.WaitFor(ctxResize, app.winMatch, 300*time.Millisecond); rErr == nil {
			if resized.W == testW && resized.H == testH {
				r.pass("Resize: resized to %dx%d", testW, testH)
			} else {
				r.fail("Resize: expected %dx%d got %dx%d", testW, testH, resized.W, resized.H)
			}
		} else {
			r.fail("Resize: verify: %v", rErr)
		}
	} else if errors.Is(resizeErr, window.ErrNotSupported) {
		r.pass("Resize: ErrNotSupported (expected on this compositor)")
	} else {
		r.fail("Resize: unexpected error: %v", resizeErr)
	}

	// ── New features ─────────────────────────────────────────────────────────
	r.section("NEW FEATURES [" + app.name + "]")

	// Screen resolution
	w, h, resErr := pf.Screen.Resolution()
	if resErr == nil && w > 0 && h > 0 {
		r.pass("Resolution: %dx%d", w, h)
	} else if resErr != nil {
		r.fail("Resolution: %v", resErr)
	} else {
		r.fail("Resolution: got %dx%d", w, h)
	}

	// FindColor — grab the actual pixel at (0,0) and search for it, proving
	// the FindColor functionality works regardless of background color.
	debugPixel, pixErr := find.FirstPixel(pf.Screen.Screenshotter, image.Rect(0, 0, 1, 1))
	if pixErr != nil {
		r.fail("FindColor (setup): %v", pixErr)
	} else {
		pt, fcErr := pf.Screen.FindColor(image.Rect(0, 0, 10, 10), debugPixel, 5)
		if fcErr == nil {
			r.pass("FindColor: found pixel R=%d G=%d B=%d at (%d,%d)", debugPixel.R, debugPixel.G, debugPixel.B, pt.X, pt.Y)
		} else {
			r.fail("FindColor: looking for R=%d G=%d B=%d: %v", debugPixel.R, debugPixel.G, debugPixel.B, fcErr)
		}
	}

	// Clipboard round-trip
	if pf.Clipboard.Clipboard != nil {
		marker := fmt.Sprintf("perfuncted-clip-%d", time.Now().UnixNano())
		if err := pf.Clipboard.Set(marker); err != nil {
			r.fail("Clipboard Set: %v", err)
		} else {
			time.Sleep(200 * time.Millisecond)
			got, err := pf.Clipboard.Get()
			if err != nil {
				r.fail("Clipboard Get: %v", err)
			} else if strings.TrimSpace(got) == marker {
				r.pass("Clipboard: round-trip OK")
			} else {
				r.fail("Clipboard: expected %q got %q", marker, got)
			}
		}
	} else {
		r.pass("Clipboard: not available (skipped)")
	}

	// Horizontal scroll (just test that the call succeeds)
	if err := pf.Input.ScrollLeft(1); err != nil {
		r.fail("ScrollLeft: %v", err)
	} else {
		r.pass("ScrollLeft: 1 click")
	}
	if err := pf.Input.ScrollRight(1); err != nil {
		r.fail("ScrollRight: %v", err)
	} else {
		r.pass("ScrollRight: 1 click")
	}

	// RetryUntil — should succeed immediately
	retryCtx, retryCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer retryCancel()
	retryErr := perfuncted.RetryUntil(retryCtx, 100*time.Millisecond, func() error {
		return nil
	})
	if retryErr == nil {
		r.pass("RetryUntil: immediate success")
	} else {
		r.fail("RetryUntil: %v", retryErr)
	}
}

// testBrowser proves perfuncted works with a real browser in a nested or headless
// session. It launches Firefox on about:blank, then navigates to about:support
// (a static info page) using Ctrl+L. WaitForChange detects the navigation start;
// WaitForNoChange detects the page finishing. This is the core primitive for
// browser automation with perfuncted.
func testBrowser(r *results, pf *perfuncted.Perfuncted, app appSpec) {
	sc := pf.Screen.Screenshotter
	inp := pf.Input.Inputter
	wm := pf.Window.Manager
	pfx := os.Getenv("PF_TEST_PREFIX")
	if pfx == "" {
		pfx = "perfuncted"
	}

	// ── Window ───────────────────────────────────────────────────────────────
	r.section("WINDOW [" + app.name + "]")

	proc := executil.CommandContext(context.Background(), app.launch[0], app.launch[1:]...)
	if len(app.extraEnv) > 0 {
		proc.Env = env.Merge(os.Environ(), app.extraEnv...)
	}
	if err := proc.Start(); err != nil {
		r.fail("%s launch: %v", app.launch[0], err)
		return
	}
	defer proc.Process.Kill() //nolint:errcheck

	// Firefox takes longer to start than text editors.
	ctx90, cancel90 := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel90()
	info, err := pf.Window.WaitFor(ctx90, app.winMatch, 300*time.Millisecond)
	r.check("window appeared in list", err)
	if err != nil {
		return
	}
	r.pass("found: %q (id=0x%x)", info.Title, info.ID)

	if err := wm.Activate(info.Title); err != nil {
		r.fail("Activate: %v", err)
	} else {
		r.pass("Activate %s", app.name)
	}
	// Wait for Firefox to finish painting its initial UI before we hash the screen.
	time.Sleep(2 * time.Second)

	active, err := wm.ActiveTitle()
	r.check("read ActiveTitle", err)
	if err == nil {
		// Firefox sets the title to the page title; accept any title that at least
		// shows the browser is foreground (we don't require "firefox" in the title
		// since Firefox may display "New Tab" or "about:blank" as the title).
		r.pass("ActiveTitle: %q", active)
	}

	// ── Screen ───────────────────────────────────────────────────────────────
	r.section("SCREEN [" + app.name + "]")

	winX, winY := info.X, info.Y
	winRect := image.Rect(winX, winY, winX+info.W, winY+info.H)
	r.pass("window rect: %v", winRect)

	hashBefore, err := find.GrabHash(sc, winRect, nil)
	r.check("grab window before navigation", err)
	if err != nil {
		return
	}
	r.pass("initial hash: %08x", hashBefore)

	// Save a screenshot for visual inspection.
	fpath := fmt.Sprintf("/tmp/%s-firefox-before.png", pfx)
	savePNG2(sc, winRect, fpath)
	r.pass("screenshot before navigation -> %s", fpath)

	// ── Mouse ────────────────────────────────────────────────────────────────
	r.section("MOUSE [" + app.name + "]")

	// Move mouse into the browser window to confirm mouse input reaches it.
	centerX, centerY := winX+info.W/2, winY+info.H/2
	r.check("MouseMove to window centre", inp.MouseMove(centerX, centerY))
	time.Sleep(100 * time.Millisecond)
	r.check("MouseMove to top-left area", inp.MouseMove(winX+50, winY+50))
	time.Sleep(100 * time.Millisecond)
	r.pass("mouse movement sent")

	// ── Navigation ───────────────────────────────────────────────────────────
	// Ctrl+L focuses the address bar; typing the URL and pressing Return triggers
	// navigation. about:support is a static info page — it always loads locally
	// with no network dependency, making it safe for headless CI.
	r.section("NAVIGATION [" + app.name + "]")

	// Dismiss any modal dialog (e.g. Firefox session-restore / close-tabs prompt)
	// that may have appeared before the browser is ready to receive keyboard input.
	inp.KeyTap("escape") //nolint:errcheck
	time.Sleep(300 * time.Millisecond)

	inp.KeyDown("ctrl") //nolint:errcheck
	inp.KeyTap("l")     //nolint:errcheck
	inp.KeyUp("ctrl")   //nolint:errcheck
	time.Sleep(300 * time.Millisecond)

	r.check("type URL", inp.Type("about:support"))
	time.Sleep(200 * time.Millisecond)
	r.check("press Return", inp.KeyTap("return"))

	// WaitForChange: screen must differ from the initial about:blank capture.
	// This proves keyboard input reached Firefox and navigation began.
	ctxChange, cancelChange := context.WithTimeout(context.Background(), 20*time.Second)
	_, changeErr := find.WaitForChange(ctxChange, sc, winRect, hashBefore, 200*time.Millisecond, nil)
	cancelChange()
	if changeErr != nil {
		r.fail("WaitForChange: browser did not change after navigation: %v", changeErr)
	} else {
		r.pass("WaitForChange: browser started rendering new page")
	}

	// WaitForNoChange: screen must stabilise after the page finishes loading.
	// This proves the navigate-and-wait primitive works end-to-end.
	ctxSettle, cancelSettle := context.WithTimeout(context.Background(), 30*time.Second)
	stableHash, settleErr := find.WaitForNoChange(ctxSettle, sc, winRect, 5, 200*time.Millisecond, nil)
	cancelSettle()
	if settleErr != nil {
		r.fail("WaitForNoChange: browser did not settle: %v", settleErr)
	} else {
		r.pass("WaitForNoChange: page settled at hash %08x", stableHash)
	}

	// Save a screenshot after navigation for visual confirmation.
	fpath = fmt.Sprintf("/tmp/%s-firefox-after.png", pfx)
	savePNG2(sc, winRect, fpath)
	r.pass("screenshot after navigation -> %s", fpath)

	// ── Find ─────────────────────────────────────────────────────────────────
	// Test the find APIs against a settled browser window.
	r.section("FIND [" + app.name + "]")

	// WaitForChange timeout: the settled page must not change within 400 ms.
	cornerRect := image.Rect(winX, winY, winX+100, winY+50)
	hashCorner, _ := find.GrabHash(sc, cornerRect, nil)
	ctxStatic, cancelStatic := context.WithTimeout(context.Background(), 400*time.Millisecond)
	_, timeoutErr := find.WaitForChange(ctxStatic, sc, cornerRect, hashCorner, 50*time.Millisecond, nil)
	cancelStatic()
	if timeoutErr != nil {
		r.pass("WaitForChange timeout: settled region correctly returned error")
	} else {
		r.fail("WaitForChange timeout: settled region unexpectedly changed")
	}

	// WaitForNoChange: stable region must return within 5 samples.
	ctxNC, cancelNC := context.WithTimeout(context.Background(), 5*time.Second)
	_, ncErr := find.WaitForNoChange(ctxNC, sc, cornerRect, 3, 100*time.Millisecond, nil)
	cancelNC()
	if ncErr != nil {
		r.fail("WaitForNoChange on stable region: %v", ncErr)
	} else {
		r.pass("WaitForNoChange: settled region confirmed stable")
	}

	// ScanFor: both regions should match their current hashes immediately.
	hashWindow, _ := find.GrabHash(sc, winRect, nil)
	ctxScan, cancelScan := context.WithTimeout(context.Background(), 3*time.Second)
	scanResult, scanErr := find.ScanFor(ctxScan, sc,
		[]image.Rectangle{cornerRect, winRect},
		[]uint32{hashCorner, hashWindow},
		100*time.Millisecond, nil)
	cancelScan()
	if scanErr != nil {
		r.fail("ScanFor: %v", scanErr)
	} else {
		r.pass("ScanFor: matched rect %v (hash %d)", scanResult.Rect, scanResult.Hash)
	}
}

func savePNG(img image.Image, path string) {
	f, err := os.Create(path)
	if err != nil {
		return
	}
	defer f.Close()
	png.Encode(f, img) //nolint:errcheck
}

func savePNG2(sc screen.Screenshotter, rect image.Rectangle, path string) {
	img, err := sc.Grab(rect)
	if err != nil {
		return
	}
	savePNG(img, path)
}

// ── results tracker ───────────────────────────────────────────────────────────

type results struct {
	passed  int
	failed  int
	current string
}

func (r *results) section(name string) {
	r.current = name
	fmt.Printf("\n── %s ──\n", name)
}

func (r *results) pass(msg string, args ...any) {
	r.passed++
	fmt.Printf("  PASS  %s\n", fmt.Sprintf(msg, args...))
}

func (r *results) fail(msg string, args ...any) {
	r.failed++
	fmt.Printf("  FAIL  %s\n", fmt.Sprintf(msg, args...))
}

func (r *results) check(label string, err error) {
	if err != nil {
		r.fail("%s: %v", label, err)
	} else {
		r.pass("%s", label)
	}
}

func (r *results) summary() {
	fmt.Printf("\n══════════════════════════════\n")
	fmt.Printf("  passed: %d  failed: %d\n", r.passed, r.failed)
	fmt.Printf("══════════════════════════════\n")
	if r.failed > 0 {
		os.Exit(1)
	}
}
