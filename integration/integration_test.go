//go:build integration
// +build integration

package integration_test

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
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
	xephyr  *exec.Cmd
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

func traceConfigFromEnv() (io.Writer, time.Duration) {
	delay := 0 * time.Millisecond
	enabled := envBool(os.Getenv("PF_TRACE_ACTIONS"))
	if raw := os.Getenv("PF_TRACE_DELAY"); raw != "" {
		if parsed, err := time.ParseDuration(raw); err == nil {
			delay = parsed
			if parsed > 0 {
				enabled = true
			}
		}
	}
	if !enabled {
		return nil, 0
	}
	return os.Stderr, delay
}

func envBool(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func newSuite(mode displayMode) (*suite, error) {
	s := &suite{mode: mode}
	traceWriter, traceDelay := traceConfigFromEnv()
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
		display, xephyr, openbox, err := startNestedX11Session()
		if err != nil {
			return nil, err
		}
		s.xephyr = xephyr
		s.openbox = openbox
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

	pf, err := perfuncted.New(perfuncted.Options{
		MaxX:        1024,
		MaxY:        768,
		TraceWriter: traceWriter,
		TraceDelay:  traceDelay,
	})
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
	os.Setenv("XDG_SESSION_TYPE", "x11")
	os.Setenv("GDK_BACKEND", "x11")
	os.Setenv("QT_QPA_PLATFORM", "xcb")
	return env.Current().Without(
		"WAYLAND_DISPLAY",
		"XDG_RUNTIME_DIR",
		"DBUS_SESSION_BUS_ADDRESS",
		"SWAYSOCK",
		"HYPRLAND_INSTANCE_SIGNATURE",
	).With("DISPLAY", display).With("XDG_SESSION_TYPE", "x11").With("GDK_BACKEND", "x11").With("QT_QPA_PLATFORM", "xcb")
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
	terminateCmd(s.openbox, 2*time.Second)
	terminateCmd(s.xephyr, 2*time.Second)
	terminateCmd(s.xvfb, 2*time.Second)
	return joinErrors(errs...)
}

func terminateCmd(cmd *exec.Cmd, timeout time.Duration) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return
	case <-timer.C:
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		select {
		case <-done:
		case <-time.After(timeout):
		}
	}
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
	xvfb.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
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
	openbox.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := openbox.Start(); err != nil {
		_ = xvfb.Process.Kill()
		_ = xvfb.Wait()
		return "", nil, nil, fmt.Errorf("start openbox: %w", err)
	}
	time.Sleep(600 * time.Millisecond)
	return display, xvfb, openbox, nil
}

func startNestedX11Session() (display string, xephyr *exec.Cmd, openbox *exec.Cmd, err error) {
	if _, err := executil.LookPath("Xephyr"); err != nil {
		return "", nil, nil, fmt.Errorf("nested x11 integration requires Xephyr: %w", err)
	}
	if _, err := executil.LookPath("openbox"); err != nil {
		return "", nil, nil, fmt.Errorf("nested x11 integration requires openbox: %w", err)
	}
	if os.Getenv("DISPLAY") == "" {
		return "", nil, nil, fmt.Errorf("nested x11 requires host DISPLAY to be set")
	}

	const dispNum = 100
	display = fmt.Sprintf(":%d", dispNum)
	xephyr = exec.Command("Xephyr", display, "-screen", "1024x768", "-ac", "-br", "-reset")
	xephyr.Env = append(os.Environ(), "DISPLAY="+os.Getenv("DISPLAY"))
	xephyr.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := xephyr.Start(); err != nil {
		return "", nil, nil, fmt.Errorf("start Xephyr: %w", err)
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
		terminateCmd(xephyr, 500*time.Millisecond)
		return "", nil, nil, fmt.Errorf("Xephyr did not start within 10s (lock %s not found)", lockFile)
	}

	openbox = exec.Command("openbox")
	openbox.Env = append(os.Environ(), "DISPLAY="+display)
	openbox.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := openbox.Start(); err != nil {
		terminateCmd(xephyr, 500*time.Millisecond)
		return "", nil, nil, fmt.Errorf("start openbox: %w", err)
	}
	time.Sleep(600 * time.Millisecond)
	return display, xephyr, openbox, nil
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
		terminateCmd(cmd, 5*time.Second)
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
	if err := pf.Input.Type("^s"); err != nil {
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
		terminateCmd(cmd, 5*time.Second)
		_ = os.Remove(saveFile)
	})

	if _, err := waitForWindow(s.pf, app.winMatch, 60*time.Second); err != nil {
		t.Fatalf("wait for %s window: %v", app.name, err)
	}

	if err := s.pf.Window.Activate(app.winMatch); err != nil {
		t.Fatalf("activate %s: %v", app.name, err)
	}

	if err := s.pf.Window.Maximize(app.winMatch); err != nil {
		t.Fatalf("maximize window %v", err)
	}

	docName := filepath.Base(saveFile)
	if _, err := waitForWindow(s.pf, docName, 20*time.Second); err != nil {
		t.Fatalf("wait for %s document title %q: %v", app.name, docName, err)
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
	screenW, screenH, err := s.pf.Screen.Resolution()
	if err != nil {
		t.Fatalf("resolution: %v", err)
	}
	captureRect := rect.Intersect(image.Rect(0, 0, screenW, screenH))
	if captureRect.Empty() {
		t.Fatalf("capture rect %v fell outside the screen %dx%d", rect, screenW, screenH)
	}

	if err := s.pf.Input.ClickCenter(rect); err != nil {
		t.Fatalf("click center: %v", err)
	}

	typingRect := image.Rect(
		rect.Min.X+rect.Dx()/4,
		rect.Min.Y+rect.Dy()/4,
		rect.Min.X+3*rect.Dx()/4,
		rect.Min.Y+3*rect.Dy()/4,
	)
	if typingRect.Empty() {
		typingRect = captureRect
	}
	ctxFocus, cancelFocus := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelFocus()
	if _, err := s.pf.Screen.WaitForStableContext(ctxFocus, typingRect, 3, 100*time.Millisecond); err != nil {
		t.Fatalf("wait for editor focus to settle: %v", err)
	}
	if err := s.pf.Input.Type("Integration"); err != nil {
		t.Fatalf("text entry: %v", err)
	}
	ctxType, cancelType := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelType()
	if _, err := s.pf.Screen.WaitForNoChangeContext(ctxType, typingRect, 3, 100*time.Millisecond); err != nil {
		t.Fatalf("wait for typed text to settle: %v", err)
	}

	img, err := s.pf.Screen.Grab(captureRect)
	if err != nil {
		t.Fatalf("grab window: %v", err)
	}
	if _, err := s.pf.Screen.GrabHash(captureRect); err != nil {
		t.Fatalf("grab hash: %v", err)
	}
	if _, err := s.pf.Screen.GetMultiplePixels([]image.Point{{captureRect.Min.X, captureRect.Min.Y}, {captureRect.Min.X + 1, captureRect.Min.Y + 1}}); err != nil {
		t.Fatalf("get multiple pixels: %v", err)
	}
	if _, err := s.pf.Screen.GetPixel(captureRect.Min.X, captureRect.Min.Y); err != nil {
		t.Fatalf("get pixel: %v", err)
	}
	refRect := image.Rect(captureRect.Min.X+20, captureRect.Min.Y+20, min(captureRect.Min.X+50, captureRect.Max.X), min(captureRect.Min.Y+50, captureRect.Max.Y))
	ref, err := s.pf.Screen.Grab(refRect)
	if err != nil {
		t.Fatalf("grab ref rect: %v", err)
	}
	if _, err := s.pf.Screen.LocateExact(captureRect, ref); err != nil {
		t.Fatalf("locate exact: %v", err)
	}
	if _, err := s.pf.Screen.ScanFor([]image.Rectangle{captureRect}, []uint32{find.PixelHash(img, nil)}, 100*time.Millisecond); err != nil {
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

	if err := s.pf.Input.ClickCenter(rect); err != nil {
		t.Fatalf("refocus before save: %v", err)
	}
	if err := s.pf.Input.Type("{ctrl+s}"); err != nil {
		t.Fatalf("ctrl+s: %v", err)
	}
	savedText, err := waitForFileContains(context.Background(), saveFile, "Integration", 10*time.Second)
	if err != nil {
		t.Fatalf("wait for save file %q contents: %v", saveFile, err)
	}
	if !strings.Contains(savedText, "Integration") {
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
		terminateCmd(cmd, 5*time.Second)
	})

	if _, err := waitForWindow(s.pf, app.winMatch, 90*time.Second); err != nil {
		t.Fatalf("wait for browser window: %v", err)
	}
	if err := s.pf.Window.Activate(app.winMatch); err != nil {
		t.Fatalf("activate browser: %v", err)
	}

	if err := s.pf.Input.Type("^l"); err != nil {
		t.Fatalf("ctrl+l: %v", err)
	}
	if err := s.pf.Input.TypeFast("about:support"); err != nil {
		t.Fatalf("type address: %v", err)
	}
	if err := s.pf.Input.Type("{enter}"); err != nil {
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

	baseEnv := rt.EnvList()
	if sess != nil {
		baseEnv = sess.Env()
	}
	path, err := executil.LookPath(app.launch[0])
	if err != nil {
		return nil, err
	}
	cmd := executil.CommandContext(context.Background(), path, args...)
	cmd.Env = env.Merge(baseEnv, extraEnv...)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
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

func waitForFileContains(ctx context.Context, path, want string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		if strings.Contains(string(data), want) {
			return string(data), nil
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
		}
	}
}

func requiredApps(t *testing.T) []appSpec {
	t.Helper()
	pfx := os.Getenv("PF_TEST_PREFIX")
	if pfx == "" {
		pfx = "integration"
	}
	all := []appSpec{
		//qt5/qt6 supported on wayland and x11
		{
			name:     "kwrite",
			launch:   []string{"kwrite"},
			winMatch: "kwrite",
			saveFile: filepath.Join(os.TempDir(), pfx+"-kwrite.txt"),
		},
		//gtk3 only supported on X11
		//{
		//	name:     "gedit",
		//	launch:   []string{"gedit"},
		//	winMatch: "gedit",
		//	saveFile: filepath.Join(os.TempDir(), pfx+"-gedit.txt"),
		//},
		//qt5 supported on wayland and x11
		{
			name:     "featherpad",
			launch:   []string{"featherpad"},
			winMatch: "featherpad",
			saveFile: filepath.Join(os.TempDir(), pfx+"-featherpad.txt"),
		},
		//gtk4 supported on wayland and x11
		{
			name:     "gnome-text-editor",
			launch:   []string{"gnome-text-editor"},
			winMatch: "gnome-text-editor",
			saveFile: filepath.Join(os.TempDir(), pfx+"-gnome-text-editor.txt"),
		},
		//{
		//	name:     "pluma",
		//	launch:   []string{"pluma"},
		//	winMatch: "pluma",
		//	saveFile: filepath.Join(os.TempDir(), pfx+"-pluma.txt"),
		//},
		//{
		//	name:      "firefox",
		//	launch:    []string{"firefox", "--no-remote", "--new-instance", "about:blank"},
		//	winMatch:  "firefox",
		//	isBrowser: true,
		//},
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

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
