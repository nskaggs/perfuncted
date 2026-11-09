package input

import (
	"context"
	"encoding/binary"
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// The EIS reader must close unsolicited descriptors even when the carrying
// frame is malformed and its transport is about to terminate. Otherwise a
// sender can retain live pipe/socket resources in the receiving process.
func TestEISMalformedWireFramesReleaseUnsolicitedDescriptors(t *testing.T) {
	for _, tc := range []struct {
		name  string
		frame []byte
		want  string
	}{
		{name: "invalid frame length", frame: func() []byte {
			v := eiRequest(eiServerIDFloor+7, 1, nil)
			binary.NativeEndian.PutUint32(v[8:12], 12)
			return v
		}(), want: "invalid message size"},
		{name: "invalid handshake connection", frame: eiRequest(0, 2, eiU64(3)), want: "invalid handshake connection"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			incoming, peer := unixConnectionPair(t)
			defer peer.Close()
			client := newEIClient(incoming)
			defer client.close()
			var pipe [2]int
			if err := unix.Pipe2(pipe[:], unix.O_CLOEXEC|unix.O_NONBLOCK); err != nil {
				t.Fatal(err)
			}
			defer unix.Close(pipe[0])
			if _, _, err := peer.WriteMsgUnix(tc.frame, unix.UnixRights(pipe[1]), nil); err != nil {
				_ = unix.Close(pipe[1])
				t.Fatal(err)
			}
			if err := unix.Close(pipe[1]); err != nil {
				t.Fatal(err)
			}
			select {
			case <-client.done:
			case <-time.After(time.Second):
				t.Fatal("malformed frame did not terminate EIS reader")
			}
			if err := client.readError(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("unexpected protocol failure: %v, want %s", err, tc.want)
			}
			var byteBuf [1]byte
			n, err := unix.Read(pipe[0], byteBuf[:])
			if n != 0 || err != nil {
				t.Fatalf("reader leaked transferred pipe write end: n=%d err=%v", n, err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			event, nextErr := client.next(ctx)
			if nextErr == nil || event.object != 0 {
				t.Fatalf("malformed transport delivered event: %+v %v", event, nextErr)
			}
			if errors.Is(nextErr, context.DeadlineExceeded) {
				t.Fatalf("terminal EIS stream remained active: %v", nextErr)
			}
		})
	}
}
