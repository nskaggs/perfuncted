package perfuncted

import (
	"context"
	"errors"
	"image"
	"os"
	"path/filepath"
	"testing"
)

// An explicitly supplied Sway config must not be replaced by an embedded
// compositor config or copied into the managed runtime. Cancellation must
// still prevent configuration selection, including explicit overrides.
func TestManagedSessionExplicitSwayConfigPreservesCallerFileAndCancellation(t *testing.T) {
	runtimeDir, err := createManagedSessionRuntimeDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	callerConfig := filepath.Join(t.TempDir(), "user-sway.conf")
	const original = "# user selected config\noutput * resolution 1280x720\n"
	if err := os.WriteFile(callerConfig, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	session := &Session{}
	infra := &sessionInfra{xdgDir: runtimeDir}
	config := SessionConfig{SwayConfigPath: callerConfig, Resolution: image.Pt(1920, 1080)}
	for _, mode := range []sessionMode{sessionModeHeadless, sessionModeNested} {
		got, err := session.resolveSessionSwayConfig(context.Background(), infra, mode, config)
		if err != nil || got != callerConfig {
			t.Fatalf("explicit config mode %d: path=%q error=%v", mode, got, err)
		}
	}
	if data, err := os.ReadFile(callerConfig); err != nil || string(data) != original {
		t.Fatalf("caller configuration mutated: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(runtimeDir, "sway.conf")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unrequested generated config exists: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := session.resolveSessionSwayConfig(ctx, infra, sessionModeHeadless, config); got != "" || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled config selected: %q %v", got, err)
	}
}
