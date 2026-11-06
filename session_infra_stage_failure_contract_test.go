package perfuncted

import (
	"context"
	"errors"
	"image"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Invalid caller log storage must not leak the freshly allocated managed
// desktop runtime, and an already canceled operation must allocate nothing.
func TestManagedSessionPreparationAndCancellationFailBeforeSpawning(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	session := &Session{}
	if infra, err := session.startSession(ctx, sessionModeHeadless, SessionConfig{}, false); infra != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled startup: infra=%v err=%v", infra, err)
	}
	root := t.TempDir()
	blocker := filepath.Join(root, "foreign-file")
	if err := os.WriteFile(blocker, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	var runtimeDir, logDir string
	err := prepareSessionRuntimeDirs(&runtimeDir, &logDir, filepath.Join(blocker, "logs"))
	if err == nil || runtimeDir == "" || logDir != "" {
		t.Fatalf("unsafe log root accepted: runtime=%q logs=%q err=%v", runtimeDir, logDir, err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	if data, readErr := os.ReadFile(blocker); readErr != nil || string(data) != "preserve" {
		t.Fatalf("foreign file altered: %q %v", data, readErr)
	}
	if err := os.RemoveAll(runtimeDir); err != nil {
		t.Fatal(err)
	}
}

func TestManagedSessionInvalidConfigurationAndDaemonAddressNeverSpawn(t *testing.T) {
	runtimeDir, err := createManagedSessionRuntimeDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	infra := &sessionInfra{xdgDir: runtimeDir, dbusAddr: "tcp:host=example.org", ctx: context.Background()}
	if err := infra.launchDBus(context.Background()); err == nil || !strings.Contains(err.Error(), "invalid D-Bus socket address") || infra.dbusCmd != nil {
		t.Fatalf("invalid daemon address admitted: %v", err)
	}
	if _, err := infra.writeEmbeddedConfig("configs/not-a-configuration.conf", image.Point{}); err == nil || !strings.Contains(err.Error(), "read embedded config") {
		t.Fatalf("missing embedded config accepted: %v", err)
	}
	if _, err := (&Session{}).resolveSessionSwayConfig(context.Background(), infra, sessionMode(-8), SessionConfig{}); err == nil || !strings.Contains(err.Error(), "unknown mode") {
		t.Fatalf("invalid compositor mode accepted: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(runtimeDir, "sway.conf")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("invalid config was written: %v", statErr)
	}
}

func TestManagedRuntimeCreationRefusesUnusableTemporaryRoot(t *testing.T) {
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "missing-root"))
	if dir, err := createManagedSessionRuntimeDir(); err == nil || dir != "" {
		if dir != "" {
			_ = os.RemoveAll(dir)
		}
		t.Fatalf("missing caller temp root accepted: dir=%q err=%v", dir, err)
	}
	t.Setenv("TMPDIR", "relative-root")
	roots := managedSessionTempRoots()
	if len(roots) != 1 || roots[0] != "/tmp" {
		t.Fatalf("relative socket root accepted: %+v", roots)
	}
}

func TestManagedChildPIDWriteFailurePreservesExistingDirectory(t *testing.T) {
	runtimeDir, err := createManagedSessionRuntimeDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	target := filepath.Join(runtimeDir, "child.pid")
	if mkdirErr := os.Mkdir(target, 0700); mkdirErr != nil {
		t.Fatal(mkdirErr)
	}
	(&sessionInfra{xdgDir: runtimeDir}).writeChildPID("child.pid", os.Getpid())
	info, err := os.Stat(target)
	if err != nil || !info.IsDir() {
		t.Fatalf("pid writer replaced foreign directory: %+v %v", info, err)
	}
}
