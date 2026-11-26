package runtime

import (
	"context"
	"errors"
	"go.uber.org/goleak"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestStaleSessionReapCanceledBeforeInspectingOwner(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := reapSessionDir(ctx, "/synthetic/perfuncted-xdg-cancelled"); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled reaping result: %v", err)
	}
	if err := reapSessionDirWhenOlderThan(ctx, "/synthetic/perfuncted-xdg-cancelled", time.Now(), time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled age check result: %v", err)
	}
}

func TestManagedUnmountEmptyOrNoMountScans(t *testing.T) {
	const root = "/synthetic/perfuncted-xdg-safe"
	readCalled := false
	if err := unmountSubdirsWith(context.Background(), "", func(string) ([]string, error) { readCalled = true; return nil, nil }, func(context.Context, string) error { t.Fatal("unexpected unmount"); return nil }); err != nil || readCalled {
		t.Fatalf("empty runtime inspected mounts=%v error=%v", readCalled, err)
	}
	readCalled = false
	if err := unmountSubdirsWith(context.Background(), root, func(string) ([]string, error) { readCalled = true; return nil, nil }, func(context.Context, string) error { t.Fatal("unexpected unmount"); return nil }); err != nil || !readCalled {
		t.Fatalf("empty mount list: read=%v error=%v", readCalled, err)
	}
}

func TestManagedUnmountCancellationAndNoMountBoundaries(t *testing.T) {
	const root = "/synthetic/perfuncted-xdg-safe"
	for _, tc := range []struct {
		name                            string
		cancelAt                        string
		expectedReads, expectedUnmounts int
	}{
		{"pre-canceled", "before", 0, 0},
		{"after initial inspection", "after read", 1, 0},
		{"after first unmount", "first unmount", 1, 1},
		{"after failed unmount", "failed unmount", 1, 1},
		{"after verification", "verify", 2, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancelAt == "before" {
				cancel()
			}
			reads, unmounts := 0, 0
			err := unmountSubdirsWith(ctx, root, func(string) ([]string, error) {
				reads++
				if tc.cancelAt == "after read" && reads == 1 || tc.cancelAt == "verify" && reads == 2 {
					cancel()
				}
				if reads == 1 {
					return []string{root + "/one", root + "/two"}, nil
				}
				return nil, nil
			}, func(context.Context, string) error {
				unmounts++
				if tc.cancelAt == "first unmount" || tc.cancelAt == "failed unmount" {
					cancel()
				}
				if tc.cancelAt == "failed unmount" {
					return errors.New("synthetic unmount failed")
				}
				return nil
			})
			if !errors.Is(err, context.Canceled) || reads != tc.expectedReads || unmounts != tc.expectedUnmounts {
				t.Fatalf("cancellation result: err=%v scans=%d/%d unmounts=%d/%d", err, reads, tc.expectedReads, unmounts, tc.expectedUnmounts)
			}
		})
	}
}

// Polling can exhaust its retry budget at the same instant startup loses
// authority. The final cancellation check must win over a timeout report.
func TestReadinessExhaustedBudgetPreservesLateOwnerCancellation(t *testing.T) {
	base, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	ctx := &cancelAtErrCallContext{Context: base, cancel: cancel, target: 3}
	calls := 0
	err := PollCondition(ctx, 1, time.Millisecond, func() bool {
		calls++
		return false
	})
	if !errors.Is(err, context.Canceled) || context.Cause(base) == nil || calls != 1 || ctx.calls != 3 {
		t.Fatalf("last readiness check returned %v (checks=%d probes=%d), want cancellation", err, ctx.calls, calls)
	}
}

// A child-completion notification can race with cancellation. Rechecking
// authority both before and after acquiring the wait-result lock prevents a
// stale, apparently successful child wait from overriding caller cancellation.
func TestCompletedSessionProcessWaitRechecksCancellationAtBothAdmissionBoundaries(t *testing.T) {
	for _, targetCheck := range []int{2, 3} {
		base, cancel := context.WithCancelCause(context.Background())
		done := make(chan struct{})
		close(done)
		ctx := &cancelAtErrCallContext{Context: base, cancel: cancel, target: targetCheck}
		process := &SessionProcess{waitDone: done}
		err := process.Wait(ctx, errInvalidArgument)
		if !errors.Is(err, context.Canceled) || !errors.Is(err, context.Cause(base)) || ctx.calls < targetCheck {
			t.Fatalf("cancel at check %d: got %v (checks=%d), want cancellation", targetCheck, err, ctx.calls)
		}
		cancel(nil)
	}
}

type cancelAtErrCallContext struct {
	context.Context
	cancel context.CancelCauseFunc
	target int
	calls  int
}

func (c *cancelAtErrCallContext) Err() error {
	c.calls++
	if c.calls == c.target {
		c.cancel(errors.New("caller withdrew cleanup authority"))
	}
	return c.Context.Err()
}

func TestProcessRuntimeOwnershipRequiresExactEnvironmentValue(t *testing.T) {
	procRoot := t.TempDir()
	const pid = 4242
	procDir := filepath.Join(procRoot, "4242")
	if err := os.Mkdir(procDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(procDir, "environ"), []byte("XDG_RUNTIME_DIR=/run/user/1000/pf-xdg-evil\x00PATH=/usr/bin\x00"), 0600); err != nil {
		t.Fatal(err)
	}
	if processUsesRuntimeDirAt(procRoot, 0, "/run/user/1000/pf-xdg") {
		t.Fatal("process without a PID was treated as a runtime owner")
	}
	if processUsesRuntimeDirAt(procRoot, pid, "") {
		t.Fatal("process without a runtime path was treated as a runtime owner")
	}
	if processUsesRuntimeDirAt(procRoot, pid, "/run/user/1000/pf-xdg") {
		t.Fatal("prefix-sharing runtime path was treated as an exact owner match")
	}
	if err := os.WriteFile(filepath.Join(procDir, "environ"), []byte("XDG_RUNTIME_DIR=/run/user/1000/pf-xdg\x00PATH=/usr/bin\x00"), 0600); err != nil {
		t.Fatal(err)
	}
	if !processUsesRuntimeDirAt(procRoot, pid, "/run/user/1000/pf-xdg") {
		t.Fatal("process with exact runtime ownership was not recognized")
	}
}

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

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

// A matching runtime name is not an ownership grant. Reaping a directory
// writable by untrusted accounts can delete files they contributed or modified.
func TestStaleCleanupRejectsNonprivateRuntimeDirectory(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	for _, tc := range []struct {
		name   string
		mode   os.FileMode
		remove bool
	}{
		{"private-runtime", 0700, true},
		{"group-writable-runtime", 0770, false},
		{"world-writable-runtime", 0777, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(root, "perfuncted-xdg-"+tc.name)
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(dir) })
			marker := filepath.Join(dir, "private-payload")
			if err := os.WriteFile(marker, []byte("preserve"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(dir, tc.mode); err != nil {
				t.Fatal(err)
			}
			if err := reapSessionDir(context.Background(), dir); err != nil {
				t.Fatal(err)
			}
			_, err := os.Stat(marker)
			if tc.remove && !os.IsNotExist(err) {
				t.Fatalf("private stale runtime not reaped: %v", err)
			}
			if !tc.remove && err != nil {
				t.Fatalf("untrusted writable runtime deleted: %v", err)
			}
		})
	}
}

func TestStaleSessionDiscoveryHonorsCancellationBetweenRoots(t *testing.T) {
	preferred := t.TempDir()
	if err := os.WriteFile(filepath.Join(preferred, "ordinary-entry"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", preferred)

	base, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	ctx := &cancelAtErrCallContext{Context: base, cancel: cancel, target: 3}
	dirs, err := staleSessionDirectories(ctx)
	if len(dirs) != 0 || !errors.Is(err, context.Canceled) || !errors.Is(err, context.Cause(base)) {
		t.Fatalf("discovery after cancellation between roots = dirs %v, err %v", dirs, err)
	}
}

func TestStaleSessionDiscoveryHonorsCancellationDuringEntryScan(t *testing.T) {
	entry, err := os.MkdirTemp("/tmp", "perfuncted-xdg-cancel-scan-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(entry) })
	t.Setenv("TMPDIR", "/tmp")

	base, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	ctx := &cancelAtErrCallContext{Context: base, cancel: cancel, target: 2}
	dirs, err := staleSessionDirectories(ctx)
	if len(dirs) != 0 || !errors.Is(err, context.Canceled) || !errors.Is(err, context.Cause(base)) {
		t.Fatalf("discovery after cancellation during entry scan = dirs %v, err %v", dirs, err)
	}
}

func TestStaleSessionDiscoveryScansFallbackWhenPreferredRootIsMissing(t *testing.T) {
	fallback, err := os.MkdirTemp("/tmp", "perfuncted-xdg-fallback-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(fallback) })
	preferred := filepath.Join(t.TempDir(), "removed-tmpdir")
	t.Setenv("TMPDIR", preferred)

	dirs, err := staleSessionDirectories(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(dirs, fallback) {
		t.Fatalf("discovery skipped short-root runtime %q when preferred root %q was missing: %v", fallback, preferred, dirs)
	}
}

// A previously enumerated managed runtime may become inaccessible before its
// inode is inspected. Fail closed: never consider a permission error evidence
// that the directory is stale or that its contents can safely be removed.
func TestReapManagedRuntimePreservesDirectoryWhenInspectionIsDenied(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses parent directory search permissions")
	}
	base := t.TempDir()
	preferred := filepath.Join(base, "inaccessible-parent")
	runtime := filepath.Join(preferred, "perfuncted-xdg-private-data")
	if err := os.MkdirAll(runtime, 0700); err != nil {
		t.Fatal(err)
	}
	payload := filepath.Join(runtime, "keep")
	if err := os.WriteFile(payload, []byte("foreign runtime content"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", preferred)
	if err := os.Chmod(preferred, 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(preferred, 0700); err != nil {
			t.Errorf("restore directory permissions: %v", err)
		}
	})
	err := reapSessionDir(context.Background(), runtime)
	if !errors.Is(err, os.ErrPermission) {
		t.Fatalf("inaccessible runtime not rejected: %v", err)
	}
	if err := os.Chmod(preferred, 0700); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(payload)
	if err != nil || string(data) != "foreign runtime content" {
		t.Fatalf("reaper damaged inaccessible runtime: data=%q err=%v", data, err)
	}
}

// A failed first mount scan leaves ownership of mounted subtrees unknown.
// Cleanup must fail before invoking any unmount helper, even if a partial
// listing accompanied the inspection failure.
func TestManagedUnmountRefusesIncompleteInitialMountInspection(t *testing.T) {
	const root = "/synthetic/perfuncted-xdg-unverified"
	inspectErr := errors.New("incomplete mount namespace snapshot")
	for _, tc := range []struct {
		name   string
		mounts []string
	}{
		{"no mount listing", nil},
		{"partial mount listing", []string{root + "/private"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reads, unmounts := 0, 0
			err := unmountSubdirsWith(context.Background(), root, func(dir string) ([]string, error) {
				if dir != root {
					t.Fatalf("inspected wrong directory: %q", dir)
				}
				reads++
				return tc.mounts, inspectErr
			}, func(context.Context, string) error { unmounts++; return nil })
			if !errors.Is(err, inspectErr) || !strings.Contains(err.Error(), "inspect mounts before cleanup") || reads != 1 || unmounts != 0 {
				t.Fatalf("unsafe cleanup after incomplete mount scan: err=%v reads=%d unmounts=%d", err, reads, unmounts)
			}
		})
	}
}

// Cancellation and a simultaneous mount-table error are independent reasons
// cleanup must stop. Preserve both so callers can distinguish cancellation
// from the inspection failure, and do not mutate any mount.
func TestManagedUnmountPreservesCancellationAndInitialInspectionFailure(t *testing.T) {
	const root = "/synthetic/perfuncted-xdg-canceled-mount-inspection"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	inspectErr := errors.New("mountinfo became unavailable")
	unmountCalls := 0
	err := unmountSubdirsWith(ctx, root, func(string) ([]string, error) {
		cancel()
		return []string{root + "/subtree"}, inspectErr
	}, func(context.Context, string) error { unmountCalls++; return nil })
	if !errors.Is(err, context.Canceled) || !errors.Is(err, inspectErr) || unmountCalls != 0 {
		t.Fatalf("canceled failed inspection lost causes or mutated mount: err=%v unmounts=%d", err, unmountCalls)
	}
}

// TMPDIR is a literal filesystem path, not a glob expression. A bracket in
// a caller-owned temp path must not make live cleanup skip its actual runtime.
func TestStaleRuntimeDiscoverySupportsLiteralMetacharactersInTempRoot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "private[unfinished")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", root)
	owned := filepath.Join(root, "perfuncted-xdg-a")
	if err := os.Mkdir(owned, 0700); err != nil {
		t.Fatal(err)
	}
	got, err := staleSessionDirectories(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(got, owned) {
		t.Fatalf("cleanup skipped managed runtime under literal TMPDIR %q: %+v", root, got)
	}
}

func TestStaleRuntimeDiscoveryDoesNotInterpretTempRootPatterns(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "sandbox[ab]")
	foreign := filepath.Join(parent, "sandboxa")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(foreign, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", root)
	owned := filepath.Join(root, "perfuncted-xdg-owned")
	intruder := filepath.Join(foreign, "perfuncted-xdg-foreign")
	for _, dir := range []string{owned, intruder} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	got, err := staleSessionDirectories(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(got, owned) || slices.Contains(got, intruder) {
		t.Fatalf("cleanup glob escaped literal root: owned=%q foreign=%q discovered=%v", owned, intruder, got)
	}
}

// Cleanup is permitted to inspect actual managed runtime directories, not
// old same-prefix files or symlinks that happen to appear in TMPDIR.
func TestStaleRuntimeDiscoveryIgnoresSamePrefixFilesAndSymlinks(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	owned := filepath.Join(root, "perfuncted-xdg-owned")
	file := filepath.Join(root, "perfuncted-xdg-not-a-runtime")
	link := filepath.Join(root, "perfuncted-xdg-link")
	if err := os.Mkdir(owned, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("do not delete"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(owned, link); err != nil {
		t.Fatal(err)
	}
	got, err := staleSessionDirectories(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(got, owned) || slices.Contains(got, file) || slices.Contains(got, link) {
		t.Fatalf("cleanup accepted non-directories: %v", got)
	}
}

// A matching name is not enough to own a filesystem object. A runtime that
// disappeared or was replaced after discovery must not be deleted if the
// replacement is now a same-prefix regular file or symlink.
func TestReapSessionDirRefusesNondirectoryReplacement(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TMPDIR", root)
	outside := filepath.Join(root, "private-original")
	if err := os.WriteFile(outside, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"regular file", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(root, "perfuncted-xdg-"+kind)
			var err error
			if kind == "regular file" {
				err = os.WriteFile(path, []byte("keep this"), 0600)
			} else {
				err = os.Symlink(outside, path)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := reapSessionDir(context.Background(), path); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(path); err != nil {
				t.Fatalf("cleanup deleted non-directory %q: %v", path, err)
			}
			if data, err := os.ReadFile(outside); err != nil || string(data) != "preserve" {
				t.Fatalf("foreign file changed: %q %v", data, err)
			}
		})
	}
}

func TestParseSessionSubdirMountsDecodesKernelEscapes(t *testing.T) {
	root := "/tmp/perfuncted-xdg-abc with space"
	info := []byte("24 23 0:12 / /tmp/perfuncted-xdg-abc\\040with\\040space rw - tmpfs tmpfs rw\n" +
		"25 24 0:12 / /tmp/perfuncted-xdg-abc\\040with\\040space/inner\\040dir rw - tmpfs tmpfs rw\n" +
		"26 23 0:12 / /tmp/perfuncted-xdg-abc\\040with\\040space-other rw - tmpfs tmpfs rw\n")
	got := ParseSessionSubdirMounts(root, info)
	want := []string{root, root + "/inner dir"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("escaped mountpoints: got %q, want %q", got, want)
	}
}

func TestUnmountSubdirsWithPreservesUnmountedState(t *testing.T) {
	const mount = "/tmp/perfuncted-xdg-test/nested"
	tests := []struct {
		name       string
		after      []string
		unmountErr error
		wantErr    bool
	}{
		{"mount still present", []string{mount}, errors.New("fusermount failed"), true},
		{"mount actually gone despite helper error", nil, errors.New("mount raced away"), false},
		{"normal successful unmount", nil, nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reads, calls := 0, 0
			list := func(string) ([]string, error) {
				reads++
				if reads == 1 {
					return []string{mount}, nil
				}
				return tc.after, nil
			}
			err := unmountSubdirsWith(context.Background(), "/tmp/perfuncted-xdg-test", list,
				func(_ context.Context, path string) error {
					calls++
					if path != mount {
						t.Fatalf("wrong unmount target %q", path)
					}
					return tc.unmountErr
				})
			if (err != nil) != tc.wantErr {
				t.Fatalf("error=%v wantErr=%v", err, tc.wantErr)
			}
			if tc.wantErr && !strings.Contains(err.Error(), "mount") {
				t.Fatalf("unclassified error: %v", err)
			}
			if calls != 1 || reads != 2 {
				t.Fatalf("calls=%d reads=%d", calls, reads)
			}
		})
	}
}

func TestUnmountSubdirsWithHonorsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	err := unmountSubdirsWith(ctx, "/tmp/perfuncted-xdg-test",
		func(string) ([]string, error) { return []string{"/tmp/perfuncted-xdg-test/a"}, nil },
		func(_ context.Context, _ string) error { calls++; cancel(); return context.Canceled })
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestUnmountSubdirsWithDeepestFirst(t *testing.T) {
	const root = "/tmp/perfuncted-xdg-nested"
	original := []string{root + "/child/grandchild", root, root + "/child"}
	var order []string
	rounds := 0
	err := unmountSubdirsWith(context.Background(), root,
		func(string) ([]string, error) {
			rounds++
			if rounds == 1 {
				return original, nil
			}
			return nil, nil
		},
		func(_ context.Context, m string) error { order = append(order, m); return nil })
	if err != nil {
		t.Fatal(err)
	}
	want := []string{root + "/child/grandchild", root + "/child", root}
	if !reflect.DeepEqual(order, want) {
		t.Fatalf("unsafe unmount order: %v, want %v", order, want)
	}
}

func TestUnmountSubdirsWithFailsClosedWhenMountsCannotBeRead(t *testing.T) {
	want := errors.New("mountinfo unavailable")
	invoked := false
	err := unmountSubdirsWith(context.Background(), "/tmp/perfuncted-xdg-test",
		func(string) ([]string, error) { return nil, want },
		func(_ context.Context, _ string) error { invoked = true; return nil })
	if !errors.Is(err, want) || invoked {
		t.Fatalf("err=%v invoked=%v", err, invoked)
	}
}

// A stale cleanup that cannot remove an owned directory must report failure;
// otherwise the scan suppresses future attempts for its normal throttle window.
func TestReapSessionDirReportsRemovalFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires unprivileged filesystem permissions")
	}
	dir, err := os.MkdirTemp("", "perfuncted-xdg-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.Chmod(dir, 0700)
		_ = os.RemoveAll(dir)
	}()
	if lintShadow127 := os.WriteFile(filepath.Join(dir, "sentinel"), []byte("keep"), 0600); lintShadow127 != nil {
		t.Fatal(lintShadow127)
	}
	if lintShadow130 := os.Chmod(dir, 0500); lintShadow130 != nil {
		t.Fatal(lintShadow130)
	}
	err = reapSessionDir(context.Background(), dir)
	if err == nil {
		t.Fatal("reap reported success despite permission-denied removal")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "sentinel")); statErr != nil {
		t.Fatalf("stale cleanup lost sentinel despite failed removal: %v", statErr)
	}
}

func TestUnmountCleanupRetainsConcurrentCancellationAndMountFailure(t *testing.T) {
	const root = "/synthetic/perfuncted-xdg-cancel-error"
	cases := []struct {
		name  string
		stage string
	}{
		{"initial mount scan", "initial"},
		{"unmount helper", "unmount"},
		{"verification scan", "verify"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			failure := errors.New("synthetic mount subsystem error")
			reads, unmounts := 0, 0
			err := unmountSubdirsWith(ctx, root, func(string) ([]string, error) {
				reads++
				if tc.stage == "initial" {
					cancel()
					return nil, failure
				}
				if reads == 1 {
					return []string{root + "/nested"}, nil
				}
				if tc.stage == "verify" {
					cancel()
					return nil, failure
				}
				return nil, nil
			}, func(context.Context, string) error {
				unmounts++
				if tc.stage == "unmount" {
					cancel()
					return failure
				}
				return nil
			})
			if !errors.Is(err, context.Canceled) || !errors.Is(err, failure) {
				t.Fatalf("lost cancellation or underlying mount error: %v", err)
			}
			if tc.stage == "initial" && unmounts != 0 {
				t.Fatalf("unmounted after failed initial scan: %d", unmounts)
			}
		})
	}
}

// A mount query may return successfully at the same instant a caller cancels.
// An empty reply is not permission to report completion after cancellation.
func TestManagedMountCleanupNeverAcknowledgesCancelledMountScan(t *testing.T) {
	const root = "/synthetic/perfuncted-xdg-unmount"
	for _, tc := range []struct {
		name         string
		cancelOnRead int
	}{
		{"initial scan", 1},
		{"post-unmount verification", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			reads := 0
			unmounts := 0
			err := unmountSubdirsWith(ctx, root,
				func(_ string) ([]string, error) {
					reads++
					if reads == tc.cancelOnRead {
						cancel()
					}
					if reads == 1 && tc.cancelOnRead == 2 {
						return []string{root + "/private-mount"}, nil
					}
					return nil, nil
				},
				func(context.Context, string) error { unmounts++; return nil },
			)
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled mount scan acknowledged success: reads=%d unmounts=%d err=%v", reads, unmounts, err)
			}
			if reads != tc.cancelOnRead {
				t.Fatalf("incorrect scan count: got=%d want=%d", reads, tc.cancelOnRead)
			}
			if tc.cancelOnRead == 1 && unmounts != 0 {
				t.Fatalf("initial canceled scan performed %d unmounts", unmounts)
			}
			if tc.cancelOnRead == 2 && unmounts != 1 {
				t.Fatalf("fixture did not reach verification after one unmount: %d", unmounts)
			}
		})
	}
}

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
	mounts, err := ReadSessionSubdirMounts(dir)
	if err != nil {
		t.Fatalf("read actual kernel mountinfo: %v", err)
	}
	if len(mounts) != 0 {
		t.Skipf("temporary directory unexpectedly contains mounts: %q", mounts)
	}
	if err := UnmountSubdirsContext(context.Background(), dir); err != nil {
		t.Fatalf("unmounted runtime cleanup refused: %v", err)
	}
	got, err := os.ReadFile(sentinel)
	if err != nil || string(got) != data {
		t.Fatalf("unmount changed unrelated session data: %q, %v", got, err)
	}
}

// A successful unmount helper is not proof that mounted filesystems have gone
// away. If the verification scan fails, removing the runtime tree is unsafe.
func TestManagedUnmountRefusesSuccessWhenVerificationCannotReadMounts(t *testing.T) {
	const root = "/synthetic/perfuncted-xdg-verification"
	const child = root + "/sensitive-mounted-data"
	unavailable := errors.New("synthetic mountinfo verification failure")
	reads, unmounts := 0, 0
	err := unmountSubdirsWith(context.Background(), root,
		func(dir string) ([]string, error) {
			if dir != root {
				t.Fatalf("unexpected scan root: %q", dir)
			}
			reads++
			if reads == 1 {
				return []string{child}, nil
			}
			return nil, unavailable
		},
		func(_ context.Context, mount string) error {
			if mount != child {
				t.Fatalf("unexpected unmount: %q", mount)
			}
			unmounts++
			return nil
		})
	if !errors.Is(err, unavailable) || !strings.Contains(err.Error(), "verify mounts") {
		t.Fatalf("verification failure was treated as safe cleanup: %v", err)
	}
	if reads != 2 || unmounts != 1 {
		t.Fatalf("verification boundary not exercised: scans=%d unmounts=%d", reads, unmounts)
	}
}

// The production unmount entry point must honor cancellation before mount
// inspection. The managed runtime and its contents must remain untouched.
func TestUnmountSubdirsContextCanceledPreservesRuntime(t *testing.T) {
	root := t.TempDir()
	sentinel := filepath.Join(root, "owned-by-other-operation")
	if err := os.WriteFile(sentinel, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	cause := errors.New("synthetic caller revocation")
	ctx, cancel := context.WithCancelCause(context.Background())
	cancel(cause)
	if err := UnmountSubdirsContext(ctx, root); !errors.Is(err, context.Canceled) || !errors.Is(err, cause) {
		t.Fatalf("canceled mount cleanup returned %v, want context cancellation and cause", err)
	}
	data, err := os.ReadFile(sentinel)
	if err != nil || string(data) != "preserve" {
		t.Fatalf("canceled mount cleanup altered runtime: content=%q error=%v", data, err)
	}
}

func TestUnmountSubdirs_NonExistentDir(t *testing.T) {
	// Should not panic or error on non-existent directory
	if err := UnmountSubdirsContext(context.Background(), "/nonexistent/path/that/does/not/exist"); err != nil {
		t.Fatalf("unexpected unmount inspection error: %v", err)
	}
}

func TestUnmountSubdirs_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	// Should not panic or error on a temp dir (no FUSE mounts under it)
	if err := UnmountSubdirsContext(context.Background(), dir); err != nil {
		t.Fatalf("unexpected unmount inspection error: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("dir was removed after unmountSubdirs: %v", err)
	}
}

func TestUnmountSubdirs_EmptyString(t *testing.T) {
	// Should not panic or error on empty string
	if err := UnmountSubdirsContext(context.Background(), ""); err != nil {
		t.Fatalf("unexpected unmount inspection error: %v", err)
	}
}

func TestUnmountSubdirs_FileInsteadOfDir(t *testing.T) {
	f, err := os.CreateTemp("", "unmount-test-*")
	if err != nil {
		t.Fatal(err)
	}
	path := f.Name()
	f.Close()
	defer os.Remove(path)

	// Should not panic when called with a file path
	if err := UnmountSubdirsContext(context.Background(), path); err != nil {
		t.Fatalf("unexpected unmount inspection error: %v", err)
	}
}

// Cancel on the first check made directly by a particular cleanup phase.
// The decision stays independent of extra entries in a shared /tmp root.
type cancelAtCleanupPhaseContext struct {
	context.Context
	cancel    context.CancelCauseFunc
	phase     string
	triggered bool
}

func (c *cancelAtCleanupPhaseContext) Err() error {
	if !c.triggered {
		pc, _, _, ok := runtime.Caller(1)
		if ok {
			fn := runtime.FuncForPC(pc)
			if fn != nil && strings.HasSuffix(fn.Name(), "."+c.phase) {
				c.triggered = true
				c.cancel(errors.New("caller revoked cleanup before phase dispatch"))
			}
		}
	}
	return c.Context.Err()
}

// Discovery is not permission to reclaim any of its results. Even when a
// managed directory has been identified, cancellation before the first reap
// check or during metadata/grace inspection must preserve the filesystem.
func TestStaleSessionScanPreservesDiscoveredRuntimeAfterCallerRevocation(t *testing.T) {
	for _, phase := range []string{"ScanStaleSessions", "reapSessionDirWhenOlderThan"} {
		t.Run(phase, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("TMPDIR", root)
			runtimeDir := filepath.Join(root, "perfuncted-xdg-private")
			if err := os.Mkdir(runtimeDir, 0700); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(runtimeDir, "private-marker")
			if err := os.WriteFile(marker, []byte("retain"), 0600); err != nil {
				t.Fatal(err)
			}
			owner, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			ctx := &cancelAtCleanupPhaseContext{Context: owner, cancel: cancel, phase: phase}
			err := ScanStaleSessions(ctx, time.Hour)
			if !ctx.triggered || !errors.Is(err, context.Canceled) || !errors.Is(err, context.Cause(owner)) {
				t.Fatalf("phase %s ignored caller revocation: triggered=%v err=%v", phase, ctx.triggered, err)
			}
			got, readErr := os.ReadFile(marker)
			if readErr != nil || string(got) != "retain" {
				t.Fatalf("phase %s deleted private runtime after cancellation: data=%q err=%v", phase, got, readErr)
			}
		})
	}
}
