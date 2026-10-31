package output

import (
	"reflect"
	"testing"

	"github.com/nskaggs/perfuncted/internal/capability"
)

func TestX11AndWaylandOutputBackendsShareCapabilityContract(t *testing.T) {
	want := capability.Operations("outputs")
	if len(want) == 0 {
		t.Fatal("missing output operations")
	}
	x11 := (*X11Lister)(nil).SupportedOperations()
	wayland := (*WaylandLister)(nil).SupportedOperations()
	if !reflect.DeepEqual(x11, want) || !reflect.DeepEqual(wayland, want) {
		t.Fatalf("backend mismatch x11=%v wayland=%v expected=%v", x11, wayland, want)
	}
	x11[0] = "tampered"
	wayland[0] = "tampered"
	if !reflect.DeepEqual((*X11Lister)(nil).SupportedOperations(), want) || !reflect.DeepEqual((*WaylandLister)(nil).SupportedOperations(), want) {
		t.Fatal("capability registry aliased to returned slice")
	}
}
