package wl

import "testing"

// A malformed or unrelated Wayland event must not complete a pending
// synchronization callback. Only wl_callback.done (opcode zero) may invoke it.
func TestCallbackDispatchOnlyDoneCompletesWaiter(t *testing.T) {
	var calls int
	callback := &Callback{}
	callback.SetDoneHandler(func() { calls++ })
	for _, opcode := range []uint32{1, 2, ^uint32(0)} {
		callback.Dispatch(opcode, -1, []byte{0xff, 0xff})
		if calls != 0 {
			t.Fatalf("unexpected opcode %d completed callback", opcode)
		}
	}
	callback.Dispatch(0, -1, nil)
	if calls != 1 {
		t.Fatalf("done callback calls = %d, want 1", calls)
	}
	callback.SetDoneHandler(nil)
	callback.Dispatch(0, -1, nil)
	if calls != 1 {
		t.Fatalf("removed done handler called again: %d", calls)
	}
}

// Shm, pool and buffer objects intentionally have no event handlers. Unknown
// compositor events must not change their identity or registry bookkeeping.
func TestSharedMemoryProxiesIgnoreUnrecognizedEvents(t *testing.T) {
	for _, tc := range []struct {
		name  string
		proxy Proxy
	}{
		{"shm", &Shm{}},
		{"pool", &ShmPool{}},
		{"buffer", &Buffer{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := &Context{}
			tc.proxy.SetID(47)
			tc.proxy.SetCtx(ctx)
			for _, opcode := range []uint32{0, 1, ^uint32(0)} {
				tc.proxy.Dispatch(opcode, -1, []byte{1, 2, 3})
				if tc.proxy.ID() != 47 {
					t.Fatalf("unexpected event %d modified object identity", opcode)
				}
			}
		})
	}
}
