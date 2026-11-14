package input

import (
	"errors"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// The EIS transport closes during an explicit local shutdown. The lifecycle
// watcher must distinguish this expected termination from a peer revocation:
// a normal Close must not convert the retired session to a remote error.
func TestPortalLifecycleReaderExitDuringLocalCloseDoesNotRevoke(t *testing.T) {
	done := make(chan struct{})
	close(done)
	client := &eiClient{done: done, messages: make(chan eiMessage), stop: make(chan struct{})}
	client.setReadError(errors.New("local EIS socket closed"))
	backend := &RemoteDesktopBackend{
		sender:  &eiSender{client: client},
		portal:  &portalSession{signals: make(chan *dbus.Signal)},
		revoked: make(chan struct{}),
		stop:    make(chan struct{}),
	}
	backend.closeStarted.Store(true)
	finished := make(chan struct{})
	go func() { backend.watchLifecycle(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("local-close EIS reader termination stranded lifecycle watcher")
	}
	if state := backend.revokedState.Load(); state != nil {
		t.Fatalf("local shutdown treated as external revocation: %v", state.err)
	}
	select {
	case <-backend.revoked:
		t.Fatal("local shutdown published remote revocation")
	default:
	}
}
