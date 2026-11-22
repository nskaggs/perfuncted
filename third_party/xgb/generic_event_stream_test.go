package xgb

import (
	"bytes"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

type testGenericEvent []byte

func (e testGenericEvent) Bytes() []byte  { return e }
func (e testGenericEvent) String() string { return "test-generic" }

func TestGenericEventStreamPreservesNextMessage(t *testing.T) {
	old, had := NewEventFuncs[35]
	NewEventFuncs[35] = func(data []byte) Event { return testGenericEvent(append([]byte(nil), data...)) }
	defer func() {
		if had {
			NewEventFuncs[35] = old
		} else {
			delete(NewEventFuncs, 35)
		}
	}()
	left, right := net.Pipe()
	conn := &Conn{
		conn:       left,
		eventChan:  make(chan eventOrError, 16),
		cookieChan: make(chan *Cookie),
		doneSend:   make(chan struct{}),
		doneRead:   make(chan struct{}),
	}
	done := make(chan struct{})
	go func() { conn.readResponses(); close(done) }()
	header := genericHeader(2, true)
	payload := bytes.Repeat([]byte{0x55}, 8)
	second := genericHeader(0, false)
	go func() {
		_, _ = right.Write(append(append(header, payload...), second...))
		_ = right.Close()
	}()
	select {
	case ev := <-conn.eventChan:
		got, ok := ev.(testGenericEvent)
		if !ok || !bytes.Equal(got.Bytes(), append(header, payload...)) {
			t.Fatalf("first event corrupt %T", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("first event timed out")
	}
	select {
	case ev := <-conn.eventChan:
		got, ok := ev.(testGenericEvent)
		if !ok || !bytes.Equal(got.Bytes(), second) {
			t.Fatalf("second event corrupt %T", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("second event timed out")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reader failed to stop")
	}
}

func TestGenericEventStreamTruncationTerminates(t *testing.T) {
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
	conn := &Conn{conn: left, eventChan: make(chan eventOrError, 2), doneSend: make(chan struct{}), doneRead: make(chan struct{})}
	done := make(chan struct{})
	go func() { conn.readResponses(); close(done) }()
	go func() {
		_, _ = right.Write(append(genericHeader(3, false), 0, 1, 2, 3))
		_ = right.Close()
	}()
	select {
	case ev := <-conn.eventChan:
		err, ok := ev.(error)
		if !ok || !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("expected truncated event, got %T %v", ev, ev)
		}
	case <-time.After(time.Second):
		t.Fatal("reader blocked on truncated event")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reader did not end")
	}
}
