package input

import (
	"context"
	"strings"
	"testing"
)

func TestScrollRejectsNegativeClicks(t *testing.T) {
	tests := []struct {
		name string
		call func(context.Context, int) error
	}{
		{name: "uinput", call: (&UinputBackend{}).ScrollDown},
		{name: "xtest", call: (&XTestBackend{}).ScrollDown},
		{name: "wl-virtual", call: (&WlVirtualBackend{}).ScrollDown},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call(context.Background(), -1)
			if err == nil || !strings.Contains(err.Error(), "non-negative") {
				t.Fatalf("ScrollDown(-1) error = %v, want non-negative validation", err)
			}
		})
	}
}
