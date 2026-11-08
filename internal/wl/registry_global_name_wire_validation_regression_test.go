package wl

import "testing"

// Registry advertisements are used directly to select protocol interfaces.
// Malformed names must not be normalized into a different bindable identity.
func TestRegistryRejectsMalformedGlobalInterfaceNames(t *testing.T) {
	for _, tc := range []struct{ name, iface string }{
		{"empty interface", ""},
		{"embedded NUL", "wl_output\x00spoofed"},
		{"invalid UTF8", "wl_output\xff"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := 0
			r := &Registry{}
			r.SetGlobalHandler(func(GlobalEvent) { got++ })
			r.Dispatch(0, -1, registryGlobalData(51, tc.iface, 1))
			if got != 0 {
				t.Fatalf("published malformed global %q", tc.iface)
			}
		})
	}
}
