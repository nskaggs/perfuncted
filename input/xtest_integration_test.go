//go:build integration
// +build integration

package input_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/internal/x11test"
)

// TestMain starts a throwaway Xvfb display for all integration tests in this
// package, sets DISPLAY, then tears the server down on exit.
func TestMain(m *testing.M) {
	display, stop, err := x11test.StartXvfb()
	if err != nil {
		fmt.Fprintf(os.Stderr, "input integration: start Xvfb: %v\n", err)
		os.Exit(1)
	}
	os.Setenv("DISPLAY", display)
	code := m.Run()
	stop()
	os.Exit(code)
}

// TestXTestBackend_Integration_Type connects to the Xvfb display and fires
// a single key event. This validates that XTEST initialises and FakeInput
// round-trips without error; it does not assert side-effects because there is
// no receiver window.
func TestXTestBackend_Integration_Type(t *testing.T) {
	b, err := input.NewXTestBackend(os.Getenv("DISPLAY"))
	if err != nil {
		t.Skipf("cannot init XTEST on %s: %v", os.Getenv("DISPLAY"), err)
	}
	defer b.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := b.Type(ctx, "a"); err != nil {
		t.Errorf("Type('a') on Xvfb: %v", err)
	}
}

// TestXTestBackend_Integration_MouseMove verifies that a mouse move completes
// successfully on the headless display.
func TestXTestBackend_Integration_MouseMove(t *testing.T) {
	b, err := input.NewXTestBackend(os.Getenv("DISPLAY"))
	if err != nil {
		t.Skipf("cannot init XTEST on %s: %v", os.Getenv("DISPLAY"), err)
	}
	defer b.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := b.MouseMove(ctx, 100, 100); err != nil {
		t.Errorf("MouseMove on Xvfb: %v", err)
	}
}
