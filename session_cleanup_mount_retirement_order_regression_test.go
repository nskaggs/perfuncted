package perfuncted

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

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
