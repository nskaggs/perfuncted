package sessionaccessibility

import (
	"errors"
	"github.com/nskaggs/perfuncted/v2/accessibility"
	"github.com/nskaggs/perfuncted/v2/internal/capability"
	"reflect"
	"testing"
)

// A semantic target must not be treated as unchanged when its PID/bus/app
// identity disappears and only a non-unique human-readable name remains.
func TestLocatorIdentityRejectsAuthoritativeEvidenceDowngrade(t *testing.T) {
	initial := LocatorIdentity{PID: 412, BusName: ":1.412", AppID: "org.editor.App", Name: "Editor"}
	cases := []struct {
		name    string
		current LocatorIdentity
		same    bool
	}{
		{"unchanged strong identity", initial, true},
		{"PID retained while optional app missing", LocatorIdentity{PID: 412, Name: "Editor"}, true},
		{"PID and bus lost, name retained", LocatorIdentity{Name: "Editor"}, false},
		{"PID lost, same app id", LocatorIdentity{AppID: "org.editor.App", Name: "Editor"}, true},
		{"PID lost, different app id", LocatorIdentity{AppID: "org.other.App", Name: "Editor"}, false},
		{"bus lost, name retained", LocatorIdentity{Name: "Editor", AppID: "org.editor.App"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := SameLocatorScope(initial, tc.current); got != tc.same {
				t.Fatalf("identity downgrade accepted=%v want=%v initial=%+v current=%+v", got, tc.same, initial, tc.current)
			}
		})
	}
}

// A locator must never accept a target after an invalidation, a closed
// subscription, or a missing subscription. An empty live stream is the only
// observation state that permits the caller to proceed with a fresh read.
func TestLocatorObservationContractRejectsMissingClosedAndInvalidatedStreams(t *testing.T) {
	live := make(chan accessibility.Event, 1)
	closed := make(chan accessibility.Event)
	close(closed)
	invalidated := make(chan accessibility.Event, 1)
	invalidated <- accessibility.Event{Kind: "object:children-changed"}
	for _, tc := range []struct {
		name        string
		stream      <-chan accessibility.Event
		wantChanged bool
	}{
		{name: "nil subscription", wantChanged: true},
		{name: "closed subscription", stream: closed, wantChanged: true},
		{name: "queued invalidation", stream: invalidated, wantChanged: true},
		{name: "live empty subscription", stream: live},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := LocatorObservationReady(tc.stream)
			if got := errors.Is(err, accessibility.ErrObservationChanged); got != tc.wantChanged {
				t.Fatalf("observation error = %v; changed = %t, want %t", err, got, tc.wantChanged)
			}
			if !tc.wantChanged && err != nil {
				t.Fatalf("live observation unexpectedly rejected: %v", err)
			}
		})
	}
}

func TestLocatorRetriesOnlyObservationAndStaleIdentityFailures(t *testing.T) {
	for _, tc := range []struct {
		name  string
		err   error
		retry bool
	}{
		{"stale generation", errors.Join(errors.New("wrapped"), accessibility.ErrStaleGeneration), true},
		{"stale node", errors.Join(errors.New("wrapped"), accessibility.ErrStaleNode), true},
		{"observation changed", errors.Join(errors.New("wrapped"), accessibility.ErrObservationChanged), true},
		{"unsupported", accessibility.ErrUnsupported, false},
		{"invalid argument", capability.ErrInvalidArgument, false},
		{"generic provider failure", errors.New("provider unavailable"), false},
		{"success", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := LocatorResolutionMayRetry(tc.err); got != tc.retry {
				t.Fatalf("retry(%v) = %t, want %t", tc.err, got, tc.retry)
			}
		})
	}
}

// Comparing consecutive provider observations must reject both target
// replacement and disappearance of previously verified identity evidence.
func TestLocatorIdentityChangeRequiresStableScopeAndRoot(t *testing.T) {
	root := accessibility.NodeID{BusName: "org.example.Editor", ObjectPath: "/app", Generation: 4}
	initial := LocatorIdentity{WindowID: "win-1", PID: 220, AppID: "org.example.Editor", BusName: root.BusName, Name: "Editor"}
	for _, tc := range []struct {
		name                     string
		initial, current         LocatorIdentity
		initialRoot, currentRoot accessibility.NodeID
		have                     bool
		changed                  bool
	}{
		{name: "uninitialized observation", initial: initial, current: LocatorIdentity{WindowID: "win-2"}, initialRoot: root, currentRoot: root, have: false},
		{name: "same root and identity", initial: initial, current: initial, initialRoot: root, currentRoot: root, have: true},
		{name: "different managed window", initial: initial, current: LocatorIdentity{WindowID: "win-2", PID: 220, BusName: root.BusName}, initialRoot: root, currentRoot: root, have: true, changed: true},
		{name: "different root path", initial: initial, current: initial, initialRoot: root, currentRoot: accessibility.NodeID{BusName: root.BusName, ObjectPath: "/replacement", Generation: 4}, have: true, changed: true},
		{name: "different root bus", initial: initial, current: initial, initialRoot: root, currentRoot: accessibility.NodeID{BusName: "org.example.Replacement", ObjectPath: root.ObjectPath, Generation: 4}, have: true, changed: true},
		{name: "lost authoritative identity", initial: initial, current: LocatorIdentity{Name: "Editor", WindowID: "win-1"}, initialRoot: root, currentRoot: root, have: true, changed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := LocatorIdentityChanged(tc.have, tc.initial, tc.initialRoot, tc.current, tc.currentRoot); got != tc.changed {
				t.Fatalf("identity changed=%v want=%v", got, tc.changed)
			}
		})
	}
}

func TestLocatorScopeIdentityNeverAcceptsDifferentAuthoritativeTargets(t *testing.T) {
	editor := LocatorIdentity{WindowID: "17", PID: 101, AppID: "org.editor", BusName: "org.editor.Bus", Name: "Editor"}
	for _, tc := range []struct {
		name          string
		first, second LocatorIdentity
		same          bool
	}{
		{"same authoritative identifiers", editor, editor, true},
		{"different managed window", editor, LocatorIdentity{WindowID: "18", PID: 101, AppID: "org.editor", BusName: "org.editor.Bus"}, false},
		{"different bus owner", editor, LocatorIdentity{WindowID: "17", PID: 101, AppID: "org.editor", BusName: "org.editor.New"}, false},
		{"different process", editor, LocatorIdentity{WindowID: "17", PID: 102, AppID: "org.editor", BusName: "org.editor.Bus"}, false},
		{"same process different app", editor, LocatorIdentity{WindowID: "17", PID: 101, AppID: "org.other", BusName: "org.editor.Bus"}, false},
		{"optional app absent", editor, LocatorIdentity{WindowID: "17", PID: 101, BusName: "org.editor.Bus"}, true},
		{"app fallback agreement", LocatorIdentity{AppID: "org.editor"}, LocatorIdentity{AppID: "org.editor"}, true},
		{"app fallback mismatch", LocatorIdentity{AppID: "org.editor"}, LocatorIdentity{AppID: "org.other"}, false},
		{"bus fallback agreement", LocatorIdentity{BusName: "org.editor.Bus"}, LocatorIdentity{BusName: "org.editor.Bus"}, true},
		{"bus fallback mismatch", LocatorIdentity{BusName: "org.editor.Bus"}, LocatorIdentity{BusName: "org.other.Bus"}, false},
		{"name fallback case insensitive", LocatorIdentity{Name: " Editor "}, LocatorIdentity{Name: "editor"}, true},
		{"name fallback mismatch", LocatorIdentity{Name: "Editor"}, LocatorIdentity{Name: "Browser"}, false},
		{"no identity evidence", LocatorIdentity{}, LocatorIdentity{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := SameLocatorScope(tc.first, tc.second)
			if got != tc.same {
				t.Fatalf("SameLocatorScope(%+v,%+v)=%v, want %v", tc.first, tc.second, got, tc.same)
			}
		})
	}
}

func TestLocatorDispatchReceiptClassificationAndSelectionAreFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want accessibility.DispatchOutcome
	}{
		{"successful", nil, accessibility.DispatchAccepted},
		{"stale generation", fmtWrap(accessibility.ErrStaleGeneration), accessibility.DispatchNotSent},
		{"stale node", fmtWrap(accessibility.ErrStaleNode), accessibility.DispatchNotSent},
		{"unsupported", fmtWrap(accessibility.ErrUnsupported), accessibility.DispatchNotSent},
		{"rejected mutation", fmtWrap(accessibility.ErrMutationRejected), accessibility.DispatchRejected},
		{"uncertain transport", errors.New("provider dropped the reply"), accessibility.DispatchUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := TextDispatchOutcome(tc.err); got != tc.want {
				t.Fatalf("dispatch=%v want=%v", got, tc.want)
			}
		})
	}
	target := accessibility.Node{ID: accessibility.NodeID{BusName: "org.editor.Bus", ObjectPath: "/target", Generation: 5}, Name: "Save"}
	for _, tc := range []struct {
		name  string
		nodes []accessibility.Node
		want  error
	}{
		{"no result", nil, accessibility.ErrNotFound},
		{"unique result", []accessibility.Node{target}, nil},
		{"ambiguous result", []accessibility.Node{target, target}, accessibility.ErrAmbiguous},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node, err := UniqueLocatorMatch(tc.nodes)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error=%v want=%v", err, tc.want)
			}
			if tc.want == nil && !reflect.DeepEqual(node, target) {
				t.Fatalf("node=%+v want=%+v", node, target)
			}
			if tc.want != nil && node.ID.BusName != "" {
				t.Fatalf("error leaked target %+v", node)
			}
		})
	}
}

func fmtWrap(err error) error { return errors.Join(errors.New("outer"), err) }
