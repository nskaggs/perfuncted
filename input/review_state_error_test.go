package input

import (
	"context"
	"errors"
	"testing"

	"github.com/jezek/xgb/xproto"
)

func TestReviewStateQueryAdmissionErrors(t *testing.T) {
	b := &x11Observer{display: "not-a-local-display"}
	queries := []struct {
		name string
		run  func(context.Context) (StateObservation, error)
	}{
		{"key", func(ctx context.Context) (StateObservation, error) { return b.KeyState(ctx, "f8") }},
		{"lock", func(ctx context.Context) (StateObservation, error) { return b.LockState(ctx, "capslock") }},
		{"button", func(ctx context.Context) (StateObservation, error) { return b.MouseButtonState(ctx, 1) }},
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, q := range queries {
		for _, tc := range []struct {
			name string
			ctx  context.Context
			want error
		}{
			{"nil", nil, ErrInvalidStateQuery}, {"canceled", canceled, context.Canceled}, {"untrusted display", context.Background(), ErrObservationUnsupported},
		} {
			t.Run(q.name+"/"+tc.name, func(t *testing.T) {
				got, err := q.run(tc.ctx)
				if !errors.Is(err, tc.want) || got != (StateObservation{}) {
					t.Fatalf("got=%+v err=%v want=%v", got, err, tc.want)
				}
			})
		}
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	for _, q := range queries {
		got, err := q.run(context.Background())
		if !errors.Is(err, ErrObservationRevoked) || got != (StateObservation{}) {
			t.Fatalf("%s after close: %+v %v", q.name, got, err)
		}
	}
}

func TestReviewStateQueryErrorClassification(t *testing.T) {
	b := &x11Observer{}
	generic := errors.New("transport failed")
	for _, tc := range []struct{ err, want error }{
		{nil, nil}, {generic, ErrObservationUnavailable}, {xproto.AccessError{}, ErrObservationDenied}, {ErrObservationUnsupported, ErrObservationUnsupported}, {ErrObservationDenied, ErrObservationDenied}, {ErrObservationUnavailable, ErrObservationUnavailable},
	} {
		got := b.stateQueryError(context.Background(), tc.err)
		if !errors.Is(got, tc.want) {
			t.Errorf("err=%v got=%v want=%v", tc.err, got, tc.want)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := b.stateQueryError(ctx, generic); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := b.finishStateQuery(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if err := b.stateQueryError(context.Background(), generic); !errors.Is(err, ErrObservationRevoked) {
		t.Fatal(err)
	}
	if err := b.finishStateQuery(context.Background()); !errors.Is(err, ErrObservationRevoked) {
		t.Fatal(err)
	}
}
