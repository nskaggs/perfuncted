package input

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/env"
)

const syntheticSessionPath = "/org/freedesktop/portal/desktop/session/fixture"

type syntheticPortalSession struct {
	portal *syntheticPortal
}

func (s *syntheticPortalSession) Close() *dbus.Error {
	s.portal.mu.Lock()
	s.portal.closedSessions++
	s.portal.mu.Unlock()
	return nil
}

func (p *syntheticPortal) SelectDevices(_ dbus.ObjectPath, options map[string]dbus.Variant) (dbus.ObjectPath, *dbus.Error) {
	p.mu.Lock()
	p.selectOptions = options
	p.mu.Unlock()
	return p.reply("SelectDevices", map[string]dbus.Variant{}), nil
}
func (p *syntheticPortal) Start(_ dbus.ObjectPath, _ string, _ map[string]dbus.Variant) (dbus.ObjectPath, *dbus.Error) {
	p.mu.Lock()
	response := make(map[string]dbus.Variant)
	if p.startResponse != nil {
		for key, value := range p.startResponse {
			response[key] = value
		}
	} else {
		response["devices"] = dbus.MakeVariant(p.deviceMask)
		if p.persistToken != "" {
			response["restore_token"] = dbus.MakeVariant(p.persistToken)
		}
	}
	p.mu.Unlock()
	return p.reply("Start", response), nil
}
func (p *syntheticPortal) ConnectToEIS(_ dbus.ObjectPath, _ map[string]dbus.Variant) (dbus.UnixFD, *dbus.Error) {
	p.mu.Lock()
	fd, err := p.eisFD, p.connectErr
	p.mu.Unlock()
	return fd, err
}
func (p *syntheticPortal) reply(method string, results map[string]dbus.Variant) dbus.ObjectPath {
	path := dbus.ObjectPath("/org/freedesktop/portal/desktop/request/fixture/" + method)
	p.mu.Lock()
	code := uint32(0)
	if method == "SelectDevices" {
		code = p.selectCode
	}
	if method == "Start" {
		code = p.startCode
	}
	p.mu.Unlock()
	go func() {
		time.Sleep(10 * time.Millisecond)
		_ = p.conn.Emit(path, portalRequestInterface+".Response", code, results)
	}()
	return path
}

func TestSyntheticPortalFullAuthorizedEISLifecycle(t *testing.T) { //nolint:gocyclo // Full protocol or mock state transitions are asserted in one transaction.
	client, portal := startSyntheticPortalBus(t)
	_ = client
	// Simulate a Wayland-capable runtime without connecting to a real display.
	runtimeDir, err := os.MkdirTemp("", "pf-eis-runtime-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	fakeWayland := filepath.Join(runtimeDir, "wayland-1")
	listener, err := net.Listen("unix", fakeWayland)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	eisClient, eisServer := unixConnectionPair(t)
	defer eisServer.Close()
	file, err := eisClient.File()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	_ = eisClient.Close()

	portal.mu.Lock()
	portal.eisFD = dbus.UnixFD(file.Fd())
	portal.deviceMask = portalDeviceKeyboard | portalDevicePointer
	portal.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	peerDone := make(chan error, 1)
	go func() { peerDone <- serveSyntheticAuthorizedEISPeer(eisServer) }()

	rt := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=" + fakeWayland,
		"DBUS_SESSION_BUS_ADDRESS=" + portal.busAddress,
	})
	inputter, err := OpenRemoteDesktopContext(ctx, rt, RemoteDesktopOptions{})
	if err != nil {
		t.Fatalf("full portal + EIS startup: %v", err)
	}
	backend, ok := inputter.(*RemoteDesktopBackend)
	if !ok {
		t.Fatalf("unexpected inputter type %T", inputter)
	}
	defer backend.Close()
	select {
	case peerErr := <-peerDone:
		if peerErr != nil {
			t.Fatalf("synthetic EIS peer: %v", peerErr)
		}
	case <-ctx.Done():
		t.Fatalf("EIS peer setup timed out: %v", ctx.Err())
	}
	portal.mu.Lock()
	options := portal.selectOptions
	portal.mu.Unlock()
	if options["types"].Value() != portalDeviceKeyboard|portalDevicePointer {
		t.Fatalf("portal grant request missing keyboard/pointer: %+v", options)
	}
	if options["persist_mode"].Value() != portalPersistNever {
		t.Fatalf("unexpected persistent consent requested: %+v", options)
	}
	if keyErr := backend.KeyDown(ctx, "a"); keyErr != nil {
		t.Fatalf("send key through authorized session: %v", keyErr)
	}
	key, err := readSyntheticEISFrame(eisServer)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := readSyntheticEISFrame(eisServer)
	if err != nil {
		t.Fatal(err)
	}
	if key.object != 103 || key.opcode != 1 || frame.object != 0xff00000000000012 || frame.opcode != 3 {
		t.Fatalf("authorized key not delivered: %+v %+v", key, frame)
	}
	if err := backend.KeyUp(ctx, "a"); err != nil {
		t.Fatalf("release key: %v", err)
	}
	for i := 0; i < 2; i++ {
		if _, err := readSyntheticEISFrame(eisServer); err != nil {
			t.Fatal(err)
		}
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("closing authorized session: %v", err)
	}
}

func readSyntheticEISFrame(conn *net.UnixConn) (testEIRequest, error) {
	header := make([]byte, eiHeaderSize)
	if _, err := io.ReadFull(conn, header); err != nil {
		return testEIRequest{}, err
	}
	n := int(binary.NativeEndian.Uint32(header[8:12]))
	if n < eiHeaderSize || n > eiMaximumMessage || n%4 != 0 {
		return testEIRequest{}, fmt.Errorf("invalid EI frame length %d", n)
	}
	body := make([]byte, n-eiHeaderSize)
	if _, err := io.ReadFull(conn, body); err != nil {
		return testEIRequest{}, err
	}
	return testEIRequest{object: binary.NativeEndian.Uint64(header[:8]), opcode: binary.NativeEndian.Uint32(header[12:16]), body: body}, nil
}

func serveSyntheticAuthorizedEISPeer(conn *net.UnixConn) error { //nolint:gocyclo // Synthetic handshake peer asserts a complete protocol exchange.
	if err := conn.SetDeadline(time.Now().Add(4 * time.Second)); err != nil {
		return err
	}
	if _, err := conn.Write(eiRequest(0, 0, eiU32(1))); err != nil {
		return err
	}
	gotFinish := false
	for i := 0; i < 24; i++ {
		request, err := readSyntheticEISFrame(conn)
		if err != nil {
			return err
		}
		if request.object != 0 {
			return fmt.Errorf("handshake object %d", request.object)
		}
		if request.opcode == 1 {
			gotFinish = true
			break
		}
	}
	if !gotFinish {
		return fmt.Errorf("EIS handshake never completed")
	}
	const connID = uint64(0xff00000000000010)
	const seatID = uint64(0xff00000000000011)
	const devID = uint64(0xff00000000000012)
	stream := eiRequest(0, 2, append(append(eiU32(9), eiU64(connID)...), eiU32(1)...))
	stream = append(stream, eiRequest(connID, 1, eiU64(seatID))...)
	for i, name := range []string{"ei_pointer_absolute", "ei_button", "ei_keyboard", "ei_scroll", "ei_text"} {
		stream = append(stream, eiRequest(seatID, 2, append(eiU64(uint64(1<<i)), eiString(name)...))...)
	}
	stream = append(stream, eiRequest(seatID, 3, nil)...)
	if _, writeErr := conn.Write(stream); writeErr != nil {
		return writeErr
	}
	bind, err := readSyntheticEISFrame(conn)
	if err != nil {
		return err
	}
	if bind.object != seatID || bind.opcode != 1 {
		return fmt.Errorf("incorrect seat binding: %+v", bind)
	}
	callback, err := readSyntheticEISFrame(conn)
	if err != nil {
		return err
	}
	if callback.object != connID || callback.opcode != 0 {
		return fmt.Errorf("incorrect callback: %+v", callback)
	}
	callbackID := binary.NativeEndian.Uint64(callback.body[:8])

	stream = eiRequest(seatID, 4, append(eiU64(devID), eiU32(1)...))
	for i, name := range []string{"ei_pointer_absolute", "ei_button", "ei_keyboard", "ei_scroll", "ei_text"} {
		stream = append(stream, eiRequest(devID, 5, append(eiU64(101+uint64(i)), eiString(name)...))...)
	}
	region := append(append(append(append(eiU32(0), eiU32(0)...), eiU32(1920)...), eiU32(1080)...), eiF32(1)...)
	stream = append(stream, eiRequest(devID, 4, region)...)
	stream = append(stream, eiRequest(devID, 6, nil)...)
	stream = append(stream, eiRequest(devID, 7, eiU32(9))...)
	stream = append(stream, eiRequest(callbackID, 0, eiU64(0))...)
	if _, writeErr := conn.Write(stream); writeErr != nil {
		return writeErr
	}
	start, err := readSyntheticEISFrame(conn)
	if err != nil {
		return err
	}
	if start.object != devID || start.opcode != 1 {
		return fmt.Errorf("device emulation not started: %+v", start)
	}
	return nil
}
