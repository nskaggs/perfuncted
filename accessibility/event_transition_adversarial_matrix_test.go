package accessibility

import (
	"fmt"
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestATSPIMalformedLifecycleSignalsInvalidateHandlesWithoutAffectingLiveDesktop(t *testing.T) {
	for _, tc := range []struct {
		name, signal string
		body         []any
	}{
		{"cache removal malformed", cacheIface + ":RemoveAccessible", []any{struct{}{}}},
		{"cache removal spoofed bus", cacheIface + ":RemoveAccessible", []any{[]any{"org.spoofed.App", dbus.ObjectPath("/node")}}},
		{"children removal missing ref", "org.a11y.atspi.Event.Object:ChildrenChanged", []any{"remove", int32(0), int32(0), "invalid", map[string]dbus.Variant{}}},
		{"children removal wrong bus", "org.a11y.atspi.Event.Object:ChildrenChanged", []any{"remove", int32(0), int32(0), []any{"org.spoofed.App", dbus.ObjectPath("/node")}, map[string]dbus.Variant{}}},
		{"defunct unknown state", "org.a11y.atspi.Event.Object:StateChanged", []any{"defunct", "untrusted", int32(0), dbus.MakeVariant(""), map[string]dbus.Variant{}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &dbusBackend{generation: 7, cacheItems: map[NodeID]cacheItem{{BusName: "org.example.App", ObjectPath: "/node", Generation: 7, Incarnation: 1}: {Name: "live"}}, cacheApps: map[string]bool{"org.example.App": true}}
			sig := &dbus.Signal{Name: tc.signal, Sender: "org.example.App", Path: dbus.ObjectPath("/node"), Body: tc.body}
			b.transitionObjectHandlesLocked(sig)
			if b.generation != 8 || b.cacheItems != nil || b.cacheApps != nil {
				t.Fatalf("malformed signal %s published stale nodes: gen=%d items=%v apps=%v", tc.name, b.generation, b.cacheItems, b.cacheApps)
			}
		})
	}
}
func TestATSPINonRemovalSignalsCannotRevokeOtherNodes(t *testing.T) {
	for _, tc := range []struct {
		name, signal string
		body         []any
	}{
		{"unrelated signal", "org.a11y.atspi.Event.Object:PropertyChange", []any{"Name"}},
		{"children added", "org.a11y.atspi.Event.Object:ChildrenChanged", []any{"add", int32(1), int32(0), "not-a-ref", map[string]dbus.Variant{}}},
		{"defunct false", "org.a11y.atspi.Event.Object:StateChanged", []any{"defunct", int32(0), int32(0), dbus.MakeVariant(""), map[string]dbus.Variant{}}},
		{"other state", "org.a11y.atspi.Event.Object:StateChanged", []any{"enabled", int32(1), int32(0), dbus.MakeVariant(""), map[string]dbus.Variant{}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &dbusBackend{generation: 7, cacheItems: map[NodeID]cacheItem{{BusName: "org.example.App", ObjectPath: "/node", Generation: 7, Incarnation: 1}: {Name: "live"}}}
			b.transitionObjectHandlesLocked(&dbus.Signal{Name: tc.signal, Sender: "org.example.App", Path: dbus.ObjectPath("/node"), Body: tc.body})
			if b.generation != 7 || len(b.cacheItems) != 1 {
				t.Fatalf("non-removal event %s revoked valid node", tc.name)
			}
		})
	}
}
func TestATSPIDefunctStateWireVariants(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state any
		want  bool
		valid bool
	}{
		{"signed true", int32(1), true, true}, {"unsigned true", uint32(1), true, true},
		{"boolean true", true, true, true}, {"signed false", int32(0), false, true},
		{"unsigned false", uint32(0), false, true}, {"boolean false", false, false, true},
		{"string refused", "1", false, false}, {"nil refused", nil, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enabled, known := stateEnabled([]any{"defunct", tc.state, int32(0), dbus.MakeVariant(""), map[string]dbus.Variant{}})
			if enabled != tc.want || known != tc.valid {
				t.Fatalf("defunct-state %T decoded %v/%v, want %v/%v", tc.state, enabled, known, tc.want, tc.valid)
			}
		})
	}
	for _, bad := range [][]any{nil, {}, {"defunct", int32(1)}, {"defunct", int32(1), "wrong", dbus.MakeVariant(""), map[string]dbus.Variant{}}, {"defunct", int32(1), int32(0), "wrong", map[string]dbus.Variant{}}, {"defunct", int32(1), int32(0), dbus.MakeVariant(""), "wrong"}} {
		if enabled, known := stateEnabled(bad); enabled || known {
			t.Fatalf("malformed state %v accepted", bad)
		}
	}
}
func TestATSPIObservationHistoryBoundedEvictionPreservesChangeOrder(t *testing.T) {
	b := &dbusBackend{generation: 3}
	for i := uint64(1); i <= observationHistoryLimit+5; i++ {
		sig := &dbus.Signal{Name: "org.a11y.atspi.Event.Object:PropertyChange", Sender: "org.example.App", Path: dbus.ObjectPath("/node")}
		event := Event{Kind: "object:property-change", Node: NodeID{BusName: "org.example.App", ObjectPath: "/node", Generation: 3}}
		b.recordObservationLocked(sig, event, i)
	}
	if len(b.observationHistory) != observationHistoryLimit || b.observationFloor != 5 {
		t.Fatalf("history overflow retained=%d floor=%d, want %d floor 5", len(b.observationHistory), b.observationFloor, observationHistoryLimit)
	}
	if first, last := b.observationHistory[0].revision, b.observationHistory[len(b.observationHistory)-1].revision; first != 6 || last != observationHistoryLimit+5 {
		t.Fatalf("history tail out of order: first=%d last=%d", first, last)
	}
	if !b.observationHistory[0].node.valid() {
		t.Fatal("event lost authoritative node identity")
	}
}
func TestATSPIEventRefDecoderRejectsSpoofedOrIncorrectlyTypedReferences(t *testing.T) {
	for _, tc := range []struct {
		name  string
		v     any
		valid bool
	}{
		{"object ref", objectRef{BusName: "org.example.App", ObjectPath: "/node"}, true},
		{"cache ref", cacheObjectRef{BusName: "org.example.App", ObjectPath: "/node"}, true},
		{"variant wrapped", dbus.MakeVariant([]any{"org.example.App", dbus.ObjectPath("/node")}), true},
		{"array path string", []any{"org.example.App", "/node"}, true},
		{"malformed array", []any{"org.example.App"}, false},
		{"wrong bus type", []any{42, dbus.ObjectPath("/node")}, false},
		{"wrong path type", []any{"org.example.App", 123}, false},
		{"untrusted whitespace bus", []any{"org. example.App", dbus.ObjectPath("/node")}, false},
		{"invalid path", []any{"org.example.App", "not-an-absolute-path"}, false},
		{"unexpected type", struct{}{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ref, ok := cacheObjectRefFromValue(tc.v)
			if ok != tc.valid {
				t.Fatalf("reference %T accepted=%v want %v ref=%+v", tc.v, ok, tc.valid, ref)
			}
		})
	}
	for _, tc := range []struct {
		name  string
		value any
		valid bool
	}{
		{"null parent", []any{"", nullObjectPath}, true},
		{"foreign parent", []any{"org.foreign.App", dbus.ObjectPath("/parent")}, true},
		{"invalid null", []any{"", dbus.ObjectPath("/non-null")}, false},
	} {
		t.Run(fmt.Sprintf("parent-%s", tc.name), func(t *testing.T) {
			ref, ok := cacheParentRefFromValue(tc.value)
			if ok != tc.valid {
				t.Fatalf("parent decode=%+v %v want %v", ref, ok, tc.valid)
			}
		})
	}
}
