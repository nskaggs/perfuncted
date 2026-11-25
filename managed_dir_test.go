package perfuncted

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStopSkipsNonSafeDir(t *testing.T) {
	dir := t.TempDir()
	infra := &sessionInfra{xdgDir: "/"}
	if err := infra.stop(); err == nil {
		t.Fatal("unsafe runtime cleanup unexpectedly succeeded")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("tmp dir stat: %v", err)
	}
	infra2Dir, err := os.MkdirTemp("", "perfuncted-xdg-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	infra2 := &sessionInfra{xdgDir: infra2Dir}
	sentinel := filepath.Join(infra2.xdgDir, "keep")
	if err := os.WriteFile(sentinel, []byte("x"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := infra2.stop(); err != nil {
		t.Fatalf("managed runtime cleanup: %v", err)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("expected managed dir to be removed, sentinel stat = %v", err)
	}
	if _, err := os.Stat(infra2Dir); !os.IsNotExist(err) {
		t.Fatalf("expected managed dir to be removed, dir stat = %v", err)
	}
}
