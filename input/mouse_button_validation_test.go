//go:build linux
// +build linux

package input

import (
	"context"
	"strconv"
	"strings"
	"testing"
)

func TestPublicMouseMethodsRejectButtonsOutsideDocumentedRange(t *testing.T) {
	for _, button := range []int{0, 4} {
		t.Run("button-"+strconv.Itoa(button), func(t *testing.T) {
			tests := []struct {
				name string
				call func() error
			}{
				{name: "xtest click", call: func() error {
					return (&XTestBackend{}).MouseClick(context.Background(), 1, 2, button)
				}},
				{name: "xtest down", call: func() error {
					return (&XTestBackend{}).MouseDown(context.Background(), button)
				}},
				{name: "xtest up", call: func() error {
					return (&XTestBackend{}).MouseUp(context.Background(), button)
				}},
				{name: "uinput click", call: func() error {
					return (&UinputBackend{}).MouseClick(context.Background(), 1, 2, button)
				}},
				{name: "uinput down", call: func() error {
					return (&UinputBackend{}).MouseDown(context.Background(), button)
				}},
				{name: "uinput up", call: func() error {
					return (&UinputBackend{}).MouseUp(context.Background(), button)
				}},
				{name: "wayland click", call: func() error {
					return (&WlVirtualBackend{}).MouseClick(context.Background(), 1, 2, button)
				}},
				{name: "wayland down", call: func() error {
					return (&WlVirtualBackend{}).MouseDown(context.Background(), button)
				}},
				{name: "wayland up", call: func() error {
					return (&WlVirtualBackend{}).MouseUp(context.Background(), button)
				}},
			}
			for _, tc := range tests {
				t.Run(tc.name, func(t *testing.T) {
					err := tc.call()
					if err == nil || !strings.Contains(err.Error(), "unsupported mouse button") {
						t.Fatalf("error = %v, want unsupported mouse button", err)
					}
				})
			}
		})
	}
}
