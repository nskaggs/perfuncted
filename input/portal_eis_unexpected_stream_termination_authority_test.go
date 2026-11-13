package input

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// Even without a parser error, a terminated EIS transport must revoke the
// portal-backed sender. A lost event source cannot keep granting fresh input.
func TestRemoteDesktopUnexpectedEISTerminationRevokesInput(t *testing.T) {
	for _, tc := range []struct {
		name      string
		terminate func(*eiClient)
		reason    string
	}{
		{"transport closes without recorded read error", func(c *eiClient) { close(c.done) }, "EIS stream closed"},
		{"event channel unexpectedly closes", func(c *eiClient) { close(c.messages) }, "EIS event stream closed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, _, client := newSyntheticRevocableRemoteDesktop(t)
			tc.terminate(client)
			select {
			case <-b.revoked:
			case <-time.After(time.Second):
				t.Fatal("lost EIS stream retained input authorization")
			}
			if err := b.stateError(); !errors.Is(err, ErrRemoteDesktopRevoked) || !strings.Contains(err.Error(), tc.reason) {
				t.Fatalf("unexpected EIS revocation: %v", err)
			}
			select {
			case <-b.writerDone:
			case <-time.After(time.Second):
				t.Fatal("EIS stream termination left writer running")
			}
			if err := b.KeyDown(context.Background(), "a"); !errors.Is(err, ErrRemoteDesktopRevoked) {
				t.Fatalf("terminated EIS source accepted key: %v", err)
			}
			if err := b.Close(); err != nil {
				t.Fatalf("idempotent cleanup of terminated EIS stream: %v", err)
			}
		})
	}
}
