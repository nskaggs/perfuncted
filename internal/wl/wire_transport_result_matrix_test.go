package wl

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// A Wayland request that sent a partial message or failed to reset an I/O
// deadline cannot be acknowledged as delivered. Preserve cancellation and
// transport errors together instead of losing the causal chain.
func TestWaylandWriteResultReportsCompleteTransportErrorMatrix(t *testing.T) {
	canceled := context.Canceled
	wireErr := errors.New("synthetic Wayland peer stopped reading")
	resetErr := errors.New("synthetic write deadline reset failed")
	for _, tc := range []struct {
		name                 string
		cancel, write, reset error
		n, oob               int
		want                 []error
		message              string
		ok                   bool
	}{
		{name: "complete", n: 8, oob: 4, ok: true},
		{name: "complete concurrent cancellation is acknowledged", cancel: canceled, n: 8, oob: 4, ok: true},
		{name: "data short", n: 4, oob: 4, message: "short write"},
		{name: "fd short", n: 8, oob: 2, message: "short write"},
		{name: "short both", n: 3, oob: 1, message: "short write"},
		{name: "short after cancellation", cancel: canceled, n: 6, oob: 4, want: []error{canceled}, message: "short write"},
		{name: "transport failure", write: wireErr, want: []error{wireErr}},
		{name: "failure with cancellation", cancel: canceled, write: wireErr, want: []error{canceled, wireErr}},
		{name: "failure plus reset", write: wireErr, reset: resetErr, want: []error{wireErr, resetErr}},
		{name: "failure plus cancellation and reset", cancel: canceled, write: wireErr, reset: resetErr, want: []error{canceled, wireErr, resetErr}},
		{name: "only reset failed", n: 8, oob: 4, reset: resetErr, want: []error{resetErr}},
		{name: "reset failed after cancellation", n: 8, oob: 4, cancel: canceled, reset: resetErr, want: []error{canceled, resetErr}},
		{name: "both short plus reset", n: 4, oob: 0, reset: resetErr, want: []error{resetErr}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := finishWriteResult(tc.cancel, tc.n, tc.oob, tc.write, tc.reset, 8, 4)
			if tc.ok {
				if err != nil {
					t.Fatalf("completed transport erroneously failed: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("transport failure acknowledged")
			}
			for _, cause := range tc.want {
				if !errors.Is(err, cause) {
					t.Fatalf("lost transport cause %v in %v", cause, err)
				}
			}
			if tc.message != "" && !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("missing failure detail %q: %v", tc.message, err)
			}
		})
	}
}
