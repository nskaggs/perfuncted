package window

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted/internal/wl"
)

// Mock Wayland Context and Display for testing.
type mockWaylandContext struct {
	objects    map[uint32]wl.Proxy
	nextID     uint32
	sentMsgs   [][]byte
	sentOOBs   [][]byte
	dispatchFn func() error
}

func (m *mockWaylandContext) Register(p wl.Proxy) {
	m.nextID++
	p.SetID(m.nextID)
	p.SetCtx(m)
	m.objects[m.nextID] = p
}

func (m *mockWaylandContext) SetProxy(id uint32, p wl.Proxy) {
	p.SetID(id)
	p.SetCtx(m)
	m.objects[id] = p
}

func (m *mockWaylandContext) WriteMsg(data, oob []byte) error {
	m.sentMsgs = append(m.sentMsgs, data)
	m.sentOOBs = append(m.sentOOBs, oob)
	return nil
}

func (m *mockWaylandContext) Dispatch() error {
	if m.dispatchFn != nil {
		return m.dispatchFn()
	}
	return nil // No-op by default
}

func (m *mockWaylandContext) Close() error { return nil }
func (m *mockWaylandContext) ID() uint32   { return 1 } // Mock display ID

type mockWaylandDisplay struct{ ctx *mockWaylandContext }

func (d *mockWaylandDisplay) Context() wl.Ctx { return d.ctx }
func (d *mockWaylandDisplay) GetRegistry() (*wl.Registry, error) {
	reg := &wl.Registry{}
	d.ctx.Register(reg)
	// Mock the wl_display.get_registry call message
	// (sender ID, size/opcode, newID)
	var buf []byte
	buf = put32(buf, 1)          // wl_display ID
	buf = put32(buf, (12<<16)|1) // size=12, opcode=1 (get_registry)
	buf = put32(buf, reg.ID())
	if err := d.ctx.WriteMsg(buf, nil); err != nil {
		return nil, err
	}
	return reg, nil
}
func (d *mockWaylandDisplay) Sync() (*wl.Callback, error) {
	cb := &wl.Callback{}
	d.ctx.Register(cb)
	// Mock the wl_display.sync call message
	var buf []byte
	buf = put32(buf, 1)          // wl_display ID
	buf = put32(buf, (12 << 16)) // size=12, opcode=0 (sync)
	buf = put32(buf, cb.ID())
	if err := d.ctx.WriteMsg(buf, nil); err != nil {
		return nil, err
	}
	return cb, nil
}
func (d *mockWaylandDisplay) RoundTrip() error {
	// Mock RoundTrip: simulate a done event immediately
	cb, err := d.Sync()
	if err != nil {
		return err
	}
	cb.SetDoneHandler(func() {
		// Simulate dispatching a done event for the callback
		cb.Dispatch(0, -1, nil) // opcode 0 for done event
	})
	return d.ctx.Dispatch() // Process the sync and done
}

func put32(buf []byte, v uint32) []byte {
	var tmp [4]byte
	wl.PutUint32(tmp[:], v)
	return append(buf, tmp[:]...)
}

// Mock for wl.RawProxy and related structs for testing window manager methods.
type mockRawProxy struct {
	wl.BaseProxy
	OnEventMock func(opcode uint32, fd int, data []byte)
}

func (p *mockRawProxy) Dispatch(opcode uint32, fd int, data []byte) {
	if p.OnEventMock != nil {
		p.OnEventMock(opcode, fd, data)
	}
}

func (p *mockRawProxy) SetCtx(c wl.Ctx) { p.BaseProxy.SetCtx(c) }
func (p *mockRawProxy) ID() uint32      { return p.BaseProxy.ID() }
func (p *mockRawProxy) SetID(id uint32) { p.BaseProxy.SetID(id) }

// Mock for the wl.Registry SetGlobalHandler
type mockRegistry struct {
	wl.BaseProxy
	globalHandler func(wl.GlobalEvent)
}

func (r *mockRegistry) SetGlobalHandler(f func(wl.GlobalEvent)) { r.globalHandler = f }
func (r *mockRegistry) Bind(name uint32, iface string, ver, newID uint32) error {
	// No-op for mock: tests simulate compositor responses via ctx.dispatchFn.
	return nil
}
func (r *mockRegistry) Dispatch(opcode uint32, fd int, data []byte) {
	if opcode != 0 || r.globalHandler == nil || len(data) < 8 {
		return
	}
	ev := wl.GlobalEvent{Name: wl.Uint32(data[0:4])}
	slen := int(wl.Uint32(data[4:8]))
	if slen > 0 && 8+slen <= len(data) {
		ev.Interface = string(data[8 : 8+slen-1])
	}
	padded := (slen + 3) &^ 3
	if off := 8 + padded; off+4 <= len(data) {
		ev.Version = wl.Uint32(data[off : off+4])
	}
	r.globalHandler(ev)
}

func findWindowHandleByTitle(wm *WaylandWindowManager, title string) (*Info, error) {
	titleLower := strings.ToLower(title)
	for _, v := range wm.toplevels {
		if strings.Contains(strings.ToLower(v.Title), titleLower) {
			return v, nil
		}
	}
	return nil, fmt.Errorf("window matching %q not found", title)
}

func TestWaylandWindowManager_New(t *testing.T) {
	originalSocketPath := os.Getenv("WAYLAND_DISPLAY")
	os.Unsetenv("WAYLAND_DISPLAY")
	defer os.Setenv("WAYLAND_DISPLAY", originalSocketPath)

	wm, err := NewWaylandWindowManager()
	if err == nil || !strings.Contains(err.Error(), "window/wayland: WAYLAND_DISPLAY not set") {
		t.Fatalf("NewWaylandWindowManager() error = %v", err)
	}
	if wm != nil {
		t.Fatal("expected nil manager when WAYLAND_DISPLAY is unset")
	}
}

func newStubWaylandManager(title string, controlProtocol bool, withSeat bool) (*WaylandWindowManager, *mockWaylandContext, uint32) {
	ctx := &mockWaylandContext{objects: make(map[uint32]wl.Proxy), nextID: 0}
	display := &mockWaylandDisplay{ctx: ctx}
	registry := &mockRegistry{}
	ctx.Register(registry)
	ctx.dispatchFn = func() error { return nil }

	handleID := uint32(50)
	wm := &WaylandWindowManager{
		display:   display,
		registry:  registry,
		toplevels: make(map[uint32]*Info),
	}
	if controlProtocol {
		wm.wlrMgrID = 1
	} else {
		wm.extMgrID = 1
	}
	if title != "" {
		wm.toplevels[handleID] = &Info{ID: uint64(handleID), Title: title}
	}
	if withSeat {
		seat := &wl.RawProxy{}
		ctx.SetProxy(60, seat)
		wm.seat = seat
		wm.seatID = 1
	}
	return wm, ctx, handleID
}

func sawHandleRequest(ctx *mockWaylandContext, handleID uint32, opcode uint32) bool {
	for _, msg := range ctx.sentMsgs {
		if len(msg) < 8 {
			continue
		}
		sender := wl.Uint32(msg[0:4])
		sizeOpcode := wl.Uint32(msg[4:8])
		if sender == handleID && sizeOpcode&0xffff == opcode {
			return true
		}
	}
	return false
}

func sawAnyHandleRequest(ctx *mockWaylandContext, handleID uint32) bool {
	for _, msg := range ctx.sentMsgs {
		if len(msg) < 8 {
			continue
		}
		if wl.Uint32(msg[0:4]) == handleID {
			return true
		}
	}
	return false
}

// TestWaylandWindowManager_Activate tests the Activate method.
func TestWaylandWindowManager_Activate(t *testing.T) {
	testCases := []struct {
		name          string
		windowTitle   string
		windowExists  bool
		expectedCmd   string
		expectedError string
	}{
		{
			name:         "Window exists",
			windowTitle:  "Test Window",
			windowExists: true,
			expectedCmd:  "activate", // Check for the presence of the activate request
		},
		{
			name:          "Window does not exist",
			windowTitle:   "Nonexistent Window",
			windowExists:  false,
			expectedError: `window/wayland: window matching "Nonexistent Window" not found`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			title := ""
			if tc.windowExists {
				title = "Test Window"
			}
			wm, ctx, handleID := newStubWaylandManager(title, true, tc.windowExists)

			err := wm.Activate(context.Background(), tc.windowTitle)

			if tc.expectedError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.expectedError) {
					t.Errorf("Activate(%q) error = %v, expected error containing %q", tc.windowTitle, err, tc.expectedError)
				}
			} else if err != nil {
				t.Errorf("Activate(%q) unexpected error: %v", tc.windowTitle, err)
			}

			// Check if the correct message was sent
			if tc.expectedCmd != "" && tc.expectedError == "" {
				if !sawHandleRequest(ctx, handleID, 4) {
					t.Errorf("Activate(%q) did not send the expected activate request", tc.windowTitle)
				}
			}
		})
	}
}

func runWindowActionTest(
	t *testing.T,
	actionName string,
	existingTitle string,
	expectedOpcode uint32,
	action func(*WaylandWindowManager, context.Context, string) error,
) {
	t.Helper()
	testCases := []struct {
		name          string
		windowTitle   string
		windowExists  bool
		expectedError string
	}{
		{
			name:         "Window exists",
			windowTitle:  existingTitle,
			windowExists: true,
		},
		{
			name:          "Window does not exist",
			windowTitle:   "Nonexistent Window",
			windowExists:  false,
			expectedError: `window/wayland: window matching "Nonexistent Window" not found`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			title := ""
			if tc.windowExists {
				title = existingTitle
			}
			wm, ctx, handleID := newStubWaylandManager(title, true, false)

			err := action(wm, context.Background(), tc.windowTitle)

			if tc.expectedError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.expectedError) {
					t.Errorf("%s(%q) error = %v, expected error containing %q", actionName, tc.windowTitle, err, tc.expectedError)
				}
			} else if err != nil {
				t.Errorf("%s(%q) unexpected error: %v", actionName, tc.windowTitle, err)
			}

			if tc.expectedError == "" {
				if !sawHandleRequest(ctx, handleID, expectedOpcode) {
					t.Errorf("%s(%q) did not send the expected request", actionName, tc.windowTitle)
				}
			}
		})
	}
}

// TestWaylandWindowManager_CloseWindow tests the CloseWindow method.
func TestWaylandWindowManager_CloseWindow(t *testing.T) {
	runWindowActionTest(
		t,
		"CloseWindow",
		"CloseMe",
		5,
		func(wm *WaylandWindowManager, ctx context.Context, title string) error {
			return wm.CloseWindow(ctx, title)
		},
	)
}

// TestWaylandWindowManager_Minimize tests the Minimize method.
func TestWaylandWindowManager_Minimize(t *testing.T) {
	runWindowActionTest(
		t,
		"Minimize",
		"MinimizeMe",
		2,
		func(wm *WaylandWindowManager, ctx context.Context, title string) error {
			return wm.Minimize(ctx, title)
		},
	)
}

// TestWaylandWindowManager_Maximize tests the Maximize method.
func TestWaylandWindowManager_Maximize(t *testing.T) {
	runWindowActionTest(
		t,
		"Maximize",
		"MaximizeMe",
		0,
		func(wm *WaylandWindowManager, ctx context.Context, title string) error {
			return wm.Maximize(ctx, title)
		},
	)
}

// TestWaylandWindowManager_List tests the List method.
func TestWaylandWindowManager_List(t *testing.T) {
	wm, _, _ := newStubWaylandManager("", true, false)
	handleID1 := uint32(50)
	handleID2 := uint32(51)
	wm.toplevels[handleID1] = &Info{ID: uint64(handleID1), Title: "Window One"}
	wm.toplevels[handleID2] = &Info{ID: uint64(handleID2), Title: "Window Two"}

	windows, err := wm.List(context.Background())

	if err != nil {
		t.Fatalf("List() unexpected error: %v", err)
	}

	if len(windows) != 2 {
		t.Errorf("List() expected 2 windows, got %d", len(windows))
	}

	// Check contents (order might not be guaranteed, so check for presence)
	found1 := false
	found2 := false
	for _, w := range windows {
		if w.Title == "Window One" && w.ID == uint64(handleID1) {
			found1 = true
		}
		if w.Title == "Window Two" && w.ID == uint64(handleID2) {
			found2 = true
		}
	}
	if !found1 || !found2 {
		t.Errorf("List() results did not contain expected windows. Found: %+v", windows)
	}
}

// TestWaylandWindowManager_FindWindowHandleByTitle tests the findWindowHandleByTitle helper.
func TestWaylandWindowManager_FindWindowHandleByTitle(t *testing.T) {
	wm, _, _ := newStubWaylandManager("", true, false)
	handleID1 := uint32(50)
	handleID2 := uint32(51)
	wm.toplevels[handleID1] = &Info{ID: uint64(handleID1), Title: "My Test Window"}
	wm.toplevels[handleID2] = &Info{ID: uint64(handleID2), Title: "Another Window"}

	// Test case 1: Exact match
	handle, err := findWindowHandleByTitle(wm, "My Test Window")
	if err != nil {
		t.Errorf("findWindowHandleByTitle(exact) unexpected error: %v", err)
	}
	if handle == nil || handle.Title != "My Test Window" {
		t.Errorf("findWindowHandleByTitle(exact) returned wrong handle: %+v", handle)
	}

	// Test case 2: Partial match (case-insensitive)
	handle, err = findWindowHandleByTitle(wm, "test")
	if err != nil {
		t.Errorf("findWindowHandleByTitle(partial, case-insensitive) unexpected error: %v", err)
	}
	if handle == nil || handle.Title != "My Test Window" {
		t.Errorf("findWindowHandleByTitle(partial, case-insensitive) returned wrong handle: %+v", handle)
	}

	// Test case 3: No match
	_, err = findWindowHandleByTitle(wm, "Nonexistent")
	if err == nil || !strings.Contains(err.Error(), `window matching "Nonexistent" not found`) {
		t.Errorf("findWindowHandleByTitle(no match) error = %v, expected error containing %q", err, `window matching "Nonexistent" not found`)
	}
}

// TestWaylandWindowManager_List_RoundTripError tests that List returns an error if RoundTrip fails.
func TestWaylandWindowManager_List_RoundTripError(t *testing.T) {
	ctx := &mockWaylandContext{objects: make(map[uint32]wl.Proxy), nextID: 0}
	display := &mockWaylandDisplay{ctx: ctx}
	_ = display
	registry := &mockRegistry{}
	ctx.Register(registry)

	managerProxyID := uint32(101)
	mockManagerProxy := &mockRawProxy{}
	ctx.SetProxy(managerProxyID, mockManagerProxy)
	mockManagerProxy.SetCtx(ctx)

	wm := &WaylandWindowManager{
		display:   display,
		registry:  registry,
		wlrMgrID:  1,
		toplevels: make(map[uint32]*Info),
	}
	ctx.objects[managerProxyID] = mockManagerProxy

	// Force RoundTrip to return an error
	ctx.dispatchFn = func() error {
		return errors.New("simulated roundtrip error")
	}

	_, err := wm.List(context.Background())
	if err == nil || !strings.Contains(err.Error(), "window/wayland: round-trip: simulated roundtrip error") {
		t.Errorf("List() error = %v, expected error containing %q", err, "window/wayland: round-trip: simulated roundtrip error")
	}
}

func TestWaylandWindowManager_ExtForeignToplevelIsListOnly(t *testing.T) {
	wm, ctx, _ := newStubWaylandManager("Ext Window", false, false)

	for name, fn := range map[string]func() error{
		"activate": func() error { return wm.Activate(context.Background(), "Ext Window") },
		"close":    func() error { return wm.CloseWindow(context.Background(), "Ext Window") },
		"minimize": func() error { return wm.Minimize(context.Background(), "Ext Window") },
		"maximize": func() error { return wm.Maximize(context.Background(), "Ext Window") },
	} {
		t.Run(name, func(t *testing.T) {
			if err := fn(); !errors.Is(err, ErrNotSupported) {
				t.Fatalf("%s error = %v, want ErrNotSupported", name, err)
			}
		})
	}

	if sawAnyHandleRequest(ctx, 50) {
		t.Fatalf("ext list-only backend sent a control request")
	}
}

func TestWaylandWindowManager_ActivateRequiresSeat(t *testing.T) {
	wm, ctx, _ := newStubWaylandManager("Seatless", true, false)

	err := wm.Activate(context.Background(), "Seatless")
	if !errors.Is(err, ErrNotSupported) {
		t.Fatalf("Activate() error = %v, want ErrNotSupported", err)
	}
	if sawAnyHandleRequest(ctx, 50) {
		t.Fatalf("Activate() sent a request without a seat")
	}
}
