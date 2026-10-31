package perfuncted

import (
	"context"
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedSwayConfigSelectionStaysInsideRuntime(t *testing.T) {
	runtimeDir, err := createManagedSessionRuntimeDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	infra := &sessionInfra{xdgDir: runtimeDir}

	headless, err := infra.resolveSwayConfig(sessionModeHeadless, image.Pt(1600, 900))
	if err != nil {
		t.Fatalf("resolve headless config: %v", err)
	}
	if filepath.Dir(headless) != runtimeDir {
		t.Fatalf("headless config escaped managed runtime: %q", headless)
	}
	data, err := os.ReadFile(headless)
	if err != nil || !strings.Contains(string(data), "resolution 1600x900") {
		t.Fatalf("headless resolution was not applied: %v", err)
	}

	nested, err := infra.resolveSwayConfig(sessionModeNested, image.Pt(1600, 900))
	if err != nil {
		t.Fatalf("resolve nested config: %v", err)
	}
	data, err = os.ReadFile(nested)
	wantNested, embeddedErr := embeddedConfigs.ReadFile("configs/nested.conf")
	if err != nil || embeddedErr != nil || string(data) != string(wantNested) {
		t.Fatalf("nested config differs from its embedded layout: read=%v embedded=%v", err, embeddedErr)
	}
	if _, err := infra.resolveSwayConfig(sessionMode(99), image.Point{}); err == nil {
		t.Fatal("unknown session mode selected a Sway config")
	}

	unsafeDir := t.TempDir()
	activeTempRoot := filepath.Join(t.TempDir(), "active")
	if err := os.Mkdir(activeTempRoot, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", activeTempRoot)
	if isSafeToRemoveDir(unsafeDir) {
		t.Fatalf("fixture path %q unexpectedly qualifies as safe", unsafeDir)
	}
	if _, err := (&sessionInfra{xdgDir: unsafeDir}).writeEmbeddedConfig("configs/headless.conf", image.Point{}); err == nil {
		t.Fatal("configuration writer accepted a directory outside managed runtimes")
	}
}

func TestWriteChildPIDRequiresSafeLocationAndPositivePID(t *testing.T) {
	runtimeDir, err := createManagedSessionRuntimeDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	infra := &sessionInfra{xdgDir: runtimeDir}
	infra.writeChildPID("child.pid", 0)
	if _, err := os.Stat(filepath.Join(runtimeDir, "child.pid")); !os.IsNotExist(err) {
		t.Fatalf("nonpositive child pid created a file: %v", err)
	}
	infra.writeChildPID("child.pid", os.Getpid())
	pid, err := readPIDFile(filepath.Join(runtimeDir, "child.pid"))
	if err != nil || pid != os.Getpid() {
		t.Fatalf("owned runtime pidfile = %d, %v", pid, err)
	}

	unsafeDir := t.TempDir()
	activeTempRoot := filepath.Join(t.TempDir(), "active")
	if err := os.Mkdir(activeTempRoot, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", activeTempRoot)
	if isSafeToRemoveDir(unsafeDir) {
		t.Fatalf("fixture path %q unexpectedly qualifies as safe", unsafeDir)
	}
	(&sessionInfra{xdgDir: unsafeDir}).writeChildPID("child.pid", os.Getpid())
	if _, err := os.Stat(filepath.Join(unsafeDir, "child.pid")); !os.IsNotExist(err) {
		t.Fatalf("pidfile was written outside an owned runtime: %v", err)
	}
	var absent *sessionInfra
	absent.writeChildPID("child.pid", os.Getpid())
}

func TestManagedLaunchersRefuseUnavailableStartupInputs(t *testing.T) {
	runtimeDir, err := createManagedSessionRuntimeDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	logDir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv("WAYLAND_DISPLAY", "")
	noHelpers := t.TempDir()
	t.Setenv("PATH", noHelpers)

	infra := &sessionInfra{
		xdgDir:    runtimeDir,
		wlDisplay: "wayland-1",
		dbusAddr:  "unix:path=" + filepath.Join(runtimeDir, "bus"),
		logDir:    logDir,
		ctx:       context.Background(),
	}
	if err := infra.launchDBus(context.Background()); err == nil {
		t.Fatal("D-Bus launcher unexpectedly started without its executable")
	}
	if infra.dbusCmd != nil {
		t.Fatal("failed D-Bus launch published a process handle")
	}

	if err := infra.launchSway(context.Background(), filepath.Join(runtimeDir, "sway.conf"), sessionMode(99)); err == nil {
		t.Fatal("Sway launcher accepted an unknown session mode")
	}
	if infra.swayCmd != nil {
		t.Fatal("rejected Sway mode published a process handle")
	}
	if err := infra.launchSway(context.Background(), filepath.Join(runtimeDir, "sway.conf"), sessionModeNested); err == nil || !strings.Contains(err.Error(), "host Wayland socket") {
		t.Fatalf("nested Sway launch without a host socket returned %v", err)
	}
	if infra.swayCmd != nil {
		t.Fatal("rejected nested launch published a process handle")
	}

	if err := infra.launchWlPaste(); err == nil {
		t.Fatal("optional clipboard helper unexpectedly started without its executable")
	}
	if infra.wlPasteCmd != nil {
		t.Fatal("failed clipboard helper start published a process handle")
	}
}
