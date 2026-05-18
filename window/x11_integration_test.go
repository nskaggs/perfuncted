//go:build integration
// +build integration

package window_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/x11test"
	"github.com/nskaggs/perfuncted/window"
)

// TestMain starts a throwaway Xvfb display for all integration tests in this
// package, sets DISPLAY, then tears the server down on exit.

// sharedBackend is created once in TestMain to avoid repeated open/close
// cycles that can cause Xvfb to refuse subsequent connections.
var sharedBackend *window.X11Backend

func TestMain(m *testing.M) {
	display, stop, err := x11test.StartXvfb()
	if err != nil {
		fmt.Fprintf(os.Stderr, "window integration: start Xvfb: %v\n", err)
		os.Exit(1)
	}
	os.Setenv("DISPLAY", display)

	sharedBackend, err = window.NewX11Backend(display)
	if err != nil {
		fmt.Fprintf(os.Stderr, "window integration: connect to Xvfb: %v\n", err)
		stop()
		os.Exit(1)
	}

	code := m.Run()
	sharedBackend.Close()
	stop()
	os.Exit(code)
}

// TestX11Backend_Integration_ActiveTitle connects to the Xvfb display and
// verifies that ActiveTitle returns without error on an empty display.
func TestX11Backend_Integration_ActiveTitle(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sharedBackend.ActiveTitle(ctx); err != nil {
		t.Errorf("ActiveTitle() on empty Xvfb display: %v", err)
	}
}

// TestX11Backend_Integration_List verifies that List does not error on an
// empty Xvfb display (result will be empty, but the round-trip must succeed).
func TestX11Backend_Integration_List(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := sharedBackend.List(ctx); err != nil {
		t.Errorf("List() on empty Xvfb display: %v", err)
	}
}
