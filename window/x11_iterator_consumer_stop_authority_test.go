//go:build linux

package window

import (
	"context"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/jezek/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
)

// A lazy X11 consumer that stops after one result must not access metadata of
// subsequent windows. Those subsequent windows can belong to other clients.
func TestX11IteratorConsumerStopDoesNotReadFollowingWindow(t *testing.T) {
	b, conn := newStubX11Backend(t, true, "first-window")
	original := conn.GetPropertyFunc
	const first = xproto.Window(50)
	const second = xproto.Window(51)
	secondReads := 0
	conn.GetPropertyFunc = func(del bool, w xproto.Window, p, tp xproto.Atom, offset, count uint32) x11.GetPropertyCookie {
		if p == b.atomNetClientList {
			data := make([]byte, 8)
			binary.LittleEndian.PutUint32(data[:4], uint32(first))
			binary.LittleEndian.PutUint32(data[4:], uint32(second))
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: data})
		}
		if w == second {
			secondReads++
		}
		return original(del, w, p, tp, offset, count)
	}
	seen := 0
	for info, err := range b.IterateWindows(context.Background()) {
		if err != nil || info.ID != uint64(first) {
			t.Fatalf("first window=%+v err=%v", info, err)
		}
		seen++
		break
	}
	if seen != 1 || secondReads != 0 {
		t.Fatalf("iterator read unrequested client: seen=%d reads=%d", seen, secondReads)
	}
}

// Once an observer cancels after seeing the first window, the iterator must
// not disclose the next window in its already-fetched client-list snapshot.
func TestX11IteratorCancellationAfterFirstYieldStopsMetadata(t *testing.T) {
	b, conn := newStubX11Backend(t, true, "first-window")
	original := conn.GetPropertyFunc
	const second = xproto.Window(51)
	secondReads := 0
	conn.GetPropertyFunc = func(del bool, w xproto.Window, p, tp xproto.Atom, offset, count uint32) x11.GetPropertyCookie {
		if p == b.atomNetClientList {
			data := make([]byte, 8)
			binary.LittleEndian.PutUint32(data[:4], 50)
			binary.LittleEndian.PutUint32(data[4:], uint32(second))
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: data})
		}
		if w == second {
			secondReads++
		}
		return original(del, w, p, tp, offset, count)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	seen, failures := 0, 0
	for info, err := range b.IterateWindows(ctx) {
		if seen == 0 {
			if err != nil || info.ID != 50 {
				t.Fatalf("first window=%+v err=%v", info, err)
			}
			cancel()
			seen++
			continue
		}
		if !errors.Is(err, context.Canceled) || info.ID != 0 {
			t.Fatalf("canceled iterator disclosed window: %+v %v", info, err)
		}
		failures++
	}
	if seen != 1 || failures != 1 || secondReads != 0 {
		t.Fatalf("late cancellation: windows=%d errors=%d private reads=%d", seen, failures, secondReads)
	}
}
