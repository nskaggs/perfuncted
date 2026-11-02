package accessibility

import (
	"context"
	"errors"
	"os"
	"testing"
)

// Exercise genuine private AT-SPI D-Bus identity reads, never a host desktop.
// Correlation must not trust unrelated roles, guessed PIDs, or incomplete
// application enumeration. Both cached and fresh paths remain distinguishable.
func TestPrivateATSPIWindowScopeIdentityAdversarialMatrix(t *testing.T) {
	for _, tc := range []struct {
		name       string
		editNode   func(*syntheticATSPINode, *syntheticATSPINode)
		editTarget func(*WindowTarget)
		wantErr    error
		freshOnly  bool
	}{
		{name: "matching frame"},
		{name: "window role", editNode: func(_ *syntheticATSPINode, f *syntheticATSPINode) { f.role = 69 }},
		{name: "unrelated child role", editNode: func(_ *syntheticATSPINode, f *syntheticATSPINode) { f.role = 43 }, wantErr: ErrNotFound},
		{name: "missing title", editNode: func(_ *syntheticATSPINode, f *syntheticATSPINode) { f.name = ""; f.description = "" }, wantErr: ErrNotFound},
		{name: "different title", editNode: func(_ *syntheticATSPINode, f *syntheticATSPINode) {
			f.name = "Other application"
			f.description = "Other"
		}, wantErr: ErrNotFound},
		{name: "PID hint", editTarget: func(target *WindowTarget) { target.PIDHint = target.PID; target.PID = 0 }},
		{name: "foreign PID", editTarget: func(target *WindowTarget) { target.PID = int32(os.Getpid() + 99999) }, wantErr: ErrNotFound},
		{name: "unsupported ID only", editTarget: func(target *WindowTarget) { *target = WindowTarget{ID: target.ID} }, wantErr: ErrUnsupportedCorrelation},
		{name: "empty ID", editTarget: func(target *WindowTarget) { target.ID = " \t " }, wantErr: ErrNotFound},
		{name: "oversized desktop child list", editNode: func(root, _ *syntheticATSPINode) {
			ref := root.children[0]
			root.children = make([]objectRef, maxApplications+1)
			for i := range root.children {
				root.children[i] = ref
			}
		}, wantErr: ErrIncompleteSnapshot, freshOnly: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend, frameID, target := newSyntheticATSPIBackendWithFrameSetup(t, false, tc.editNode)
			if tc.editTarget != nil {
				tc.editTarget(&target)
			}
			for _, fresh := range []bool{false, true} {
				if tc.freshOnly && !fresh {
					continue
				}
				variant := "cached"
				if fresh {
					variant = "fresh"
				}
				t.Run(variant, func(t *testing.T) {
					var scope WindowScope
					var err error
					if fresh {
						scope, err = backend.ResolveWindowFresh(context.Background(), target)
					} else {
						scope, err = backend.ResolveWindow(context.Background(), target)
					}
					if tc.wantErr != nil {
						if !errors.Is(err, tc.wantErr) || scope.Root.BusName != "" {
							t.Fatalf("untrusted correlation scope=%+v error=%v, want %v", scope, err, tc.wantErr)
						}
					} else if err != nil || scope.Root != frameID || scope.ApplicationRoot.BusName == "" {
						t.Fatalf("authorized target did not resolve to its frame: %+v error=%v", scope, err)
					}
				})
			}
		})
	}
}

func TestWindowFreshIdentityRejectsStaleApplicationAndNilContext(t *testing.T) {
	backend := &dbusBackend{generation: 7}
	target := WindowTarget{ID: "win-7", PID: 42, Title: "Private"}
	app := Application{Node: Node{ID: NodeID{BusName: "org.example.App", ObjectPath: "/app", Generation: 6}}, PID: 42}
	if _, _, err := backend.windowCandidatesFresh(context.Background(), target, []Application{app}); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("stale app generation accepted: %v", err)
	}
	var absentContext context.Context
	if _, err := backend.windowApplicationsFresh(absentContext, target); err == nil {
		t.Fatal("nil fresh identity read context accepted")
	}
	if _, err := backend.windowApplications(absentContext, target); err == nil {
		t.Fatal("nil cached identity read context accepted")
	}
}
