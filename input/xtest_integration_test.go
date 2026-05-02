//go:build integration
// +build integration

package input_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/input"
)

// TestXTestBackend_Integration_KeyTap connects to the DISPLAY set by the
// window package's TestMain (Xvfb) and fires a single key tap. This validates
// that XTEST initialises and FakeInput round-trips without error; it does not
// assert side-effects because there is no receiver window.
func TestXTestBackend_Integration_KeyTap(t *testing.T) {
	display := os.Getenv("DISPLAY")
	if display == "" {
		t.Skip("DISPLAY not set")
	}

	b, err := input.NewXTestBackend(display)
	if err != nil {
		t.Skipf("cannot init XTEST on %s: %v", display, err)
	}
	defer b.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := b.KeyTap(ctx, "a"); err != nil {
		t.Errorf("KeyTap('a') on Xvfb: %v", err)
	}
}

// TestXTestBackend_Integration_MouseMove verifies that a mouse move completes
// successfully on the headless display.
func TestXTestBackend_Integration_MouseMove(t *testing.T) {
	display := os.Getenv("DISPLAY")
	if display == "" {
		t.Skip("DISPLAY not set")
	}

	b, err := input.NewXTestBackend(display)
	if err != nil {
		t.Skipf("cannot init XTEST on %s: %v", display, err)
	}
	defer b.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := b.MouseMove(ctx, 100, 100); err != nil {
		t.Errorf("MouseMove on Xvfb: %v", err)
	}
}
