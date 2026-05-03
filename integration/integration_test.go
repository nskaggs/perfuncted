//go:build integration
// +build integration

package integration_test

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/find"
	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/internal/env"
	"github.com/nskaggs/perfuncted/internal/executil"
	"github.com/nskaggs/perfuncted/screen"
	"github.com/nskaggs/perfuncted/window"
)

type displayMode string

const (
	displayHeadlessX11     displayMode = "headless-x11"
	displayNestedX11       displayMode = "nested-x11"
	displayHeadlessWayland displayMode = "headless-wayland"
	displayNestedWayland   displayMode = "nested-wayland"
)

type appSpec struct {
	name      string
	launch    []string
	winMatch  string
	saveFile  string
	extraEnv  []string
	isBrowser bool
}

type suite struct {
	mode displayMode
	rt   env.Runtime
	pf   *perfuncted.Perfuncted

	session *perfuncted.Session
	xvfb    *exec.Cmd
	openbox *exec.Cmd
}

var currentSuite *suite

func TestMain(m *testing.M) {
	mode, err := parseDisplayMode(strings.ToLower(os.Getenv("PF_TEST_DISPLAY_SERVER")))
	if err != nil {
		fmt.Fprintf(os.Stderr, "integration setup failed: %v\n", err)
		os.Exit(1)
	}

	s, err := newSuite(mode)
	if err != nil {
		fmt.Fprintf(os.Stderr, "integration setup failed: %v\n", err)
		os.Exit(1)
	}
	currentSuite = s

	code := m.Run()
	if err := s.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "integration cleanup failed: %v\n", err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

func parseDisplayMode(raw string) (displayMode, error) {
	switch raw {
	case "", "wayland", "headless-wayland":
		return displayHeadlessWayland, nil
	case "nested-wayland":
		return displayNestedWayland, nil
	case "x11", "headless-x11":
		return displayHeadlessX11, nil
	case "nested-x11":
		return displayNestedX11, nil
	case "nested":
		return displayNestedWayland, nil
	default:
		return "", fmt.Errorf("unknown PF_TEST_DISPLAY_SERVER=%q", raw)
	}
}

func newSuite(mode displayMode) (*suite, error) {
	s := &suite{mode: mode}
	switch mode {
	case displayHeadlessX11:
		display, xvfb, openbox, err := startX11Session()
		if err != nil {
			return nil, err
		}
		s.xvfb = xvfb
		s.openbox = openbox
		s.rt = configureX11Runtime(display)
	case displayNestedX11:
		display := os.Getenv("DISPLAY")
		if display == "" {
			return nil, fmt.Errorf("nested x11 requires DISPLAY to be set")
		}
		s.rt = configureX11Runtime(display)
	case displayHeadlessWayland:
		sess, err := perfuncted.StartSession(perfuncted.SessionConfig{Resolution: image.Pt(1024, 768)})
		if err != nil {
			return nil, err
		}
		s.session = sess
		s.rt = configureWaylandRuntime(sess)
	case displayNestedWayland:
		sess, err := perfuncted.StartNestedSession(perfuncted.SessionConfig{Resolution: image.Pt(1024, 768)})
		if err != nil {
			return nil, err
		}
		s.session = sess
		s.rt = configureWaylandRuntime(sess)
	default:
		return nil, fmt.Errorf("unknown PF_TEST_DISPLAY_SERVER=%q", mode)
	}

	pf, err := perfuncted.New(perfuncted.Options{MaxX: 1024, MaxY: 768})
	if err != nil {
		_ = s.Close()
		return nil, err
	}
	s.pf = pf
	return s, nil
}

func configureX11Runtime(display string) env.Runtime {
	os.Setenv("DISPLAY", display)
	os.Unsetenv("WAYLAND_DISPLAY")
	os.Unsetenv("XDG_RUNTIME_DIR")
	os.Unsetenv("DBUS_SESSION_BUS_ADDRESS")
	os.Unsetenv("SWAYSOCK")
	os.Setenv("GDK_BACKEND", "x11")
	os.Setenv("QT_QPA_PLATFORM", "xcb")
	return env.Current().Without(
		"WAYLAND_DISPLAY",
		"XDG_RUNTIME_DIR",
		"DBUS_SESSION_BUS_ADDRESS",
		"SWAYSOCK",
		"HYPRLAND_INSTANCE_SIGNATURE",
	).With("DISPLAY", display).With("GDK_BACKEND", "x11").With("QT_QPA_PLATFORM", "xcb")
}

func configureWaylandRuntime(sess *perfuncted.Session) env.Runtime {
	os.Setenv("XDG_RUNTIME_DIR", sess.XDGRuntimeDir())
	os.Setenv("WAYLAND_DISPLAY", sess.WaylandDisplay())
	os.Setenv("DBUS_SESSION_BUS_ADDRESS", sess.DBusAddress())
	os.Unsetenv("DISPLAY")
	return env.Current().WithSession(sess.XDGRuntimeDir(), sess.WaylandDisplay(), sess.DBusAddress())
}

func (s *suite) Close() error {
	var errs []error
	if s.pf != nil {
		errs = append(errs, s.pf.Close())
	}
	if s.session != nil {
		s.session.Stop()
	}
	if s.openbox != nil && s.openbox.Process != nil {
		_ = s.openbox.Process.Kill()
		_ = s.openbox.Wait()
	}
	if s.xvfb != nil && s.xvfb.Process != nil {
		_ = s.xvfb.Process.Kill()
		_ = s.xvfb.Wait()
	}
	return joinErrors(errs...)
}

func joinErrors(errs ...error) error {
	var out []error
	for _, err := range errs {
		if err != nil {
			out = append(out, err)
		}
	}
	switch len(out) {
	case 0:
		return nil
	case 1:
		return out[0]
	default:
		return errors.Join(out...)
	}
}

func startX11Session() (display string, xvfb *exec.Cmd, openbox *exec.Cmd, err error) {
	if _, err := executil.LookPath("Xvfb"); err != nil {
		return "", nil, nil, fmt.Errorf("x11 integration requires Xvfb: %w", err)
	}
	if _, err := executil.LookPath("openbox"); err != nil {
		return "", nil, nil, fmt.Errorf("x11 integration requires openbox: %w", err)
	}

	const dispNum = 99
	display = fmt.Sprintf(":%d", dispNum)
	xvfb = exec.Command("Xvfb", display, "-screen", "0", "1024x768x24")
	if err := xvfb.Start(); err != nil {
		return "", nil, nil, fmt.Errorf("start Xvfb: %w", err)
	}

	lockFile := fmt.Sprintf("/tmp/.X%d-lock", dispNum)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, statErr := os.Stat(lockFile); statErr == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if _, statErr := os.Stat(lockFile); statErr != nil {
		_ = xvfb.Process.Kill()
		_ = xvfb.Wait()
		return "", nil, nil, fmt.Errorf("Xvfb did not start within 10s (lock %s not found)", lockFile)
	}

	openbox = exec.Command("openbox")
	openbox.Env = append(os.Environ(), "DISPLAY="+display)
	if err := openbox.Start(); err != nil {
		_ = xvfb.Process.Kill()
		_ = xvfb.Wait()
		return "", nil, nil, fmt.Errorf("start openbox: %w", err)
	}
	time.Sleep(600 * time.Millisecond)
	return display, xvfb, openbox, nil
}

func TestIntegration(t *testing.T) {
	s := mustSuite(t)
	t.Run(string(s.mode), func(t *testing.T) {
		t.Run("probe", func(t *testing.T) { runProbe(t, s) })
		t.Run("screen", func(t *testing.T) { runScreenSmoke(t, s) })
		for _, app := range requiredApps(t) {
			app := app
			if app.isBrowser {
				t.Run(app.name, func(t *testing.T) { runBrowserScenario(t, s, app) })
			} else {
				t.Run(app.name, func(t *testing.T) { runEditorScenario(t, s, app) })
			}
		}
	})
}

func TestSessionLifecycle(t *testing.T) {
	sess, err := perfuncted.StartSession(perfuncted.SessionConfig{Resolution: image.Pt(1024, 768)})
	if err != nil {
		t.Fatalf("session lifecycle requires sway/dbus/wl-paste: %v", err)
	}
	t.Cleanup(sess.Stop)

	rt := env.Current().WithSession(sess.XDGRuntimeDir(), sess.WaylandDisplay(), sess.DBusAddress())
	t.Setenv("XDG_RUNTIME_DIR", sess.XDGRuntimeDir())
	t.Setenv("WAYLAND_DISPLAY", sess.WaylandDisplay())
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", sess.DBusAddress())
	t.Setenv("DISPLAY", "")

	pf, err := perfuncted.New(perfuncted.Options{MaxX: 1024, MaxY: 768})
	if err != nil {
		t.Fatalf("perfuncted.New: %v", err)
	}
	t.Cleanup(func() { _ = pf.Close() })

	apps := requiredApps(t)
	if len(apps) == 0 {
		t.Fatal("no supported apps found in PATH")
	}
	app := apps[0]
	for _, candidate := range apps {
		if candidate.saveFile != "" {
			app = candidate
			break
		}
	}

	cmd, err := launchApp(rt, sess, app, app.extraEnvFor(displayHeadlessWayland)...)
	if err != nil {
		t.Fatalf("launch %s: %v", app.name, err)
	}
	t.Cleanup(func() {
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
	})

	if _, err := waitForWindow(pf, app.winMatch, 30*time.Second); err != nil {
		t.Fatalf("wait for %s window: %v", app.name, err)
	}
	if err := pf.Window.Activate(app.winMatch); err != nil {
		t.Fatalf("activate %s: %v", app.name, err)
	}
	if err := pf.Input.Type("session test"); err != nil {
		t.Fatalf("type: %v", err)
	}
	if err := pf.Input.PressCombo("ctrl+s"); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := pf.Window.CloseWindow(app.winMatch); err != nil {
		t.Fatalf("close window: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := pf.Window.WaitForClose(ctx, app.winMatch, 200*time.Millisecond); err != nil {
		t.Fatalf("wait for close: %v", err)
	}
}

func mustSuite(t *testing.T) *suite {
	t.Helper()
	if currentSuite == nil {
		t.Fatal("integration suite was not initialized")
	}
	return currentSuite
}

func runProbe(t *testing.T, s *suite) {
	t.Helper()
	if got := compositorKind(s.rt); got == "" {
		t.Fatal("expected compositor detection to produce a value")
	}
	if len(screen.ProbeRuntime(s.rt)) == 0 {
		t.Fatal("screen probe returned no results")
	}
	if len(input.ProbeRuntime(s.rt)) == 0 {
		t.Fatal("input probe returned no results")
	}
	if len(window.ProbeRuntime(s.rt)) == 0 {
		t.Fatal("window probe returned no results")
	}
}

func compositorKind(rt env.Runtime) string {
	if rt.Get("WAYLAND_DISPLAY") != "" {
		return "wayland"
	}
	if rt.Display() != "" {
		return "x11"
	}
	return ""
}

func runScreenSmoke(t *testing.T, s *suite) {
	t.Helper()
	w, h, err := s.pf.Screen.Resolution()
	if err != nil {
		t.Fatalf("resolution: %v", err)
	}
	if w <= 0 || h <= 0 {
		t.Fatalf("resolution returned invalid size %dx%d", w, h)
	}

	rect := image.Rect(0, 0, min(w, 200), min(h, 200))
	img, err := s.pf.Screen.Grab(rect)
	if err != nil {
		t.Fatalf("grab: %v", err)
	}
	if _, err := s.pf.Screen.GrabHash(rect); err != nil {
		t.Fatalf("grab hash: %v", err)
	}
	if _, err := s.pf.Screen.GrabFullHash(); err != nil {
		t.Fatalf("grab full hash: %v", err)
	}

	tmp := filepath.Join(t.TempDir(), "capture.png")
	if err := s.pf.Screen.CaptureRegion(rect, tmp); err != nil {
		t.Fatalf("capture region: %v", err)
	}
	if _, err := os.Stat(tmp); err != nil {
		t.Fatalf("capture region output missing: %v", err)
	}
	if _, err := s.pf.Screen.GetPixel(rect.Min.X, rect.Min.Y); err != nil {
		t.Fatalf("get pixel: %v", err)
	}
	if _, err := s.pf.Screen.GetMultiplePixels([]image.Point{{rect.Min.X, rect.Min.Y}}); err != nil {
		t.Fatalf("get multiple pixels: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := s.pf.Screen.WaitForFnContext(ctx, rect, func(i image.Image) bool { return i != nil }, 100*time.Millisecond); err != nil {
		t.Fatalf("wait for fn: %v", err)
	}

	pt, err := s.pf.Screen.FindColor(rect, colorAt(img, rect.Min.X, rect.Min.Y), 0)
	if err != nil {
		t.Fatalf("find color: %v", err)
	}
	if !pt.In(rect) {
		t.Fatalf("find color returned point outside rect: %v", pt)
	}

	refRect := image.Rect(rect.Min.X, rect.Min.Y, min(rect.Min.X+20, rect.Max.X), min(rect.Min.Y+20, rect.Max.Y))
	ref, err := s.pf.Screen.Grab(refRect)
	if err == nil {
		if _, _, err := s.pf.Screen.WaitWithTolerance(refRect, ref, 0, 100*time.Millisecond); err != nil {
			t.Fatalf("wait with tolerance: %v", err)
		}
	}
}

func runEditorScenario(t *testing.T, s *suite, app appSpec) {
	t.Helper()
	saveFile := app.saveFile
	if saveFile == "" {
		saveFile = filepath.Join(t.TempDir(), app.name+".txt")
	}
	if err := os.WriteFile(saveFile, nil, 0o644); err != nil {
		t.Fatalf("create %s: %v", saveFile, err)
	}

	cmd, err := launchApp(s.rt, s.session, app, app.extraEnvFor(s.mode)...)
	if err != nil {
		t.Fatalf("launch %s: %v", app.name, err)
	}
	t.Cleanup(func() {
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
		_ = os.Remove(saveFile)
	})

	if _, err := waitForWindow(s.pf, app.winMatch, 60*time.Second); err != nil {
		t.Fatalf("wait for %s window: %v", app.name, err)
	}

	if err := s.pf.Window.Activate(app.winMatch); err != nil {
		t.Fatalf("activate %s: %v", app.name, err)
	}
	active, err := s.pf.Window.ActiveTitle()
	if err != nil {
		t.Fatalf("active title: %v", err)
	}
	if !strings.Contains(strings.ToLower(active), strings.ToLower(app.winMatch)) {
		t.Fatalf("active title %q does not match %q", active, app.winMatch)
	}

	rect, err := s.pf.Window.GetGeometry(app.winMatch)
	if err != nil {
		t.Fatalf("get geometry: %v", err)
	}
	if rect.Empty() {
		t.Fatal("geometry returned empty rect")
	}

	if err := s.pf.Input.ClickCenter(rect); err != nil {
		t.Fatalf("click center: %v", err)
	}
	if err := s.pf.Input.MouseMove(rect.Min.X+20, rect.Min.Y+20); err != nil {
		t.Fatalf("mouse move: %v", err)
	}
	if err := s.pf.Input.MouseClick(rect.Min.X+20, rect.Min.Y+20, 1); err != nil {
		t.Fatalf("mouse click: %v", err)
	}
	time.Sleep(700 * time.Millisecond)
	clickX := rect.Min.X + 40
	clickY := rect.Min.Y + 120
	if err := s.pf.Input.MouseMove(clickX, clickY); err != nil {
		t.Fatalf("mouse move document area: %v", err)
	}
	if err := s.pf.Input.MouseClick(clickX, clickY, 1); err != nil {
		t.Fatalf("mouse click document area: %v", err)
	}
	time.Sleep(700 * time.Millisecond)
	if err := s.pf.Input.TypeWithDelay("Integration", 20*time.Millisecond); err != nil {
		t.Fatalf("type with delay: %v", err)
	}

	img, err := s.pf.Screen.Grab(rect)
	if err != nil {
		t.Fatalf("grab window: %v", err)
	}
	if _, err := s.pf.Screen.GrabHash(rect); err != nil {
		t.Fatalf("grab hash: %v", err)
	}
	if _, err := s.pf.Screen.GetMultiplePixels([]image.Point{{rect.Min.X, rect.Min.Y}, {rect.Min.X + 1, rect.Min.Y + 1}}); err != nil {
		t.Fatalf("get multiple pixels: %v", err)
	}
	if _, err := s.pf.Screen.GetPixel(rect.Min.X, rect.Min.Y); err != nil {
		t.Fatalf("get pixel: %v", err)
	}
	if _, err := s.pf.Screen.LocateExact(rect, img); err != nil {
		t.Fatalf("locate exact: %v", err)
	}
	if _, err := s.pf.Screen.ScanFor([]image.Rectangle{rect}, []uint32{find.PixelHash(img, nil)}, 100*time.Millisecond); err != nil {
		t.Fatalf("scan for: %v", err)
	}

	marker := "pf-" + app.name + "-integration"
	if err := s.pf.Clipboard.Set(marker); err != nil {
		t.Fatalf("clipboard set: %v", err)
	}
	if got, err := s.pf.Clipboard.Get(); err != nil || got != marker {
		t.Fatalf("clipboard get = %q, %v", got, err)
	}
	if err := s.pf.Paste(marker); err != nil {
		t.Fatalf("paste: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() {
		time.Sleep(1 * time.Second)
		if err := s.pf.Input.PressCombo("alt+f"); err != nil {
			return
		}
		time.Sleep(200 * time.Millisecond)
		_ = s.pf.Input.KeyTap("escape")
	}()
	if _, err := s.pf.Screen.WaitForVisibleChangeContext(ctx, rect, 100*time.Millisecond, 2); err != nil {
		t.Fatalf("wait for visible change: %v", err)
	}

	if err := s.pf.Input.PressCombo("ctrl+s"); err != nil {
		t.Fatalf("ctrl+s: %v", err)
	}
	time.Sleep(1500 * time.Millisecond)
	content, err := os.ReadFile(saveFile)
	if err != nil {
		t.Fatalf("read saved file: %v", err)
	}
	if !strings.Contains(string(content), "Integration") {
		t.Fatalf("saved file %q does not contain typed text", saveFile)
	}

	if err := s.pf.Window.Resize(app.winMatch, 800, 600); err != nil {
		t.Fatalf("resize: %v", err)
	}
	if err := s.pf.Window.CloseWindow(app.winMatch); err != nil {
		t.Fatalf("close window: %v", err)
	}
	ctxClose, cancelClose := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelClose()
	if err := s.pf.Window.WaitForClose(ctxClose, app.winMatch, 200*time.Millisecond); err != nil {
		t.Fatalf("wait for close: %v", err)
	}
}

func runBrowserScenario(t *testing.T, s *suite, app appSpec) {
	t.Helper()
	cmd, err := launchApp(s.rt, s.session, app, app.extraEnvFor(s.mode)...)
	if err != nil {
		t.Fatalf("launch %s: %v", app.name, err)
	}
	t.Cleanup(func() {
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
	})

	if _, err := waitForWindow(s.pf, app.winMatch, 90*time.Second); err != nil {
		t.Fatalf("wait for browser window: %v", err)
	}
	if err := s.pf.Window.Activate(app.winMatch); err != nil {
		t.Fatalf("activate browser: %v", err)
	}

	if err := s.pf.Input.PressCombo("ctrl+l"); err != nil {
		t.Fatalf("ctrl+l: %v", err)
	}
	if err := s.pf.Input.TypeWithDelay("about:support", 10*time.Millisecond); err != nil {
		t.Fatalf("type address: %v", err)
	}
	if err := s.pf.Input.KeyTap("return"); err != nil {
		t.Fatalf("return: %v", err)
	}

	rect := image.Rect(0, 0, 100, 100)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := s.pf.Screen.WaitForStableContext(ctx, rect, 3, 500*time.Millisecond); err != nil {
		t.Fatalf("wait for stable: %v", err)
	}
}

func launchApp(rt env.Runtime, sess *perfuncted.Session, app appSpec, extraEnv ...string) (*exec.Cmd, error) {
	args := append([]string{}, app.launch[1:]...)
	if app.saveFile != "" && !app.isBrowser {
		args = append(args, app.saveFile)
	}
	if app.name == "firefox" {
		profileDir, err := os.MkdirTemp("", "perfuncted-firefox-profile-")
		if err != nil {
			return nil, err
		}
		args = append(args, "--profile", profileDir)
		extraEnv = append(extraEnv, "MOZ_DISABLE_CONTENT_SANDBOX=1")
		if sess != nil {
			extraEnv = append(extraEnv, "MOZ_ENABLE_WAYLAND=1")
		}
	}

	if sess != nil {
		return sess.LaunchEnv(extraEnv, app.launch[0], args...)
	}
	cmd := exec.Command(app.launch[0], args...)
	cmd.Env = env.Merge(rt.EnvList(), extraEnv...)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

func waitForWindow(pf *perfuncted.Perfuncted, pattern string, timeout time.Duration) (window.Info, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return pf.Window.WaitFor(ctx, pattern, 500*time.Millisecond)
}

func requiredApps(t *testing.T) []appSpec {
	t.Helper()
	pfx := os.Getenv("PF_TEST_PREFIX")
	if pfx == "" {
		pfx = "integration"
	}
	all := []appSpec{
		{
			name:     "kwrite",
			launch:   []string{"kwrite"},
			winMatch: "kwrite",
			saveFile: filepath.Join(os.TempDir(), pfx+"-kwrite.txt"),
		},
		{
			name:     "pluma",
			launch:   []string{"dbus-run-session", "pluma"},
			winMatch: "pluma",
			saveFile: filepath.Join(os.TempDir(), pfx+"-pluma.txt"),
			extraEnv: []string{"GTK_USE_PORTAL=0"},
		},
		{
			name:      "firefox",
			launch:    []string{"firefox", "--no-remote", "--new-instance", "about:blank"},
			winMatch:  "firefox",
			isBrowser: true,
		},
	}

	for _, app := range all {
		candidates := []string{app.launch[0]}
		if len(app.launch) > 1 && app.launch[0] == "dbus-run-session" {
			candidates = append(candidates, app.launch[1])
		}
		for _, candidate := range candidates {
			if _, err := executil.LookPath(candidate); err != nil {
				t.Fatalf("%s integration requires %s in PATH: %v", app.name, candidate, err)
			}
		}
	}
	return all
}

func (a appSpec) extraEnvFor(mode displayMode) []string {
	envs := append([]string{}, a.extraEnv...)
	switch a.name {
	case "firefox":
		envs = append(envs, "MOZ_DISABLE_CONTENT_SANDBOX=1")
		if mode == displayHeadlessWayland || mode == displayNestedWayland {
			envs = append(envs, "MOZ_ENABLE_WAYLAND=1")
		}
	}
	if mode == displayHeadlessX11 || mode == displayNestedX11 {
		envs = append(envs, "GDK_BACKEND=x11", "QT_QPA_PLATFORM=xcb")
	}
	return envs
}

func colorAt(img image.Image, x, y int) color.RGBA {
	return color.RGBAModel.Convert(img.At(x, y)).(color.RGBA)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
