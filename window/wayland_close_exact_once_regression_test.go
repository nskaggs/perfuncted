package window

import "testing"

// A Wayland window manager owns one release of its underlying transport.
// Repeated Close must not close a shared/observed transport repeatedly.
func TestWaylandWindowManagerRepeatedCloseReleasesTransportOnce(t *testing.T) {
	wm, wire, _ := newStubWaylandManager("Lifetime", true, false)
	if err := wm.Close(); err != nil {
		t.Fatal(err)
	}
	if err := wm.Close(); err != nil {
		t.Fatal(err)
	}
	if wire.closeCount != 1 {
		t.Fatalf("transport closed %d times; want exactly once", wire.closeCount)
	}
}
