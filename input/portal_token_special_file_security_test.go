//go:build linux

package input

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// A hostile session can replace the grant file with a FIFO. Consumption must
// fail immediately, without waiting for a writer or accepting its data.
func TestPortalTokenConsumeRejectsFIFOWithoutBlockingOrLeakingGrant(t *testing.T) { //nolint:gocyclo // Validate rejection, timing, retained foreign object, and atomic replacement together.
	runtime := portalTokenTestRuntime(t)
	lock, err := lockPortalTokenStore(runtime)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	filename := filepath.Join(runtime.Get("XDG_STATE_HOME"), "perfuncted", portalRestoreTokenFile)
	if mkErr := unix.Mkfifo(filename, 0600); mkErr != nil {
		t.Fatal(mkErr)
	}
	started := time.Now()
	got, consumeErr := lock.consume()
	if got != "" || consumeErr == nil {
		t.Fatalf("FIFO was accepted as a restore credential: token=%q error=%v", got, consumeErr)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("FIFO read blocked for %s", elapsed)
	}
	if !strings.Contains(consumeErr.Error(), "restore-token") && !errors.Is(consumeErr, unix.ENXIO) {
		t.Fatalf("unexpected FIFO rejection error: %v", consumeErr)
	}
	fi, err := os.Lstat(filename)
	if err != nil || fi.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("failed validation unexpectedly altered foreign FIFO: %v %v", fi, err)
	}
	// Storage must atomically replace, rather than open and write, an untrusted
	// preexisting FIFO when the portal supplies a new authorized grant.
	if storeErr := lock.store("fresh-private-authorization"); storeErr != nil {
		t.Fatalf("atomic replacement of FIFO failed: %v", storeErr)
	}
	fi, err = os.Lstat(filename)
	if err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm() != 0600 {
		t.Fatalf("replacement was not a private regular file: %v %v", fi, err)
	}
	if got, err := lock.consume(); err != nil || got != "fresh-private-authorization" {
		t.Fatalf("fresh token not recovered: %q %v", got, err)
	}
}
