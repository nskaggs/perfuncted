package perfuncted

import (
	"context"
	"errors"
	"strings"
	"testing"
)

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
