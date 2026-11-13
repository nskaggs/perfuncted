package perfuncted

import (
	"image"
	"os"
	"path/filepath"
	"testing"
)

// A matching managed-runtime name is insufficient authority to create PID
// metadata or compositor config in a directory another account can modify.
func TestManagedSessionWritersRejectOtherWritableRuntime(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	for _, tc := range []struct {
		name  string
		mode  os.FileMode
		allow bool
	}{
		{"private", 0700, true},
		{"group-writable", 0770, false},
		{"world-writable", 0777, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(root, "perfuncted-xdg-"+tc.name)
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(dir, tc.mode); err != nil {
				t.Fatal(err)
			}
			infra := &sessionInfra{xdgDir: dir}
			infra.writeChildPID("dbus.pid", os.Getpid())
			_, pidErr := os.Lstat(filepath.Join(dir, "dbus.pid"))
			_, configErr := infra.writeEmbeddedConfig("configs/headless.conf", image.Pt(1280, 720))
			_, confStatErr := os.Lstat(filepath.Join(dir, "sway.conf"))
			if tc.allow {
				if pidErr != nil || configErr != nil || confStatErr != nil {
					t.Fatalf("private runtime rejected: pid=%v config=%v stat=%v", pidErr, configErr, confStatErr)
				}
			} else {
				if !os.IsNotExist(pidErr) || configErr == nil || !os.IsNotExist(confStatErr) {
					t.Fatalf("writable runtime admitted: pid=%v config=%v stat=%v", pidErr, configErr, confStatErr)
				}
			}
		})
	}
}
