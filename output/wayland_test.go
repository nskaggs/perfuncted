package output

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/wl"
)

func wlStringData(s string) []byte {
	n := uint32(len(s) + 1)
	padded := (n + 3) &^ 3
	out := make([]byte, 4+int(padded))
	wl.PutUint32(out[0:4], n)
	copy(out[4:], s)
	return out
}

func TestWaylandListerListCancelsBlockedRoundTrip(t *testing.T) {
	sock := t.TempDir() + "/wayland.sock"
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatalf("ListenUnix: %v", err)
	}
	defer listener.Close()

	serverConn := make(chan net.Conn, 1)
	requestRead := make(chan struct{})
	serverErr := make(chan error, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			serverErr <- acceptErr
			return
		}
		serverConn <- conn
		var request [12]byte
		_, readErr := io.ReadFull(conn, request[:])
		if readErr != nil {
			serverErr <- readErr
			return
		}
		close(requestRead)
	}()

	waylandCtx, err := wl.Connect(sock)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer waylandCtx.Close()
	conn := <-serverConn
	defer conn.Close()

	lister := &WaylandLister{
		session: &wl.Session{
			Sock:    sock,
			Ctx:     waylandCtx,
			Display: wl.NewDisplay(waylandCtx),
		},
		outputs: []*waylandOutput{{info: Info{Name: "test", Backend: "wayland"}}},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	listDone := make(chan error, 1)
	go func() {
		_, listErr := lister.List(ctx)
		listDone <- listErr
	}()

	select {
	case <-requestRead:
	case err := <-serverErr:
		t.Fatalf("server read: %v", err)
	case <-time.After(time.Second):
		t.Fatal("Wayland sync request was not sent")
	}
	cancel()

	select {
	case err := <-listDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("List error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("List did not return after context cancellation")
	}
}

func TestNewWaylandListerPreservesRepeatedOutputs(t *testing.T) {
	sock := t.TempDir() + "/wayland.sock"
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		t.Fatalf("ListenUnix: %v", err)
	}
	serverDone := make(chan error, 1)
	go func() { serverDone <- serveWaylandOutputs(listener) }()

	lister, err := NewWaylandLister(sock)
	if err != nil {
		listener.Close()
		t.Fatalf("NewWaylandLister: %v", err)
	}
	defer func() {
		_ = lister.Close()
		_ = listener.Close()
	}()
	globals := lister.session.GlobalsSnapshot()
	if len(globals) != 2 || globals[0].Name != 4 || globals[1].Name != 12 {
		t.Fatalf("session globals = %+v, want wl_output names 4, 12", globals)
	}

	got, err := lister.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("List returned %d outputs, want 2: %+v", len(got), got)
	}
	if got[0].Name != "DP-1" || got[1].Name != "DP-2" {
		t.Fatalf("output order = %q, %q; want DP-1, DP-2", got[0].Name, got[1].Name)
	}
	if err := lister.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("fake compositor: %v", err)
	}
}

func serveWaylandOutputs(listener *net.UnixListener) error {
	conn, err := listener.AcceptUnix()
	if err != nil {
		return err
	}
	defer conn.Close()

	var registryID uint32
	outputIDs := make(map[uint32]uint32, 2)
	roundTrips := 0
	for {
		message, err := readWaylandMessage(conn)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		var handleErr error
		registryID, roundTrips, handleErr = handleWaylandOutputRequest(
			conn,
			message,
			registryID,
			outputIDs,
			roundTrips,
		)
		if handleErr != nil {
			return handleErr
		}
	}
}

func handleWaylandOutputRequest(
	conn net.Conn,
	message []byte,
	registryID uint32,
	outputIDs map[uint32]uint32,
	roundTrips int,
) (uint32, int, error) {
	sender := wl.Uint32(message[0:4])
	opcode := wl.Uint32(message[4:8]) & 0xffff
	payload := message[8:]
	switch {
	case sender == 1 && opcode == 1:
		if len(payload) < 4 {
			return registryID, roundTrips, errors.New("get_registry request missing new object ID")
		}
		return wl.Uint32(payload[:4]), roundTrips, nil
	case sender == 1 && opcode == 0:
		roundTrips++
		if err := sendWaylandRoundTripEvents(conn, payload, registryID, outputIDs, roundTrips); err != nil {
			return registryID, roundTrips, err
		}
		return registryID, roundTrips, nil
	case sender == registryID && opcode == 0:
		name, objectID, err := parseWaylandBind(payload)
		if err != nil {
			return registryID, roundTrips, err
		}
		if name == 4 || name == 12 {
			outputIDs[name] = objectID
		}
	}
	return registryID, roundTrips, nil
}

func sendWaylandRoundTripEvents(
	conn net.Conn,
	payload []byte,
	registryID uint32,
	outputIDs map[uint32]uint32,
	roundTrips int,
) error {
	switch roundTrips {
	case 1:
		if err := sendWaylandGlobalEvents(conn, registryID); err != nil {
			return err
		}
	case 2:
		if len(outputIDs) != 2 {
			return fmt.Errorf("bound output objects = %d, want 2", len(outputIDs))
		}
		for _, event := range []struct {
			name uint32
			text string
		}{
			{name: 12, text: "DP-2"},
			{name: 4, text: "DP-1"},
		} {
			if err := writeWaylandMessage(
				conn,
				outputIDs[event.name],
				4,
				wlStringData(event.text),
			); err != nil {
				return err
			}
		}
	}
	if len(payload) < 4 {
		return errors.New("sync request missing callback ID")
	}
	return writeWaylandMessage(conn, wl.Uint32(payload[:4]), 0, nil)
}

func sendWaylandGlobalEvents(conn net.Conn, registryID uint32) error {
	for _, event := range []struct {
		name    uint32
		version uint32
	}{
		{name: 12, version: 4},
		{name: 4, version: 4},
	} {
		data := make([]byte, 0, 32)
		var name [4]byte
		wl.PutUint32(name[:], event.name)
		data = append(data, name[:]...)
		data = appendWlStringData(data, "wl_output")
		var version [4]byte
		wl.PutUint32(version[:], event.version)
		data = append(data, version[:]...)
		if err := writeWaylandMessage(conn, registryID, 0, data); err != nil {
			return err
		}
	}
	return nil
}

func parseWaylandBind(payload []byte) (uint32, uint32, error) {
	if len(payload) < 8 {
		return 0, 0, errors.New("bind request missing interface")
	}
	name := wl.Uint32(payload[:4])
	length := int(wl.Uint32(payload[4:8]))
	if length <= 0 {
		return 0, 0, errors.New("bind request has empty interface")
	}
	padded := (length + 3) &^ 3
	offset := 4 + 4 + padded
	if offset+8 > len(payload) {
		return 0, 0, errors.New("bind request missing object ID")
	}
	return name, wl.Uint32(payload[offset+4 : offset+8]), nil
}

func readWaylandMessage(conn net.Conn) ([]byte, error) {
	var header [8]byte
	if _, err := io.ReadFull(conn, header[:]); err != nil {
		return nil, err
	}
	size := int(wl.Uint32(header[4:8]) >> 16)
	if size < len(header) {
		return nil, fmt.Errorf("invalid Wayland message size %d", size)
	}
	message := make([]byte, size)
	copy(message, header[:])
	if _, err := io.ReadFull(conn, message[len(header):]); err != nil {
		return nil, err
	}
	return message, nil
}

func writeWaylandMessage(conn net.Conn, sender, opcode uint32, payload []byte) error {
	message := make([]byte, 8+len(payload))
	wl.PutUint32(message[0:4], sender)
	wl.PutUint32(message[4:8], uint32(len(message))<<16|opcode)
	copy(message[8:], payload)
	for len(message) > 0 {
		n, err := conn.Write(message)
		if err != nil {
			return err
		}
		message = message[n:]
	}
	return nil
}

func appendWlStringData(dst []byte, s string) []byte {
	return append(dst, wlStringData(s)...)
}

func TestReadWlString(t *testing.T) {
	t.Parallel()

	data := wlStringData("HDMI-A-1")

	got, next, ok := readWlString(data, 0)
	if !ok {
		t.Fatal("readWlString returned ok=false")
	}
	if got != "HDMI-A-1" {
		t.Fatalf("string = %q, want %q", got, "HDMI-A-1")
	}
	if next != len(data) {
		t.Fatalf("next = %d, want %d", next, len(data))
	}
}

func TestReadWlStringRejectsMalformedData(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data []byte
	}{
		{
			name: "missing nul terminator",
			data: func() []byte {
				data := make([]byte, 8)
				wl.PutUint32(data[0:4], 4)
				copy(data[4:], "abcd")
				return data
			}(),
		},
		{
			name: "missing padding bytes",
			data: func() []byte {
				data := make([]byte, 6)
				wl.PutUint32(data[0:4], 2)
				data[4] = 'x'
				data[5] = 0
				return data
			}(),
		},
		{
			name: "length extends beyond data",
			data: func() []byte {
				data := make([]byte, 8)
				wl.PutUint32(data[0:4], 8)
				return data
			}(),
		},
		{
			name: "zero length",
			data: func() []byte {
				data := make([]byte, 4)
				wl.PutUint32(data[0:4], 0)
				return data
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got, next, ok := readWlString(tt.data, 0); ok {
				t.Fatalf("readWlString(%v) = %q, next=%d, ok=true; want ok=false", tt.data, got, next)
			}
		})
	}
}

func TestWaylandOutputEventParsing(t *testing.T) {
	t.Parallel()

	out := &waylandOutput{
		info: Info{Name: "fallback-name", Backend: "wayland", Scale: 1},
	}
	proxy := &wl.RawProxy{}
	out.updateProxy(proxy)

	geometry := make([]byte, 20)
	wl.PutUint32(geometry[0:4], ^uint32(9))
	wl.PutUint32(geometry[4:8], 20)
	wl.PutUint32(geometry[8:12], 600)
	wl.PutUint32(geometry[12:16], 340)
	geometry = appendWlStringData(geometry, "Acme")
	geometry = appendWlStringData(geometry, "Panel 4K")
	proxy.OnEvent(0, 0, geometry)

	mode := make([]byte, 16)
	wl.PutUint32(mode[0:4], 1)
	wl.PutUint32(mode[4:8], 3840)
	wl.PutUint32(mode[8:12], 2160)
	proxy.OnEvent(1, 0, mode)

	scale := make([]byte, 4)
	wl.PutUint32(scale, 2)
	proxy.OnEvent(3, 0, scale)

	proxy.OnEvent(4, 0, wlStringData("DP-1"))
	proxy.OnEvent(5, 0, wlStringData("Acme Panel 4K"))

	if out.info.Geometry.X != -10 || out.info.Geometry.Y != 20 {
		t.Fatalf("geometry origin = %+v, want -10,20", out.info.Geometry)
	}
	if out.info.PhysicalW != 600 || out.info.PhysicalH != 340 {
		t.Fatalf("physical size = %dx%d, want 600x340", out.info.PhysicalW, out.info.PhysicalH)
	}
	if out.info.Make != "Acme" || out.info.Model != "Panel 4K" {
		t.Fatalf("make/model = %q/%q, want Acme/Panel 4K", out.info.Make, out.info.Model)
	}
	if out.info.ResolutionW != 3840 || out.info.ResolutionH != 2160 {
		t.Fatalf("resolution = %dx%d, want 3840x2160", out.info.ResolutionW, out.info.ResolutionH)
	}
	if out.info.Scale != 2 {
		t.Fatalf("scale = %d, want 2", out.info.Scale)
	}
	if out.info.Geometry.W != 1920 || out.info.Geometry.H != 1080 {
		t.Fatalf("geometry size = %dx%d, want 1920x1080", out.info.Geometry.W, out.info.Geometry.H)
	}
	if out.info.Name != "DP-1" {
		t.Fatalf("name = %q, want DP-1", out.info.Name)
	}
	if out.info.Description != "Acme Panel 4K" {
		t.Fatalf("description = %q, want Acme Panel 4K", out.info.Description)
	}
}

func TestWaylandOutputEventParsingIgnoresMalformedEvents(t *testing.T) {
	t.Parallel()

	out := &waylandOutput{
		info: Info{
			Name:        "before",
			Description: "description before",
			Geometry:    Geometry{X: 1, Y: 2, W: 3, H: 4},
			ResolutionW: 3,
			ResolutionH: 4,
			Scale:       1,
		},
	}
	proxy := &wl.RawProxy{}
	out.updateProxy(proxy)

	proxy.OnEvent(0, 0, []byte{1, 2, 3})
	proxy.OnEvent(1, 0, []byte{1, 2, 3})
	proxy.OnEvent(3, 0, []byte{1, 2, 3})
	proxy.OnEvent(4, 0, []byte{1, 2, 3})
	proxy.OnEvent(5, 0, []byte{1, 2, 3})

	if out.info.Name != "before" {
		t.Fatalf("name changed to %q after malformed event", out.info.Name)
	}
	if out.info.Description != "description before" {
		t.Fatalf("description changed to %q after malformed event", out.info.Description)
	}
	if out.info.Geometry != (Geometry{X: 1, Y: 2, W: 3, H: 4}) {
		t.Fatalf("geometry changed to %+v after malformed event", out.info.Geometry)
	}
}

func TestOutputGlobalsPreservesSessionOrdering(t *testing.T) {
	t.Parallel()

	globals := []wl.GlobalEvent{
		{Name: 4, Interface: "wl_output", Version: 4},
		{Name: 1, Interface: "wl_seat", Version: 7},
		{Name: 12, Interface: "wl_output", Version: 3},
	}

	got := outputGlobals(globals)
	if len(got) != 2 {
		t.Fatalf("outputGlobals returned %d outputs, want 2", len(got))
	}
	if got[0].Name != 4 || got[1].Name != 12 {
		t.Fatalf("global order = %d, %d; want 4, 12", got[0].Name, got[1].Name)
	}
}

func TestWaylandListerSnapshotOutputsIsDeterministic(t *testing.T) {
	t.Parallel()

	lister := &WaylandLister{
		session: &wl.Session{},
		outputs: []*waylandOutput{
			{globalID: 12, info: Info{Name: "DP-2", Backend: "wayland"}},
			{globalID: 4, info: Info{Name: "DP-1", Backend: "wayland"}},
		},
	}

	for range 10 {
		got, err := lister.List(context.Background())
		if err != nil {
			t.Fatalf("List returned an error: %v", err)
		}
		if len(got) != 2 {
			t.Fatalf("snapshot returned %d outputs, want 2", len(got))
		}
		if got[0].Name != "DP-1" || got[1].Name != "DP-2" {
			t.Fatalf("output order = %q, %q; want DP-1, DP-2", got[0].Name, got[1].Name)
		}
	}
}

func TestWaylandOutputSnapshotSynchronizesWithEvents(t *testing.T) {
	out := &waylandOutput{
		info: Info{Name: "DP-1", Backend: "wayland", Scale: 1},
	}
	proxy := &wl.RawProxy{}
	out.updateProxy(proxy)
	lister := &WaylandLister{session: &wl.Session{}, outputs: []*waylandOutput{out}}

	geometryA := make([]byte, 20)
	wl.PutUint32(geometryA[0:4], 1)
	wl.PutUint32(geometryA[4:8], 2)
	wl.PutUint32(geometryA[8:12], 600)
	wl.PutUint32(geometryA[12:16], 340)
	geometryA = appendWlStringData(geometryA, "Acme")
	geometryA = appendWlStringData(geometryA, "Panel A")
	geometryB := make([]byte, 20)
	wl.PutUint32(geometryB[0:4], 3)
	wl.PutUint32(geometryB[4:8], 4)
	wl.PutUint32(geometryB[8:12], 700)
	wl.PutUint32(geometryB[12:16], 400)
	geometryB = appendWlStringData(geometryB, "Other")
	geometryB = appendWlStringData(geometryB, "Panel B")

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			proxy.OnEvent(0, 0, geometryA)
		}()
		go func() {
			defer wg.Done()
			got, err := lister.List(context.Background())
			if err != nil {
				t.Errorf("List returned an error: %v", err)
				return
			}
			if len(got) != 1 {
				t.Errorf("snapshot returned %d outputs, want 1", len(got))
				return
			}
			info := got[0]
			if !isCompleteOutputGeometrySnapshot(info) {
				t.Errorf("snapshot contains a partial event update: %+v", info)
			}
		}()
	}
	// Exercise a second complete event shape after the concurrent snapshots;
	// this also verifies the callback remains usable after contention.
	wg.Wait()
	proxy.OnEvent(0, 0, geometryB)
	got, err := lister.List(context.Background())
	if err != nil {
		t.Fatalf("List returned an error: %v", err)
	}
	if got[0].Make != "Other" || got[0].Model != "Panel B" {
		t.Fatalf("final snapshot = %+v, want Panel B", got[0])
	}
}

func isCompleteOutputGeometrySnapshot(info Info) bool {
	validInitial := info.Geometry == (Geometry{}) &&
		info.PhysicalW == 0 && info.PhysicalH == 0 &&
		info.Make == "" && info.Model == ""
	validA := info.Geometry == (Geometry{X: 1, Y: 2}) &&
		info.PhysicalW == 600 && info.PhysicalH == 340 &&
		info.Make == "Acme" && info.Model == "Panel A"
	validB := info.Geometry == (Geometry{X: 3, Y: 4}) &&
		info.PhysicalW == 700 && info.PhysicalH == 400 &&
		info.Make == "Other" && info.Model == "Panel B"
	return validInitial || validA || validB
}
