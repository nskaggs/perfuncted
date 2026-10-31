package window

import "testing"

// Native IDs are serialized as decimal text. Leading zeroes do not mean octal:
// accepting base-0 parser semantics would route a request for ID 10 to ID 8.
func TestNumericNativeIDsUseDecimalNotOctal(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want uint64
	}{
		{"0", 0}, {"08", 8}, {"010", 10}, {"00042", 42},
		{"0x2a", 42}, {"0X2A", 42},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			got, err := numericID(tc.raw)
			if err != nil || got != tc.want {
				t.Fatalf("numericID(%q) = %d, %v; want %d", tc.raw, got, err, tc.want)
			}
		})
	}
}

func TestX11NativeIDsDoNotRetargetLeadingZeroDecimal(t *testing.T) {
	got, err := x11WindowID("010")
	if err != nil || uint64(got) != 10 {
		t.Fatalf("X11 handle leading-zero decimal = %d, %v; want 10", got, err)
	}
}
