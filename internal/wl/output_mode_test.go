package wl

import "testing"

// A dispatched payload omits the object id and the size/opcode header, so the
// four wl_output.mode arguments start at offset zero in the order flags, width,
// height, refresh. Reading width and height one field late yields the height
// and the refresh rate instead.
func TestDecodeOutputModeReadsArgumentsInProtocolOrder(t *testing.T) {
	data := make([]byte, 16)
	PutUint32(data[0:4], OutputModeCurrent) // flags
	PutUint32(data[4:8], 3840)              // width
	PutUint32(data[8:12], 2160)             // height
	PutUint32(data[12:16], 60000)           // refresh

	mode, ok := DecodeOutputMode(data)
	if !ok {
		t.Fatal("DecodeOutputMode rejected a complete payload")
	}
	if mode.Flags != OutputModeCurrent {
		t.Fatalf("flags = %d, want %d", mode.Flags, OutputModeCurrent)
	}
	if mode.Width != 3840 || mode.Height != 2160 {
		t.Fatalf("size = %dx%d, want 3840x2160", mode.Width, mode.Height)
	}
	if mode.Refresh != 60000 {
		t.Fatalf("refresh = %d, want 60000", mode.Refresh)
	}
}

// Distinguishable field values catch a shift by one field, which repeating the
// same value would hide.
func TestDecodeOutputModeRejectsOffByOneFieldReads(t *testing.T) {
	data := make([]byte, 16)
	PutUint32(data[0:4], 7)
	PutUint32(data[4:8], 11) // width
	PutUint32(data[8:12], 22)
	PutUint32(data[12:16], 33)

	mode, ok := DecodeOutputMode(data)
	if !ok {
		t.Fatal("DecodeOutputMode rejected a complete payload")
	}
	if mode.Flags != 7 || mode.Width != 11 || mode.Height != 22 || mode.Refresh != 33 {
		t.Fatalf("decoded %+v, want flags=7 width=11 height=22 refresh=33", mode)
	}
	// The historical off-by-one read produced height and refresh as the size.
	if mode.Width == mode.Height || mode.Height == mode.Refresh {
		t.Fatalf("fields are not distinguishable, so a shift would go unnoticed: %+v", mode)
	}
}

// A truncated payload is rejected rather than decoded from adjacent memory.
func TestDecodeOutputModeRejectsShortPayloads(t *testing.T) {
	for _, size := range []int{0, 4, 8, 12, 15} {
		if _, ok := DecodeOutputMode(make([]byte, size)); ok {
			t.Fatalf("DecodeOutputMode accepted a %d-byte payload", size)
		}
	}
	if _, ok := DecodeOutputMode(make([]byte, 16)); !ok {
		t.Fatal("DecodeOutputMode rejected a 16-byte payload")
	}
}

// The current-mode flag is what consumers use to adopt a mode.
func TestOutputModeCurrentFlagIsTheLowBit(t *testing.T) {
	data := make([]byte, 16)
	PutUint32(data[0:4], OutputModeCurrent)
	mode, ok := DecodeOutputMode(data)
	if !ok {
		t.Fatal("DecodeOutputMode rejected a complete payload")
	}
	if mode.Flags&OutputModeCurrent == 0 {
		t.Fatalf("flags %d do not report the current mode", mode.Flags)
	}

	PutUint32(data[0:4], 0)
	mode, ok = DecodeOutputMode(data)
	if !ok {
		t.Fatal("DecodeOutputMode rejected a complete payload")
	}
	if mode.Flags&OutputModeCurrent != 0 {
		t.Fatalf("flags %d report the current mode", mode.Flags)
	}
}
