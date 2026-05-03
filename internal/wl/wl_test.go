package wl

import (
	"bytes"
	"testing"
)

func TestPutUint32(t *testing.T) {
	var buf [4]byte
	PutUint32(buf[:], 0x01020304)
	want := []byte{0x04, 0x03, 0x02, 0x01} // little-endian
	if !bytes.Equal(buf[:], want) {
		t.Errorf("PutUint32(0x01020304) = %v, want %v", buf, want)
	}
}

func TestUint32(t *testing.T) {
	buf := []byte{0x04, 0x03, 0x02, 0x01}
	got := Uint32(buf)
	if got != 0x01020304 {
		t.Errorf("Uint32 = 0x%x, want 0x01020304", got)
	}
}

func TestPutUint32_Zero(t *testing.T) {
	var buf [4]byte
	PutUint32(buf[:], 0)
	want := []byte{0, 0, 0, 0}
	if !bytes.Equal(buf[:], want) {
		t.Errorf("PutUint32(0) = %v, want %v", buf, want)
	}
}

func TestPutUint32_Max(t *testing.T) {
	var buf [4]byte
	PutUint32(buf[:], 0xffffffff)
	want := []byte{0xff, 0xff, 0xff, 0xff}
	if !bytes.Equal(buf[:], want) {
		t.Errorf("PutUint32(max) = %v, want %v", buf, want)
	}
}

func TestPutStr(t *testing.T) {
	tests := []struct {
		input string
		want  []byte
	}{
		// "hello" → length=6, "hello\0", padded to 8 bytes
		{
			"hello",
			[]byte{
				6, 0, 0, 0, // length = strlen+1 = 6
				'h', 'e', 'l', 'l', 'o', 0, // string + null
				0, 0, // padding to 8 bytes
			},
		},
		// empty string → length=1, null, padded to 4 bytes
		{
			"",
			[]byte{
				1, 0, 0, 0, // length = 1
				0, // null terminator
				0, 0, 0, // padding to 4 bytes
			},
		},
		// "ab" → length=3, "ab" + 2 zeros (null + 1 pad) = 4 bytes
		{
			"ab",
			[]byte{
				3, 0, 0, 0, // length = 3
				'a', 'b', 0, 0, // string + null + 1 pad byte
			},
		},
		// "abc" → length=4, "abc\0", padded to 4 bytes (exact)
		{
			"abc",
			[]byte{
				4, 0, 0, 0, // length = 4
				'a', 'b', 'c', 0, // string + null (exactly 4 bytes)
			},
		},
		// "abcd" → length=5, "abcd\0", padded to 8 bytes
		{
			"abcd",
			[]byte{
				5, 0, 0, 0, // length = 5
				'a', 'b', 'c', 'd', 0, // string + null
				0, 0, 0, // padding to 8 bytes
			},
		},
	}
	for _, tc := range tests {
		got := putStr(nil, tc.input)
		if !bytes.Equal(got, tc.want) {
			t.Errorf("putStr(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestPutStr_PrependToExistingBuffer(t *testing.T) {
	prefix := []byte{0xAA, 0xBB}
	buf := putStr(prefix, "x")
	// prefix + length(2) + "x" + 3 zeros (null + 2 pad to reach 4)
	want := []byte{0xAA, 0xBB, 2, 0, 0, 0, 'x', 0, 0, 0}
	if !bytes.Equal(buf, want) {
		t.Errorf("putStr with prefix = %v, want %v", buf, want)
	}
}

func TestRawProxy_Dispatch(t *testing.T) {
	var gotOpcode uint32
	var gotData []byte
	p := &RawProxy{
		OnEvent: func(opcode uint32, fd int, data []byte) {
			gotOpcode = opcode
			gotData = append([]byte{}, data...)
		},
	}
	p.Dispatch(3, -1, []byte{0x01, 0x02})
	if gotOpcode != 3 {
		t.Errorf("opcode = %d, want 3", gotOpcode)
	}
	if !bytes.Equal(gotData, []byte{0x01, 0x02}) {
		t.Errorf("data = %v, want [1 2]", gotData)
	}
}

func TestRawProxy_DispatchNilHandler(t *testing.T) {
	// Should not panic with nil OnEvent
	p := &RawProxy{}
	p.Dispatch(0, -1, nil) //nolint:errcheck
}

func TestBaseProxy_ID(t *testing.T) {
	var bp BaseProxy
	if bp.ID() != 0 {
		t.Errorf("zero-value ID = %d, want 0", bp.ID())
	}
	bp.SetID(42)
	if bp.ID() != 42 {
		t.Errorf("ID = %d, want 42", bp.ID())
	}
}

func TestBaseProxy_Ctx(t *testing.T) {
	var bp BaseProxy
	if bp.Ctx() != nil {
		t.Errorf("zero-value Ctx = %v, want nil", bp.Ctx())
	}
}

func TestRegistry_Dispatch_GlobalEvent(t *testing.T) {
	var got GlobalEvent
	r := &Registry{}
	r.SetGlobalHandler(func(ev GlobalEvent) { got = ev })

	// Build a wl_registry.global event using putStr for the interface string.
	// Layout: name(4) + interface(string: length+bytes+pad) + version(4)
	data := make([]byte, 0)
	data = append(data, 1, 0, 0, 0) // name = 1
	data = putStr(data, "wl_seat")   // string: len=7, "wl_seat\0" + pad to 8
	data = append(data, 3, 0, 0, 0) // version = 3

	r.Dispatch(0, -1, data)
	if got.Name != 1 {
		t.Errorf("name = %d, want 1", got.Name)
	}
	if got.Interface != "wl_seat" {
		t.Errorf("interface = %q, want %q", got.Interface, "wl_seat")
	}
	if got.Version != 3 {
		t.Errorf("version = %d, want 3", got.Version)
	}
}

func TestRegistry_Dispatch_NoHandler(t *testing.T) {
	r := &Registry{}
	// Should not panic
	r.Dispatch(0, -1, []byte{1, 0, 0, 0, 4, 0, 0, 0, 'x', 0, 0, 0, 1, 0, 0, 0}) //nolint:errcheck
}

func TestRegistry_Dispatch_WrongOpcode(t *testing.T) {
	var got GlobalEvent
	r := &Registry{}
	r.SetGlobalHandler(func(ev GlobalEvent) { got = ev })
	r.Dispatch(1, -1, []byte{1, 0, 0, 0}) // opcode 1, not global
	if got.Name != 0 {
		t.Error("handler should not be called for non-zero opcode")
	}
}

func TestRegistry_Dispatch_ShortData(t *testing.T) {
	var got GlobalEvent
	r := &Registry{}
	r.SetGlobalHandler(func(ev GlobalEvent) { got = ev })
	r.Dispatch(0, -1, []byte{1, 0}) // too short
	if got.Name != 0 {
		t.Error("handler should not be called for short data")
	}
}

func TestCallback_Dispatch_Done(t *testing.T) {
	called := false
	c := &Callback{}
	c.SetDoneHandler(func() { called = true })
	c.Dispatch(0, -1, nil)
	if !called {
		t.Error("done handler not called")
	}
}

func TestCallback_Dispatch_WrongOpcode(t *testing.T) {
	called := false
	c := &Callback{}
	c.SetDoneHandler(func() { called = true })
	c.Dispatch(1, -1, nil)
	if called {
		t.Error("handler should not be called for non-zero opcode")
	}
}

func TestCallback_DispatchNilHandler(t *testing.T) {
	c := &Callback{}
	c.Dispatch(0, -1, nil) // should not panic
}

func TestContext_WriteMsg_NilConn(t *testing.T) {
	// Context with nil conn (test construction) should be a no-op
	ctx := &Context{}
	if err := ctx.WriteMsg([]byte{1}, nil); err != nil {
		t.Errorf("WriteMsg on nil conn returned error: %v", err)
	}
}

func TestContext_Dispatch_NilConn(t *testing.T) {
	ctx := &Context{}
	if err := ctx.Dispatch(); err != nil {
		t.Errorf("Dispatch on nil conn returned error: %v", err)
	}
}

func TestSafeClose(t *testing.T) {
	// nil context
	if err := SafeClose(nil); err != nil {
		t.Errorf("SafeClose(nil) = %v", err)
	}
	// context with nil conn
	ctx := &Context{}
	if err := SafeClose(ctx); err != nil {
		t.Errorf("SafeClose(zero-ctx) = %v", err)
	}
}

func TestRegistry_BindMessageLayout(t *testing.T) {
	// Verify that Registry.Bind produces the correct wire format.
	// We can't call Bind without a real ctx, but we can test the encoding
	// indirectly by checking putStr output.
	var buf []byte
	buf = put32(buf, 5)   // registry id
	buf = put32(buf, 0)   // placeholder for size|opcode
	buf = put32(buf, 10)  // name
	buf = putStr(buf, "wl_seat")
	buf = put32(buf, 3)   // version
	buf = put32(buf, 20)  // new_id
	PutUint32(buf[4:], uint32(len(buf))<<16) // opcode 0 = bind

	// Verify the buffer has the right structure
	if Uint32(buf[0:4]) != 5 {
		t.Errorf("registry id = %d, want 5", Uint32(buf[0:4]))
	}
	sizeOpcode := Uint32(buf[4:8])
	size := int(sizeOpcode >> 16)
	if size != len(buf) {
		t.Errorf("message size = %d, want %d", size, len(buf))
	}
	if sizeOpcode&0xffff != 0 {
		t.Errorf("opcode = %d, want 0", sizeOpcode&0xffff)
	}
}
