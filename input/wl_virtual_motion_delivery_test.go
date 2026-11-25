//go:build linux

package input

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/wl"
)

func TestWlVirtualMotionDeliveryBarrier(t *testing.T) {
	b, conn := newCapturedWlVirtualBackend(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := b.MouseMove(ctx, 60, 70); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- b.FlushMotionRequests(ctx) }()
	read := func(size int) []byte {
		t.Helper()
		data := make([]byte, size)
		if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		if _, err := io.ReadFull(conn, data); err != nil {
			t.Fatal(err)
		}
		return data
	}
	motion, frame, sync := read(28), read(8), read(12)
	if wl.Uint32(motion[12:16]) != 60 || wl.Uint32(motion[16:20]) != 70 || wl.Uint32(frame[4:8])&0xffff != 4 || wl.Uint32(sync[:4]) != 1 || wl.Uint32(sync[4:8])&0xffff != 0 {
		t.Fatalf("unexpected wire sequence: %x %x %x", motion, frame, sync)
	}
	select {
	case err := <-done:
		t.Fatalf("barrier returned before acknowledgement: %v", err)
	default:
	}
	callback := wl.Uint32(sync[8:12])
	reply := make([]byte, 12)
	binary.NativeEndian.PutUint32(reply[:4], callback)
	binary.NativeEndian.PutUint32(reply[4:8], 12<<16)
	if _, err := conn.Write(reply); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if err := b.FlushMotionRequests(ctx); err == nil {
		t.Fatal("closed backend accepted barrier")
	}
}

func TestWlVirtualMotionDeliveryBarrierCancellation(t *testing.T) {
	b, _ := newCapturedWlVirtualBackend(t)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := b.FlushMotionRequests(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unacknowledged barrier: %v", err)
	}
}
