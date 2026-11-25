package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
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
		pid, err := ReadPIDFile(path)
		if err == nil || pid != 0 {
			t.Fatalf("unsafe PID record %q accepted: pid=%d err=%v", value, pid, err)
		}
	}
	if err := os.WriteFile(path, []byte("  1234\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if pid, err := ReadPIDFile(path); err != nil || pid != 1234 {
		t.Fatalf("valid PID record rejected: pid=%d err=%v", pid, err)
	}
}

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

// Ownership metadata with a zero or negative process ID is malformed, not
// proof that the owner died. A newly created runtime must retain the same
// creation grace as an unreadable or nonnumeric owner file.
func TestManagedRuntimeNonpositiveOwnerPIDPreservesCreationGrace(t *testing.T) {
	for _, owner := range []string{"0", "-1", " -42 ", "not-a-pid"} {
		t.Run(owner, func(t *testing.T) {
			dir, err := os.MkdirTemp("", "perfuncted-xdg-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(dir) })
			sentinel := filepath.Join(dir, "private-session-data")
			if err := os.WriteFile(sentinel, []byte("preserve"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, OwnerPIDFile), []byte(owner), 0600); err != nil {
				t.Fatal(err)
			}
			if err := reapSessionDirIfStale(context.Background(), dir, 0, time.Now()); err != nil {
				t.Fatal(err)
			}
			if data, err := os.ReadFile(sentinel); err != nil || string(data) != "preserve" {
				t.Fatalf("recent managed runtime with owner %q was removed: data=%q error=%v", owner, data, err)
			}
		})
	}
}

func TestMalformedManagedRuntimeOwnerHonorsMinimumAndConfiguredAge(t *testing.T) {
	for _, tc := range []struct {
		name, owner string
		age, maxAge time.Duration
		wantRemoved bool
	}{
		{"zero age below minimum", "0", 4 * time.Minute, 0, false},
		{"negative age below minimum", "-1", 4 * time.Minute, 0, false},
		{"zero age above minimum", "0", 6 * time.Minute, 0, true},
		{"negative age above minimum", "-1", 6 * time.Minute, 0, true},
		{"configured age protects zero", "0", 6 * time.Minute, time.Hour, false},
		{"configured age protects negative", "-5", 6 * time.Minute, time.Hour, false},
		{"configured age reaps zero", "0", 2 * time.Hour, time.Hour, true},
		{"configured age reaps negative", "-1", 2 * time.Hour, time.Hour, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, err := os.MkdirTemp("", "perfuncted-xdg-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(dir) })
			sentinel := filepath.Join(dir, "session-sentinel")
			if err := os.WriteFile(sentinel, []byte("private"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, OwnerPIDFile), []byte(tc.owner), 0600); err != nil {
				t.Fatal(err)
			}
			now := time.Now()
			old := now.Add(-tc.age)
			if err := os.Chtimes(dir, old, old); err != nil {
				t.Fatal(err)
			}
			if err := reapSessionDirIfStale(context.Background(), dir, tc.maxAge, now); err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(sentinel)
			if tc.wantRemoved {
				if !os.IsNotExist(err) {
					t.Fatalf("expired malformed-owner runtime remains: %v %q", err, b)
				}
			} else if err != nil || string(b) != "private" {
				t.Fatalf("unexpired malformed-owner runtime removed: %v %q", err, b)
			}
		})
	}
}

// A failing unmount helper must not authorize removal of a runtime containing
// still-mounted filesystems. Nested mounts must be attempted before parents.
func TestManagedUnmountNestedOrderAndResidualMountFailClosed(t *testing.T) {
	const root = "/synthetic/perfuncted-xdg-nested"
	parent := root + "/mount"
	child := parent + "/submount"
	grandchild := child + "/nested"
	mountList := []string{parent, child, grandchild}
	var unmounted []string
	reads := 0
	err := unmountSubdirsWith(context.Background(), root, func(path string) ([]string, error) {
		if path != root {
			t.Fatalf("unexpected mount scan: %q", path)
		}
		reads++
		if reads == 1 {
			return slices.Clone(mountList), nil
		}
		return []string{parent}, nil
	}, func(ctx context.Context, path string) error {
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		unmounted = append(unmounted, path)
		if path == parent {
			return errors.New("synthetic unmount denied")
		}
		return nil
	})
	if !slices.Equal(unmounted, []string{grandchild, child, parent}) {
		t.Fatalf("unsafe unmount order: %v", unmounted)
	}
	if reads != 2 || err == nil || !strings.Contains(err.Error(), "mounts remain") {
		t.Fatalf("residual mount accepted: reads=%d err=%v", reads, err)
	}
}

// Only the authoritative second mount scan may establish that deletion is safe,
// even if an attempted unmount returned an error.
func TestManagedUnmountHelperFailureRequiresIndependentEmptyRescan(t *testing.T) {
	const root = "/synthetic/perfuncted-xdg-removed"
	reads, attempted := 0, 0
	err := unmountSubdirsWith(context.Background(), root, func(string) ([]string, error) {
		reads++
		if reads == 1 {
			return []string{root + "/mount"}, nil
		}
		return nil, nil
	}, func(context.Context, string) error {
		attempted++
		return errors.New("helper exited unsuccessfully after unmount")
	})
	if err != nil || reads != 2 || attempted != 1 {
		t.Fatalf("verified removed mount rejected: reads=%d attempts=%d err=%v", reads, attempted, err)
	}
}

// Only live caller authority permits proceeding from ownership checks to
// child cleanup, or from an empty mount scan to deleting the runtime tree.
// Revocation at either boundary must preserve all private contents.
func TestReapSessionPreservesPrivateRuntimeAtAuthorityBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name  string
		check int
	}{
		{name: "before first child PID inspection", check: 2},
		{name: "after mount safety inspection before directory deletion", check: 8},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := t.TempDir()
			t.Setenv("TMPDIR", base)
			runtime := filepath.Join(base, "perfuncted-xdg-owned")
			if err := os.Mkdir(runtime, 0700); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(runtime, "do-not-remove")
			if err := os.WriteFile(marker, []byte("private runtime data"), 0600); err != nil {
				t.Fatal(err)
			}
			owner, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			ctx := &cancelAtErrCallContext{Context: owner, cancel: cancel, target: tc.check}
			err := reapSessionDir(ctx, runtime)
			if !errors.Is(err, context.Canceled) || !errors.Is(err, context.Cause(owner)) || ctx.calls < tc.check {
				t.Fatalf("cleanup revocation: err=%v checks=%d trigger=%d", err, ctx.calls, tc.check)
			}
			content, readErr := os.ReadFile(marker)
			if readErr != nil || string(content) != "private runtime data" {
				t.Fatalf("cleanup removed private runtime after cancellation: data=%q err=%v", content, readErr)
			}
		})
	}
}
