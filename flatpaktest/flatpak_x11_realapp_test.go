//go:build integration && linux

package flatpaktest

import (
	"bytes"
	"fmt"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Unlike synthetic input success codes, xev records real X events delivered
// to a distinct GUI process running on the same Xvfb desktop as the bundle.
func testInstalledFlatpakRealX11Application(t *testing.T, h *harness, sourceRoot, testRoot string) {
	t.Helper()
	for _, tool := range []string{"openbox", "xev", "xdotool", "stdbuf", "xprop"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("real X11 acceptance requires %s: %v", tool, err)
		}
	}
	logPath := filepath.Join(testRoot, "x11-target-events.log")
	events, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer events.Close()
	wmLog, err := os.Create(filepath.Join(testRoot, "x11-window-manager.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer wmLog.Close()
	wm := exec.Command("openbox", "--sm-disable")
	wm.Env = h.env
	wm.Stdout = wmLog
	wm.Stderr = wmLog
	if err := wm.Start(); err != nil {
		t.Fatalf("start isolated X11 WM: %v", err)
	}
	t.Cleanup(func() { _ = wm.Process.Kill(); _ = wm.Wait() })
	if !pollX11Program(5*time.Second, func() bool {
		cmd := exec.Command("xprop", "-root", "_NET_SUPPORTING_WM_CHECK")
		cmd.Env = h.env
		out, err := cmd.CombinedOutput()
		return err == nil && bytes.Contains(out, []byte("_NET_SUPPORTING_WM_CHECK"))
	}) {
		log, _ := os.ReadFile(wmLog.Name())
		t.Fatalf("X11 WM did not advertise EWMH: %s", log)
	}
	title := "perfuncted-flatpak-positive-app"
	target := exec.Command("stdbuf", "-oL", "xev", "-name", title, "-geometry", "280x160+80+80", "-event", "mouse", "-event", "keyboard")
	target.Env = h.env
	target.Stdout = events
	target.Stderr = events
	if err := target.Start(); err != nil {
		t.Fatalf("start isolated X11 event target: %v", err)
	}
	t.Cleanup(func() { _ = target.Process.Kill(); _ = target.Wait() })
	var id string
	if !pollX11Program(8*time.Second, func() bool {
		cmd := exec.Command("xdotool", "search", "--onlyvisible", "--name", title)
		cmd.Env = h.env
		out, err := cmd.CombinedOutput()
		if err == nil && len(strings.Fields(string(out))) > 0 {
			id = strings.Fields(string(out))[0]
			return true
		}
		return false
	}) {
		t.Fatalf("X11 event target never became visible (DISPLAY=%s)", getEnv(h.env, "DISPLAY"))
	}
	// xdotool sees a newly mapped X11 window before Openbox necessarily
	// publishes it in the EWMH _NET_CLIENT_LIST. The Flatpak uses that
	// authoritative managed-window list, not xdotool's full X11 tree.
	// Wait for the window manager first, then the installed Flatpak. An
	// empty successful list on the first tick is not a product failure.
	windowID, parseErr := strconv.ParseUint(id, 10, 32)
	if parseErr != nil {
		t.Fatalf("invalid real X11 target ID %q: %v", id, parseErr)
	}
	var ewmhClients string
	if !pollX11Program(12*time.Second, func() bool {
		cmd := exec.Command("xprop", "-root", "_NET_CLIENT_LIST")
		cmd.Env = h.env
		out, commandErr := cmd.CombinedOutput()
		ewmhClients = string(out)
		return commandErr == nil && x11ClientListContains(ewmhClients, uint32(windowID))
	}) {
		wmOutput, _ := os.ReadFile(wmLog.Name())
		targetOutput, _ := os.ReadFile(logPath)
		t.Fatalf("X11 target %q (XID=0x%x) mapped but Openbox never registered it in _NET_CLIENT_LIST; clients=%q wm=%q target=%q",
			title, windowID, ewmhClients, wmOutput, targetOutput)
	}
	var list, stderr string
	var code int
	if !pollX11Program(12*time.Second, func() bool {
		list, stderr, code = h.run(t, sourceRoot, "run", "--socket=x11", appID, "window", "list")
		return code == 0 && strings.Contains(list, title)
	}) {
		wmOutput, _ := os.ReadFile(wmLog.Name())
		t.Fatalf("installed Flatpak could not discover managed X11 window %q (XID=0x%x); EWMH clients=%q; code=%d list=%q stderr=%q wm=%q",
			title, windowID, ewmhClients, code, list, stderr, wmOutput)
	}
	active, stderr, code := h.run(t, sourceRoot, "run", "--socket=x11", appID, "window", "activate", title, "--verify")
	if code != 0 || !strings.Contains(active, "verified") {
		t.Fatalf("packaged Flatpak did not activate real X11 window: code=%d out=%q err=%q", code, active, stderr)
	}
	geom := exec.Command("xdotool", "getwindowgeometry", "--shell", id)
	geom.Env = h.env
	layout, err := geom.CombinedOutput()
	if err != nil {
		t.Fatalf("real X11 target geometry: %v: %s", err, layout)
	}
	pos := map[string]int{}
	for _, line := range strings.Split(string(layout), "\n") {
		tokens := strings.SplitN(line, "=", 2)
		if len(tokens) != 2 {
			continue
		}
		if n, parseErr := strconv.Atoi(strings.TrimSpace(tokens[1])); parseErr == nil {
			pos[tokens[0]] = n
		}
	}
	x, y := pos["X"]+pos["WIDTH"]/2, pos["Y"]+pos["HEIGHT"]/2
	if pos["WIDTH"] < 30 || pos["HEIGHT"] < 30 {
		t.Fatalf("invalid X11 target size: %s", layout)
	}
	out, stderr, code := h.run(t, sourceRoot, "run", "--socket=x11", appID, "input", "click", fmt.Sprintf("--x=%d", x), fmt.Sprintf("--y=%d", y))
	if code != 0 {
		t.Fatalf("packaged Flatpak did not deliver X11 click: %d %q %q", code, out, stderr)
	}
	out, stderr, code = h.run(t, sourceRoot, "run", "--socket=x11", appID, "input", "type", "p")
	if code != 0 {
		t.Fatalf("packaged Flatpak did not type into X11 app: %d %q %q", code, out, stderr)
	}
	if !pollX11Program(5*time.Second, func() bool {
		raw, readErr := os.ReadFile(logPath)
		return readErr == nil && bytes.Contains(raw, []byte("ButtonPress event")) && bytes.Contains(raw, []byte("KeyPress event"))
	}) {
		raw, _ := os.ReadFile(logPath)
		t.Fatalf("real X11 application did not receive click AND typing; events=%s", raw)
	}
	screenshot, stderr, code := h.run(t, sourceRoot, "run", "--socket=x11", appID,
		"screen", "grab", "--rect", fmt.Sprintf("%d,%d,%d,%d", x-16, y-12, x+16, y+12), "--out", "-")
	if code != 0 {
		t.Fatalf("real X11 app screenshot failed: %s", stderr)
	}
	img, err := png.DecodeConfig(bytes.NewReader([]byte(screenshot)))
	if err != nil || img.Width != 32 || img.Height != 24 {
		t.Fatalf("real X11 screenshot malformed: %+v %v", img, err)
	}
	t.Logf("installed Flatpak discovered, focused, clicked, typed into, and captured a real X11 GUI app")
}

func pollX11Program(timeout time.Duration, condition func() bool) bool {
	for deadline := time.Now().Add(timeout); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if condition() {
			return true
		}
	}
	return false
}

func getEnv(env []string, key string) string {
	for _, entry := range env {
		if strings.HasPrefix(entry, key+"=") {
			return strings.TrimPrefix(entry, key+"=")
		}
	}
	return ""
}

// xprop formats WINDOW IDs as hexadecimal tokens separated by commas. Check
// exact tokens: substring matching could confuse e.g. 0x32 with 0x320.
func x11ClientListContains(property string, target uint32) bool {
	parts := strings.SplitN(property, "#", 2)
	if len(parts) != 2 {
		return false
	}
	for _, field := range strings.Split(parts[1], ",") {
		value, err := strconv.ParseUint(strings.TrimSpace(field), 0, 32)
		if err == nil && uint32(value) == target {
			return true
		}
	}
	return false
}
