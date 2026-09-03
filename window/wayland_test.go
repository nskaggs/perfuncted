package window

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

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

func (m *mockWaylandContext) Unregister(p wl.Proxy) {
	delete(m.objects, p.ID())
}

func (m *mockWaylandContext) WriteMsg(data, oob []byte) error {
	m.sentMsgs = append(m.sentMsgs, data)
	m.sentOOBs = append(m.sentOOBs, oob)
	return nil
}

func (m *mockWaylandContext) WriteMsgContext(ctx context.Context, data, oob []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return m.WriteMsg(data, oob)
}

func (m *mockWaylandContext) Dispatch() error {
	if m.dispatchFn != nil {
		return m.dispatchFn()
	}
	return nil // No-op by default
}

func (m *mockWaylandContext) DispatchContext(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return m.Dispatch()
}

func (m *mockWaylandContext) WithOperationContext(ctx context.Context, fn func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return fn()
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
func (d *mockWaylandDisplay) RoundTripContext(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
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

type blockingWaylandDisplay struct {
	*mockWaylandDisplay
	started chan struct{}
}

func (d *blockingWaylandDisplay) RoundTripContext(ctx context.Context) error {
	close(d.started)
	<-ctx.Done()
	return ctx.Err()
}

type sequencedWaylandDisplay struct {
	*mockWaylandDisplay
	started chan struct{}
	calls   int
}

func (d *sequencedWaylandDisplay) RoundTripContext(ctx context.Context) error {
	d.calls++
	if d.calls == 1 || d.calls >= 3 {
		return nil
	}
	close(d.started)
	<-ctx.Done()
	return ctx.Err()
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
func (r *mockRegistry) BindContext(ctx context.Context, name uint32, iface string, ver, newID uint32) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return r.Bind(name, iface, ver, newID)
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

	wm, err := NewWaylandWindowManagerForSocket("")
	if err == nil || !strings.Contains(err.Error(), "window/wayland: WAYLAND_DISPLAY not set") {
		t.Fatalf("NewWaylandWindowManagerForSocket() error = %v", err)
	}
	if wm != nil {
		t.Fatal("expected nil manager when socket is empty")
	}
}

func TestWaylandWindowManagerRejectsOperationsAfterClose(t *testing.T) {
	wm, _, _ := newStubWaylandManager("Closed", true, true)
	if err := wm.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := wm.Sync(context.Background()); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Sync error = %v, want net.ErrClosed", err)
	}
	if _, err := wm.List(context.Background()); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("List error = %v, want net.ErrClosed", err)
	}
}

func TestWaylandWindowManagerRejectsInvalidHandleRequests(t *testing.T) {
	wm, ctx, _ := newStubWaylandManager("Request", true, true)
	initialWrites := len(ctx.sentMsgs)

	if err := wm.sendHandleRequest(context.Background(), 50, 0x10000, nil); err == nil {
		t.Fatal("sendHandleRequest accepted an oversized opcode")
	}
	if err := wm.sendHandleRequest(context.Background(), 50, 0, make([]byte, 1<<16-7)); err == nil {
		t.Fatal("sendHandleRequest accepted an oversized payload")
	}
	if got := len(ctx.sentMsgs); got != initialWrites {
		t.Fatalf("invalid requests wrote %d messages, want no additional messages", got-initialWrites)
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

func TestWaylandWindowManager_ActivateByID(t *testing.T) {
	testCases := []struct {
		name          string
		windowID      string
		windowExists  bool
		expectedCmd   string
		expectedError string
	}{
		{
			name:         "Window exists",
			windowID:     "50",
			windowExists: true,
			expectedCmd:  "activate", // Check for the presence of the activate request
		},
		{
			name:          "Window does not exist",
			windowID:      "999",
			windowExists:  false,
			expectedError: ErrWindowNotFound.Error(),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			title := ""
			if tc.windowExists {
				title = "Test Window"
			}
			wm, ctx, handleID := newStubWaylandManager(title, true, tc.windowExists)

			err := wm.ActivateByID(context.Background(), tc.windowID)

			if tc.expectedError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.expectedError) {
					t.Errorf("ActivateByID(%q) error = %v, expected error containing %q", tc.windowID, err, tc.expectedError)
				}
			} else if err != nil {
				t.Errorf("ActivateByID(%q) unexpected error: %v", tc.windowID, err)
			}

			// Check if the correct message was sent
			if tc.expectedCmd != "" && tc.expectedError == "" {
				if !sawHandleRequest(ctx, handleID, 4) {
					t.Errorf("ActivateByID(%q) did not send the expected activate request", tc.windowID)
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
		windowID      string
		windowExists  bool
		expectedError string
	}{
		{
			name:         "Window exists",
			windowID:     "50",
			windowExists: true,
		},
		{
			name:          "Window does not exist",
			windowID:      "999",
			windowExists:  false,
			expectedError: ErrWindowNotFound.Error(),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			title := ""
			if tc.windowExists {
				title = existingTitle
			}
			wm, ctx, handleID := newStubWaylandManager(title, true, false)

			err := action(wm, context.Background(), tc.windowID)

			if tc.expectedError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.expectedError) {
					t.Errorf("%s(%q) error = %v, expected error containing %q", actionName, tc.windowID, err, tc.expectedError)
				}
			} else if err != nil {
				t.Errorf("%s(%q) unexpected error: %v", actionName, tc.windowID, err)
			}

			if tc.expectedError == "" {
				if !sawHandleRequest(ctx, handleID, expectedOpcode) {
					t.Errorf("%s(%q) did not send the expected request", actionName, tc.windowID)
				}
			}
		})
	}
}

// TestWaylandWindowManager_CloseWindow tests the CloseWindow method.
func TestWaylandWindowManager_CloseWindowByID(t *testing.T) {
	runWindowActionTest(
		t,
		"CloseWindow",
		"CloseMe",
		5,
		func(wm *WaylandWindowManager, ctx context.Context, id string) error {
			return wm.CloseWindowByID(ctx, id)
		},
	)
}

// TestWaylandWindowManager_Minimize tests the Minimize method.
func TestWaylandWindowManager_MinimizeByID(t *testing.T) {
	runWindowActionTest(
		t,
		"Minimize",
		"MinimizeMe",
		2,
		func(wm *WaylandWindowManager, ctx context.Context, id string) error {
			return wm.MinimizeByID(ctx, id)
		},
	)
}

// TestWaylandWindowManager_Maximize tests the Maximize method.
func TestWaylandWindowManager_MaximizeByID(t *testing.T) {
	runWindowActionTest(
		t,
		"Maximize",
		"MaximizeMe",
		0,
		func(wm *WaylandWindowManager, ctx context.Context, id string) error {
			return wm.MaximizeByID(ctx, id)
		},
	)
}

func TestWaylandWindowManager_FullscreenByID(t *testing.T) {
	runWindowActionTest(
		t,
		"Fullscreen",
		"FullscreenMe",
		7,
		func(wm *WaylandWindowManager, ctx context.Context, id string) error {
			return wm.FullscreenByID(ctx, id)
		},
	)
}

func TestWaylandWindowManager_UnfullscreenByID(t *testing.T) {
	runWindowActionTest(
		t,
		"Unfullscreen",
		"UnfullscreenMe",
		8,
		func(wm *WaylandWindowManager, ctx context.Context, id string) error {
			return wm.UnfullscreenByID(ctx, id)
		},
	)
}

func TestWaylandWindowManagerPrefersWlrootsProtocolForControl(t *testing.T) {
	wm := &WaylandWindowManager{extMgrID: 2, wlrMgrID: 3}
	iface, id, version := wm.foreignToplevelProtocol()
	if iface != "zwlr_foreign_toplevel_manager_v1" || id != 3 || version != 3 {
		t.Fatalf("foreign protocol = (%q, %d, %d), want wlroots id 3 version 3", iface, id, version)
	}

	wm = &WaylandWindowManager{extMgrID: 2}
	iface, id, version = wm.foreignToplevelProtocol()
	if iface != "ext_foreign_toplevel_list_v1" || id != 2 || version != 1 {
		t.Fatalf("list protocol = (%q, %d, %d), want ext id 2 version 1", iface, id, version)
	}
}

func TestWaylandWindowManagerHonorsWlrootsProtocolVersion(t *testing.T) {
	wm := &WaylandWindowManager{wlrMgrID: 7, wlrMgrVersion: 1}
	iface, id, version := wm.foreignToplevelProtocol()
	if iface != "zwlr_foreign_toplevel_manager_v1" || id != 7 || version != 1 {
		t.Fatalf("foreign protocol = (%q, %d, %d), want wlroots id 7 version 1", iface, id, version)
	}
	for _, operation := range wm.SupportedOperations() {
		if operation == "fullscreen" {
			t.Fatal("SupportedOperations advertised fullscreen for wlroots protocol version 1")
		}
	}
}

func TestWaylandWindowManagerRejectsFullscreenOnWlrootsVersionOne(t *testing.T) {
	wm, ctx, _ := newStubWaylandManager("Version one", true, false)
	wm.wlrMgrVersion = 1

	if err := wm.FullscreenByID(context.Background(), "50"); !errors.Is(err, ErrNotSupported) {
		t.Fatalf("FullscreenByID error = %v, want ErrNotSupported", err)
	}
	if sawAnyHandleRequest(ctx, 50) {
		t.Fatal("FullscreenByID sent a request to a version-one wlroots handle")
	}
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

	if windows[0].ID != uint64(handleID1) || windows[1].ID != uint64(handleID2) {
		t.Fatalf("List() order = %d, %d; want %d, %d", windows[0].ID, windows[1].ID, handleID1, handleID2)
	}

	// Check contents as well as order so a stable sort cannot hide a missing item.
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

func TestDecodeWaylandStringRejectsOversizedLength(t *testing.T) {
	data := []byte{0xff, 0xff, 0xff, 0xff}
	if got := decodeWaylandString(data); got != "" {
		t.Fatalf("decodeWaylandString oversized length = %q, want empty", got)
	}
}

func TestWaylandWindowManager_ListIsDeterministicWithStableIDTies(t *testing.T) {
	wm, _, _ := newStubWaylandManager("", true, false)
	wm.toplevels = map[uint32]*Info{
		2: {ID: 2, NativeID: "shared", Title: "second"},
		1: {ID: 1, NativeID: "shared", Title: "first"},
		3: {ID: 7, NativeID: "shared", Title: "z"},
		4: {ID: 7, NativeID: "shared", Title: "a"},
	}

	for range 20 {
		windows, err := wm.List(context.Background())
		if err != nil {
			t.Fatalf("List() unexpected error: %v", err)
		}
		if len(windows) != 4 {
			t.Fatalf("List() returned %d windows, want 4", len(windows))
		}
		want := []struct {
			id    uint64
			title string
		}{
			{id: 1, title: "first"},
			{id: 2, title: "second"},
			{id: 7, title: "a"},
			{id: 7, title: "z"},
		}
		for i, expected := range want {
			if windows[i].ID != expected.id || windows[i].Title != expected.title {
				t.Fatalf("List() order at index %d = (%d, %q), want (%d, %q)",
					i, windows[i].ID, windows[i].Title, expected.id, expected.title)
			}
		}
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

func TestWaylandWindowManager_ListHonorsCancellationDuringRoundTrip(t *testing.T) {
	ctx := &mockWaylandContext{objects: make(map[uint32]wl.Proxy)}
	display := &blockingWaylandDisplay{
		mockWaylandDisplay: &mockWaylandDisplay{ctx: ctx},
		started:            make(chan struct{}),
	}
	wm := &WaylandWindowManager{
		display:   display,
		toplevels: make(map[uint32]*Info),
	}

	requestCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := wm.List(requestCtx)
		done <- err
	}()

	select {
	case <-display.started:
		cancel()
	case <-time.After(time.Second):
		t.Fatal("List did not enter the cancellable round trip")
	}

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("List error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("List did not return after cancellation")
	}
}

func TestWaylandWindowManager_ActionHonorsCancellationDuringRoundTrip(t *testing.T) {
	ctx := &mockWaylandContext{objects: make(map[uint32]wl.Proxy)}
	display := &blockingWaylandDisplay{
		mockWaylandDisplay: &mockWaylandDisplay{ctx: ctx},
		started:            make(chan struct{}),
	}
	wm := &WaylandWindowManager{
		display:   display,
		toplevels: map[uint32]*Info{50: {ID: 50, NativeID: "50"}},
		wlrMgrID:  1,
	}

	requestCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- wm.CloseWindowByID(requestCtx, "50")
	}()

	select {
	case <-display.started:
		cancel()
	case <-time.After(time.Second):
		t.Fatal("CloseWindowByID did not enter the cancellable round trip")
	}

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("CloseWindowByID error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("CloseWindowByID did not return after cancellation")
	}
}

func TestWaylandWindowManager_ActivateRetainsSeatAfterCanceledBindRoundTrip(t *testing.T) {
	ctx := &mockWaylandContext{objects: make(map[uint32]wl.Proxy)}
	display := &sequencedWaylandDisplay{
		mockWaylandDisplay: &mockWaylandDisplay{ctx: ctx},
		started:            make(chan struct{}),
	}
	registry := &mockRegistry{}
	ctx.Register(registry)
	wm := &WaylandWindowManager{
		display:   display,
		registry:  registry,
		seatID:    7,
		wlrMgrID:  1,
		toplevels: map[uint32]*Info{50: {ID: 50, NativeID: "50"}},
	}

	requestCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- wm.ActivateByID(requestCtx, "50") }()
	select {
	case <-display.started:
		cancel()
	case <-time.After(time.Second):
		t.Fatal("ActivateByID did not reach lazy seat round trip")
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("ActivateByID error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ActivateByID did not return after cancellation")
	}
	if wm.seat == nil {
		t.Fatal("ActivateByID discarded a successfully bound seat")
	}
	if len(ctx.objects) != 2 {
		t.Fatalf("registered objects after canceled round trip = %d, want registry and seat", len(ctx.objects))
	}
	if err := wm.ActivateByID(context.Background(), "50"); err != nil {
		t.Fatalf("ActivateByID retry: %v", err)
	}
	if display.calls != 3 {
		t.Fatalf("round trips = %d, want 3 including successful retry", display.calls)
	}
	if len(ctx.objects) != 2 {
		t.Fatalf("registered objects after retry = %d, want registry and seat", len(ctx.objects))
	}
}

func TestWaylandWindowManager_IterateWindowsAllowsReentrantCallbacks(t *testing.T) {
	wm, _, _ := newStubWaylandManager("Reentrant", false, false)

	done := make(chan error, 1)
	go func() {
		for info, err := range wm.IterateWindows(context.Background()) {
			if err != nil {
				done <- err
				return
			}
			_, _, err = wm.lookupByID(info.StableID())
			done <- err
			return
		}
		done <- errors.New("iterator yielded no windows")
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("reentrant callback: %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("reentrant callback deadlocked")
	}
}

func TestWaylandWindowManager_ExtForeignToplevelIsListOnly(t *testing.T) {
	wm, ctx, _ := newStubWaylandManager("Ext Window", false, false)

	for name, fn := range map[string]func() error{
		"activate": func() error { return wm.ActivateByID(context.Background(), "50") },
		"close":    func() error { return wm.CloseWindowByID(context.Background(), "50") },
		"minimize": func() error { return wm.MinimizeByID(context.Background(), "50") },
		"maximize": func() error { return wm.MaximizeByID(context.Background(), "50") },
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

	err := wm.ActivateByID(context.Background(), "50")
	if !errors.Is(err, ErrNotSupported) {
		t.Fatalf("Activate() error = %v, want ErrNotSupported", err)
	}
	if sawAnyHandleRequest(ctx, 50) {
		t.Fatalf("Activate() sent a request without a seat")
	}
}

func TestWaylandWindowManager_SupportedOperationsRequireSeatForActivation(t *testing.T) {
	wm, _, _ := newStubWaylandManager("Seatless", true, false)
	for _, operation := range wm.SupportedOperations() {
		if operation == "activate" {
			t.Fatal("SupportedOperations advertised activate without a wl_seat")
		}
	}

	wm, _, _ = newStubWaylandManager("With seat", true, true)
	found := false
	for _, operation := range wm.SupportedOperations() {
		if operation == "activate" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("SupportedOperations omitted activate when a wl_seat is available")
	}
}

func TestWaylandNilManagerLifecycleQueriesAreSafe(t *testing.T) {
	var manager *WaylandWindowManager
	if got := manager.WindowChanges(); got != nil {
		t.Fatalf("WindowChanges() = %v, want nil", got)
	}
	if got := manager.SupportedOperations(); got != nil {
		t.Fatalf("SupportedOperations() = %v, want nil", got)
	}
	if err := manager.Sync(context.Background()); err == nil {
		t.Fatal("Sync() returned nil error for nil manager")
	}
}

func TestWaylandExtEventsPreserveStableIdentifierAndNotify(t *testing.T) {
	info := &Info{ID: 41, NativeID: "41"}
	manager := &WaylandWindowManager{
		toplevels: map[uint32]*Info{41: info},
	}
	changes := manager.WindowChanges()

	manager.handleExtToplevelEvent(
		41,
		info,
		4,
		encodeWaylandTestString("opaque-window-id"),
	)
	if info.NativeID != "opaque-window-id" {
		t.Fatalf("NativeID = %q, want opaque-window-id", info.NativeID)
	}
	if _, _, err := manager.lookupByID("opaque-window-id"); err != nil {
		t.Fatalf("lookupByID: %v", err)
	}
	select {
	case <-changes:
	default:
		t.Fatal("stable identifier event did not notify")
	}

	manager.handleExtToplevelEvent(41, info, 0, nil)
	if _, _, err := manager.lookupByID("opaque-window-id"); !errors.Is(
		err,
		ErrWindowNotFound,
	) {
		t.Fatalf("lookup after closed event = %v", err)
	}
}

func encodeWaylandTestString(value string) []byte {
	length := len(value) + 1
	data := make([]byte, 4+length)
	wl.PutUint32(data[:4], uint32(length))
	copy(data[4:], value)
	return data
}
