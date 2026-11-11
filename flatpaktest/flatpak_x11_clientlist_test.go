//go:build integration && linux

package flatpaktest

import "testing"

func TestFlatpakX11ClientListParser(t *testing.T) {
	tests := []struct {
		name, property string
		id             uint32
		want           bool
	}{
		{"normal client list", "_NET_CLIENT_LIST(WINDOW): window id # 0x31, 0x35", 0x35, true},
		{"no target", "_NET_CLIENT_LIST(WINDOW): window id # 0x31", 0x35, false},
		{"empty EWMH", "_NET_CLIENT_LIST(WINDOW): window id #", 0x35, false},
		{"not a client list", "unavailable", 0x35, false},
		{"boundary prefix", "_NET_CLIENT_LIST(WINDOW): window id # 0x350", 0x35, false},
		{"decimal fallback", "_NET_CLIENT_LIST(WINDOW): window id # 49, 53", 53, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := x11ClientListContains(tc.property, tc.id); got != tc.want {
				t.Errorf("contains(%q, 0x%x)=%v, want %v", tc.property, tc.id, got, tc.want)
			}
		})
	}
}
