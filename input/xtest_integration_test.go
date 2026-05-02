//go:build integration
// +build integration

package input_test

import (
"context"
"fmt"
"os"
"os/exec"
"strconv"
"testing"
"time"

"github.com/nskaggs/perfuncted/input"
)

// TestMain starts a throwaway Xvfb display for all integration tests in this
// package, sets DISPLAY, then tears the server down on exit.
func TestMain(m *testing.M) {
display, stop, err := startXvfb()
if err != nil {
fmt.Fprintf(os.Stderr, "input integration: start Xvfb: %v\n", err)
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
const dispNum = 98
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

// TestXTestBackend_Integration_KeyTap connects to the Xvfb display and fires
// a single key tap.  This validates that XTEST initialises and FakeInput
// round-trips without error; it does not assert side-effects because there is
// no receiver window.
func TestXTestBackend_Integration_KeyTap(t *testing.T) {
b, err := input.NewXTestBackend(os.Getenv("DISPLAY"))
if err != nil {
t.Skipf("cannot init XTEST on %s: %v", os.Getenv("DISPLAY"), err)
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
