//go:build linux

package input

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/env"
	"golang.org/x/sys/unix"
)

func TestRemoteDesktopPortalOwnerLossBeforeLifecycleSubscriptionFailsClosed(t *testing.T) {
	_, portal := startSyntheticPortalBus(t)
	portal.mu.Lock()
	portal.releaseOwner = true
	portal.mu.Unlock()
	rt := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=/tmp/pf-test-wayland.sock",
		"DBUS_SESSION_BUS_ADDRESS=" + portal.busAddress,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	backend, err := OpenRemoteDesktopContext(ctx, rt, RemoteDesktopOptions{})
	if backend != nil {
		_ = backend.Close()
		t.Fatalf("portal owner loss returned input authority: %T", backend)
	}
	if !errors.Is(err, ErrRemoteDesktopUnavailable) || !strings.Contains(err.Error(), "subscribe session lifecycle") {
		t.Fatalf("portal owner loss error = %v, want fail-closed lifecycle subscription error", err)
	}
	portal.mu.Lock()
	selectOptions := portal.selectOptions
	portal.mu.Unlock()
	if selectOptions != nil {
		t.Fatalf("startup continued to device selection after portal owner loss: %v", selectOptions)
	}
}

func TestRemoteDesktopMouseClickButtonTransportFailureDoesNotClaimButton(t *testing.T) {
	backend, peer := newWireObservedRemoteDesktop(t)
	backend.sender.pointers = []eiPointerDevice{{
		device: 701, pointer: 702,
		regions: []eiRegion{{x: 0, y: 0, width: 100, height: 100, scale: 1}},
	}}
	if err := peer.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	buttonFramePending := make(chan struct{})
	continueButtonFrame := make(chan struct{})
	var releaseFrame sync.Once
	resumeButtonFrame := func() { releaseFrame.Do(func() { close(continueButtonFrame) }) }
	defer resumeButtonFrame()
	var clockCalls atomic.Int32
	backend.sender.clockGettime = func(clockID int32, ts *unix.Timespec) error {
		if clockCalls.Add(1) == 2 {
			close(buttonFramePending)
			<-continueButtonFrame
		}
		return unix.ClockGettime(clockID, ts)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- backend.MouseClick(ctx, 20, 30, 1) }()

	pointer, err := readSyntheticEISFrame(peer)
	if err != nil || pointer.object != 702 || pointer.opcode != 1 {
		t.Fatalf("click pointer movement = %+v, %v", pointer, err)
	}
	frame, err := readSyntheticEISFrame(peer)
	if err != nil || frame.object != 701 || frame.opcode != 3 {
		t.Fatalf("click pointer frame = %+v, %v", frame, err)
	}
	select {
	case <-buttonFramePending:
	case err := <-result:
		t.Fatalf("click stopped before its button frame: %v", err)
	case <-ctx.Done():
		t.Fatalf("click did not reach the button frame: %v", ctx.Err())
	}
	if err := peer.Close(); err != nil {
		t.Fatal(err)
	}
	resumeButtonFrame()
	if err := <-result; !errors.Is(err, ErrRemoteDesktopRevoked) || !strings.Contains(err.Error(), "EIS transport failed") {
		t.Fatalf("button-frame transport failure = %v, want revoked EIS transport error", err)
	}
	if !backend.hasPointer || backend.pointerX != 20 || backend.pointerY != 30 {
		t.Fatalf("accepted pointer movement was not recorded: %d,%d has=%v", backend.pointerX, backend.pointerY, backend.hasPointer)
	}
	if len(backend.buttons) != 0 {
		t.Fatalf("failed button frame claimed held-button ownership: %v", backend.buttons)
	}
	if stateErr := backend.stateError(); !errors.Is(stateErr, ErrRemoteDesktopRevoked) {
		t.Fatalf("failed button frame left authorization active: %v", stateErr)
	}
}
