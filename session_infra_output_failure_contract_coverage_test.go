package perfuncted

import (
	"context"
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedSwayLogCreationFailureStopsBeforeProcessSpawn(t *testing.T) {
	runtimeDir, err := createManagedSessionRuntimeDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })

	logBlocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(logBlocker, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	infra := &sessionInfra{
		xdgDir:    runtimeDir,
		wlDisplay: "wayland-1",
		logDir:    logBlocker,
		ctx:       context.Background(),
	}
	err = infra.launchSway(context.Background(), filepath.Join(runtimeDir, "sway.conf"), sessionModeHeadless)
	if err == nil || !strings.Contains(err.Error(), "create log") {
		t.Fatalf("startup with an unusable log directory returned %v", err)
	}
	if infra.swayCmd != nil {
		t.Fatal("Sway was started before its private log could be created")
	}
	if contents, readErr := os.ReadFile(logBlocker); readErr != nil || string(contents) != "preserve" {
		t.Fatalf("log setup failure changed its blocking file: %q %v", contents, readErr)
	}
}

func TestManagedSwayConfigPublishErrorPreservesObstructingDirectory(t *testing.T) {
	runtimeDir, err := createManagedSessionRuntimeDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })

	target := filepath.Join(runtimeDir, "sway.conf")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(target, "preserve")
	if err := os.WriteFile(sentinel, []byte("existing state"), 0600); err != nil {
		t.Fatal(err)
	}
	configPath, err := (&sessionInfra{xdgDir: runtimeDir}).writeEmbeddedConfig("configs/headless.conf", image.Point{})
	if err == nil || configPath != "" || !strings.Contains(err.Error(), "write config") {
		t.Fatalf("config publication over a directory returned path=%q err=%v", configPath, err)
	}
	if contents, readErr := os.ReadFile(sentinel); readErr != nil || string(contents) != "existing state" {
		t.Fatalf("failed config publication changed obstructing state: %q %v", contents, readErr)
	}
	entries, readErr := os.ReadDir(runtimeDir)
	if readErr != nil || len(entries) != 1 || entries[0].Name() != "sway.conf" || !entries[0].IsDir() {
		t.Fatalf("failed config publication leaked staged data or replaced the directory: %v %v", entries, readErr)
	}
}
