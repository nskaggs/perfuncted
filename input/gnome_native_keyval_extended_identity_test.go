package input

import "testing"

// GNOME keysyms use ordinary ASCII values for physical digits and the XKB
// Unicode marker for non-Latin-1 codepoints. A physical key and Unicode
// character must never be silently translated to a different identity.
func TestGnomeNativePhysicalDigitsAndExtendedUnicodeKeysyms(t *testing.T) {
	for _, tc := range []struct {
		key  string
		want uint32
	}{
		{"0", '0'}, {"5", '5'}, {"9", '9'},
		{"Ā", 0x01000100}, {"中", 0x01004e2d}, {"🚀", 0x0101f680},
	} {
		t.Run(tc.key, func(t *testing.T) {
			got, err := gnomeKeyval(tc.key)
			if err != nil || got != tc.want {
				t.Fatalf("gnome keyval %q = %#x %v, want %#x", tc.key, got, err, tc.want)
			}
		})
	}
}
