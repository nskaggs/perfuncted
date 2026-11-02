//go:build linux

package input

import (
	"context"
	"strings"
	"testing"
)

// An EIS key-down belongs to one caller-held key. A second key-down for an
// already-owned key, whether in the same Type batch or from KeyDown, must
// never re-inject a press (or leak earlier typed content from that batch).
func TestRemoteDesktopTypeRejectsRepeatedKeyDownBeforeEmittingAnyBatchFrames(t *testing.T) {
	for _, tc := range []struct{ name, key, batch string }{
		{name: "duplicate within batch", batch: "private{a down}{a down}"},
		{name: "duplicate modifier within batch", batch: "{ctrl down}{ctrl down}"},
		{name: "duplicate previously held key", key: "a", batch: "private{a down}"},
		{name: "duplicate previously held modifier", key: "ctrl", batch: "{a}{ctrl down}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, peer := newWireObservedRemoteDesktop(t)
			if tc.key != "" {
				if err := b.KeyDown(context.Background(), tc.key); err != nil {
					t.Fatal(err)
				}
				for range 2 {
					if _, err := readSyntheticEISFrame(peer); err != nil {
						t.Fatal(err)
					}
				}
			}
			before := len(b.heldKeys)
			err := b.Type(context.Background(), tc.batch)
			if err == nil || !strings.Contains(err.Error(), "already held") {
				t.Fatalf("duplicate EIS press accepted: %v", err)
			}
			assertNoEIWireRequest(t, peer)
			if len(b.heldKeys) != before {
				t.Fatalf("denied batch mutated held state: %+v", b.heldKeys)
			}
			if err := b.stateError(); err != nil {
				t.Fatalf("preflight revoked EIS session: %v", err)
			}
		})
	}
}
