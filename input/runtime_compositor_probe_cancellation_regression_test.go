//go:build linux

package input

import (
	"context"
	"errors"
	"io"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/env"
)

func TestInputRuntimeStopsBackendSelectionWhenCompositorProbeIsCanceled(t *testing.T) {
	resetRuntimeSelectorConstructors(t)
	fallbackCalls := 0
	newWlVirtualBackend = func(context.Context, string) (Inputter, error) {
		fallbackCalls++
		return nil, errors.New("unexpected virtual backend")
	}
	statUinput = func() error {
		fallbackCalls++
		return nil
	}
	socket := filepath.Join(t.TempDir(), "wayland.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	connected := make(chan struct{})
	serverDone := make(chan error, 1)
	go func() {
		peer, err := listener.AcceptUnix()
		if err != nil {
			serverDone <- err
			return
		}
		defer peer.Close()
		_ = peer.SetReadDeadline(time.Now().Add(3 * time.Second))
		var requests [24]byte
		if _, err := io.ReadFull(peer, requests[:]); err != nil {
			serverDone <- err
			return
		}
		close(connected)
		_, err = io.Copy(io.Discard, peer)
		serverDone <- err
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt := env.FromEnviron([]string{"WAYLAND_DISPLAY=" + socket, "XDG_CURRENT_DESKTOP=Sway"})
	type result struct {
		backend Inputter
		err     error
	}
	opened := make(chan result, 1)
	go func() {
		backend, err := OpenRuntimeContext(ctx, rt, 1024, 768)
		opened <- result{backend, err}
	}()
	select {
	case <-connected:
	case <-time.After(3 * time.Second):
		t.Fatal("input discovery did not reach compositor registry probe")
	}
	cancel()
	select {
	case got := <-opened:
		if got.backend != nil || !errors.Is(got.err, context.Canceled) || fallbackCalls != 0 {
			t.Fatalf("canceled discovery returned backend=%T err=%v fallback calls=%d", got.backend, got.err, fallbackCalls)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("canceled input discovery did not return")
	}
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("compositor probe did not close cleanly: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("canceled input discovery leaked compositor connection")
	}
}
