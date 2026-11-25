package capability

import (
	"errors"
	"slices"
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

func TestOperationsReturnsDefensiveCopy(t *testing.T) {
	got := Operations("screen")
	got[0] = "mutated"

	if fresh := Operations("screen"); fresh[0] != "capture" {
		t.Fatalf("Operations mutated canonical registry: %v", fresh)
	}
}

func TestOperationsExclusionsAreOrderIndependentAndDoNotMutateRegistry(t *testing.T) {
	all := Operations("input")
	without := Operations("input", "pointer", "keyboard", "pointer", "not-an-operation")
	if slices.Contains(without, "pointer") || slices.Contains(without, "keyboard") {
		t.Fatalf("excluded operations remain: %v", without)
	}
	if len(without) != len(all)-2 {
		t.Fatalf("excluded duplicate modified length: all=%v filtered=%v", all, without)
	}
	if !slices.Contains(Operations("input"), "pointer") {
		t.Fatal("exclusion modified registry")
	}
	if result := Operations("nonexistent", "keyboard"); len(result) != 0 {
		t.Fatalf("unknown surface returned operations: %v", result)
	}
}
