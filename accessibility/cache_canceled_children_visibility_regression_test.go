package accessibility

import (
	"context"
	"errors"
	"testing"

	"github.com/godbus/dbus/v5"
)

// Even the in-memory provider cache is an accessibility observation: an
// already canceled caller has no permission to receive private node handles.
func TestATSPIChildrenCacheDoesNotReturnPrivateHandlesAfterCancellation(t *testing.T) {
	parent := NodeID{BusName: "org.example.Private", ObjectPath: "/parent", Generation: 7, Incarnation: 1}
	child := NodeID{BusName: parent.BusName, ObjectPath: "/private/document", Generation: 7, Incarnation: 1}
	backend := &dbusBackend{generation: 7, cacheApps: map[string]bool{parent.BusName: true}, cacheItems: map[NodeID]cacheItem{
		parent: {Object: cacheObjectRef{BusName: parent.BusName, ObjectPath: dbus.ObjectPath(parent.ObjectPath)}, Name: "Secret parent"},
		child:  {Object: cacheObjectRef{BusName: child.BusName, ObjectPath: dbus.ObjectPath(child.ObjectPath)}, Parent: cacheObjectRef{BusName: parent.BusName, ObjectPath: dbus.ObjectPath(parent.ObjectPath)}, Index: 0, Name: "Secret child"},
	}}
	children, err := backend.children(context.Background(), parent)
	if err != nil || len(children) != 1 || string(children[0].ObjectPath) != child.ObjectPath {
		t.Fatalf("valid cache children not available: %+v err=%v", children, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, mode := range []struct {
		name string
		run  func() ([]objectRef, error)
	}{
		{"cached lookup", func() ([]objectRef, error) { return backend.children(ctx, parent) }},
	} {
		t.Run(mode.name, func(t *testing.T) {
			actual, err := mode.run()
			if !errors.Is(err, context.Canceled) || len(actual) != 0 {
				t.Fatalf("canceled lookup leaked cached node handles: children=%+v err=%v", actual, err)
			}
		})
	}
}
