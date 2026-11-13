package perfuncted

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Once the last unmount attempt returns, caller cancellation still owns the
// cleanup transaction. No second mount scan may authorize directory removal.
func TestManagedUnmountCancellationAfterLastHelperSkipsVerification(t *testing.T) {
	const root = "/synthetic/perfuncted-xdg-last-unmount"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reads, unmounts := 0, 0
	err := unmountSubdirsWith(ctx, root, func(dir string) ([]string, error) {
		if dir != root {
			t.Fatalf("inspected unexpected runtime %q", dir)
		}
		reads++
		if reads > 1 {
			t.Fatal("canceled cleanup performed authoritative verification scan")
		}
		return []string{root + "/child"}, nil
	}, func(ctx context.Context, mount string) error {
		if mount != root+"/child" || ctx.Err() != nil {
			t.Fatalf("invalid unmount request %q: %v", mount, ctx.Err())
		}
		unmounts++
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) || reads != 1 || unmounts != 1 {
		t.Fatalf("canceled final unmount: err=%v reads=%d unmounts=%d", err, reads, unmounts)
	}
}

// Enumeration is not a deletion lease. If the runtime vanished or its path
// was replaced with an ordinary file, reaping must leave it alone.
func TestStaleRuntimeDisappearanceOrFileReplacementIsHarmless(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	path := filepath.Join(root, "perfuncted-xdg-vanishing")
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := reapSessionDir(context.Background(), path); err != nil {
		t.Fatalf("absent runtime: %v", err)
	}
	if err := os.WriteFile(path, []byte("other process's file"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := reapSessionDir(context.Background(), path); err != nil {
		t.Fatalf("replaced path: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "other process's file" {
		t.Fatalf("reaping touched unrelated replacement: %q, %v", data, err)
	}
	if err := reapSessionDirWhenOlderThan(context.Background(), path, time.Now().Add(time.Hour), time.Minute); err != nil {
		t.Fatalf("non-directory replacement age check: %v", err)
	}
	data, err = os.ReadFile(path)
	if err != nil || string(data) != "other process's file" {
		t.Fatalf("age-based reaping touched replacement: %q, %v", data, err)
	}
}
