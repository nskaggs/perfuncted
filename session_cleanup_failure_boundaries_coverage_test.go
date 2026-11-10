package perfuncted

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Losing access to the mount table after an unmount is not proof that the
// runtime tree is safe to delete. A caller must receive the inspection error.
func TestManagedUnmountRejectsFailedPostUnmountVerification(t *testing.T) {
	root := "/synthetic/perfuncted-xdg-unmount"
	mount := root + "/nested"
	verificationErr := errors.New("mount namespace unavailable")
	reads, unmounts := 0, 0
	err := unmountSubdirsWith(context.Background(), root,
		func(path string) ([]string, error) {
			if path != root {
				t.Fatalf("unexpected inspected path: %q", path)
			}
			reads++
			if reads == 1 {
				return []string{mount}, nil
			}
			return nil, verificationErr
		},
		func(ctx context.Context, path string) error {
			if ctx.Err() != nil || path != mount {
				t.Fatalf("unexpected unmount target %q: %v", path, ctx.Err())
			}
			unmounts++
			return nil
		},
	)
	if !errors.Is(err, verificationErr) || reads != 2 || unmounts != 1 {
		t.Fatalf("unsafe verification accepted: err=%v reads=%d unmounts=%d", err, reads, unmounts)
	}
}

// A proc-like directory can contain unrelated names and malformed entries;
// only processes with a successfully parsed matching group are candidates.
func TestStaleProcessGroupEnumerationIgnoresUnreadableAndForeignEntries(t *testing.T) {
	root := t.TempDir()
	if members := processGroupMembersAt(filepath.Join(root, "not-present"), 42); len(members) != 0 {
		t.Fatalf("missing proc root produced members: %+v", members)
	}
	file := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(file, []byte("not a procfs"), 0600); err != nil {
		t.Fatal(err)
	}
	if members := processGroupMembersAt(file, 42); len(members) != 0 {
		t.Fatalf("regular-file proc root produced members: %+v", members)
	}
	for _, name := range []string{"README", "0", "-4", "123abc"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if members := processGroupMembersAt(root, 42); len(members) != 0 {
		t.Fatalf("unrelated proc entries produced members: %+v", members)
	}
}

// Child metadata must be a positive decimal PID and never an arbitrary path,
// oversized record, or negative PID that could be interpreted by signal(2).
func TestStaleChildPIDMetadataRejectsInvalidProcessIdentifiers(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "dbus.pid")
	for _, value := range []string{"", "not-a-pid", "0", "-1", strings.Repeat("1", 65)} {
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
		pid, err := readPIDFile(path)
		if err == nil || pid != 0 {
			t.Fatalf("unsafe PID record %q accepted: pid=%d err=%v", value, pid, err)
		}
	}
	if err := os.WriteFile(path, []byte("  1234\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if pid, err := readPIDFile(path); err != nil || pid != 1234 {
		t.Fatalf("valid PID record rejected: pid=%d err=%v", pid, err)
	}
}
