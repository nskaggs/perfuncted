package window

import "testing"

// Late callbacks can hold a nil manager while compositor teardown is in
// progress. Reporting a notification must not panic or publish an event.
func TestWaylandNilManagerChangeNotificationFailsClosed(t *testing.T) {
	var manager *WaylandWindowManager
	manager.notifyWindowChange()
	if changes := manager.WindowChanges(); changes != nil {
		t.Fatal("nil manager advertised an event channel")
	}
}
