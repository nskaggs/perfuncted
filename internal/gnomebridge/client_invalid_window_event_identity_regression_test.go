//go:build linux

package gnomebridge

import (
	"github.com/godbus/dbus/v5"
	"testing"
)

// A bridge event must carry a concrete window identity before entering the
// subscription queue. Empty identifiers are not valid lifecycle transitions.
func TestBridgeRefusesWindowLifecycleSignalsWithoutConcreteIdentity(t *testing.T) {
	for _, kind := range []string{"WindowRemoved", "FocusChanged", "WindowAdded", "WindowChanged"} {
		for _, id := range []string{"", "   ", "\t"} {
			t.Run(kind+"/"+id, func(t *testing.T) {
				body := []any{id}
				if kind == "WindowAdded" || kind == "WindowChanged" {
					body = []any{WindowInfo{ID: id, Title: "Private browser window"}}
				}
				signal := &dbus.Signal{Name: WindowsInterface + "." + kind, Path: dbus.ObjectPath(ObjectPath), Body: body}
				if event, ok := decodeWindowEvent(signal); ok {
					t.Fatalf("unidentifiable event admitted: %+v", event)
				}
			})
		}
	}
}
