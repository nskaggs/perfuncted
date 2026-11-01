package window

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

// Exercise response parsing through the actual request/response transport, not
// a fabricated parsed message: malformed and truncated compositor frames must
// fail closed rather than becoming successful window discovery or mutation.
func TestSwayIPCRejectsMalformedResponseWireFrames(t *testing.T) {
	for _, tc := range []struct {
		name      string
		makeReply func() (uint32, []byte)
		want      string
	}{
		{"valid tree response", func() (uint32, []byte) { return swayMsgGetTree, []byte(`{"id":1,"type":"root"}`) }, ""},
		{"wrong response type", func() (uint32, []byte) { return swayMsgSubscribe, []byte(`{"success":true}`) }, "unexpected response type"},
		{"bad magic", func() (uint32, []byte) {
			msg := swayFrame(swayMsgGetTree, []byte(`{}`))
			copy(msg[:6], []byte("BADIPC"))
			// Invalid magic is rejected after the header; sending an extra
			// payload on a synchronous pipe would block the fake peer.
			return ^uint32(0), msg[:14]
		}, "bad magic"},
		{"oversized advertised frame", func() (uint32, []byte) {
			msg := swayFrame(swayMsgGetTree, nil)
			binary.LittleEndian.PutUint32(msg[6:10], 64<<20+1)
			return ^uint32(0), msg
		}, "message body too large"},
		{"short header", func() (uint32, []byte) { return ^uint32(0), []byte("i3-ipc") }, "unexpected EOF"},
		{"truncated response payload", func() (uint32, []byte) {
			msg := swayFrame(swayMsgGetTree, []byte(`{"id":1}`))
			return ^uint32(0), msg[:len(msg)-2]
		}, "unexpected EOF"},
		{"unexpected command response", func() (uint32, []byte) { return swayMsgRunCommand, []byte(`[{"success":true}]`) }, "unexpected response type"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, peer := net.Pipe()
			defer client.Close()
			defer peer.Close()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			serverErr := make(chan error, 1)
			go func() {
				defer peer.Close()
				typ, body, err := readSwayMessage(peer)
				if err != nil {
					serverErr <- err
					return
				}
				if typ != swayMsgGetTree || len(body) != 0 {
					serverErr <- fmt.Errorf("unexpected request type=%d body=%q", typ, body)
					return
				}
				kind, raw := tc.makeReply()
				if kind != ^uint32(0) {
					raw = swayFrame(kind, raw)
				}
				_, err = peer.Write(raw)
				serverErr <- err
			}()
			raw, err := swayQueryConnContext(ctx, client, swayMsgGetTree, "")
			if tc.want == "" {
				if err != nil || string(raw) != `{"id":1,"type":"root"}` {
					t.Fatalf("valid tree response got %q %v", raw, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) || len(raw) != 0 {
				t.Fatalf("malformed response accepted raw=%q error=%v, want %q", raw, err, tc.want)
			}
			if err := <-serverErr; err != nil && !errors.Is(err, net.ErrClosed) {
				t.Fatalf("synthetic compositor send: %v", err)
			}
		})
	}
}

func swayFrame(typ uint32, body []byte) []byte {
	out := make([]byte, 14+len(body))
	copy(out[:6], swayMagic[:])
	binary.LittleEndian.PutUint32(out[6:10], uint32(len(body)))
	binary.LittleEndian.PutUint32(out[10:14], typ)
	copy(out[14:], body)
	return out
}

func TestSwayTreeIteratorRejectsMalformedTreeAndStopsAtConsumerBoundary(t *testing.T) {
	m, finish := newSyntheticSwayCommands(t, "{no-json", `[{"success":true}]`)
	if got, err := m.List(context.Background()); err == nil || got != nil {
		t.Fatalf("malformed tree accepted: windows=%v error=%v", got, err)
	}
	if _, err := finish(); err != nil {
		t.Fatal(err)
	}
	m, finish = newSyntheticSwayCommands(t, `{"id":1,"type":"root","floating_nodes":[{"id":42,"type":"floating_con","name":"First"},{"id":43,"type":"floating_con","name":"Second"}]}`, `[{"success":true}]`)
	emitted := 0
	for info, err := range m.IterateWindows(context.Background()) {
		if err != nil {
			t.Fatal(err)
		}
		if info.Title != "First" {
			t.Fatalf("iterator emitted beyond consumer stop: %+v", info)
		}
		emitted++
		break
	}
	if emitted != 1 {
		t.Fatalf("iterator emitted %d windows", emitted)
	}
	if _, err := finish(); err != nil {
		t.Fatal(err)
	}
}
