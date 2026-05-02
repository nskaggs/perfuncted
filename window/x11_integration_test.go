//go:build integration
// +build integration

package window_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/window"
)

// TestMain starts a throwaway Xvfb display for all integration tests in this
// package, sets DISPLAY, then tears the server down on exit.
func TestMain(m *testing.M) {
	display, stop, err := startXvfb()
	if err != nil {
		fmt.Fprintf(os.Stderr, "window integration: start Xvfb: %v\n", err)
		os.Exit(1)
	}
	os.Setenv("DISPLAY", display)
	code := m.Run()
	stop()
	os.Exit(code)
}

// startXvfb launches Xvfb on a free display number and returns the display
// string plus a stop function.
func startXvfb() (display string, stop func(), err error) {
	const dispNum = 99
	display = fmt.Sprintf(":%d", dispNum)

	cmd := exec.Command("Xvfb", display, "-screen", "0", "1024x768x24")
	if err := cmd.Start(); err != nil {
		return "", nil, fmt.Errorf("exec Xvfb: %w", err)
	}

	lockFile := fmt.Sprintf("/tmp/.X%d-lock", dispNum)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(lockFile); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if _, err := os.Stat(lockFile); err != nil {
		cmd.Process.Kill() //nolint:errcheck
		return "", nil, fmt.Errorf("Xvfb did not start within 10 s (lock %s not found)", lockFile)
	}

	stop = func() {
		cmd.Process.Kill() //nolint:errcheck
		cmd.Wait()         //nolint:errcheck
		os.Remove(lockFile)
		os.Remove(fmt.Sprintf("/tmp/.X11-unix/X%s", strconv.Itoa(dispNum)))
	}
	return display, stop, nil
}

// TestX11Backend_Integration_ActiveTitle connects to the Xvfb display and
// verifies that ActiveTitle returns without error on an empty display.
func TestX11Backend_Integration_ActiveTitle(t *testing.T) {
	b, err := window.NewX11Backend(os.Getenv("DISPLAY"))
	if err != nil {
		t.Skipf("cannot connect to X11 display: %v", err)
	}
	defer b.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := b.ActiveTitle(ctx); err != nil {
		t.Errorf("ActiveTitle() on empty Xvfb display: %v", err)
	}
}

// TestX11Backend_Integration_List verifies that List does not error on an
// empty Xvfb display (result will be empty, but the round-trip must succeed).
func TestX11Backend_Integration_List(t *testing.T) {
	b, err := window.NewX11Backend(os.Getenv("DISPLAY"))
	if err != nil {
		t.Skipf("cannot connect to X11 display: %v", err)
	}
	defer b.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := b.List(ctx); err != nil {
		t.Errorf("List() on empty Xvfb display: %v", err)
	}
}

