package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
