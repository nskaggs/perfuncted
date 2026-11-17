package perfuncted

import (
	"context"
	"errors"
	"image"
	"image/color"
	"testing"
	"time"
)

func TestScreenBundleOperationsFailClosedWithoutBackend(t *testing.T) {
	session := NewSessionForTesting(nil, nil, nil, nil, nil)
	defer session.Close()

	ctx := context.Background()
	region := image.Rect(2, 3, 8, 9)
	cases := []struct {
		name      string
		operation string
		run       func() error
	}{
		{
			name:      "wait for no change",
			operation: "wait-stable",
			run: func() error {
				_, err := session.Screen.WaitForNoChange(ctx, region, 1, time.Millisecond)
				return err
			},
		},
		{
			name:      "wait for no change from initial hash",
			operation: "wait-stable",
			run: func() error {
				_, err := session.Screen.WaitForNoChangeFrom(ctx, region, 7, 1, time.Millisecond)
				return err
			},
		},
		{
			name:      "find color",
			operation: "pixel",
			run: func() error {
				_, err := session.Screen.FindColor(ctx, region, color.RGBA{R: 1, A: 255}, 0)
				return err
			},
		},
		{
			name:      "wait for change",
			operation: "wait-change",
			run: func() error {
				_, err := session.Screen.WaitForChange(ctx, region, 7, time.Millisecond)
				return err
			},
		},
		{
			name:      "wait for hash",
			operation: "wait",
			run: func() error {
				_, err := session.Screen.WaitFor(ctx, region, 9, time.Millisecond)
				return err
			},
		},
		{
			name:      "scan for hash",
			operation: "wait",
			run: func() error {
				_, err := session.Screen.ScanFor(ctx, []image.Rectangle{region}, []uint32{9}, time.Millisecond)
				return err
			},
		},
		{
			name:      "resolve dimensions",
			operation: "resolution",
			run: func() error {
				_, _, err := session.Screen.Resolution(ctx)
				return err
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run()
			var unavailable *CapabilityError
			if !errors.Is(err, ErrUnavailable) || !errors.As(err, &unavailable) || unavailable.Operation != tc.operation {
				t.Fatalf("operation error = %v, want unavailable screen capability for %q", err, tc.operation)
			}
		})
	}
}
