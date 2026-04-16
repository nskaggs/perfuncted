package window

import (
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
	d.ctx.WriteMsg(buf, nil)
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
	d.ctx.WriteMsg(buf, nil)
	return cb, nil
}
func (d *mockWaylandDisplay) RoundTrip() error {
	// Mock RoundTrip: simulate a done event immediately
	cb, _ := d.Sync()
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

func putStr(buf []byte, s string) []byte {
	// Encode length (strlen+1), string bytes, null terminator and padding to 4B.
	n := uint32(len(s) + 1)
	buf = put32(buf, n)
	buf = append(buf, s...)
	padded := (int(n) + 3) &^ 3
	zeros := int(padded) - len(s)
	for i := 0; i < zeros; i++ {
		buf = append(buf, 0)
	}
	return buf
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

// TestWaylandWindowManager_New tests the NewWaylandWindowManager function.
func TestWaylandWindowManager_New(t *testing.T) {
	tests := []struct {
		name          string
		mockSockPath  string
		mockGlobals   []wl.GlobalEvent
		expectError   bool
		expectedError string
	}{
		{
			name:          "WAYLAND_DISPLAY not set",
			mockSockPath:  "",
			expectError:   true,
			expectedError: "window/wayland: WAYLAND_DISPLAY not set",
		},
		{
			name:         "No foreign toplevel protocols advertised",
			mockSockPath: "/tmp/wayland-0",
			mockGlobals: []wl.GlobalEvent{
				{Name: 1, Interface: "wl_compositor", Version: 4},
				{Name: 2, Interface: "wl_seat", Version: 7},
			},
			expectError:   true,
			expectedError: "window/wayland: neither ext_foreign_toplevel_list_v1 nor zwlr_foreign_toplevel_manager_v1 advertised",
		},
		{
			name:         "ext_foreign_toplevel_list_v1 advertised",
			mockSockPath: "/tmp/wayland-0",
			mockGlobals: []wl.GlobalEvent{
				{Name: 1, Interface: "wl_compositor", Version: 4},
				{Name: 2, Interface: "ext_foreign_toplevel_list_v1", Version: 1},
				{Name: 3, Interface: "wl_seat", Version: 7},
			},
			expectError: false,
		},
		{
			name:         "zwlr_foreign_toplevel_manager_v1 advertised",
			mockSockPath: "/tmp/wayland-0",
			mockGlobals: []wl.GlobalEvent{
				{Name: 1, Interface: "wl_compositor", Version: 4},
				{Name: 2, Interface: "wl_seat", Version: 7},
				{Name: 3, Interface: "zwlr_foreign_toplevel_manager_v1", Version: 3},
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Mock wl.SocketPath
			originalSocketPath := os.Getenv("WAYLAND_DISPLAY")
			if tt.mockSockPath == "" {
				os.Unsetenv("WAYLAND_DISPLAY")
			} else {
				os.Setenv("WAYLAND_DISPLAY", tt.mockSockPath)
			}
			defer os.Setenv("WAYLAND_DISPLAY", originalSocketPath)

			ctx := &mockWaylandContext{objects: make(map[uint32]wl.Proxy), nextID: 0}
			display := &mockWaylandDisplay{ctx: ctx}
			_ = display
			registry := &mockRegistry{}
			ctx.Register(registry)

			// Mock registry binding and global events
			registry.SetGlobalHandler(func(ev wl.GlobalEvent) {
				// Simulate compositor advertising globals
				for _, global := range tt.mockGlobals {
					if global.Interface == ev.Interface {
						// Simulate binding to the global
						var buf []byte
						buf = put32(buf, registry.ID())
						buf = put32(buf, (28 << 16)) // size=28, opcode=0 (bind)
						buf = put32(buf, global.Name)
						buf = putStr(buf, global.Interface)
						buf = put32(buf, global.Version)
						var newID uint32 = 100 // Dummy new ID
						buf = put32(buf, newID)
						ctx.WriteMsg(buf, nil)

						// Simulate new_id event for the bound interface
						var newIDMsg []byte
						newIDMsg = put32(newIDMsg, 0)         // Sender ID is compositor (0)
						newIDMsg = put32(newIDMsg, (4 << 16)) // size=4, opcode=0 (new_id)
						newIDMsg = put32(newIDMsg, newID)
						ctx.objects[newID] = &mockRawProxy{OnEventMock: func(opcode uint32, fd int, data []byte) {}} // Add a mock proxy
						ctx.objects[newID].SetID(newID)                                                              // Ensure ID is set on mock proxy
						ctx.objects[newID].SetCtx(ctx)
						ctx.objects[newID].Dispatch(0, -1, newIDMsg)
						// Dispatching here directly might lead to issues with the mock dispatchFn.
						// Let RoundTrip handle dispatching.
					}
				}
			})

			// Mock display.RoundTrip to process the registry events
			ctx.dispatchFn = func() error {
				// Simulate receiving globals from compositor
				for _, global := range tt.mockGlobals {
					var msg []byte
					msg = put32(msg, 0)          // compositor ID
					msg = put32(msg, (28 << 16)) // size=28, opcode=0 (global)
					msg = put32(msg, global.Name)
					msg = putStr(msg, global.Interface)
					msg = put32(msg, global.Version)
					ctx.objects[registry.ID()].Dispatch(0, -1, msg) // Dispatch global event to registry

					// Simulate new_id event for the manager proxy itself
					if global.Interface == "ext_foreign_toplevel_list_v1" || global.Interface == "zwlr_foreign_toplevel_manager_v1" {
						var managerProxyNewIDMsg []byte
						managerProxyNewIDMsg = put32(managerProxyNewIDMsg, 0)         // compositor ID
						managerProxyNewIDMsg = put32(managerProxyNewIDMsg, (4 << 16)) // size=4, opcode=0 (new_id)
						managerProxyNewIDMsg = put32(managerProxyNewIDMsg, 101)       // Manager proxy ID

						// Corrected initialization for mock manager proxy
						mockManagerProxy := &mockRawProxy{} // Initialize struct first
						ctx.SetProxy(101, mockManagerProxy) // Use SetProxy to handle ID and Ctx
						mockManagerProxy.Dispatch(0, -1, managerProxyNewIDMsg)
					}
				}
				return nil
			}

			wm, err := NewWaylandWindowManager()

			if (err != nil) != tt.expectError {
				t.Fatalf("NewWaylandWindowManager() error = %v, expectError %v", err, tt.expectError)
			}
			if err != nil && !strings.Contains(err.Error(), tt.expectedError) {
				t.Errorf("NewWaylandWindowManager() error = %v, expected error containing %q", err, tt.expectedError)
			}
			if wm != nil {
				wm.Close() // Clean up
			}
		})
	}
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
			ctx := &mockWaylandContext{objects: make(map[uint32]wl.Proxy), nextID: 0}
			display := &mockWaylandDisplay{ctx: ctx}
			_ = display
			registry := &mockRegistry{}
			ctx.Register(registry)

			managerProxyID := uint32(101)
			mockManagerProxy := &mockRawProxy{}
			ctx.SetProxy(managerProxyID, mockManagerProxy)
			mockManagerProxy.SetCtx(ctx)

			var mockHandleProxy *mockRawProxy
			var handleID uint32 = 50
			if tc.windowExists {
				mockHandleProxy = &mockRawProxy{}
				ctx.SetProxy(handleID, mockHandleProxy)
				mockHandleProxy.SetCtx(ctx)
				// Mock event handler to simulate title being set
				mockHandleProxy.OnEventMock = func(opcode uint32, fd int, data []byte) {
					if opcode == 0 { // title event
						var buf []byte
						buf = put32(buf, wl.Uint32(data[0:4]))
						buf = putStr(buf, "Test Window")
						mockHandleProxy.Dispatch(0, -1, buf)
					}
				}
			}

			wm := &WaylandWindowManager{
				display:   display,
				registry:  registry,
				wlrMgrID:  1, // Simulate manager being advertised
				toplevels: make(map[uint32]*Info),
			}
			ctx.objects[managerProxyID] = mockManagerProxy // Ensure manager proxy is in context
			if tc.windowExists {
				wm.toplevels[handleID] = &Info{ID: uint64(handleID), Title: "Test Window"}
			}

			// Mock RoundTrip to ensure events are processed
			ctx.dispatchFn = func() error {
				// Simulate new_id for manager proxy and its events
				var managerProxyNewIDMsg []byte
				managerProxyNewIDMsg = put32(managerProxyNewIDMsg, 0)
				managerProxyNewIDMsg = put32(managerProxyNewIDMsg, (4 << 16))
				managerProxyNewIDMsg = put32(managerProxyNewIDMsg, managerProxyID)
				ctx.objects[managerProxyID].Dispatch(0, -1, managerProxyNewIDMsg)

				if tc.windowExists {
					// Simulate new_id for handle proxy and its events
					var handleNewIDMsg []byte
					handleNewIDMsg = put32(handleNewIDMsg, 0)
					handleNewIDMsg = put32(handleNewIDMsg, (4 << 16))
					handleNewIDMsg = put32(handleNewIDMsg, handleID)
					ctx.objects[handleID] = mockHandleProxy
					ctx.objects[handleID].Dispatch(0, -1, handleNewIDMsg)
					// Simulate title event
					mockHandleProxy.OnEventMock(0, -1, []byte{})
				}
				return nil
			}

			err := wm.Activate(tc.windowTitle)

			if tc.expectedError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.expectedError) {
					t.Errorf("Activate(%q) error = %v, expected error containing %q", tc.windowTitle, err, tc.expectedError)
				}
			} else if err != nil {
				t.Errorf("Activate(%q) unexpected error: %v", tc.windowTitle, err)
			}

			// Check if the correct message was sent
			if tc.expectedCmd != "" && tc.expectedError == "" {
				sent := false
				for _, msg := range ctx.sentMsgs {
					// Check sender ID, opcode, and size for activate request
					sender := wl.Uint32(msg[0:4])
					sizeOpcode := wl.Uint32(msg[4:8])
					opcode := sizeOpcode & 0xffff
					if sender == handleID && opcode == 4 { // Opcode 4 for activate
						sent = true
						break
					}
				}
				if !sent {
					t.Errorf("Activate(%q) did not send the expected activate request", tc.windowTitle)
				}
			}
		})
	}
}

// TestWaylandWindowManager_CloseWindow tests the CloseWindow method.
func TestWaylandWindowManager_CloseWindow(t *testing.T) {
	testCases := []struct {
		name          string
		windowTitle   string
		windowExists  bool
		expectedCmd   string
		expectedError string
	}{
		{
			name:         "Window exists",
			windowTitle:  "CloseMe",
			windowExists: true,
			expectedCmd:  "close", // Check for the presence of the close request
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
			ctx := &mockWaylandContext{objects: make(map[uint32]wl.Proxy), nextID: 0}
			display := &mockWaylandDisplay{ctx: ctx}
			_ = display
			registry := &mockRegistry{}
			ctx.Register(registry)

			managerProxyID := uint32(101)
			mockManagerProxy := &mockRawProxy{}
			ctx.SetProxy(managerProxyID, mockManagerProxy)
			mockManagerProxy.SetCtx(ctx)

			var mockHandleProxy *mockRawProxy
			var handleID uint32 = 50
			if tc.windowExists {
				mockHandleProxy = &mockRawProxy{}
				ctx.SetProxy(handleID, mockHandleProxy)
				mockHandleProxy.SetCtx(ctx)
				mockHandleProxy.OnEventMock = func(opcode uint32, fd int, data []byte) {
					if opcode == 0 { // title event
						var buf []byte
						buf = put32(buf, wl.Uint32(data[0:4]))
						buf = putStr(buf, "CloseMe")
						mockHandleProxy.Dispatch(0, -1, buf)
					}
				}
			}

			wm := &WaylandWindowManager{
				display:   display,
				registry:  registry,
				wlrMgrID:  1,
				toplevels: make(map[uint32]*Info),
			}
			ctx.objects[managerProxyID] = mockManagerProxy
			if tc.windowExists {
				wm.toplevels[handleID] = &Info{ID: uint64(handleID), Title: "CloseMe"}
			}

			// Mock RoundTrip to ensure events are processed
			ctx.dispatchFn = func() error {
				var managerProxyNewIDMsg []byte
				managerProxyNewIDMsg = put32(managerProxyNewIDMsg, 0)
				managerProxyNewIDMsg = put32(managerProxyNewIDMsg, (4 << 16))
				managerProxyNewIDMsg = put32(managerProxyNewIDMsg, managerProxyID)
				ctx.objects[managerProxyID].Dispatch(0, -1, managerProxyNewIDMsg)

				if tc.windowExists {
					var handleNewIDMsg []byte
					handleNewIDMsg = put32(handleNewIDMsg, 0)
					handleNewIDMsg = put32(handleNewIDMsg, (4 << 16))
					handleNewIDMsg = put32(handleNewIDMsg, handleID)
					ctx.objects[handleID] = mockHandleProxy
					ctx.objects[handleID].Dispatch(0, -1, handleNewIDMsg)
					mockHandleProxy.OnEventMock(0, -1, []byte{})
				}
				return nil
			}

			err := wm.CloseWindow(tc.windowTitle)

			if tc.expectedError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.expectedError) {
					t.Errorf("CloseWindow(%q) error = %v, expected error containing %q", tc.windowTitle, err, tc.expectedError)
				}
			} else if err != nil {
				t.Errorf("CloseWindow(%q) unexpected error: %v", tc.windowTitle, err)
			}

			if tc.expectedCmd != "" && tc.expectedError == "" {
				sent := false
				for _, msg := range ctx.sentMsgs {
					sender := wl.Uint32(msg[0:4])
					sizeOpcode := wl.Uint32(msg[4:8])
					opcode := sizeOpcode & 0xffff
					if sender == handleID && opcode == 5 { // Opcode 5 for close
						sent = true
						break
					}
				}
				if !sent {
					t.Errorf("CloseWindow(%q) did not send the expected close request", tc.windowTitle)
				}
			}
		})
	}
}

// TestWaylandWindowManager_Minimize tests the Minimize method.
func TestWaylandWindowManager_Minimize(t *testing.T) {
	testCases := []struct {
		name          string
		windowTitle   string
		windowExists  bool
		expectedCmd   string
		expectedError string
	}{
		{
			name:         "Window exists",
			windowTitle:  "MinimizeMe",
			windowExists: true,
			expectedCmd:  "minimize", // Check for the presence of the minimize request
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
			ctx := &mockWaylandContext{objects: make(map[uint32]wl.Proxy), nextID: 0}
			display := &mockWaylandDisplay{ctx: ctx}
			_ = display
			registry := &mockRegistry{}
			ctx.Register(registry)

			managerProxyID := uint32(101)
			mockManagerProxy := &mockRawProxy{}
			ctx.SetProxy(managerProxyID, mockManagerProxy)
			mockManagerProxy.SetCtx(ctx)

			var mockHandleProxy *mockRawProxy
			var handleID uint32 = 50
			if tc.windowExists {
				mockHandleProxy = &mockRawProxy{}
				ctx.SetProxy(handleID, mockHandleProxy)
				mockHandleProxy.SetCtx(ctx)
				mockHandleProxy.OnEventMock = func(opcode uint32, fd int, data []byte) {
					if opcode == 0 { // title event
						var buf []byte
						buf = put32(buf, wl.Uint32(data[0:4]))
						buf = putStr(buf, "MinimizeMe")
						mockHandleProxy.Dispatch(0, -1, buf)
					}
				}
			}

			wm := &WaylandWindowManager{
				display:   display,
				registry:  registry,
				wlrMgrID:  1,
				toplevels: make(map[uint32]*Info),
			}
			ctx.objects[managerProxyID] = mockManagerProxy
			if tc.windowExists {
				wm.toplevels[handleID] = &Info{ID: uint64(handleID), Title: "MinimizeMe"}
			}

			// Mock RoundTrip to ensure events are processed
			ctx.dispatchFn = func() error {
				var managerProxyNewIDMsg []byte
				managerProxyNewIDMsg = put32(managerProxyNewIDMsg, 0)
				managerProxyNewIDMsg = put32(managerProxyNewIDMsg, (4 << 16))
				managerProxyNewIDMsg = put32(managerProxyNewIDMsg, managerProxyID)
				ctx.objects[managerProxyID].Dispatch(0, -1, managerProxyNewIDMsg)

				if tc.windowExists {
					var handleNewIDMsg []byte
					handleNewIDMsg = put32(handleNewIDMsg, 0)
					handleNewIDMsg = put32(handleNewIDMsg, (4 << 16))
					handleNewIDMsg = put32(handleNewIDMsg, handleID)
					ctx.objects[handleID] = mockHandleProxy
					ctx.objects[handleID].Dispatch(0, -1, handleNewIDMsg)
					mockHandleProxy.OnEventMock(0, -1, []byte{})
				}
				return nil
			}

			err := wm.Minimize(tc.windowTitle)

			if tc.expectedError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.expectedError) {
					t.Errorf("Minimize(%q) error = %v, expected error containing %q", tc.windowTitle, err, tc.expectedError)
				}
			} else if err != nil {
				t.Errorf("Minimize(%q) unexpected error: %v", tc.windowTitle, err)
			}

			if tc.expectedCmd != "" && tc.expectedError == "" {
				sent := false
				for _, msg := range ctx.sentMsgs {
					sender := wl.Uint32(msg[0:4])
					sizeOpcode := wl.Uint32(msg[4:8])
					opcode := sizeOpcode & 0xffff
					if sender == handleID && opcode == 2 { // Opcode 2 for set_minimized
						sent = true
						break
					}
				}
				if !sent {
					t.Errorf("Minimize(%q) did not send the expected minimize request", tc.windowTitle)
				}
			}
		})
	}
}

// TestWaylandWindowManager_Maximize tests the Maximize method.
func TestWaylandWindowManager_Maximize(t *testing.T) {
	testCases := []struct {
		name          string
		windowTitle   string
		windowExists  bool
		expectedCmd   string
		expectedError string
	}{
		{
			name:         "Window exists",
			windowTitle:  "MaximizeMe",
			windowExists: true,
			expectedCmd:  "maximize", // Check for the presence of the maximize request
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
			ctx := &mockWaylandContext{objects: make(map[uint32]wl.Proxy), nextID: 0}
			display := &mockWaylandDisplay{ctx: ctx}
			_ = display
			registry := &mockRegistry{}
			ctx.Register(registry)

			managerProxyID := uint32(101)
			mockManagerProxy := &mockRawProxy{}
			ctx.SetProxy(managerProxyID, mockManagerProxy)
			mockManagerProxy.SetCtx(ctx)

			var mockHandleProxy *mockRawProxy
			var handleID uint32 = 50
			if tc.windowExists {
				mockHandleProxy = &mockRawProxy{}
				ctx.SetProxy(handleID, mockHandleProxy)
				mockHandleProxy.SetCtx(ctx)
				mockHandleProxy.OnEventMock = func(opcode uint32, fd int, data []byte) {
					if opcode == 0 { // title event
						var buf []byte
						buf = put32(buf, wl.Uint32(data[0:4]))
						buf = putStr(buf, "MaximizeMe")
						mockHandleProxy.Dispatch(0, -1, buf)
					}
				}
			}

			wm := &WaylandWindowManager{
				display:   display,
				registry:  registry,
				wlrMgrID:  1,
				toplevels: make(map[uint32]*Info),
			}
			ctx.objects[managerProxyID] = mockManagerProxy
			if tc.windowExists {
				wm.toplevels[handleID] = &Info{ID: uint64(handleID), Title: "MaximizeMe"}
			}

			// Mock RoundTrip to ensure events are processed
			ctx.dispatchFn = func() error {
				var managerProxyNewIDMsg []byte
				managerProxyNewIDMsg = put32(managerProxyNewIDMsg, 0)
				managerProxyNewIDMsg = put32(managerProxyNewIDMsg, (4 << 16))
				managerProxyNewIDMsg = put32(managerProxyNewIDMsg, managerProxyID)
				ctx.objects[managerProxyID].Dispatch(0, -1, managerProxyNewIDMsg)

				if tc.windowExists {
					var handleNewIDMsg []byte
					handleNewIDMsg = put32(handleNewIDMsg, 0)
					handleNewIDMsg = put32(handleNewIDMsg, (4 << 16))
					handleNewIDMsg = put32(handleNewIDMsg, handleID)
					ctx.objects[handleID] = mockHandleProxy
					ctx.objects[handleID].Dispatch(0, -1, handleNewIDMsg)
					mockHandleProxy.OnEventMock(0, -1, []byte{})
				}
				return nil
			}

			err := wm.Maximize(tc.windowTitle)

			if tc.expectedError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.expectedError) {
					t.Errorf("Maximize(%q) error = %v, expected error containing %q", tc.windowTitle, err, tc.expectedError)
				}
			} else if err != nil {
				t.Errorf("Maximize(%q) unexpected error: %v", tc.windowTitle, err)
			}

			if tc.expectedCmd != "" && tc.expectedError == "" {
				sent := false
				for _, msg := range ctx.sentMsgs {
					sender := wl.Uint32(msg[0:4])
					sizeOpcode := wl.Uint32(msg[4:8])
					opcode := sizeOpcode & 0xffff
					if sender == handleID && opcode == 0 { // Opcode 0 for set_maximized
						sent = true
						break
					}
				}
				if !sent {
					t.Errorf("Maximize(%q) did not send the expected maximize request", tc.windowTitle)
				}
			}
		})
	}
}

// TestWaylandWindowManager_List tests the List method.
func TestWaylandWindowManager_List(t *testing.T) {
	ctx := &mockWaylandContext{objects: make(map[uint32]wl.Proxy), nextID: 0}
	display := &mockWaylandDisplay{ctx: ctx}
	_ = display
	registry := &mockRegistry{}
	ctx.Register(registry)

	managerProxyID := uint32(101)
	mockManagerProxy := &mockRawProxy{}
	ctx.SetProxy(managerProxyID, mockManagerProxy)
	mockManagerProxy.SetCtx(ctx)

	// Setup mock handles
	handleID1 := uint32(50)
	mockHandleProxy1 := &mockRawProxy{}
	ctx.SetProxy(handleID1, mockHandleProxy1)
	mockHandleProxy1.SetCtx(ctx)
	mockHandleProxy1.OnEventMock = func(opcode uint32, fd int, data []byte) {
		if opcode == 0 { // title event
			var buf []byte
			buf = put32(buf, wl.Uint32(data[0:4]))
			buf = putStr(buf, "Window One")
			mockHandleProxy1.Dispatch(0, -1, buf)
		}
	}

	handleID2 := uint32(51)
	mockHandleProxy2 := &mockRawProxy{}
	ctx.SetProxy(handleID2, mockHandleProxy2)
	mockHandleProxy2.SetCtx(ctx)
	mockHandleProxy2.OnEventMock = func(opcode uint32, fd int, data []byte) {
		if opcode == 0 { // title event
			var buf []byte
			buf = put32(buf, wl.Uint32(data[0:4]))
			buf = putStr(buf, "Window Two")
			mockHandleProxy2.Dispatch(0, -1, buf)
		}
	}

	wm := &WaylandWindowManager{
		display:   display,
		registry:  registry,
		wlrMgrID:  1,
		toplevels: make(map[uint32]*Info),
	}
	ctx.objects[managerProxyID] = mockManagerProxy

	// Manually add handles to the toplevels map as fetchToplevels would do
	wm.toplevels[handleID1] = &Info{ID: uint64(handleID1), Title: "Window One"}
	wm.toplevels[handleID2] = &Info{ID: uint64(handleID2), Title: "Window Two"}

	// Mock RoundTrip to ensure events are processed
	ctx.dispatchFn = func() error {
		// Simulate new_id for manager proxy
		var managerProxyNewIDMsg []byte
		managerProxyNewIDMsg = put32(managerProxyNewIDMsg, 0)
		managerProxyNewIDMsg = put32(managerProxyNewIDMsg, (4 << 16))
		managerProxyNewIDMsg = put32(managerProxyNewIDMsg, managerProxyID)
		ctx.objects[managerProxyID].Dispatch(0, -1, managerProxyNewIDMsg)

		// Simulate new_id for handle proxies
		var handleNewIDMsg1 []byte
		handleNewIDMsg1 = put32(handleNewIDMsg1, 0)
		handleNewIDMsg1 = put32(handleNewIDMsg1, (4 << 16))
		handleNewIDMsg1 = put32(handleNewIDMsg1, handleID1)
		ctx.objects[handleID1] = mockHandleProxy1
		ctx.objects[handleID1].Dispatch(0, -1, handleNewIDMsg1)

		var handleNewIDMsg2 []byte
		handleNewIDMsg2 = put32(handleNewIDMsg2, 0)
		handleNewIDMsg2 = put32(handleNewIDMsg2, (4 << 16))
		handleNewIDMsg2 = put32(handleNewIDMsg2, handleID2)
		ctx.objects[handleID2] = mockHandleProxy2
		ctx.objects[handleID2].Dispatch(0, -1, handleNewIDMsg2)

		// Simulate title events
		mockHandleProxy1.OnEventMock(0, -1, []byte{})
		mockHandleProxy2.OnEventMock(0, -1, []byte{})
		return nil
	}

	windows, err := wm.List()

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
	ctx := &mockWaylandContext{objects: make(map[uint32]wl.Proxy), nextID: 0}
	display := &mockWaylandDisplay{ctx: ctx}
	_ = display
	registry := &mockRegistry{}
	ctx.Register(registry)

	managerProxyID := uint32(101)
	mockManagerProxy := &mockRawProxy{}
	ctx.SetProxy(managerProxyID, mockManagerProxy)
	mockManagerProxy.SetCtx(ctx)

	handleID1 := uint32(50)
	mockHandleProxy1 := &mockRawProxy{}
	ctx.SetProxy(handleID1, mockHandleProxy1)
	mockHandleProxy1.SetCtx(ctx)
	mockHandleProxy1.OnEventMock = func(opcode uint32, fd int, data []byte) {
		if opcode == 0 { // title event
			var buf []byte
			buf = put32(buf, wl.Uint32(data[0:4]))
			buf = putStr(buf, "My Test Window")
			mockHandleProxy1.Dispatch(0, -1, buf)
		}
	}

	handleID2 := uint32(51)
	mockHandleProxy2 := &mockRawProxy{}
	ctx.SetProxy(handleID2, mockHandleProxy2)
	mockHandleProxy2.SetCtx(ctx)
	mockHandleProxy2.OnEventMock = func(opcode uint32, fd int, data []byte) {
		if opcode == 0 { // title event
			var buf []byte
			buf = put32(buf, wl.Uint32(data[0:4]))
			buf = putStr(buf, "Another Window")
			mockHandleProxy2.Dispatch(0, -1, buf)
		}
	}

	wm := &WaylandWindowManager{
		display:   display,
		registry:  registry,
		wlrMgrID:  1,
		toplevels: make(map[uint32]*Info),
	}
	ctx.objects[managerProxyID] = mockManagerProxy

	wm.toplevels[handleID1] = &Info{ID: uint64(handleID1), Title: "My Test Window"}
	wm.toplevels[handleID2] = &Info{ID: uint64(handleID2), Title: "Another Window"}

	// Mock RoundTrip to ensure events are processed
	ctx.dispatchFn = func() error {
		// Simulate new_id for manager proxy
		var managerProxyNewIDMsg []byte
		managerProxyNewIDMsg = put32(managerProxyNewIDMsg, 0)
		managerProxyNewIDMsg = put32(managerProxyNewIDMsg, (4 << 16))
		managerProxyNewIDMsg = put32(managerProxyNewIDMsg, managerProxyID)
		ctx.objects[managerProxyID].Dispatch(0, -1, managerProxyNewIDMsg)

		// Simulate new_id for handle proxies
		var handleNewIDMsg1 []byte
		handleNewIDMsg1 = put32(handleNewIDMsg1, 0)
		handleNewIDMsg1 = put32(handleNewIDMsg1, (4 << 16))
		handleNewIDMsg1 = put32(handleNewIDMsg1, handleID1)
		ctx.objects[handleID1] = mockHandleProxy1
		ctx.objects[handleID1].Dispatch(0, -1, handleNewIDMsg1)

		var handleNewIDMsg2 []byte
		handleNewIDMsg2 = put32(handleNewIDMsg2, 0)
		handleNewIDMsg2 = put32(handleNewIDMsg2, (4 << 16))
		handleNewIDMsg2 = put32(handleNewIDMsg2, handleID2)
		ctx.objects[handleID2] = mockHandleProxy2
		ctx.objects[handleID2].Dispatch(0, -1, handleNewIDMsg2)

		// Simulate title events
		mockHandleProxy1.OnEventMock(0, -1, []byte{})
		mockHandleProxy2.OnEventMock(0, -1, []byte{})
		return nil
	}

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

	_, err := wm.List()
	if err == nil || !strings.Contains(err.Error(), "window/wayland: round-trip: simulated roundtrip error") {
		t.Errorf("List() error = %v, expected error containing %q", err, "window/wayland: round-trip: simulated roundtrip error")
	}
}
