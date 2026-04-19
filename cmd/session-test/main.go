// cmd/session-test is a standalone integration test for the session package.
// It creates its own headless sway session from scratch (no pre-existing
// compositor needed), connects perfuncted to it, launches an app, grabs the
// screen, and tears everything down.
//
// Exit code 0 = all checks passed.
package main

import (
	"context"
	"fmt"
	"image"
	"os"
	"time"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/session"
)

func main() {
	passed, failed := 0, 0

	pass := func(msg string, args ...any) {
		passed++
		fmt.Printf("  PASS  %s\n", fmt.Sprintf(msg, args...))
	}
	fail := func(msg string, args ...any) {
		failed++
		fmt.Printf("  FAIL  %s\n", fmt.Sprintf(msg, args...))
	}

	// 1. Start a session.
	fmt.Println("── SESSION LIFECYCLE ─────────────────────────────")
	sess, err := session.Start(session.Config{
		Resolution: image.Pt(800, 600),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "session.Start: %v\n", err)
		os.Exit(1)
	}
	defer sess.Stop()
	pass("session.Start (xdg=%s)", sess.XDGRuntimeDir())

	// 2. Verify accessors.
	if sess.WaylandDisplay() == "" {
		fail("WaylandDisplay is empty")
	} else {
		pass("WaylandDisplay = %s", sess.WaylandDisplay())
	}
	if sess.DBusAddress() == "" {
		fail("DBusAddress is empty")
	} else {
		pass("DBusAddress = %s", sess.DBusAddress())
	}

	// 3. Env returns session vars.
	env := sess.Env()
	hasXDG := false
	for _, e := range env {
		if e == "XDG_RUNTIME_DIR="+sess.XDGRuntimeDir() {
			hasXDG = true
		}
	}
	if hasXDG {
		pass("Env() contains session XDG_RUNTIME_DIR")
	} else {
		fail("Env() missing session XDG_RUNTIME_DIR")
	}

	// 4. Connect perfuncted.
	pf, err := sess.Perfuncted(perfuncted.Options{})
	if err != nil {
		fail("sess.Perfuncted: %v", err)
	} else {
		defer pf.Close()
		pass("sess.Perfuncted connected")
	}

	// 5. Grab screen.
	if pf != nil && pf.Screen.Screenshotter != nil {
		img, err := pf.Screen.Grab(context.Background(), image.Rect(0, 0, 100, 100))
		if err != nil {
			fail("Screen.Grab: %v", err)
		} else if img.Bounds().Dx() >= 100 {
			pass("Screen.Grab: %dx%d", img.Bounds().Dx(), img.Bounds().Dy())
		} else {
			fail("Screen.Grab: unexpected size %v", img.Bounds())
		}
	}

	// 6. Launch an app.
	cmd, err := sess.Launch("kwrite")
	if err != nil {
		fail("sess.Launch kwrite: %v", err)
	} else {
		pass("sess.Launch kwrite (pid=%d)", cmd.Process.Pid)
		time.Sleep(2 * time.Second)

		// Check window appeared.
		if pf != nil && pf.Window.Manager != nil {
			wins, err := pf.Window.List()
			if err != nil {
				fail("Window.List: %v", err)
			} else if len(wins) > 0 {
				pass("Window.List: %d window(s), first=%q", len(wins), wins[0].Title)
			} else {
				fail("Window.List: no windows after launching kwrite")
			}
		}

		cmd.Process.Kill()
		cmd.Wait()
	}

	// 7. Clipboard round-trip.
	if pf != nil && pf.Clipboard.Clipboard != nil {
		marker := "session-test-clip"
		if err := pf.Clipboard.Set(marker); err != nil {
			fail("Clipboard.Set: %v", err)
		} else {
			time.Sleep(200 * time.Millisecond)
			got, err := pf.Clipboard.Get()
			if err != nil {
				fail("Clipboard.Get: %v", err)
			} else if got == marker {
				pass("Clipboard round-trip OK")
			} else {
				fail("Clipboard: expected %q got %q", marker, got)
			}
		}
	}

	// Summary.
	fmt.Printf("\n══════════════════════════════\n")
	fmt.Printf("  passed: %d  failed: %d\n", passed, failed)
	fmt.Printf("══════════════════════════════\n")
	if failed > 0 {
		os.Exit(1)
	}
}
