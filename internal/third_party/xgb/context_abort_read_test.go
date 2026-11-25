package xgb

import (
	"io"
	"net"
	"testing"
	"time"
)

type abortTestError uint16

func (e abortTestError) Error() string      { return "controlled X11 error" }
func (e abortTestError) SequenceId() uint16 { return uint16(e) }
func (e abortTestError) BadId() uint32      { return 0 }

// The error branches below cover failures on the shared readResponses owner,
// including a partially advertised reply and an asynchronous X11 error.
func TestAbortReadResponsesTruncatedReplyAndBackpressure(t *testing.T) {
	for _, full := range []bool{false, true} {
		t.Run(map[bool]string{false: "truncated", true: "aborted-full-queue"}[full], func(t *testing.T) {
			left, right := net.Pipe()
			defer right.Close()
			c := &Conn{conn: left, eventChan: make(chan eventOrError, 1), cookieChan: make(chan *Cookie, 1), aborted: make(chan struct{}), doneRead: make(chan struct{}), doneSend: make(chan struct{})}
			if full {
				c.eventChan <- io.EOF
			}
			done := make(chan struct{})
			go func() { c.readResponses(); close(done) }()
			frame := make([]byte, 32)
			frame[0] = 1
			Put16(frame[2:4], 1)
			Put32(frame[4:8], 1)
			go func() {
				_, _ = right.Write(append(frame, 1))
				_ = right.Close()
			}()
			if full {
				time.Sleep(10 * time.Millisecond)
				c.Abort()
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("reader blocked on reply truncation")
			}
			if !full {
				select {
				case event := <-c.eventChan:
					if _, ok := event.(error); !ok {
						t.Fatalf("expected terminal error, got %T", event)
					}
				default:
					t.Fatal("missing truncated reply error")
				}
			}
		})
	}
}

func TestAbortReadResponsesTruncatedGenericWithFullQueue(t *testing.T) {
	left, right := net.Pipe()
	defer right.Close()
	c := &Conn{conn: left, eventChan: make(chan eventOrError, 1), cookieChan: make(chan *Cookie), aborted: make(chan struct{}), doneRead: make(chan struct{}), doneSend: make(chan struct{})}
	c.eventChan <- io.EOF
	done := make(chan struct{})
	go func() { c.readResponses(); close(done) }()
	header := genericHeader(4, false)
	go func() {
		_, _ = right.Write(append(header, 1, 2))
		_ = right.Close()
	}()
	time.Sleep(10 * time.Millisecond)
	c.Abort()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reader blocked on truncated GenericEvent")
	}
}

func TestAbortReadResponsesAsyncErrorBackpressure(t *testing.T) {
	old, had := NewErrorFuncs[99]
	NewErrorFuncs[99] = func([]byte) Error { return abortTestError(1) }
	defer func() {
		if had {
			NewErrorFuncs[99] = old
		} else {
			delete(NewErrorFuncs, 99)
		}
	}()
	left, right := net.Pipe()
	defer right.Close()
	c := &Conn{conn: left, eventChan: make(chan eventOrError, 1), cookieChan: make(chan *Cookie, 1), aborted: make(chan struct{}), doneRead: make(chan struct{}), doneSend: make(chan struct{})}
	c.eventChan <- io.EOF
	c.cookieChan <- &Cookie{Sequence: 1, pingChan: make(chan bool, 1)}
	done := make(chan struct{})
	go func() { c.readResponses(); close(done) }()
	errFrame := make([]byte, 32)
	errFrame[0], errFrame[1] = 0, 99
	Put16(errFrame[2:4], 1)
	if _, err := right.Write(errFrame); err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	c.Abort()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("async X11 error blocked full dispatch queue")
	}
}

func TestAbortReadResponsesClosedCookieQueue(t *testing.T) {
	left, right := net.Pipe()
	defer right.Close()
	cookies := make(chan *Cookie)
	close(cookies)
	c := &Conn{conn: left, eventChan: make(chan eventOrError, 1), cookieChan: cookies, aborted: make(chan struct{}), doneRead: make(chan struct{}), doneSend: make(chan struct{})}
	done := make(chan struct{})
	go func() { c.readResponses(); close(done) }()
	reply := make([]byte, 32)
	reply[0] = 1
	Put16(reply[2:4], 1)
	go func() { _, _ = right.Write(reply); _ = right.Close() }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("closed cookie channel blocked response reader")
	}
}
