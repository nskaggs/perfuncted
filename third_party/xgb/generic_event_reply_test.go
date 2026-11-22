package xgb

import (
	"net"
	"testing"
	"time"
)

func TestGenericEventThenReplyKeepsCookieAligned(t *testing.T) {
	old, had := NewEventFuncs[35]
	NewEventFuncs[35] = func(data []byte) Event { return testGenericEvent(data) }
	defer func() {
		if had {
			NewEventFuncs[35] = old
		} else {
			delete(NewEventFuncs, 35)
		}
	}()
	left, right := net.Pipe()
	cookie := &Cookie{Sequence: 1, replyChan: make(chan []byte, 1), errorChan: make(chan error, 1)}
	cookies := make(chan *Cookie, 1)
	cookies <- cookie
	conn := &Conn{conn: left, eventChan: make(chan eventOrError, 4), cookieChan: cookies, doneSend: make(chan struct{}), doneRead: make(chan struct{})}
	go conn.readResponses()
	reply := make([]byte, 32)
	reply[0] = 1
	Put16(reply[2:4], 1)
	header := genericHeader(1, false)
	go func() {
		_, _ = right.Write(append(append(header, 1, 2, 3, 4), reply...))
		_ = right.Close()
	}()
	select {
	case ev := <-conn.eventChan:
		if _, ok := ev.(testGenericEvent); !ok {
			t.Fatalf("expected GenericEvent, got %T", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("event timeout")
	}
	select {
	case got := <-cookie.replyChan:
		if len(got) != 32 || got[0] != 1 || Get16(got[2:4]) != 1 {
			t.Fatalf("reply misaligned: %x", got)
		}
	case <-time.After(time.Second):
		t.Fatal("reply timeout")
	}
}
