//go:build integration && linux

package flatpaktest

import (
	"bytes"
	"context"
	"fmt"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/v2/internal/x11test"
)

const (
	x11WMReadyProperty  = "_PERFUNCTED_TEST_WM_READY"
	x11WMStartupCommand = "xprop -root -f " + x11WMReadyProperty + " 8s -set " + x11WMReadyProperty + " ready"
)

type x11EventTarget struct {
	title, id, logPath, wmLogPath, ewmhClients string
}

// startX11EventTarget operates only on a fresh, isolated Xvfb display owned
// by the caller. Both child processes are cleaned up before the X server.
func startX11EventTarget(t *testing.T, env []string, testRoot string) x11EventTarget {
	t.Helper()
	for _, tool := range []string{"openbox", "xev", "xdotool", "stdbuf", "xprop", "xwininfo"} {
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
	wmLogPath := filepath.Join(testRoot, "x11-window-manager.log")
	wmLog, err := os.Create(wmLogPath)
	if err != nil {
		t.Fatal(err)
	}
	defer wmLog.Close()
	// Openbox publishes _NET_SUPPORTING_WM_CHECK before it has completed
	// startup. A client mapped in that interval can remain unmapped.
	// Use Openbox's post-startup command as a readiness barrier instead
	// of treating a nonzero EWMH window as proof of readiness.
	wm := exec.Command("openbox", "--sm-disable", "--startup", x11WMStartupCommand)
	wm.Env = env
	wm.Stdout = wmLog
	wm.Stderr = wmLog
	if err := wm.Start(); err != nil {
		t.Fatalf("start isolated X11 WM: %v", err)
	}
	t.Cleanup(func() { _ = wm.Process.Kill(); _ = wm.Wait() })
	var ewmh, startup string
	if !pollX11Program(5*time.Second, func() bool {
		ewmh = x11FixtureCommand(env, "xprop", "-root", "_NET_SUPPORTING_WM_CHECK")
		startup = x11FixtureCommand(env, "xprop", "-root", x11WMReadyProperty)
		return x11WindowManagerReady(ewmh) && x11WindowManagerStartupComplete(startup)
	}) {
		log, _ := os.ReadFile(wmLogPath)
		t.Fatalf("X11 WM startup incomplete (DISPLAY=%s): EWMH=%q startup=%q wm=%q",
			getEnv(env, "DISPLAY"), ewmh, startup, log)
	}
	title := "perfuncted-flatpak-positive-app"
	target := exec.Command("stdbuf", "-oL", "xev", "-name", title, "-geometry", "280x160+80+80", "-event", "mouse", "-event", "keyboard")
	target.Env = env
	target.Stdout = events
	target.Stderr = events
	if err := target.Start(); err != nil {
		t.Fatalf("start isolated X11 event target: %v", err)
	}
	t.Cleanup(func() { _ = target.Process.Kill(); _ = target.Wait() })
	var id, searchResult string
	if !pollX11Program(8*time.Second, func() bool {
		searchResult = x11FixtureCommand(env, "xdotool", "search", "--onlyvisible", "--name", title)
		fields := strings.Fields(searchResult)
		if len(fields) == 1 {
			if value, err := strconv.ParseUint(fields[0], 10, 32); err == nil && value != 0 {
				id = fields[0]
				return true
			}
		}
		return false
	}) {
		wmOutput, _ := os.ReadFile(wmLogPath)
		targetOutput, _ := os.ReadFile(logPath)
		t.Fatalf("X11 event target never became visible (DISPLAY=%s): search=%q wm=%q target=%q clients=%q tree=%q",
			getEnv(env, "DISPLAY"), searchResult, wmOutput, targetOutput,
			x11FixtureCommand(env, "xprop", "-root", "_NET_CLIENT_LIST", "_NET_ACTIVE_WINDOW"),
			x11FixtureCommand(env, "xwininfo", "-root", "-tree"))
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
		ewmhClients = x11FixtureCommand(env, "xprop", "-root", "_NET_CLIENT_LIST")
		return x11ClientListContains(ewmhClients, uint32(windowID))
	}) {
		wmOutput, _ := os.ReadFile(wmLogPath)
		targetOutput, _ := os.ReadFile(logPath)
		t.Fatalf("X11 target %q (XID=0x%x) mapped but Openbox never registered it in _NET_CLIENT_LIST; clients=%q wm=%q target=%q",
			title, windowID, ewmhClients, wmOutput, targetOutput)
	}
	return x11EventTarget{title: title, id: id, logPath: logPath, wmLogPath: wmLogPath, ewmhClients: ewmhClients}
}

// Unlike synthetic input success codes, xev records real X events delivered
// to a distinct GUI process running on the same Xvfb desktop as the bundle.
func testInstalledFlatpakRealX11Application(t *testing.T, h *harness, sourceRoot, testRoot string) {
	t.Helper()
	target := startX11EventTarget(t, h.env, testRoot)
	title, id, logPath := target.title, target.id, target.logPath
	windowID, _ := strconv.ParseUint(id, 10, 32) // validated by startX11EventTarget
	var list, stderr string
	var code int
	if !pollX11Program(12*time.Second, func() bool {
		list, stderr, code = h.run(t, sourceRoot, "run", "--socket=x11", appID, "window", "list")
		return code == 0 && strings.Contains(list, title)
	}) {
		wmOutput, _ := os.ReadFile(target.wmLogPath)
		t.Fatalf("installed Flatpak could not discover managed X11 window %q (XID=0x%x); EWMH clients=%q; code=%d list=%q stderr=%q wm=%q",
			title, windowID, target.ewmhClients, code, list, stderr, wmOutput)
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

// Bound individual host diagnostic/readiness commands too: a wedged X
// connection must not make an otherwise bounded readiness poll hang.
func x11FixtureCommand(env []string, program string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, program, args...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Sprintf("%s: %v; %s", program, err, out)
	}
	return string(out)
}

func x11WindowManagerStartupComplete(property string) bool {
	return strings.TrimSpace(property) == x11WMReadyProperty+"(STRING) = \"ready\""
}

// Run the same host fixture independently of Flatpak build/install so startup
// races can be reproduced quickly against fresh isolated X servers.
func TestX11RealApplicationFixture(t *testing.T) {
	display, stop, err := x11test.StartXvfbNoReset()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	root := t.TempDir()
	env := setEnv(os.Environ(), "DISPLAY", display)
	env = setEnv(env, "XDG_SESSION_TYPE", "x11")
	env = setEnv(env, "HOME", root)
	env = setEnv(env, "XDG_CONFIG_HOME", filepath.Join(root, "config"))
	target := startX11EventTarget(t, env, root)
	if got := x11FixtureCommand(env, "xdotool", "getwindowname", target.id); strings.TrimSpace(got) != target.title {
		t.Fatalf("unexpected managed target title: %q", got)
	}
}

func TestX11WindowManagerStartupRequiresHandshake(t *testing.T) {
	for _, tc := range []struct {
		property string
		want     bool
	}{
		{"_NET_SUPPORTING_WM_CHECK(WINDOW): window id # 0x200001", false},
		{x11WMReadyProperty + ":  no such atom on any window.", false},
		{x11WMReadyProperty + "(STRING) = \"not-ready\"", false},
		{x11WMReadyProperty + "(STRING) = \"ready\"\n", true},
	} {
		if got := x11WindowManagerStartupComplete(tc.property); got != tc.want {
			t.Errorf("%q: startup=%t want %t", tc.property, got, tc.want)
		}
	}
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

// xprop exits successfully even for an absent property. Accept only a
// nonzero EWMH WINDOW reference, never its diagnostic or the root itself.
func x11WindowManagerReady(property string) bool {
	const prefix = "_NET_SUPPORTING_WM_CHECK(WINDOW): window id # "
	rest, ok := strings.CutPrefix(strings.TrimSpace(property), prefix)
	if !ok {
		return false
	}
	id, err := strconv.ParseUint(strings.TrimSpace(rest), 0, 32)
	return err == nil && id != 0
}

func TestX11WindowManagerReadinessRequiresNonzeroWindow(t *testing.T) {
	for _, tc := range []struct {
		property string
		want     bool
	}{
		{"_NET_SUPPORTING_WM_CHECK:  no such atom on any window.\n", false},
		{"_NET_SUPPORTING_WM_CHECK(WINDOW): window id # 0x0\n", false},
		{"_NET_SUPPORTING_WM_CHECK(WINDOW): window id # 0x200001\n", true},
		{"_NET_SUPPORTING_WM_CHECK(WINDOW): window id # 2097153\n", true},
		{"_NET_SUPPORTING_WM_CHECK(WINDOW): window id # junk\n", false},
	} {
		if got := x11WindowManagerReady(tc.property); got != tc.want {
			t.Errorf("%q: readiness=%t want %t", tc.property, got, tc.want)
		}
	}
}
