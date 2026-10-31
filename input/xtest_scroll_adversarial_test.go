package input

import (
	"context"
	"errors"
	"testing"

	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

type syntheticPointerReply struct {
	reply *xproto.QueryPointerReply
	err   error
}

func (s syntheticPointerReply) Reply() (*xproto.QueryPointerReply, error) { return s.reply, s.err }

func TestXTestScrollButtonsAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		button byte
		act    func(*XTestBackend, context.Context, int) error
	}{
		{"up", 4, (*XTestBackend).ScrollUp},
		{"down", 5, (*XTestBackend).ScrollDown},
		{"left", 6, (*XTestBackend).ScrollLeft},
		{"right", 7, (*XTestBackend).ScrollRight},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, events := newTestXTestBackend(t, 1, []xproto.Keysym{0x61}, nil)
			if err := tc.act(b, context.Background(), 2); err != nil {
				t.Fatal(err)
			}
			expected := []xtestEvent{{xproto.ButtonPress, tc.button}, {xproto.ButtonRelease, tc.button}, {xproto.ButtonPress, tc.button}, {xproto.ButtonRelease, tc.button}}
			if !sameXTestEvents(*events, expected) {
				t.Fatalf("wrong scroll event sequence: %#v want %#v", *events, expected)
			}
			before := len(*events)
			if err := tc.act(b, context.Background(), -1); err == nil {
				t.Fatal("negative clicks were accepted")
			}
			canceled, cancel := context.WithCancel(context.Background())
			cancel()
			if err := tc.act(b, canceled, 1); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled scroll=%v", err)
			}
			if len(*events) != before {
				t.Fatalf("invalid/canceled scroll injected events: %#v", (*events)[before:])
			}
		})
	}
}

func TestXTestScrollStopsImmediatelyOnTransportFailure(t *testing.T) {
	failure := errors.New("synthetic XTEST wheel transport failure")
	failedOnce := false
	b, events := newTestXTestBackend(t, 1, []xproto.Keysym{0x61}, func(ev xtestEvent) error {
		if ev.eventType == xproto.ButtonRelease && !failedOnce {
			failedOnce = true
			return failure
		}
		return nil
	})
	if err := b.ScrollDown(context.Background(), 3); !errors.Is(err, failure) {
		t.Fatalf("scroll failure lost source error: %v", err)
	}
	expected := []xtestEvent{
		{eventType: xproto.ButtonPress, detail: 5},
		{eventType: xproto.ButtonRelease, detail: 5}, // failed transiently
		{eventType: xproto.ButtonRelease, detail: 5}, // best-effort cleanup
	}
	if !sameXTestEvents(*events, expected) {
		t.Fatalf("failed scroll did not clean up held wheel button: got %#v want %#v", *events, expected)
	}
}

func TestXTestPointerQueryAndSyncUseSameConnection(t *testing.T) {
	b, _ := newTestXTestBackend(t, 1, []xproto.Keysym{0x61}, nil)
	conn, ok := b.conn.(*x11.MockConnection)
	if !ok {
		t.Fatalf("unexpected X11 connection %T", b.conn)
	}
	queries := 0
	conn.QueryPointerFunc = func(window xproto.Window) x11.QueryPointerCookie {
		queries++
		if window != 1 {
			t.Fatalf("pointer queried on wrong root: %d", window)
		}
		return syntheticPointerReply{reply: &xproto.QueryPointerReply{RootX: 71, RootY: -52}}
	}
	x, y, err := b.PointerLocation(context.Background())
	if err != nil || x != 71 || y != -52 || queries != 1 {
		t.Fatalf("pointer=(%d,%d) err=%v queries=%d", x, y, err, queries)
	}
	if err := b.Sync(context.Background()); err != nil {
		t.Fatalf("synchronize mocked X connection: %v", err)
	}
	conn.QueryPointerFunc = func(xproto.Window) x11.QueryPointerCookie {
		return syntheticPointerReply{err: errors.New("disconnected")}
	}
	if _, _, err := b.PointerLocation(context.Background()); err == nil {
		t.Fatal("failed pointer query acknowledged")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := b.PointerLocation(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled query=%v", err)
	}
	if err := b.Sync(canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled sync=%v", err)
	}
}

func TestXTestScrollPermanentReleaseFailureIsBoundedAndReported(t *testing.T) {
	failure := errors.New("permanent synthetic XTEST release failure")
	b, events := newTestXTestBackend(t, 1, []xproto.Keysym{0x61}, func(ev xtestEvent) error {
		if ev.eventType == xproto.ButtonRelease {
			return failure
		}
		return nil
	})
	if err := b.ScrollRight(context.Background(), 5); !errors.Is(err, failure) {
		t.Fatalf("permanent release failure lost: %v", err)
	}
	if len(*events) != 3 {
		t.Fatalf("unbounded cleanup or additional clicks: %+v", *events)
	}
	for _, ev := range (*events)[1:] {
		if ev.eventType != xproto.ButtonRelease || ev.detail != 7 {
			t.Fatalf("cleanup targeted wrong XTEST button: %+v", *events)
		}
	}
	if err := b.ScrollRight(context.Background(), 0); err != nil {
		t.Fatalf("zero scroll should do no work: %v", err)
	}
	if len(*events) != 3 {
		t.Fatal("zero-click call injected input")
	}
}
