package input

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// Use a real connected EIS Unix socket to verify that local authorization
// rejection sends *no* frames at all, including object-zero protocol frames.
func TestRemoteDesktopMissingCapabilityNeverSendsEISWireFrames(t *testing.T) {
	cases := []struct {
		name    string
		disable func(*eiSender)
		call    func(*RemoteDesktopBackend) error
	}{
		{"keyboard object", func(s *eiSender) { s.keyboard = 0 }, func(b *RemoteDesktopBackend) error { return b.KeyDown(context.Background(), "a") }},
		{"keyboard device", func(s *eiSender) { s.keyboardDev = 0 }, func(b *RemoteDesktopBackend) error { return b.Type(context.Background(), "prefix {enter}") }},
		{"button object", func(s *eiSender) { s.button = 0 }, func(b *RemoteDesktopBackend) error { return b.MouseClick(context.Background(), 25, 30, 1) }},
		{"button device", func(s *eiSender) { s.buttonDev = 0 }, func(b *RemoteDesktopBackend) error { return b.MouseDown(context.Background(), 1) }},
		{"pointer object", func(s *eiSender) { s.pointers[0].pointer = 0 }, func(b *RemoteDesktopBackend) error { return b.MouseMove(context.Background(), 25, 30) }},
		{"pointer device", func(s *eiSender) { s.pointers[0].device = 0 }, func(b *RemoteDesktopBackend) error { return b.MouseMove(context.Background(), 25, 30) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn, peer := unixConnectionPair(t)
			t.Cleanup(func() { _ = peer.Close() })
			client := &eiClient{conn: conn, messages: make(chan eiMessage, 8), done: make(chan struct{}), stop: make(chan struct{})}
			sender := &eiSender{client: client, connection: 0xff00000000000010, seat: 0xff00000000000011, keyboard: 101, button: 102, scroll: 103, text: 104, keyboardDev: 0xff00000000000012, buttonDev: 0xff00000000000012, scrollDev: 0xff00000000000012, textDev: 0xff00000000000012}
			sender.pointers = []eiPointerDevice{{device: 501, pointer: 502, regions: []eiRegion{{x: 0, y: 0, width: 100, height: 100, scale: 1}}}}
			portal := &portalSession{path: dbus.ObjectPath("/org/freedesktop/portal/desktop/session/fixture"), signals: make(chan *dbus.Signal, 8)}
			b := newRemoteDesktopBackend(sender, portal)
			t.Cleanup(func() { _ = b.Close() })
			tc.disable(sender)
			if err := tc.call(b); !errors.Is(err, ErrNotSupported) {
				t.Fatalf("missing EIS capability did not produce typed refusal: %v", err)
			}
			if err := peer.SetReadDeadline(time.Now().Add(30 * time.Millisecond)); err != nil {
				t.Fatal(err)
			}
			buf := make([]byte, 64)
			n, err := peer.Read(buf)
			if n != 0 {
				t.Fatalf("denied EIS request sent %d bytes: %x", n, buf[:n])
			}
			var netErr net.Error
			if !errors.As(err, &netErr) || !netErr.Timeout() {
				t.Fatalf("expected clean EIS socket with no frames, got %v", err)
			}
		})
	}
}
