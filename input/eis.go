package input

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"net"
	"sort"
	"sync"
	"sync/atomic"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

const (
	eiHeaderSize     = 16
	eiMaximumMessage = 1 << 20
	eiServerIDFloor  = uint64(0xff00000000000000)
)

type eiMessage struct {
	object uint64
	opcode uint32
	body   []byte
}

type eiClient struct {
	conn      *net.UnixConn
	messages  chan eiMessage
	done      chan struct{}
	stop      chan struct{}
	closeOnce sync.Once
	// A whole EI request batch must remain contiguous on the byte stream.
	writeMu    sync.Mutex
	errorMu    sync.RWMutex
	readErr    error
	serial     atomic.Uint32
	clientID   atomic.Uint64
	connection atomic.Uint64
}

func newEIClient(conn *net.UnixConn) *eiClient {
	c := &eiClient{conn: conn, messages: make(chan eiMessage, 128), done: make(chan struct{}), stop: make(chan struct{})}
	c.clientID.Store(1)
	go c.readLoop()
	return c
}

func (c *eiClient) readLoop() {
	defer close(c.done)
	buffer := make([]byte, 0, 64*1024)
	data := make([]byte, 64*1024)
	oob := make([]byte, 4096)
	for {
		n, oobn, _, _, err := c.conn.ReadMsgUnix(data, oob)
		if oobn > 0 {
			closeReceivedFDs(oob[:oobn])
		}
		if err != nil {
			c.setReadError(err)
			return
		}
		if n == 0 {
			c.setReadError(errors.New("input/eis: peer closed the EIS stream"))
			return
		}
		buffer = append(buffer, data[:n]...)
		for len(buffer) >= eiHeaderSize {
			size := int(binary.NativeEndian.Uint32(buffer[8:12]))
			if size < eiHeaderSize || size > eiMaximumMessage || size%4 != 0 {
				c.setReadError(fmt.Errorf("input/eis: invalid message size %d", size))
				return
			}
			if len(buffer) < size {
				break
			}
			message := eiMessage{
				object: binary.NativeEndian.Uint64(buffer[:8]),
				opcode: binary.NativeEndian.Uint32(buffer[12:16]),
				body:   append([]byte(nil), buffer[16:size]...),
			}
			buffer = buffer[size:]
			if err := c.handleProtocolMessage(message); err != nil {
				c.setReadError(err)
				return
			}
			select {
			case c.messages <- message:
			case <-c.stop:
				return
			}
		}
	}
}

func (c *eiClient) handleProtocolMessage(message eiMessage) error {
	if message.object == 0 && message.opcode == 2 {
		return c.captureConnection(message)
	}
	if message.object != 0 && message.object == c.connection.Load() && message.opcode == 3 {
		return c.answerPing(message)
	}
	return nil
}

func (c *eiClient) captureConnection(message eiMessage) error {
	if len(message.body) != 16 {
		return fmt.Errorf("input/eis: invalid handshake connection event size %d", len(message.body))
	}
	connection, err := eiReadU64(message.body, 4)
	if err != nil {
		return fmt.Errorf("input/eis: invalid handshake connection event: %w", err)
	}
	version, err := eiReadU32(message.body, 12)
	if err != nil || connection == 0 || version != 1 {
		return fmt.Errorf("input/eis: unsupported handshake connection object %d version %d", connection, version)
	}
	// Publish the connection before its event so this reader can answer a following ping.
	c.connection.Store(connection)
	return nil
}

func (c *eiClient) answerPing(message eiMessage) error {
	if len(message.body) != 12 {
		return fmt.Errorf("input/eis: invalid connection ping size %d", len(message.body))
	}
	ping, err := eiReadU64(message.body, 0)
	if err != nil {
		return fmt.Errorf("input/eis: invalid connection ping: %w", err)
	}
	version, err := eiReadU32(message.body, 8)
	if err != nil {
		return fmt.Errorf("input/eis: invalid connection ping version: %w", err)
	}
	if ping == 0 || version != 1 {
		return fmt.Errorf("input/eis: unsupported connection ping object %d version %d", ping, version)
	}
	if err := c.writeMessages(eiRequest(ping, 0, eiU64(0))); err != nil {
		return fmt.Errorf("input/eis: answer connection ping: %w", err)
	}
	return nil
}

func closeReceivedFDs(control []byte) {
	messages, err := unix.ParseSocketControlMessage(control)
	if err != nil {
		return
	}
	for _, message := range messages {
		if message.Header.Level != unix.SOL_SOCKET || message.Header.Type != unix.SCM_RIGHTS {
			continue
		}
		fds, err := unix.ParseUnixRights(&message)
		if err != nil {
			continue
		}
		for _, fd := range fds {
			_ = unix.Close(fd)
		}
	}
}

func (c *eiClient) setReadError(err error) {
	c.errorMu.Lock()
	c.readErr = err
	c.errorMu.Unlock()
}

func (c *eiClient) readError() error {
	c.errorMu.RLock()
	err := c.readErr
	c.errorMu.RUnlock()
	return err
}

func (c *eiClient) next(ctx context.Context) (eiMessage, error) {
	if ctx == nil {
		return eiMessage{}, errors.New("input/eis: nil context")
	}
	select {
	case message := <-c.messages:
		c.updateSerial(message)
		return message, nil
	case <-c.done:
		if err := c.readError(); err != nil {
			return eiMessage{}, err
		}
		return eiMessage{}, errors.New("input/eis: EIS stream closed")
	case <-ctx.Done():
		return eiMessage{}, ctx.Err()
	}
}

func (c *eiClient) updateSerial(message eiMessage) {
	if len(message.body) < 4 {
		return
	}
	var hasSerial bool
	switch {
	case message.object == 0 && message.opcode == 2:
		hasSerial = true
	case message.opcode == 0 && message.object >= eiServerIDFloor:
		hasSerial = true
	case message.object >= eiServerIDFloor && message.opcode == 7:
		hasSerial = true
	case message.object >= eiServerIDFloor && message.opcode == 8:
		hasSerial = true
	case message.object >= eiServerIDFloor && message.opcode == 0 && len(message.body) >= 8:
		hasSerial = true
	}
	if hasSerial {
		c.serial.Store(binary.NativeEndian.Uint32(message.body[:4]))
	}
}

func (c *eiClient) writeMessages(messages ...[]byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	for _, message := range messages {
		for len(message) > 0 {
			n, err := c.conn.Write(message)
			if err != nil {
				return err
			}
			if n == 0 {
				return errors.New("input/eis: zero-length socket write")
			}
			message = message[n:]
		}
	}
	return nil
}

func (c *eiClient) close() error {
	var err error
	c.closeOnce.Do(func() {
		close(c.stop)
		err = c.conn.Close()
	})
	return err
}

type eiRegion struct {
	x      uint32
	y      uint32
	width  uint32
	height uint32
	scale  float32
}

type eiPointerDevice struct {
	device  uint64
	pointer uint64
	regions []eiRegion
}

type eiDevice struct {
	id         uint64
	version    uint32
	interfaces map[string]uint64
	regions    []eiRegion
	done       bool
	resumed    bool
}

type eiSender struct {
	client      *eiClient
	connection  uint64
	seat        uint64
	keyboard    uint64
	text        uint64
	pointer     uint64
	button      uint64
	scroll      uint64
	scrollDev   uint64
	pointerDev  uint64
	buttonDev   uint64
	keyboardDev uint64
	textDev     uint64
	regions     []eiRegion
	pointers    []eiPointerDevice
	devices     map[uint64]*eiDevice
}

func setupEISender(ctx context.Context, conn *net.UnixConn) (*eiSender, error) { //nolint:gocyclo // handshake, capability binding, and resumed-device readiness form one bounded protocol state machine.
	client := newEIClient(conn)
	sender := &eiSender{client: client, devices: make(map[uint64]*eiDevice)}
	failed := true
	defer func() {
		if failed {
			_ = client.close()
		}
	}()
	first, err := client.next(ctx)
	if err != nil {
		return nil, fmt.Errorf("input/eis: handshake event: %w", err)
	}
	if first.object != 0 || first.opcode != 0 || len(first.body) < 4 {
		return nil, errors.New("input/eis: missing handshake version event")
	}
	handshakeVersion := binary.NativeEndian.Uint32(first.body[:4])
	if handshakeVersion == 0 {
		return nil, errors.New("input/eis: server advertised handshake version zero")
	}
	if err := client.writeMessages(
		eiRequest(0, 0, eiU32(min(handshakeVersion, uint32(1)))),
		eiRequest(0, 2, eiU32(2)),
		eiRequest(0, 3, eiString("Perfuncted")),
	); err != nil {
		return nil, fmt.Errorf("input/eis: configure sender: %w", err)
	}
	for _, name := range []string{"ei_connection", "ei_callback", "ei_pingpong", "ei_seat", "ei_device", "ei_pointer_absolute", "ei_button", "ei_keyboard", "ei_scroll", "ei_text"} {
		if err := client.writeMessages(eiRequest(0, 4, append(eiString(name), eiU32(1)...))); err != nil {
			return nil, fmt.Errorf("input/eis: announce %s: %w", name, err)
		}
	}
	if err := client.writeMessages(eiRequest(0, 1, nil)); err != nil {
		return nil, fmt.Errorf("input/eis: finish handshake: %w", err)
	}
	for {
		message, err := client.next(ctx)
		if err != nil {
			return nil, fmt.Errorf("input/eis: await connection: %w", err)
		}
		if message.object == 0 && message.opcode == 2 {
			serial, err := eiReadU32(message.body, 0)
			if err != nil {
				return nil, err
			}
			connection, err := eiReadU64(message.body, 4)
			if err != nil {
				return nil, err
			}
			sender.connection = connection
			client.serial.Store(serial)
			break
		}
	}
	for {
		message, err := client.next(ctx)
		if err != nil {
			return nil, fmt.Errorf("input/eis: await seat: %w", err)
		}
		if message.object == sender.connection && message.opcode == 1 {
			seat, err := eiReadU64(message.body, 0)
			if err != nil {
				return nil, err
			}
			sender.seat = seat
			break
		}
		if isEISDisconnected(message, sender.connection) {
			return nil, ErrRemoteDesktopRevoked
		}
	}
	capabilityMasks := make(map[string]uint64)
	for {
		message, err := client.next(ctx)
		if err != nil {
			return nil, fmt.Errorf("input/eis: read seat capabilities: %w", err)
		}
		if message.object != sender.seat {
			if isEISDisconnected(message, sender.connection) {
				return nil, ErrRemoteDesktopRevoked
			}
			continue
		}
		switch message.opcode {
		case 2:
			mask, err := eiReadU64(message.body, 0)
			if err != nil {
				return nil, err
			}
			name, err := eiReadString(message.body, 8)
			if err != nil {
				return nil, err
			}
			capabilityMasks[name] |= mask
		case 3:
			goto seatComplete
		}
	}
seatComplete:
	var bindMask uint64
	for _, name := range []string{"ei_pointer_absolute", "ei_button", "ei_keyboard", "ei_scroll", "ei_text"} {
		if name == "ei_scroll" || name == "ei_text" {
			bindMask |= capabilityMasks[name]
			continue
		}
		mask := capabilityMasks[name]
		if mask == 0 {
			return nil, fmt.Errorf("input/eis: EIS seat does not offer %s", name)
		}
		bindMask |= mask
	}
	if err := client.writeMessages(eiRequest(sender.seat, 1, eiU64(bindMask))); err != nil {
		return nil, fmt.Errorf("input/eis: bind seat: %w", err)
	}
	callbackID := client.nextClientObjectID()
	if err := client.writeMessages(eiRequest(sender.connection, 0, append(eiU64(callbackID), eiU32(1)...))); err != nil {
		return nil, fmt.Errorf("input/eis: synchronize device setup: %w", err)
	}
	for {
		message, err := client.next(ctx)
		if err != nil {
			return nil, fmt.Errorf("input/eis: enumerate input devices: %w", err)
		}
		if message.object == callbackID && message.opcode == 0 {
			data, err := eiReadU64(message.body, 0)
			if err != nil || data != 0 {
				return nil, errors.New("input/eis: invalid device setup callback")
			}
			break
		}
		if err := sender.consumeSetupEvent(message); err != nil {
			return nil, err
		}
		if isEISDisconnected(message, sender.connection) {
			return nil, ErrRemoteDesktopRevoked
		}
	}
	for !sender.devicesReady() {
		message, err := client.next(ctx)
		if err != nil {
			return nil, fmt.Errorf("input/eis: await resumed input devices: %w", err)
		}
		if err := sender.consumeSetupEvent(message); err != nil {
			return nil, err
		}
		if isEISDisconnected(message, sender.connection) {
			return nil, ErrRemoteDesktopRevoked
		}
	}
	if err := sender.selectDevices(); err != nil {
		return nil, err
	}
	failed = false
	return sender, nil
}

func (s *eiSender) consumeSetupEvent(message eiMessage) error { //nolint:gocyclo // the protocol event opcodes update distinct device setup fields.
	if message.object == s.seat && message.opcode == 4 {
		id, err := eiReadU64(message.body, 0)
		if err != nil {
			return err
		}
		version, err := eiReadU32(message.body, 8)
		if err != nil {
			return err
		}
		s.devices[id] = &eiDevice{id: id, version: version, interfaces: make(map[string]uint64)}
		return nil
	}
	device, ok := s.devices[message.object]
	if !ok {
		return nil
	}
	switch message.opcode {
	case 4:
		x, err := eiReadU32(message.body, 0)
		if err != nil {
			return err
		}
		y, err := eiReadU32(message.body, 4)
		if err != nil {
			return err
		}
		width, err := eiReadU32(message.body, 8)
		if err != nil {
			return err
		}
		height, err := eiReadU32(message.body, 12)
		if err != nil {
			return err
		}
		scaleBits, err := eiReadU32(message.body, 16)
		if err != nil {
			return err
		}
		scale := math.Float32frombits(scaleBits)
		if width == 0 || height == 0 || math.IsNaN(float64(scale)) || math.IsInf(float64(scale), 0) || scale <= 0 {
			return errors.New("input/eis: EIS advertised invalid absolute region geometry or scale")
		}
		device.regions = append(device.regions, eiRegion{x: x, y: y, width: width, height: height, scale: scale})
	case 5:
		object, err := eiReadU64(message.body, 0)
		if err != nil {
			return err
		}
		name, err := eiReadString(message.body, 8)
		if err != nil {
			return err
		}
		device.interfaces[name] = object
	case 6:
		device.done = true
	case 7:
		device.resumed = true
	case 8:
		device.resumed = false
	}
	return nil
}

func (s *eiSender) devicesReady() bool { //nolint:gocyclo // readiness requires deterministic device selection across all advertised capabilities.
	s.pointers = s.pointers[:0]
	s.regions = s.regions[:0]
	s.keyboardDev, s.keyboard = 0, 0
	s.pointerDev, s.pointer = 0, 0
	s.buttonDev, s.button = 0, 0
	s.scrollDev, s.scroll = 0, 0
	s.textDev, s.text = 0, 0
	deviceIDs := make([]uint64, 0, len(s.devices))
	for id := range s.devices {
		deviceIDs = append(deviceIDs, id)
	}
	sort.Slice(deviceIDs, func(i, j int) bool { return deviceIDs[i] < deviceIDs[j] })
	for _, id := range deviceIDs {
		device := s.devices[id]
		if !device.resumed {
			continue
		}
		if s.keyboardDev == 0 && device.interfaces["ei_keyboard"] != 0 {
			s.keyboardDev = device.id
			s.keyboard = device.interfaces["ei_keyboard"]
		}
		if pointer := device.interfaces["ei_pointer_absolute"]; pointer != 0 && len(device.regions) > 0 {
			s.pointers = append(s.pointers, eiPointerDevice{device: device.id, pointer: pointer, regions: append([]eiRegion(nil), device.regions...)})
			s.regions = append(s.regions, device.regions...)
			if s.pointerDev == 0 {
				s.pointerDev, s.pointer = device.id, pointer
			}
		}
		if s.buttonDev == 0 && device.interfaces["ei_button"] != 0 {
			s.buttonDev = device.id
			s.button = device.interfaces["ei_button"]
		}
		if s.scroll == 0 && device.interfaces["ei_scroll"] != 0 {
			s.scroll = device.interfaces["ei_scroll"]
			s.scrollDev = device.id
		}
		if s.textDev == 0 && device.interfaces["ei_text"] != 0 {
			s.textDev = device.id
			s.text = device.interfaces["ei_text"]
		}
	}
	sort.Slice(s.pointers, func(i, j int) bool { return s.pointers[i].device < s.pointers[j].device })
	return s.keyboardDev != 0 && len(s.pointers) > 0 && s.buttonDev != 0 && len(s.regions) > 0
}

func (s *eiSender) selectDevices() error {
	devices := make([]uint64, 0, 4+len(s.pointers))
	devices = append(devices, s.keyboardDev, s.buttonDev, s.scrollDev, s.textDev)
	for _, pointer := range s.pointers {
		devices = append(devices, pointer.device)
	}
	started := make(map[uint64]struct{}, len(devices))
	for _, id := range devices {
		if id == 0 {
			continue
		}
		if _, ok := started[id]; ok {
			continue
		}
		started[id] = struct{}{}
		sequence := uint32(1)
		if err := s.client.writeMessages(eiRequest(id, 1, append(eiU32(s.client.serial.Load()), eiU32(sequence)...))); err != nil {
			return fmt.Errorf("input/eis: start device emulation: %w", err)
		}
	}
	return nil
}

func isEISDisconnected(message eiMessage, connection uint64) bool {
	return message.object == connection && message.opcode == 0
}

func (s *eiSender) validatePoint(x, y int) bool {
	_, ok := s.pointerForPoint(x, y)
	return ok
}

func (s *eiSender) pointerForPoint(x, y int) (eiPointerDevice, bool) {
	var selected eiPointerDevice
	found := false
	for _, pointer := range s.pointers {
		for _, region := range pointer.regions {
			if uint64(x) >= uint64(region.x) && uint64(y) >= uint64(region.y) && uint64(x) < uint64(region.x)+uint64(region.width) && uint64(y) < uint64(region.y)+uint64(region.height) {
				if found && selected.device != pointer.device {
					return eiPointerDevice{}, false
				}
				selected = pointer
				found = true
				break
			}
		}
	}
	return selected, found
}

func eiRequest(object uint64, opcode uint32, body []byte) []byte {
	size := eiHeaderSize + len(body)
	if size%4 != 0 {
		body = append(body, make([]byte, 4-size%4)...)
		size = eiHeaderSize + len(body)
	}
	message := make([]byte, size)
	binary.NativeEndian.PutUint64(message[:8], object)
	binary.NativeEndian.PutUint32(message[8:12], uint32(size))
	binary.NativeEndian.PutUint32(message[12:16], opcode)
	copy(message[16:], body)
	return message
}

func eiU32(value uint32) []byte {
	buf := make([]byte, 4)
	binary.NativeEndian.PutUint32(buf, value)
	return buf
}

func eiI32(value int32) []byte { return eiU32(uint32(value)) }

func eiU64(value uint64) []byte {
	buf := make([]byte, 8)
	binary.NativeEndian.PutUint64(buf, value)
	return buf
}

func eiF32(value float32) []byte { return eiU32(math.Float32bits(value)) }

func eiString(value string) []byte {
	raw := append([]byte(value), 0)
	body := eiU32(uint32(len(raw)))
	body = append(body, raw...)
	for len(body)%4 != 0 {
		body = append(body, 0)
	}
	return body
}

func eiReadU32(body []byte, offset int) (uint32, error) {
	if offset < 0 || len(body)-offset < 4 {
		return 0, errors.New("input/eis: truncated uint32 argument")
	}
	return binary.NativeEndian.Uint32(body[offset : offset+4]), nil
}

func eiReadU64(body []byte, offset int) (uint64, error) {
	if offset < 0 || len(body)-offset < 8 {
		return 0, errors.New("input/eis: truncated uint64 argument")
	}
	return binary.NativeEndian.Uint64(body[offset : offset+8]), nil
}

func eiReadString(body []byte, offset int) (string, error) {
	length, err := eiReadU32(body, offset)
	if err != nil {
		return "", err
	}
	if length == 0 {
		return "", nil
	}
	start := offset + 4
	end := start + int(length)
	if end > len(body) || body[end-1] != 0 {
		return "", errors.New("input/eis: invalid string argument")
	}
	value := body[start : end-1]
	if !utf8.Valid(value) {
		return "", errors.New("input/eis: invalid UTF-8 string argument")
	}
	return string(value), nil
}

func (c *eiClient) nextClientObjectID() uint64 { return c.clientID.Add(1) - 1 }

func monotonicMicroseconds() (uint64, error) {
	var ts unix.Timespec
	if err := unix.ClockGettime(unix.CLOCK_MONOTONIC, &ts); err != nil {
		return 0, err
	}
	return uint64(ts.Sec)*1_000_000 + uint64(ts.Nsec)/1_000, nil
}

func eiFrame(device uint64, serial uint32) ([]byte, error) {
	timestamp, err := monotonicMicroseconds()
	if err != nil {
		return nil, fmt.Errorf("input/eis: read monotonic clock: %w", err)
	}
	return eiRequest(device, 3, append(eiU32(serial), eiU64(timestamp)...)), nil
}

func eiTextFrames(sender *eiSender, text string) ([][]byte, error) {
	if !utf8.ValidString(text) {
		return nil, errors.New("input: text must be valid UTF-8")
	}
	if text == "" {
		return nil, nil
	}
	if sender.text == 0 || sender.textDev == 0 {
		return nil, unsupportedError("remote-desktop", "text input")
	}
	chunks := splitUTF8(text, 254)
	frames := make([][]byte, 0, len(chunks))
	for _, chunk := range chunks {
		request := eiRequest(sender.text, 2, eiString(chunk))
		frame, err := eiFrame(sender.textDev, sender.client.serial.Load())
		if err != nil {
			return nil, err
		}
		frames = append(frames, append(request, frame...))
	}
	return frames, nil
}

func splitUTF8(value string, maximum int) []string {
	if len(value) <= maximum {
		return []string{value}
	}
	chunks := make([]string, 0, (len(value)+maximum-1)/maximum)
	for len(value) > 0 {
		end := maximum
		if end >= len(value) {
			end = len(value)
		} else {
			for end > 0 && !utf8.RuneStart(value[end]) {
				end--
			}
		}
		chunks = append(chunks, value[:end])
		value = value[end:]
	}
	return chunks
}

func eiKeyFrame(sender *eiSender, key uint32, pressed bool) ([]byte, error) {
	state := uint32(0)
	if pressed {
		state = 1
	}
	request := eiRequest(sender.keyboard, 1, append(eiU32(key), eiU32(state)...))
	frame, err := eiFrame(sender.keyboardDev, sender.client.serial.Load())
	if err != nil {
		return nil, err
	}
	return append(request, frame...), nil
}

func eiButtonFrame(sender *eiSender, button uint32, pressed bool) ([]byte, error) {
	state := uint32(0)
	if pressed {
		state = 1
	}
	request := eiRequest(sender.button, 1, append(eiU32(button), eiU32(state)...))
	frame, err := eiFrame(sender.buttonDev, sender.client.serial.Load())
	if err != nil {
		return nil, err
	}
	return append(request, frame...), nil
}

func eiPointerFrame(sender *eiSender, x, y int) ([]byte, error) {
	pointer, ok := sender.pointerForPoint(x, y)
	if !ok {
		return nil, fmt.Errorf("input/eis: (%d,%d) is outside the available absolute pointer regions", x, y)
	}
	floatX, floatY := float32(x), float32(y)
	if float64(floatX) != float64(x) || float64(floatY) != float64(y) {
		return nil, errors.New("input/eis: pointer coordinate cannot be represented exactly by the protocol")
	}
	request := eiRequest(pointer.pointer, 1, append(eiF32(floatX), eiF32(floatY)...))
	frame, err := eiFrame(pointer.device, sender.client.serial.Load())
	if err != nil {
		return nil, err
	}
	return append(request, frame...), nil
}

func eiScrollFrame(sender *eiSender, x, y int32) ([]byte, error) {
	request := eiRequest(sender.scroll, 2, append(eiI32(x), eiI32(y)...))
	frame, err := eiFrame(sender.scrollDev, sender.client.serial.Load())
	if err != nil {
		return nil, err
	}
	return append(request, frame...), nil
}
