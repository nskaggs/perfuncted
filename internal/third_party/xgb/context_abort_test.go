package xgb

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

func waitAbortDone(t *testing.T, conn *Conn) {
	t.Helper()
	select {
	case <-conn.doneRead:
	case <-time.After(time.Second):
		t.Fatal("XGB reader leaked after abort")
	}
	select {
	case <-conn.doneSend:
	case <-time.After(time.Second):
		t.Fatal("XGB sender leaked after abort")
	}
}

func TestContextConstructorRejectsNilAndCanceled(t *testing.T) {
	if _, err := NewConnDisplayContext(nil, ":99"); err == nil {
		t.Fatal("nil context accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewConnDisplayContext(ctx, ":99"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestHandshakeContextInterruptsStalledSocket(t *testing.T) {
	for _, consumeSetup := range []bool{false, true} {
		t.Run(map[bool]string{true: "blocked-reply", false: "blocked-write"}[consumeSetup], func(t *testing.T) {
			left, right := net.Pipe()
			defer right.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 70*time.Millisecond)
			defer cancel()
			if consumeSetup {
				go func() { _, _ = io.Copy(io.Discard, right) }()
			}
			c := &Conn{conn: left}
			done := make(chan error, 1)
			go func() { done <- c.handshakeContext(ctx) }()
			select {
			case err := <-done:
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("context error: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("stalled X11 handshake leaked")
			}
		})
	}
}

func TestAbortUnblocksReplyAndReleasesAllWorkers(t *testing.T) {
	left, right := net.Pipe()
	defer right.Close()
	c, err := postNewConn(&Conn{conn: left, setupResourceIdMask: 0x3ffff})
	if err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = io.Copy(io.Discard, right) }()
	cookie := c.NewCookie(true, true)
	c.NewRequest([]byte{43, 0, 1, 0}, cookie)
	ready := make(chan error, 1)
	go func() { _, err := cookie.Reply(); ready <- err }()
	c.Abort()
	c.Abort()
	select {
	case err := <-ready:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("blocked Reply returned %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("checked reply leaked after Abort")
	}
	waitAbortDone(t, c)
}

func TestAbortUnblocksReaderWithFullEventQueue(t *testing.T) {
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
	defer right.Close()
	c := &Conn{conn: left, eventChan: make(chan eventOrError, 1), cookieChan: make(chan *Cookie), aborted: make(chan struct{}), doneRead: make(chan struct{}), doneSend: make(chan struct{})}
	done := make(chan struct{})
	go func() { c.readResponses(); close(done) }()
	payload := genericHeader(0, false)
	go func() {
		for i := 0; i < 8; i++ {
			if _, err := right.Write(payload); err != nil {
				return
			}
		}
	}()
	deadline := time.After(time.Second)
	for len(c.eventChan) != 1 {
		select {
		case <-deadline:
			t.Fatal("event queue never filled")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	c.Abort()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reader blocked in full event queue")
	}
}

func TestAbortUnblocksReaderWithMissingCookie(t *testing.T) {
	left, right := net.Pipe()
	defer right.Close()
	c := &Conn{conn: left, eventChan: make(chan eventOrError, 1), cookieChan: make(chan *Cookie), aborted: make(chan struct{}), doneRead: make(chan struct{}), doneSend: make(chan struct{})}
	done := make(chan struct{})
	go func() { c.readResponses(); close(done) }()
	reply := make([]byte, 32)
	reply[0] = 1
	Put16(reply[2:4], 1)
	if _, err := right.Write(reply); err != nil {
		t.Fatal(err)
	}
	c.Abort()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reader blocked waiting for cookie after Abort")
	}
}

func TestBoundContextAbortsExistingConnection(t *testing.T) {
	left, right := net.Pipe()
	defer right.Close()
	c, err := postNewConn(&Conn{conn: left, setupResourceIdMask: 0x3ffff})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.bindAbortContext(ctx)
	cancel()
	waitAbortDone(t, c)
	c.bindAbortContext(context.Background()) // late bind to closed connection
	c.Abort()
}

func TestCloseDetachesBoundAbortContext(t *testing.T) {
	left, right := net.Pipe()
	defer right.Close()
	c, err := postNewConn(&Conn{conn: left, setupResourceIdMask: 0x3ffff})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.bindAbortContext(ctx)

	serverDone := make(chan error, 1)
	go func() {
		request := make([]byte, 4)
		if _, err := io.ReadFull(right, request); err != nil {
			serverDone <- err
			return
		}
		reply := make([]byte, 32)
		reply[0] = 1
		Put16(reply[2:4], 1)
		_, err := right.Write(reply)
		serverDone <- err
	}()

	c.Close()
	cancel()
	select {
	case <-c.doneRead:
	case <-time.After(time.Second):
		t.Fatal("graceful close did not stop response reader")
	}
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("fake X server did not complete close handshake")
	}
	select {
	case <-c.aborted:
		t.Fatal("context cancellation aborted a gracefully closing connection")
	default:
	}
}
