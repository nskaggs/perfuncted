package perfuncted

import (
	"image"
	"os"
	"path/filepath"
	"testing"
)

// Managed-session helpers must not consider an arbitrary child of TMPDIR
// a session runtime. Only our managed runtime names are valid write targets.
func TestManagedSessionWritersRejectUnmanagedTempSubdirectory(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	foreign := filepath.Join(root, "unrelated-application-data")
	if err := os.Mkdir(foreign, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(foreign, "keep")
	if err := os.WriteFile(marker, []byte("unrelated"), 0600); err != nil {
		t.Fatal(err)
	}
	infra := &sessionInfra{xdgDir: foreign}
	infra.writeChildPID("dbus.pid", os.Getpid())
	if _, err := os.Stat(filepath.Join(foreign, "dbus.pid")); !os.IsNotExist(err) {
		t.Fatalf("managed PID writer wrote into unrelated temp directory: %v", err)
	}
	if config, err := infra.writeEmbeddedConfig("configs/headless.conf", image.Pt(1024, 768)); err == nil || config != "" {
		t.Fatalf("managed config writer accepted unrelated temp directory: %q %v", config, err)
	}
	if _, err := os.Stat(filepath.Join(foreign, "sway.conf")); !os.IsNotExist(err) {
		t.Fatalf("managed config writer modified unrelated temp directory: %v", err)
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "unrelated" {
		t.Fatalf("foreign marker changed: %q %v", data, err)
	}
}
