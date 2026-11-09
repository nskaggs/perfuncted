//go:build linux

package input

import (
	"context"
	"errors"
	"slices"
	"testing"
)

// A virtual device may report an error after a partial press write. The
// physical state is then uncertain, so a click must attempt a compensating
// release rather than leaving the button potentially held.
type uncertainPressTouchpad struct {
	recordingTouchPad
	pressErr   error
	releaseErr error
}

func (p *uncertainPressTouchpad) LeftPress() error {
	p.record("left-press")
	return p.pressErr
}
func (p *uncertainPressTouchpad) LeftRelease() error {
	p.record("left-release")
	return p.releaseErr
}

func TestUinputClickAttemptsReleaseAfterUncertainPressFailure(t *testing.T) {
	for _, tc := range []struct {
		name         string
		releaseFails bool
	}{
		{name: "release succeeds"},
		{name: "release fails", releaseFails: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pressFailure := errors.New("partial press write failed")
			releaseFailure := errors.New("compensating release failed")
			tp := &uncertainPressTouchpad{pressErr: pressFailure}
			if tc.releaseFails {
				tp.releaseErr = releaseFailure
			}
			b := &UinputBackend{touchpad: tp}
			err := b.MouseClick(context.Background(), 12, 34, 1)
			if !errors.Is(err, pressFailure) {
				t.Fatalf("partial press error lost: %v", err)
			}
			if errors.Is(err, releaseFailure) != tc.releaseFails {
				t.Fatalf("cleanup failure not preserved: %v", err)
			}
			if want := []string{"move:12,34", "left-press", "left-release"}; !slices.Equal(tp.events, want) {
				t.Fatalf("partial press cleanup events=%v, want=%v", tp.events, want)
			}
		})
	}
}
