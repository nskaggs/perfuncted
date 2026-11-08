//go:build linux

package window

import (
	"context"
	"strings"
	"testing"
)

// X11 WindowNone (0) is not an addressable client. Reject it at the shared
// handle parser so no mutation can target the invalid protocol identifier.
func TestX11WindowHandleRejectsNone(t *testing.T) {
	if got, err := x11WindowID("0"); err == nil || got != 0 {
		t.Fatalf("invalid X11 window handle accepted: id=%d err=%v", got, err)
	}
	b, _ := newStubX11Backend(t, false, "")
	for _, tc := range []struct {
		name   string
		mutate func() error
	}{
		{"activate", func() error { return b.ActivateByID(context.Background(), "0") }},
		{"move", func() error { return b.MoveByID(context.Background(), "0", 1, 1) }},
		{"resize", func() error { return b.ResizeByID(context.Background(), "0", 100, 100) }},
		{"close", func() error { return b.CloseWindowByID(context.Background(), "0") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.mutate(); err == nil || !strings.Contains(err.Error(), "window") {
				t.Fatalf("invalid zero ID mutation returned %v", err)
			}
		})
	}
}
