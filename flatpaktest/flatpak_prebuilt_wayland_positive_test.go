//go:build integration && linux

package flatpaktest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

type swayAcceptedNode struct {
	AppID   string `json:"app_id"`
	Focused bool   `json:"focused"`
	Rect    struct {
		X, Y, Width, Height int
	} `json:"rect"`
	Nodes         []swayAcceptedNode `json:"nodes"`
	FloatingNodes []swayAcceptedNode `json:"floating_nodes"`
}

func swayFindAcceptedNode(root swayAcceptedNode, appID string) (swayAcceptedNode, bool) {
	if root.AppID == appID {
		return root, true
	}
	for _, n := range root.Nodes {
		if found, ok := swayFindAcceptedNode(n, appID); ok {
			return found, true
		}
	}
	for _, n := range root.FloatingNodes {
		if found, ok := swayFindAcceptedNode(n, appID); ok {
			return found, true
		}
	}
	return swayAcceptedNode{}, false
}

func swayAcceptedCommand(t *testing.T, env []string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "swaymsg", args...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("swaymsg %q: %v: %s", args, err, out)
	}
	return string(out)
}

func swayAwaitAcceptedFocus(t *testing.T, env []string, appID string) swayAcceptedNode {
	t.Helper()
	var last swayAcceptedNode
	if !pollX11Program(12*time.Second, func() bool {
		raw := swayAcceptedCommand(t, env, "-t", "get_tree")
		var tree swayAcceptedNode
		if err := json.Unmarshal([]byte(raw), &tree); err != nil {
			t.Fatalf("decode real compositor tree: %v", err)
		}
		n, ok := swayFindAcceptedNode(tree, appID)
		last = n
		return ok && n.Focused && n.Rect.Width > 50 && n.Rect.Height > 50
	}) {
		t.Fatalf("Sway did not focus actual Wayland application %q; last node=%+v", appID, last)
	}
	return last
}

// This opt-in test installs a compiled and exported Flatpak, starts a real
// isolated Sway compositor and verifies the installed package's info/status,
// session diagnostics and actual interaction outcome. Flatpak's Wayland proxy
// can filter wlroots privileged input even when the native CLI can use it:
// unsupported input must fail explicitly, without focus changes or key events.
// If the installed sandbox really has input capability, test successful
// pointer focus and keyboard effects instead. No portal consent is bypassed.
func TestFlatpakPrebuiltWlrootsCapabilityAndEffects(t *testing.T) {
	if os.Getenv("PF_FLATPAK_WLROOTS_TEST") != "1" {
		t.Skip("opt in using PF_FLATPAK_WLROOTS_TEST=1 with PF_FLATPAK_TEST_BUNDLE")
	}
	bundle := os.Getenv("PF_FLATPAK_TEST_BUNDLE")
	if bundle == "" {
		t.Fatal("real Wayland input requires a previously validated Flatpak bundle")
	}
	if info, err := os.Stat(bundle); err != nil || info.Size() == 0 {
		t.Fatalf("real Flatpak artifact unavailable: %v", err)
	}
	for _, name := range []string{"sway", "swaymsg", "foot"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Fatalf("real native Wayland compositor requires %s: %v", name, err)
		}
	}
	repo := mustRepoRoot(t)
	root, err := os.MkdirTemp("/var/tmp", "pf-wl-flatpak-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	// Short socket paths are required by the Linux AF_UNIX path limit.
	runRoot, err := os.MkdirTemp("/tmp", "pf-wl-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runRoot) })
	if err := os.Chmod(runRoot, 0700); err != nil {
		t.Fatal(err)
	}

	dbusHome := filepath.Join(root, "bus-home")
	if err := mustWriteFlatpakServiceFile(dbusHome); err != nil {
		t.Fatal(err)
	}
	session := mustStartDBusSession(t, dbusHome)
	t.Cleanup(session.close)
	h := newHarness(t, repo, session.address, filepath.Join(root, "home"),
		filepath.Join(root, "tmp"), runRoot)
	h.mustRun(t, repo, "remote-add", "--if-not-exists", "--user", "flathub", flathubRemoteURL)
	h.mustRun(t, repo, "install", "-y", "--user", bundle)

	configPath := filepath.Join(root, "sway.conf")
	if err := os.WriteFile(configPath, []byte(
		"output * resolution 800x600\nseat seat0 hide_cursor 2000\nfocus_follows_mouse no\nfont pango:monospace 10\n"), 0600); err != nil {
		t.Fatal(err)
	}
	logFile, err := os.Create(filepath.Join(root, "sway.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	env := append([]string(nil), h.env...)
	for key, val := range map[string]string{
		"WLR_BACKENDS": "headless", "WLR_RENDERER": "pixman",
		"WLR_LIBINPUT_NO_DEVICES": "1", "XDG_CURRENT_DESKTOP": "sway",
		"XDG_SESSION_TYPE": "wayland",
	} {
		env = setEnv(env, key, val)
	}
	for _, key := range []string{"DISPLAY", "XAUTHORITY", "WAYLAND_DISPLAY", "SWAYSOCK"} {
		env = unsetEnv(env, key)
	}
	ctx, cancel := context.WithCancel(context.Background())
	compositor := exec.CommandContext(ctx, "sway", "--unsupported-gpu", "-c", configPath)
	compositor.Env = env
	compositor.Stdout, compositor.Stderr = logFile, logFile
	compositor.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := compositor.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- compositor.Wait() }()
	t.Cleanup(func() {
		cancel()
		_ = syscall.Kill(-compositor.Process.Pid, syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			_ = syscall.Kill(-compositor.Process.Pid, syscall.SIGKILL)
		}
	})
	var display, ipc string
	if !pollX11Program(15*time.Second, func() bool {
		for _, name := range []string{"wayland-1", "wayland-0"} {
			path := filepath.Join(runRoot, name)
			if info, statErr := os.Stat(path); statErr == nil && info.Mode()&os.ModeSocket != 0 {
				display = name
				break
			}
		}
		sockets, _ := filepath.Glob(filepath.Join(runRoot, "sway-ipc.*.sock"))
		for _, path := range sockets {
			if info, statErr := os.Stat(path); statErr == nil && info.Mode()&os.ModeSocket != 0 {
				ipc = path
				break
			}
		}
		return display != "" && ipc != ""
	}) {
		content, _ := os.ReadFile(logFile.Name())
		t.Fatalf("Sway Wayland/IPC sockets did not appear: %s", content)
	}
	env = setEnv(env, "WAYLAND_DISPLAY", display)
	env = setEnv(env, "SWAYSOCK", ipc)
	h.env = env

	// Check the actual installed package and both public status commands
	// before attempting any desktop effect. No native CLI involved.
	infoJSON, infoErr, infoCode := h.run(t, repo, "run", appID, "info", "--output=json")
	if infoCode != 0 {
		t.Fatalf("installed Flatpak info failed: %d %q", infoCode, infoErr)
	}
	statusJSON, statusErr, statusCode := h.run(t, repo, "run", appID, "status", "--output=json")
	if statusCode != 0 {
		t.Fatalf("installed Flatpak status failed: %d %q", statusCode, statusErr)
	}
	var infoReport, statusReport struct {
		Compositor   string `json:"compositor"`
		Capabilities map[string]struct {
			Supported bool   `json:"supported"`
			Backend   string `json:"backend"`
			Reason    string `json:"reason"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal([]byte(infoJSON), &infoReport); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(statusJSON), &statusReport); err != nil {
		t.Fatal(err)
	}
	if infoReport.Compositor != statusReport.Compositor ||
		!reflect.DeepEqual(infoReport.Capabilities, statusReport.Capabilities) {
		t.Fatalf("installed Flatpak status disagrees with info: info=%s status=%s", infoJSON, statusJSON)
	}
	if !strings.Contains(strings.ToLower(infoReport.Compositor), "wayland") {
		t.Fatalf("installed Flatpak status misidentified actual Wayland compositor: %s", infoJSON)
	}
	if len(infoReport.Capabilities) < 3 {
		t.Fatalf("Flatpak status omitted capability matrix: %s", infoJSON)
	}
	diagnostics, diagErr, diagCode := h.run(t, repo, "run", appID, "session", "check")
	if diagCode != 0 || !strings.Contains(diagnostics, "WAYLAND_DISPLAY") ||
		!strings.Contains(diagnostics, "Flatpak CLI Notes") {
		t.Fatalf("installed Flatpak session diagnostics invalid: code=%d out=%q err=%q", diagCode, diagnostics, diagErr)
	}
	t.Logf("installed Flatpak on native Sway: compositor=%s input=%+v", infoReport.Compositor, infoReport.Capabilities["input"])

	markerPath := filepath.Join(root, "received.txt")
	launch := func(appID, script string) {
		t.Helper()
		log, logErr := os.Create(filepath.Join(root, appID+".log"))
		if logErr != nil {
			t.Fatal(logErr)
		}
		t.Cleanup(func() { _ = log.Close() })
		terminal := exec.Command("foot", "--app-id", appID, "-e", "sh", "-c", script)
		terminal.Env = setEnv(append([]string(nil), env...), "PF_FLATPAK_WAYLAND_MARKER", markerPath)
		terminal.Stdout, terminal.Stderr = log, log
		if startErr := terminal.Start(); startErr != nil {
			t.Fatal(startErr)
		}
		t.Cleanup(func() { _ = terminal.Process.Kill(); _ = terminal.Wait() })
	}
	const decoy = "pf-flatpak-sway-decoy"
	const target = "pf-flatpak-sway-target"
	launch(decoy, "sleep 30")
	swayAwaitAcceptedFocus(t, env, decoy)
	launch(target, "read -r value; printf '%s\\n' \"$value\" > \"$PF_FLATPAK_WAYLAND_MARKER\"; sleep 15")
	targetWindow := swayAwaitAcceptedFocus(t, env, target)

	swayAcceptedCommand(t, env, "[app_id="+decoy+"]", "focus")
	swayAwaitAcceptedFocus(t, env, decoy)
	x := targetWindow.Rect.X + targetWindow.Rect.Width/2
	y := targetWindow.Rect.Y + targetWindow.Rect.Height/2
	if x < 0 || x >= 800 || y < 0 || y >= 600 {
		t.Fatalf("invalid native Wayland window geometry: %+v", targetWindow.Rect)
	}

	stdout, stderr, code := h.run(t, repo, "run", appID, "input", "click",
		fmt.Sprintf("--x=%d", x), fmt.Sprintf("--y=%d", y))
	if !infoReport.Capabilities["input"].Supported {
		if code == 0 || !strings.Contains(stderr, "no backend available for Wayland desktop") {
			t.Fatalf("installed Flatpak falsely reported unavailable Wayland input as success or without reason: code=%d out=%q err=%q", code, stdout, stderr)
		}
		// The denied sandbox must not move focus to the real native target.
		swayAwaitAcceptedFocus(t, env, decoy)
		out, typErr, typCode := h.run(t, repo, "run", appID, "input", "type", "unauthorized_marker{enter}")
		if typCode == 0 || !strings.Contains(typErr, "no backend available for Wayland desktop") {
			t.Fatalf("installed Flatpak falsely acknowledged unavailable Wayland keyboard: code=%d out=%q err=%q", typCode, out, typErr)
		}
		if contents, readErr := os.ReadFile(markerPath); readErr == nil {
			t.Fatalf("denied Wayland input still modified native GUI: %q", contents)
		} else if !os.IsNotExist(readErr) {
			t.Fatal(readErr)
		}
		t.Log("sandboxed native Wayland input correctly reported unavailable and caused no unauthorized GUI action")
		return
	}
	if code != 0 {
		t.Fatalf("installed Flatpak reported native Wayland input available but failed to click: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	swayAwaitAcceptedFocus(t, env, target)
	const marker = "flatpak_wayland_verified"
	stdout, stderr, code = h.run(t, repo, "run", appID, "input", "type", marker+"{enter}")
	if code != 0 {
		t.Fatalf("installed Flatpak did not type on Wayland: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !pollX11Program(8*time.Second, func() bool {
		text, readErr := os.ReadFile(markerPath)
		return readErr == nil && strings.TrimSpace(string(text)) == marker
	}) {
		text, _ := os.ReadFile(markerPath)
		t.Fatalf("native Wayland target did not receive exact keyboard input: %q", text)
	}
	// Sway advertises zwlr_screencopy; the released Flatpak must also
	// actually capture compositor pixels, not just claim screen capability.
	pngBytes, captureErr, captureCode := h.run(t, repo, "run", appID,
		"screen", "grab", "--rect", "0,0,64,48", "--out", "-")
	if captureCode != 0 {
		t.Fatalf("installed Flatpak could not capture native Wayland compositor: code=%d err=%q", captureCode, captureErr)
	}
	decoded, decodeErr := png.DecodeConfig(bytes.NewReader([]byte(pngBytes)))
	if decodeErr != nil || decoded.Width != 64 || decoded.Height != 48 {
		t.Fatalf("native Wayland capture was not a valid 64x48 PNG: image=%+v err=%v", decoded, decodeErr)
	}
	t.Log("Installed Flatpak clicked a previously unfocused native Wayland window, typed the exact marker, and captured native compositor pixels")
}
