//go:build linux

package input

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

// A one-shot D-Bus failure while releasing a tapped key must not leave the
// key logically pressed. A bounded best-effort release should run even when
// the operation failed, while preserving the original provider failure.
type failFirstGnomeKeyRelease struct {
	recordingGnomeBridge
	releaseFailed bool
	calls         []string
}

func (b *failFirstGnomeKeyRelease) Key(ctx context.Context, keyval uint32, down bool) error {
	b.calls = append(b.calls, fmt.Sprintf("%d:%t", keyval, down))
	if !down && !b.releaseFailed {
		b.releaseFailed = true
		return errors.New("synthetic GNOME key release failure")
	}
	return nil
}
func TestGnomeNativeTapRetriesTransientFailedReleaseWithoutHidingError(t *testing.T) {
	for _, seq := range []string{"{enter}", "{ctrl+a}"} {
		t.Run(seq, func(t *testing.T) {
			bridge := &failFirstGnomeKeyRelease{}
			b := &GnomeNativeBackend{bridge: bridge}
			err := b.Type(context.Background(), seq)
			if err == nil {
				t.Fatalf("failed key release acknowledged: events=%v", bridge.calls)
			}
			keyval := uint32(0xff0d)
			if seq == "{ctrl+a}" {
				keyval = uint32('a')
			}
			release := fmt.Sprintf("%d:false", keyval)
			count := 0
			for _, call := range bridge.calls {
				if call == release {
					count++
				}
			}
			if count != 2 {
				t.Fatalf("failed release not retried: release calls=%d wire=%v error=%v", count, bridge.calls, err)
			}
			if b.held.any() {
				t.Fatalf("transient failure stranded modifiers: %+v", b.held)
			}
		})
	}
}
