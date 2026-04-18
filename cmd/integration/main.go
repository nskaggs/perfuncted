// cmd/integration is a live integration test that validates each core capability
// of the perfuncted library against the current display. It tests against
// every app executable found in PATH (kwrite, pluma, firefox).
//
// Usage:
//
//	go run ./cmd/integration --headless   # Start an isolated session and run tests
//	go run ./cmd/integration --nested     # Auto-detect and run against nested session
//	go run ./cmd/integration --app kwrite # Run tests only for kwrite
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"image"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/find"
	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/internal/executil"
	"github.com/nskaggs/perfuncted/screen"
	"github.com/nskaggs/perfuncted/session"
	"github.com/nskaggs/perfuncted/window"
)

func main() {
	headless := flag.Bool("headless", false, "start a new isolated headless sway session for the test")
	nested := flag.Bool("nested", false, "connect to an existing nested sway session in /tmp")
	appFilter := flag.String("app", "", "run only this app (kwrite, pluma, firefox); empty = all")
	flag.Parse()

	var sess *session.Session
	var err error

	if *headless {
		fmt.Println("▶ starting headless session...")
		sess, err = session.Start(session.Config{
			Resolution: image.Pt(1024, 768),
		})
		if err != nil {
			log.Fatalf("failed to start headless session: %v", err)
		}
		defer sess.Stop()
		fmt.Printf("  session ready (XDG=%s)\n", sess.XDGRuntimeDir())
	}

	opts := perfuncted.Options{
		Nested: *nested,
		MaxX:   1024,
		MaxY:   768,
	}
	if sess != nil {
		opts.XDGRuntimeDir = sess.XDGRuntimeDir()
		opts.WaylandDisplay = sess.WaylandDisplay()
		opts.DBusSessionAddress = sess.DBusAddress()
	}

	pf, err := perfuncted.New(opts)
	if err != nil {
		log.Fatalf("perfuncted.New: %v", err)
	}
	defer pf.Close()

	fmt.Printf("screen: %T\ninput:  %T\nwindow: %T\n\n", pf.Screen.Screenshotter, pf.Input.Inputter, pf.Window.Manager)

	r := &results{}
	ctx := &testContext{pf: pf, r: r, sess: sess}

	// ── 1. Global Screen/Probe Tests ──────────────────────────────────────────
	r.section("SYSTEM PROBE")
	testProbes(r)

	r.section("BASIC SCREEN")
	testBasicScreen(ctx)

	// ── 2. Per-app Integration Tests ─────────────────────────────────────────
	apps := detectApps()
	if *appFilter != "" {
		var filtered []appSpec
		for _, a := range apps {
			if a.name == *appFilter {
				filtered = append(filtered, a)
			}
		}
		apps = filtered
	}

	for _, app := range apps {
		if app.isBrowser {
			testBrowser(ctx, app)
		} else {
			testApp(ctx, app)
		}
	}

	r.summary()
}

type testContext struct {
	pf   *perfuncted.Perfuncted
	r    *results
	sess *session.Session
}

// ── Tests ────────────────────────────────────────────────────────────────────

func testProbes(r *results) {
	fmt.Println("  (enumerating backends...)")
	for _, res := range screen.Probe() {
		fmt.Printf("  screen: %v\n", res)
	}
	for _, res := range input.Probe() {
		fmt.Printf("  input: %v\n", res)
	}
	for _, res := range window.Probe() {
		fmt.Printf("  window: %v\n", res)
	}
	r.pass("probes enumerated")
}

func testBasicScreen(ctx *testContext) {
	pf, r := ctx.pf, ctx.r
	w, h, err := pf.Screen.Resolution()
	r.check("Resolution", err)
	if err == nil {
		r.pass("resolution: %dx%d", w, h)
	}

	rect := image.Rect(0, 0, 100, 100)
	img, err := pf.Screen.Grab(rect)
	r.check("Grab 100x100", err)
	if err == nil {
		hVal := find.PixelHash(img, nil)
		r.pass("PixelHash: %08x", hVal)
	}

	// CaptureRegion
	tmpPNG := filepath.Join(os.TempDir(), "pf-test-capture.png")
	defer os.Remove(tmpPNG)
	r.check("CaptureRegion", pf.Screen.CaptureRegion(rect, tmpPNG))
	if _, err := os.Stat(tmpPNG); err == nil {
		r.pass("CaptureRegion: file created")
	}

	// GetPixel
	c, err := pf.Screen.GetPixel(5, 5)
	r.check("GetPixel", err)
	if err == nil {
		r.pass("GetPixel(5,5): RGBA(%d,%d,%d,%d)", c.R, c.G, c.B, c.A)
	}

	// WaitForFn
	fmt.Println("  (testing WaitForFn...)")
	ctxF, cancelF := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelF()
	_, err = pf.Screen.WaitForFn(ctxF, rect, func(i image.Image) bool {
		return i != nil
	}, 100*time.Millisecond)
	r.check("WaitForFn", err)
}

func testApp(ctx *testContext, app appSpec) {
	pf, r, sess := ctx.pf, ctx.r, ctx.sess
	r.section("APP [" + app.name + "]")

	// Cleanup and pre-create file
	os.Remove(app.saveFile)
	if err := os.WriteFile(app.saveFile, []byte(""), 0644); err != nil {
		r.fail("pre-create %s: %v", app.saveFile, err)
		return
	}
	defer os.Remove(app.saveFile)

	var cmd *exec.Cmd
	if sess != nil {
		c, err := sess.Launch(app.launch[0], append(app.launch[1:], app.saveFile)...)
		if err != nil {
			r.fail("launch %s via session: %v", app.name, err)
			return
		}
		cmd = c
	} else {
		cmd = exec.Command(app.launch[0], append(app.launch[1:], app.saveFile)...)
		if len(app.extraEnv) > 0 {
			cmd.Env = append(os.Environ(), app.extraEnv...)
		}
		if err := cmd.Start(); err != nil {
			r.fail("launch %s: %v", app.name, err)
			return
		}
	}
	defer cmd.Process.Kill()

	// 1. Window Management
	wctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	info, err := pf.Window.WaitFor(wctx, app.winMatch, 500*time.Millisecond)
	r.check("WaitFor window", err)
	if err != nil {
		return
	}
	r.pass("Found window: %q (id=0x%x)", info.Title, info.ID)

	r.check("Activate window", pf.Window.Activate(app.winMatch))
	time.Sleep(1 * time.Second)

	active, err := pf.Window.ActiveTitle()
	r.check("ActiveTitle", err)
	if err == nil {
		r.pass("ActiveTitle: %q", active)
	}

	rect, err := pf.Window.GetGeometry(app.winMatch)
	r.check("GetGeometry", err)
	if err == nil {
		r.pass("window rect: %v", rect)
	}

	// 2. Input
	r.section("INPUT [" + app.name + "]")

	// ClickCenter
	r.check("ClickCenter", pf.Input.ClickCenter(rect))
	time.Sleep(500 * time.Millisecond)

	// Type with Delay
	r.check("TypeWithDelay", pf.Input.TypeWithDelay("Integration", 20*time.Millisecond))
	time.Sleep(200 * time.Millisecond)

	// Paste
	marker := "PF-PASTE-" + app.name
	r.check("Paste", pf.Paste(marker))
	time.Sleep(1 * time.Second)

	// Save before opening a new tab: Action: Ctrl+S. Verify content and mtime.
	var beforeMod time.Time
	if fi, err := os.Stat(app.saveFile); err == nil {
		beforeMod = fi.ModTime()
	}
	r.check("Ctrl+S (Save)", pf.Input.PressCombo("ctrl+s"))
	time.Sleep(2 * time.Second)
	content, err := os.ReadFile(app.saveFile)
	if err == nil && strings.Contains(string(content), marker) {
		r.pass("File saved correctly with marker (pre-tab save)")
		if fi, err := os.Stat(app.saveFile); err == nil {
			if fi.ModTime().After(beforeMod) {
				r.pass("File mtime updated after save (pre-tab)")
			} else {
				r.fail("File mtime not updated after save (pre-tab)")
			}
		}
	} else {
		r.fail("Pre-tab save failed: marker %q not found or unreadable: %v", marker, err)
	}

	// Ctrl+N test: press Ctrl+N then immediately close with Ctrl+W. Verify
	// that the original buffer remains saved and no unintended dialogs appeared.
	if app.name == "kwrite" || app.name == "pluma" {
		r.check("Ctrl+N (New Tab)", pf.Input.PressCombo("ctrl+n"))
		time.Sleep(500 * time.Millisecond)
		r.check("Ctrl+W (Close Tab)", pf.Input.PressCombo("ctrl+w"))
		time.Sleep(500 * time.Millisecond)

		// Ensure the app window is still visible (we closed only the tab)
		if !pf.Window.IsVisible(app.winMatch) {
			r.fail("After Ctrl+W the application window is not visible; tab/close behavior incorrect")
		} else {
			// Check file still contains marker and no save dialogs appeared.
			content2, err2 := os.ReadFile(app.saveFile)
			if err2 != nil {
				r.fail("Post-tab-close: could not read save file: %v", err2)
			} else if !strings.Contains(string(content2), marker) {
				r.fail("Post-tab-close: marker %q missing (tab close may have affected buffer)", marker)
			} else {
				r.pass("Ctrl+N/Ctrl+W sequence closed new tab and original buffer preserved")
			}
			// Also assert no Save dialog popped up during close
			dialogs := []string{"Save", "Save As", "Save Changes", "Do you want to save", "Document Modified"}
			for _, d := range dialogs {
				ctxD, cancelD := context.WithTimeout(context.Background(), 1*time.Second)
				_, err := pf.Window.WaitFor(ctxD, d, 200*time.Millisecond)
				cancelD()
				if err == nil {
					r.fail("Save dialog %q appeared during Ctrl+N/Ctrl+W sequence", d)
					break
				}
			}
		}
	}

	// 3. Screen Find
	r.section("SCREEN-FIND [" + app.name + "]")

	// WaitForVisibleChange
	fmt.Println("  (testing WaitForVisibleChange via typing...)")
	ctxV, cancelV := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelV()
	go func() {
		time.Sleep(1 * time.Second)
		// Ensure the application is still focused before interacting. If not, record a failure.
		if title, terr := pf.Window.ActiveTitle(); terr == nil {
			if !strings.Contains(strings.ToLower(title), strings.ToLower(app.winMatch)) {
				r.fail("Menu action target not focused: active title %q", title)
				return
			}
		}
		// Open the File menu with Alt+F, then close it with Escape to provoke a visible
		// UI change without modifying the document contents.
		if err := pf.Input.PressCombo("alt+f"); err != nil {
			r.fail("Alt+F failed: %v", err)
			return
		}
		time.Sleep(200 * time.Millisecond)
		_ = pf.Input.KeyTap("escape")
	}()
	_, err = pf.Screen.WaitForVisibleChange(ctxV, rect, 100*time.Millisecond, 2)
	r.check("WaitForVisibleChange", err)

	// LocateExact
	refRect := image.Rect(rect.Min.X+20, rect.Min.Y+20, rect.Min.X+50, rect.Min.Y+50)
	refImg, err := pf.Screen.Grab(refRect)
	if err == nil {
		found, err := pf.Screen.LocateExact(rect, refImg)
		r.check("LocateExact", err)
		if err == nil && found.Min != refRect.Min {
			r.fail("LocateExact: expected %v, got %v", refRect, found)
		}
	}

	// 4. Window State
	r.section("WINDOW-STATE [" + app.name + "]")

	r.check("Resize", pf.Window.Resize(app.winMatch, 800, 600))
	time.Sleep(1 * time.Second)
	newRect, _ := pf.Window.GetGeometry(app.winMatch)
	// Allow some tolerance in CI where window decorations or scaling may alter final size.
	minW, maxW := 800*80/100, 800*120/100
	minH, maxH := 600*80/100, 600*120/100
	if newRect.Dx() >= minW && newRect.Dx() <= maxW && newRect.Dy() >= minH && newRect.Dy() <= maxH {
		r.pass("Resize: confirmed %dx%d (within tolerance)", newRect.Dx(), newRect.Dy())
	} else {
		r.fail("Resize: expected 800x600, got %dx%d", newRect.Dx(), newRect.Dy())
	}

	r.check("CloseWindow", pf.Window.CloseWindow(app.winMatch))
	time.Sleep(1 * time.Second)
	if pf.Window.IsVisible(app.winMatch) {
		_ = pf.Input.KeyTap("escape")
		time.Sleep(500 * time.Millisecond)
		_ = pf.Window.CloseWindow(app.winMatch)
	}
	// If the window is still visible, try killing the launched process as a last resort.
	if pf.Window.IsVisible(app.winMatch) {
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
			time.Sleep(1 * time.Second)
			// Try a second kill if it's still visible.
			if pf.Window.IsVisible(app.winMatch) {
				_ = cmd.Process.Kill()
				time.Sleep(1 * time.Second)
			}
		}
	}

	// Choose a longer timeout for pluma, which can be slow to exit in CI.
	timeout := 45 * time.Second
	if app.name == "pluma" {
		timeout = 90 * time.Second
	}
	ctxC, cancelC := context.WithTimeout(context.Background(), timeout)
	defer cancelC()
	r.check("WaitForClose", pf.Window.WaitForClose(ctxC, app.winMatch, 200*time.Millisecond))
}

func testBrowser(ctx *testContext, app appSpec) {
	pf, r, sess := ctx.pf, ctx.r, ctx.sess
	r.section("BROWSER [" + app.name + "]")

	var cmd *exec.Cmd
	if sess != nil {
		c, err := sess.Launch(app.launch[0], app.launch[1:]...)
		if err != nil {
			r.fail("launch browser via session: %v", err)
			return
		}
		cmd = c
	} else {
		cmd = exec.Command(app.launch[0], app.launch[1:]...)
		cmd.Env = append(os.Environ(), app.extraEnv...)
		if err := cmd.Start(); err != nil {
			r.fail("launch browser: %v", err)
			return
		}
	}
	defer cmd.Process.Kill()

	wctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	info, err := pf.Window.WaitFor(wctx, app.winMatch, 1*time.Second)
	r.check("Browser appeared", err)
	if err != nil {
		return
	}

	r.check("Activate browser", pf.Window.Activate(app.winMatch))
	time.Sleep(5 * time.Second)

	// Navigation test
	r.check("Ctrl+L (Focus Address Bar)", pf.Input.PressCombo("ctrl+l"))
	time.Sleep(1 * time.Second)
	r.check("Type URL", pf.Input.Type("about:support"))
	time.Sleep(500 * time.Millisecond)
	r.check("Return", pf.Input.KeyTap("return"))

	fmt.Println("  (testing WaitForStable...)")
	rect := image.Rect(info.X, info.Y, info.X+info.W, info.Y+info.H)
	ctxS, cancelS := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelS()
	_, err = pf.Screen.WaitForStable(ctxS, rect, 5, 1*time.Second)
	r.check("WaitForStable", err)
}

// ── App Detection ────────────────────────────────────────────────────────────

type appSpec struct {
	name      string
	launch    []string
	winMatch  string
	saveFile  string
	extraEnv  []string
	isBrowser bool
}

func detectApps() []appSpec {
	pfx := os.Getenv("PF_TEST_PREFIX")
	if pfx == "" {
		pfx = "integration"
	}
	all := []appSpec{
		{
			name:     "kwrite",
			launch:   []string{"kwrite"},
			winMatch: "kwrite",
			saveFile: filepath.Join(os.TempDir(), pfx+"-kwrite.txt"),
		},
		{
			name:     "pluma",
			launch:   []string{"dbus-run-session", "pluma"},
			winMatch: "pluma",
			saveFile: filepath.Join(os.TempDir(), pfx+"-pluma.txt"),
			extraEnv: []string{"GTK_USE_PORTAL=0"},
		},
		{
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
		// Prefer detecting the real application binary rather than wrapper commands
		// (e.g. pluma is launched via "dbus-run-session pluma"). If a wrapper like
		// dbus-run-session is used, check the wrapped command exists.
		candidate := a.launch[0]
		if len(a.launch) > 1 && a.launch[0] == "dbus-run-session" {
			candidate = a.launch[1]
		} else {
			// Otherwise pick the first element that looks like a command (not a flag or url).
			for _, el := range a.launch {
				if strings.HasPrefix(el, "-") || strings.Contains(el, ":") {
					continue
				}
				candidate = el
				break
			}
		}
		if _, err := executil.LookPath(candidate); err == nil {
			found = append(found, a)
		}
	}

	// Temporarily exclude pluma from the test matrix. Pluma requires a full
	// window manager to handle some of its GTK dialogs reliably in CI. Track
	// re-enabling the test once a minimal WM or improved session handling is
	// added.
	var filtered []appSpec
	for _, a := range found {
		if a.name == "pluma" {
			continue
		}
		filtered = append(filtered, a)
	}
	return filtered
}

// ── Results Tracker ──────────────────────────────────────────────────────────

type results struct {
	passed  int
	failed  int
	current string
	logs    bytes.Buffer
}

func (r *results) section(name string) {
	r.current = name
	fmt.Printf("\n── %s ──\n", name)
}

func (r *results) pass(msg string, args ...any) {
	r.passed++
	s := fmt.Sprintf("  PASS  %s\n", fmt.Sprintf(msg, args...))
	fmt.Print(s)
	r.logs.WriteString(s)
}

func (r *results) fail(msg string, args ...any) {
	r.failed++
	s := fmt.Sprintf("  FAIL  %s\n", fmt.Sprintf(msg, args...))
	fmt.Print(s)
	r.logs.WriteString(s)
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
