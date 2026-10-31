//go:build linux

package input

import (
	"context"
	"errors"
	"testing"
)

// A failed device setup or an injected partially built backend must never
// dereference a missing uinput device. Each operation fails closed rather
// than crashing the automation process.
func TestUinputPartiallyAcquiredDevicesFailClosedWithoutPanic(t *testing.T) {
	for _, tc := range []struct {
		name    string
		backend func() *UinputBackend
		execute func(*UinputBackend) error
	}{
		{"key down missing keyboard", func() *UinputBackend { return &UinputBackend{} }, func(b *UinputBackend) error { return b.KeyDown(context.Background(), "a") }},
		{"key up missing keyboard", func() *UinputBackend { return &UinputBackend{} }, func(b *UinputBackend) error { return b.KeyUp(context.Background(), "a") }},
		{"literal type missing keyboard", func() *UinputBackend { return &UinputBackend{charToRune: qwertyRuneMap()} }, func(b *UinputBackend) error { return b.TypeLiteral(context.Background(), "abc") }},
		{"syntax type missing keyboard", func() *UinputBackend { return &UinputBackend{charToRune: qwertyRuneMap()} }, func(b *UinputBackend) error { return b.Type(context.Background(), "abc") }},
		{"move missing touchpad", func() *UinputBackend { return &UinputBackend{} }, func(b *UinputBackend) error { return b.MouseMove(context.Background(), 12, 23) }},
		{"click missing touchpad", func() *UinputBackend { return &UinputBackend{} }, func(b *UinputBackend) error { return b.MouseClick(context.Background(), 12, 23, 1) }},
		{"down missing touchpad", func() *UinputBackend { return &UinputBackend{} }, func(b *UinputBackend) error { return b.MouseDown(context.Background(), 1) }},
		{"up missing touchpad", func() *UinputBackend { return &UinputBackend{} }, func(b *UinputBackend) error { return b.MouseUp(context.Background(), 1) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := tc.backend()
			defer func() {
				if recovered := recover(); recovered != nil {
					t.Errorf("missing device caused panic: %v", recovered)
				}
			}()
			if err := tc.execute(b); !errors.Is(err, ErrNotSupported) {
				t.Errorf("missing device result=%v, want typed not-supported", err)
			}
		})
	}
}
