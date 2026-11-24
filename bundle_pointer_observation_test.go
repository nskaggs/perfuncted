package perfuncted

import (
	"context"
	"errors"
	"github.com/nskaggs/perfuncted/input"
	"testing"
	"time"
)

type observationFixture struct {
	*closeCountingInput
	result input.PointerObservation
	after  func()
	err    error
}

func (f *observationFixture) ObservePointer(context.Context) (input.PointerObservation, error) {
	if f.after != nil {
		f.after()
	}
	return f.result, f.err
}
func TestObservePointerContract(t *testing.T) {
	for _, mode := range []string{"origin", "negative", "unsupported", "invalid-scale", "cancel", "close"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f := &observationFixture{closeCountingInput: &closeCountingInput{}, result: input.PointerObservation{Provider: "synthetic", ObservedAt: time.Now(), CoordinateSpace: input.CoordinateSpaceInfo{Kind: input.CoordinateSpaceLogical, ScaleX: 1, ScaleY: 1}}}
			s := NewSessionForTesting(nil, f, nil, nil, nil)
			defer s.Close()
			var want error
			switch mode {
			case "negative":
				f.result.X = -17
			case "unsupported":
				f.err = input.ErrNotSupported
				want = ErrUnsupported
			case "invalid-scale":
				f.result.CoordinateSpace.ScaleX = 0
			case "cancel":
				f.after = cancel
				want = context.Canceled
			case "close":
				f.after = func() { _ = s.Close() }
				want = ErrSessionClosed
			}
			got, err := s.Input.ObservePointer(ctx)
			if want != nil || mode == "invalid-scale" {
				if err == nil || (want != nil && !errors.Is(err, want)) || got != (input.PointerObservation{}) {
					t.Fatalf("got %+v, %v; want %v", got, err, want)
				}
			} else if err != nil || got != f.result {
				t.Fatalf("got %+v, %v; want %+v", got, err, f.result)
			}
		})
	}
}
