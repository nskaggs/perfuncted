package window

import (
	"context"
	"errors"
	"net"
	"testing"
)

// A dial failure cannot establish a reusable Sway connection. In particular,
// cancellation racing a transport error must retain the caller's context
// reason, and either outcome must leave the manager able to connect later.
func TestSwayQueryDialFailureDoesNotPublishTransportOrLoseCancellation(t *testing.T) {
	sentinel := errors.New("synthetic socket unavailable")
	for _, tc := range []struct {
		name             string
		cancelDuringDial bool
		want             error
	}{
		{"socket unavailable", false, sentinel},
		{"canceled during dial", true, context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := swayDialContext
			t.Cleanup(func() { swayDialContext = old })
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			swayDialContext = func(_ context.Context, network, address string) (net.Conn, error) {
				calls++
				if network != "unix" || address != "synthetic.sock" {
					t.Fatalf("unexpected transport %q %q", network, address)
				}
				if calls == 1 {
					if tc.cancelDuringDial {
						cancel()
					}
					return nil, sentinel
				}
				return newSuccessSwayConn([]byte(`{"id":1,"type":"root"}`)), nil
			}
			manager := &SwayManager{sock: "synthetic.sock"}
			data, err := manager.query(ctx, swayMsgGetTree, "")
			if len(data) != 0 || !errors.Is(err, tc.want) {
				t.Fatalf("failed dial returned %q, %v; want %v", data, err, tc.want)
			}
			if manager.conn != nil {
				t.Fatal("failed socket was retained as reusable connection")
			}
			if got := calls; got != 1 {
				t.Fatalf("failed dial attempted %d connections", got)
			}
			data, err = manager.query(context.Background(), swayMsgGetTree, "")
			if err != nil || string(data) != `{"id":1,"type":"root"}` {
				t.Fatalf("failed query blocked subsequent recovered connection: %q %v", data, err)
			}
			if calls != 2 {
				t.Fatalf("recovered query made %d dials", calls)
			}
			if err := manager.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
