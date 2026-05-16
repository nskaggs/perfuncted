package output

import (
	"testing"

	"github.com/nskaggs/perfuncted/internal/wl"
)

func wlStringData(s string) []byte {
	n := uint32(len(s) + 1)
	padded := (n + 3) &^ 3
	out := make([]byte, 4+int(padded))
	wl.PutUint32(out[0:4], n)
	copy(out[4:], s)
	return out
}

func TestReadWlString(t *testing.T) {
	t.Parallel()

	data := wlStringData("HDMI-A-1")

	got, next, ok := readWlString(data, 0)
	if !ok {
		t.Fatal("readWlString returned ok=false")
	}
	if got != "HDMI-A-1" {
		t.Fatalf("string = %q, want %q", got, "HDMI-A-1")
	}
	if next != len(data) {
		t.Fatalf("next = %d, want %d", next, len(data))
	}
}

func TestReadWlStringRejectsMalformedData(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data []byte
	}{
		{
			name: "missing nul terminator",
			data: func() []byte {
				data := make([]byte, 8)
				wl.PutUint32(data[0:4], 4)
				copy(data[4:], "abcd")
				return data
			}(),
		},
		{
			name: "missing padding bytes",
			data: func() []byte {
				data := make([]byte, 6)
				wl.PutUint32(data[0:4], 2)
				data[4] = 'x'
				data[5] = 0
				return data
			}(),
		},
		{
			name: "length extends beyond data",
			data: func() []byte {
				data := make([]byte, 8)
				wl.PutUint32(data[0:4], 8)
				return data
			}(),
		},
		{
			name: "zero length",
			data: func() []byte {
				data := make([]byte, 4)
				wl.PutUint32(data[0:4], 0)
				return data
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got, next, ok := readWlString(tt.data, 0); ok {
				t.Fatalf("readWlString(%v) = %q, next=%d, ok=true; want ok=false", tt.data, got, next)
			}
		})
	}
}
