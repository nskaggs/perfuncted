package main

import (
	"context"
	"flag"
	"fmt"
	"image"
	"log"
	"os"
	"time"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/screen"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	headless := flag.Bool("headless", false, "start a new headless session")
	flag.Parse()

	var sess *perfuncted.Session
	var err error

	if *headless {
		sess, err = perfuncted.StartSession(perfuncted.SessionConfig{
			Resolution: image.Pt(1024, 768),
		})
		if err != nil {
			return fmt.Errorf("failed to start session: %w", err)
		}
		defer sess.Stop()
		fmt.Printf("Started session: %s\n", sess.XDGRuntimeDir())

		// Set env for the library to auto-detect
		os.Setenv("XDG_RUNTIME_DIR", sess.XDGRuntimeDir())
		os.Setenv("WAYLAND_DISPLAY", sess.WaylandDisplay())
		os.Setenv("DBUS_SESSION_BUS_ADDRESS", sess.DBusAddress())
	}

	pf, err := perfuncted.New(perfuncted.Options{})
	if err != nil {
		return fmt.Errorf("failed to open backends: %w", err)
	}
	defer pf.Close()

	fmt.Printf("Screen: %T\n", pf.Screen.Screenshotter)
	fmt.Printf("Input:  %T\n", pf.Input.Inputter)
	fmt.Printf("Window: %T\n", pf.Window.Manager)

	if _, ok := pf.Screen.Screenshotter.(*screen.PortalDBusBackend); ok {
		fmt.Println("WARNING: Using Portal backend; this requires manual permission and is slow.")
	}

	// 1. Basic Screen Capture
	start := time.Now()
	img, err := pf.Screen.Grab(image.Rect(0, 0, 100, 100))
	if err != nil {
		return fmt.Errorf("grab failed: %w", err)
	}
	fmt.Printf("Grabbed 100x100 in %v (bounds: %v)\n", time.Since(start), img.Bounds())

	// 2. Input & Window Test
	fmt.Println("Launching kwrite...")
	var launchErr error
	if sess == nil {
		return fmt.Errorf("this test requires --headless to safely launch apps")
	}
	_, launchErr = sess.Launch("kwrite")

	if launchErr != nil {
		return fmt.Errorf("failed to launch app: %w", launchErr)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	fmt.Println("Waiting for window...")
	info, err := pf.Window.WaitFor(ctx, "kwrite", 500*time.Millisecond)
	if err != nil {
		return fmt.Errorf("window did not appear: %w", err)
	}
	fmt.Printf("Found window: %q (0x%x)\n", info.Title, info.ID)

	pf.Window.Activate("kwrite")
	time.Sleep(1 * time.Second)

	fmt.Println("Typing into kwrite...")
	pf.Input.Type("Hello from perfuncted session test!")
	pf.Input.KeyTap("return")

	fmt.Println("Closing in 3 seconds...")
	time.Sleep(3 * time.Second)
	return nil
}
