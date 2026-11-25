package xgb

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func genericHeader(units uint32, sent bool) []byte {
	b := make([]byte, 32)
	b[0] = 35
	if sent {
		b[0] |= 128
	}
	Put32(b[4:8], units)
	return b
}

func TestGenericEventFraming(t *testing.T) {
	for _, tt := range []struct {
		name  string
		units uint32
		sent  bool
	}{
		{"zero", 0, false}, {"one", 1, false}, {"large", 100, false}, {"send", 2, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			header := genericHeader(tt.units, tt.sent)
			payload := bytes.Repeat([]byte{0xad}, int(tt.units)*4)
			stream := bytes.NewBuffer(append(append([]byte{}, payload...), 0xab, 0xcd))
			got, err := readGenericEvent(stream, header)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, append(header, payload...)) {
				t.Fatalf("frame mismatch: %d", len(got))
			}
			rest := stream.Bytes()
			if !bytes.Equal(rest, []byte{0xab, 0xcd}) {
				t.Fatalf("next message misaligned %x", rest)
			}
		})
	}
}
func TestGenericEventFramingFailures(t *testing.T) {
	if _, err := readGenericEvent(bytes.NewReader(nil), make([]byte, 31)); err == nil {
		t.Fatal("short header accepted")
	}
	for _, units := range []uint32{maxGenericEventPayload/4 + 1, ^uint32(0)} {
		if _, err := readGenericEvent(bytes.NewReader(nil), genericHeader(units, false)); err == nil || !strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("length %d: %v", units, err)
		}
	}
	if _, err := readGenericEvent(bytes.NewReader([]byte{1, 2}), genericHeader(1, false)); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("truncated frame: %v", err)
	}
}
