//go:build integration && linux

package flatpaktest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// testFlatpakWaylandMatrix uses isolated *real* compositor processes. It
// refuses to interpret XWayland results as native GNOME/KDE Wayland success.
// Unlike the X11 smoke, this is an unattended capability/diagnostic contract:
// portals requiring user consent cannot be invoked automatically.
func testFlatpakWaylandMatrix(t *testing.T, h *harness, sourceRoot, testRoot string) {
	t.Helper()
	targets := strings.ToLower(strings.TrimSpace(os.Getenv("PF_FLATPAK_DESKTOP_TARGET")))
	if targets == "" {
		t.Log("Wayland GNOME/KDE compositor matrix not requested; set PF_FLATPAK_DESKTOP_TARGET=kde,gnome")
		return
	}
	for _, target := range strings.Split(targets, ",") {
		target = strings.TrimSpace(target)
		if target != "kde" && target != "gnome" {
			t.Fatalf("unknown desktop target %q; expected kde or gnome", target)
		}
		t.Run(target, func(t *testing.T) {
			runInstalledFlatpakWaylandCompositor(t, h, sourceRoot, testRoot, target)
		})
	}
}

func runInstalledFlatpakWaylandCompositor(t *testing.T, h *harness, sourceRoot, testRoot, desktop string) {
	t.Helper()
	// Unix domain sockets have a ~108-byte path bound on Linux. The large
	// /var/tmp test workspace is used for artifacts only, never these sockets.
	runRoot, err := os.MkdirTemp("/tmp", "pfw-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runRoot) })
	if err := os.Chmod(runRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	name := "wayland-flatpak"
	sock := filepath.Join(runRoot, name)
	var executable string
	var arguments []string
	switch desktop {
	case "kde":
		executable = "kwin_wayland"
		arguments = []string{"--virtual", "--no-lockscreen", "--socket", name, "--width", "1024", "--height", "768"}
	case "gnome":
		executable = "gnome-shell"
		arguments = []string{"--headless", "--wayland", "--no-x11"}
		name = "wayland-0"
		sock = filepath.Join(runRoot, name)
	default:
		t.Fatalf("unexpected target %s", desktop)
	}
	if _, err := exec.LookPath(executable); err != nil {
		t.Fatalf("%s compositor required by Flatpak CI matrix: %v", desktop, err)
	}
	logFile, err := os.Create(filepath.Join(testRoot, desktop+"-headless.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	env := append([]string(nil), h.env...)
	env = setEnv(env, "XDG_RUNTIME_DIR", runRoot)
	env = setEnv(env, "XDG_CURRENT_DESKTOP", strings.ToUpper(desktop))
	env = setEnv(env, "XDG_SESSION_TYPE", "wayland")
	env = setEnv(env, "WAYLAND_DISPLAY", name)
	env = setEnv(env, "MUTTER_DEBUG_DUMMY_MODE_SPECS", "1024x768")
	env = setEnv(env, "QT_QPA_PLATFORM", "offscreen")
	env = unsetEnv(env, "DISPLAY")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, arguments...)
	cmd.Env = env
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("launch %s: %v", desktop, err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		cancel()
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
	})
	ready := false
	for deadline := time.Now().Add(25 * time.Second); time.Now().Before(deadline); {
		if stat, err := os.Stat(sock); err == nil && stat.Mode()&os.ModeSocket != 0 {
			ready = true
			break
		}
		select {
		case err := <-done:
			data, _ := os.ReadFile(filepath.Join(testRoot, desktop+"-headless.log"))
			t.Fatalf("%s compositor exited before socket ready: %v; log=%s", desktop, err, data)
		default:
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !ready {
		data, _ := os.ReadFile(filepath.Join(testRoot, desktop+"-headless.log"))
		t.Fatalf("%s compositor socket never appeared: %s; log=%s", desktop, sock, data)
	}
	original := h.env
	h.env = env
	defer func() { h.env = original }()
	t.Run("packaged session diagnostics", func(t *testing.T) {
		stdout, stderr, code := h.run(t, sourceRoot, "run", appID, "session", "check")
		if code != 0 || !strings.Contains(stdout, "WAYLAND_DISPLAY") {
			t.Fatalf("%s Flatpak session diagnostics: code=%d stdout=%q stderr=%q", desktop, code, stdout, stderr)
		}
	})
	t.Run("packaged compositor detection", func(t *testing.T) {
		stdout, stderr, code := h.run(t, sourceRoot, "run", appID, "info", "--output=json")
		if code != 0 {
			t.Fatalf("%s Flatpak info failed: code=%d stdout=%q stderr=%q", desktop, code, stdout, stderr)
		}
		var report struct {
			Compositor   string                     `json:"compositor"`
			Environment  map[string]string          `json:"environment"`
			Capabilities map[string]json.RawMessage `json:"capabilities"`
		}
		if err := json.Unmarshal([]byte(stdout), &report); err != nil {
			t.Fatal(err)
		}
		if len(report.Capabilities) == 0 {
			t.Fatalf("%s Flatpak did not report capabilities: %s", desktop, stdout)
		}
		if strings.Contains(strings.ToLower(report.Compositor), "x11") && !strings.Contains(strings.ToLower(report.Compositor), "wayland") {
			t.Errorf("%s Wayland test incorrectly detected only X11 compositor: %s", desktop, report.Compositor)
		}
		t.Logf("%s installed Flatpak capabilities: %s", desktop, stdout)
	})
	t.Run("RemoteDesktop portal fails closed without attended authorization", func(t *testing.T) {
		// A headless compositor cannot display an authorization dialogue.
		// Successful synthetic input (e.g. via XTEST) would be a security
		// regression when the user explicitly requested RemoteDesktop/EIS.
		probeCtx, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		cmd := exec.CommandContext(probeCtx, flatpakBinaryName, "run", appID,
			"--input-backend", "remote-desktop", "input", "click", "--x=9", "--y=11")
		cmd.Env = h.env
		cmd.Dir = sourceRoot
		var out, diagnostics bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &diagnostics
		err := cmd.Run()
		if probeCtx.Err() != nil {
			t.Fatalf("%s headless portal input was left waiting for authorization without a user", desktop)
		}
		if err == nil {
			t.Fatalf("%s headless portal input succeeded without user authorization: %q", desktop, out.String())
		}
		if diagnostics.Len() == 0 {
			t.Fatalf("%s denied RemoteDesktop input without an actionable error", desktop)
		}
		t.Logf("%s portal input denied as expected: %s", desktop, diagnostics.String())
	})
	t.Run("capability-aware operation errors", func(t *testing.T) {
		// A compositor may deny window enumeration, capture or input. The
		// tool must report an explicit capability/error rather than panic.
		stdout, stderr, code := h.run(t, sourceRoot, "run", appID, "window", "list")
		if code != 0 && strings.TrimSpace(stderr) == "" {
			t.Fatalf("%s window enumeration failed silently (code=%d): %q", desktop, code, stdout)
		}
		t.Logf("%s window list: code=%d stdout=%q stderr=%q", desktop, code, stdout, stderr)
	})
}

func TestFlatpakWaylandMatrixEnvironment(t *testing.T) {
	// Fail clearly on invalid CI opt-in; no false green due to a typo.
	value := os.Getenv("PF_FLATPAK_DESKTOP_TARGET")
	for _, v := range strings.Split(value, ",") {
		v = strings.TrimSpace(v)
		if v != "" && v != "kde" && v != "gnome" {
			t.Fatal(fmt.Errorf("invalid Flatpak Wayland CI target: %q", v))
		}
	}
	_ = errors.Is // keep errors import reserved for process status introspection
}
