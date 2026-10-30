package input

import (
	"context"
	"encoding/binary"
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

func TestEIReadLoopRejectsInvalidFrameSizes(t *testing.T) {
	for _, size := range []uint32{0, 8, 17, 18, eiMaximumMessage + 4} {
		t.Run(strings.ReplaceAll(string(rune(size)), "/", "_"), func(t *testing.T) {
			clientConn, serverConn := unixConnectionPair(t)
			defer serverConn.Close()
			client := newEIClient(clientConn)
			defer client.close()
			header := make([]byte, eiHeaderSize)
			binary.NativeEndian.PutUint32(header[8:12], size)
			if _, err := serverConn.Write(header); err != nil {
				t.Fatal(err)
			}
			select {
			case <-client.done:
				if err := client.readError(); err == nil || !strings.Contains(err.Error(), "invalid message size") {
					t.Fatalf("frame size %d: got %v, want invalid size error", size, err)
				}
			case <-time.After(time.Second):
				t.Fatalf("parser did not reject frame size %d", size)
			}
		})
	}
}

func TestEIReadLoopRejectsMalformedConnectionAndPing(t *testing.T) {
	for _, tc := range []struct {
		name     string
		messages [][]byte
		want     string
	}{
		{"short connection", [][]byte{eiRequest(0, 2, eiU64(12))}, "invalid handshake connection"},
		{"zero connection", [][]byte{eiRequest(0, 2, append(append(eiU32(7), eiU64(0)...), eiU32(1)...))}, "unsupported handshake"},
		{"wrong version", [][]byte{eiRequest(0, 2, append(append(eiU32(7), eiU64(11)...), eiU32(2)...))}, "unsupported handshake"},
		{"short ping", [][]byte{eiRequest(0, 2, append(append(eiU32(7), eiU64(11)...), eiU32(1)...)), eiRequest(11, 3, eiU64(3))}, "invalid connection ping"},
		{"wrong ping version", [][]byte{eiRequest(0, 2, append(append(eiU32(7), eiU64(11)...), eiU32(1)...)), eiRequest(11, 3, append(eiU64(3), eiU32(2)...))}, "unsupported connection ping"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clientConn, serverConn := unixConnectionPair(t)
			defer serverConn.Close()
			client := newEIClient(clientConn)
			defer client.close()
			for _, message := range tc.messages {
				if _, err := serverConn.Write(message); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case <-client.done:
				if err := client.readError(); err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("read error = %v, want %q", err, tc.want)
				}
			case <-time.After(time.Second):
				t.Fatal("malformed protocol was not rejected")
			}
		})
	}
}

func TestEISenderRejectsIncompleteNegotiation(t *testing.T) {
	for _, tc := range []struct {
		name           string
		initial        []byte
		afterHandshake []byte
		want           string
		revoked        bool
	}{
		{"missing version", eiRequest(0, 0, nil), nil, "missing handshake version", false},
		{"version zero", eiRequest(0, 0, eiU32(0)), nil, "version zero", false},
		{"missing capabilities", eiRequest(0, 0, eiU32(1)), nil, "does not offer", false},
		{"disconnect before seat", eiRequest(0, 0, eiU32(1)), nil, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, ctx, cancel, done := startEISenderSetup(t)
			defer cancel()
			if _, err := server.Write(tc.initial); err != nil {
				t.Fatal(err)
			}
			if tc.name != "missing version" && tc.name != "version zero" {
				assertEISenderAnnouncesPingPong(t, server)
				const connection = uint64(0xff00000000000010)
				if _, err := server.Write(eiRequest(0, 2, append(append(eiU32(1), eiU64(connection)...), eiU32(1)...))); err != nil {
					t.Fatal(err)
				}
				if tc.revoked {
					if _, err := server.Write(eiRequest(connection, 0, eiU32(1))); err != nil {
						t.Fatal(err)
					}
				} else {
					const seat = uint64(0xff00000000000020)
					if _, err := server.Write(append(eiRequest(connection, 1, eiU64(seat)), eiRequest(seat, 3, nil)...)); err != nil {
						t.Fatal(err)
					}
				}
			}
			select {
			case err := <-done:
				if tc.revoked {
					if !errors.Is(err, ErrRemoteDesktopRevoked) {
						t.Fatalf("got %v, want revoked", err)
					}
				} else if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("got %v, want %q", err, tc.want)
				}
			case <-ctx.Done():
				t.Fatalf("negotiation did not reject invalid peer: %v", ctx.Err())
			}
		})
	}
}

func TestEISetupEventRejectsInvalidRegions(t *testing.T) {
	for _, tc := range []struct {
		name          string
		width, height uint32
		scale         float32
		truncate      bool
	}{
		{"zero width", 0, 50, 1, false},
		{"zero height", 50, 0, 1, false},
		{"zero scale", 50, 50, 0, false},
		{"nan scale", 50, 50, float32(math.NaN()), false},
		{"infinite scale", 50, 50, float32(math.Inf(1)), false},
		{"truncated region", 50, 50, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sender := &eiSender{devices: map[uint64]*eiDevice{42: {id: 42, interfaces: map[string]uint64{}}}}
			data := append(append(append(append(eiU32(0), eiU32(0)...), eiU32(tc.width)...), eiU32(tc.height)...), eiF32(tc.scale)...)
			if tc.truncate {
				data = data[:16]
			}
			if err := sender.consumeSetupEvent(eiMessage{object: 42, opcode: 4, body: data}); err == nil {
				t.Fatalf("accepted invalid region %+v", tc)
			}
			if len(sender.devices[42].regions) != 0 {
				t.Fatal("invalid region was retained")
			}
		})
	}
}

func TestEIReadStringRejectsEmbeddedNUL(t *testing.T) {
	data := eiU32(6)
	data = append(data, []byte{'h', 'e', 0, 'l', 'o', 0}...)
	if value, err := eiReadString(data, 0); err == nil {
		t.Fatalf("accepted embedded NUL in protocol string: %q", value)
	}
}

func TestEIStringRoundtripRejectsTruncation(t *testing.T) {
	for _, value := range []string{"hello", "échéance", ""} {
		actual, err := eiReadString(eiString(value), 0)
		if err != nil || actual != value {
			t.Fatalf("string %q decoded as %q, %v", value, actual, err)
		}
	}
	for _, data := range [][]byte{{1, 2}, {1, 0, 0, 0, 'x'}, {2, 0, 0, 0, 'a', 'b'}} {
		if _, err := eiReadString(data, 0); err == nil {
			t.Fatalf("accepted malformed string %v", data)
		}
	}
}

func TestEIClientContextCancellation(t *testing.T) {
	conn, peer := unixConnectionPair(t)
	defer peer.Close()
	client := newEIClient(conn)
	defer client.close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.next(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("next canceled: %v", err)
	}
}
