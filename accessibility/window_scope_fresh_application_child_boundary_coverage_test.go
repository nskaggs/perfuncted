package accessibility

import (
	"context"
	"errors"
	"testing"
)

// Once an application has been correlated to a managed process, fresh window
// resolution must still refuse an incomplete or unbounded child enumeration.
// A private AT-SPI frame acts as the application object for this direct
// candidate-enumeration boundary test; no host accessibility service is used.
func TestFreshWindowCandidatesRejectFaultedAndOversizedChildEnumeration(t *testing.T) {
	for _, tc := range []struct {
		name      string
		configure func(*syntheticATSPINode)
		want      error
	}{
		{"provider read failure", func(frame *syntheticATSPINode) {
			frame.childrenFailure = "permission revoked during window enumeration"
		}, nil},
		{"oversized window child list", func(frame *syntheticATSPINode) {
			frame.children = make([]objectRef, maxApplications+1)
			for i := range frame.children {
				frame.children[i] = objectRef{BusName: registryName, ObjectPath: "/org/a11y/atspi/accessible/editor"}
			}
		}, ErrIncompleteSnapshot},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend, id, _ := newSyntheticATSPIBackendWithFrameSetup(t, false, func(_, frame *syntheticATSPINode) { tc.configure(frame) })
			app := Application{Node: Node{ID: id}, PID: 4123}
			target := WindowTarget{ID: "managed-frame", PID: 4123}
			if !windowAppMatches(app, target) {
				t.Fatal("test application not matched to authoritative process")
			}
			candidates, observed, err := backend.windowCandidatesFresh(context.Background(), target, []Application{app})
			if err == nil || len(candidates) != 0 || len(observed) != 0 {
				t.Fatalf("bad child enumeration admitted candidates: candidates=%+v observed=%+v err=%v", candidates, observed, err)
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("error %v, want %v", err, tc.want)
			}
		})
	}
}
