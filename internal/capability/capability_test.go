package capability

import (
	"errors"
	"testing"
)

func TestUnsupportedErrorMessages(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  UnsupportedError
		want string
	}{
		{
			name: "surface backend reason",
			err:  UnsupportedError{Surface: "input", Backend: "wayland", Reason: "missing protocol"},
			want: "input: unsupported on wayland: missing protocol",
		},
		{
			name: "surface reason",
			err:  UnsupportedError{Surface: "screen", Reason: "missing compositor"},
			want: "screen: unsupported: missing compositor",
		},
		{
			name: "surface only",
			err:  UnsupportedError{Surface: "output"},
			want: "output: unsupported",
		},
		{
			name: "empty",
			err:  UnsupportedError{},
			want: "unsupported",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.err.Error(); got != tt.want {
				t.Fatalf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestUnsupportedReturnsTypedError(t *testing.T) {
	t.Parallel()

	err := Unsupported("clipboard", "x11", "xclip missing")

	var got UnsupportedError
	if !errors.As(err, &got) {
		t.Fatalf("Unsupported returned %T, want UnsupportedError", err)
	}
	if got.Surface != "clipboard" || got.Backend != "x11" || got.Reason != "xclip missing" {
		t.Fatalf("Unsupported fields = %+v, want clipboard/x11/xclip missing", got)
	}
}
