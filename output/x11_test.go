package output

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/nskaggs/perfuncted/internal/x11"
)

func TestX11ListerListRejectsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	lister := &X11Lister{conn: &x11.MockConnection{}}
	if _, err := lister.List(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("List error = %v, want context.Canceled", err)
	}
}

func TestX11ListerRejectsListAfterClose(t *testing.T) {
	lister := &X11Lister{conn: &x11.MockConnection{}}
	if err := lister.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := lister.List(context.Background()); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("List error = %v, want net.ErrClosed", err)
	}
}
