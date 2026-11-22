package xproto

import (
	"bytes"
	"github.com/jezek/xgb"
	"testing"
)

func TestGenericEventPreservesPayloadAndSequence(t *testing.T) {
	src := make([]byte, 40)
	src[0] = 35
	xgb.Put16(src[2:4], 0xabc)
	xgb.Put32(src[4:8], 2)
	for i := 32; i < 40; i++ {
		src[i] = byte(i)
	}
	value := GeGenericEventNew(src).(GeGenericEvent)
	if value.Sequence != 0xabc {
		t.Fatalf("sequence: %d", value.Sequence)
	}
	if !bytes.Equal(value.Bytes(), src) {
		t.Fatal("payload lost")
	}
	src[32] = 0
	if value.Raw[32] != 32 {
		t.Fatal("source mutation leaked into event")
	}
	got := value.Bytes()
	got[32] = 0
	if value.Raw[32] != 32 {
		t.Fatal("caller mutated event via Bytes")
	}
}
func TestGenericEventShortAndEmpty(t *testing.T) {
	value := GeGenericEventNew([]byte{35}).(GeGenericEvent)
	if value.Sequence != 0 {
		t.Fatal("short event sequence")
	}
	empty := GeGenericEvent{}.Bytes()
	if len(empty) != 32 || empty[0] != 35 {
		t.Fatal("default serialization")
	}
}
