package perfuncted

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Cleanup must inspect the actual kernel mount table and leave an ordinary
// unmounted session runtime untouched. In particular it must never invoke
// fusermount or delete files merely because the directory has children.
func TestUnmountSubdirsContextLeavesUnMountedPrivateRuntimeIntact(t *testing.T) {
	dir := t.TempDir()
	sentinel := filepath.Join(dir, "another-owner-data")
	const data = "must survive safe mount cleanup"
	if err := os.WriteFile(sentinel, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	mounts, err := readSessionSubdirMounts(dir)
	if err != nil {
		t.Fatalf("read actual kernel mountinfo: %v", err)
	}
	if len(mounts) != 0 {
		t.Skipf("temporary directory unexpectedly contains mounts: %q", mounts)
	}
	if err := unmountSubdirsContext(context.Background(), dir); err != nil {
		t.Fatalf("unmounted runtime cleanup refused: %v", err)
	}
	got, err := os.ReadFile(sentinel)
	if err != nil || string(got) != data {
		t.Fatalf("unmount changed unrelated session data: %q, %v", got, err)
	}
}
