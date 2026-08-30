package x11

import (
	"sync"

	"github.com/jezek/xgb/randr"
	"github.com/jezek/xgb/xproto"
)

// Minimal mock connection for unit tests.

type MockGetPropertyCookie struct {
	reply *xproto.GetPropertyReply
	err   error
}

func (m *MockGetPropertyCookie) Reply() (*xproto.GetPropertyReply, error) { return m.reply, m.err }

type MockGetKeyboardMappingCookie struct {
	reply *xproto.GetKeyboardMappingReply
}

func (m *MockGetKeyboardMappingCookie) Reply() (*xproto.GetKeyboardMappingReply, error) {
	return m.reply, nil
}

// NewMockGetKeyboardMappingCookie returns a keyboard-mapping cookie for tests.
func NewMockGetKeyboardMappingCookie(reply *xproto.GetKeyboardMappingReply) GetKeyboardMappingCookie {
	return &MockGetKeyboardMappingCookie{reply: reply}
}

type MockCheckCookie struct{}

func (m *MockCheckCookie) Check() error { return nil }

type MockGetGeometryCookie struct {
	reply *xproto.GetGeometryReply
}

func (m *MockGetGeometryCookie) Reply() (*xproto.GetGeometryReply, error) { return m.reply, nil }

type MockTranslateCoordinatesCookie struct {
	reply *xproto.TranslateCoordinatesReply
}

func (m *MockTranslateCoordinatesCookie) Reply() (*xproto.TranslateCoordinatesReply, error) {
	return m.reply, nil
}

type MockQueryPointerCookie struct {
	reply *xproto.QueryPointerReply
}

func (m *MockQueryPointerCookie) Reply() (*xproto.QueryPointerReply, error) { return m.reply, nil }

type MockXTestFakeInputCookie struct{ err error }

func (m *MockXTestFakeInputCookie) Check() error { return m.err }

// NewMockXTestFakeInputCookie returns an XTEST fake-input cookie for tests.
func NewMockXTestFakeInputCookie(err error) XTestFakeInputCookie {
	return &MockXTestFakeInputCookie{err: err}
}

type MockInternAtomCookie struct {
	reply *xproto.InternAtomReply
}

func (m *MockInternAtomCookie) Reply() (*xproto.InternAtomReply, error) { return m.reply, nil }

type MockGetImageCookie struct {
	reply *xproto.GetImageReply
	err   error
}

func (m *MockGetImageCookie) Reply() (*xproto.GetImageReply, error) {
	if m.reply != nil {
		return m.reply, m.err
	}
	return &xproto.GetImageReply{}, m.err
}

func NewMockGetImageCookie(reply *xproto.GetImageReply, err error) GetImageCookie {
	return &MockGetImageCookie{reply: reply, err: err}
}

type MockRandRScreenResourcesCookie struct {
	reply *randr.GetScreenResourcesCurrentReply
	err   error
}

// NewMockRandRScreenResourcesCookie returns a RandR resources reply for tests.
func NewMockRandRScreenResourcesCookie(reply *randr.GetScreenResourcesCurrentReply, err error) RandRScreenResourcesCookie {
	return &MockRandRScreenResourcesCookie{reply: reply, err: err}
}

func (m *MockRandRScreenResourcesCookie) Reply() (*randr.GetScreenResourcesCurrentReply, error) {
	return m.reply, m.err
}

type MockRandROutputInfoCookie struct {
	reply *randr.GetOutputInfoReply
	err   error
}

// NewMockRandROutputInfoCookie returns a RandR output-info reply for tests.
func NewMockRandROutputInfoCookie(reply *randr.GetOutputInfoReply, err error) RandROutputInfoCookie {
	return &MockRandROutputInfoCookie{reply: reply, err: err}
}

func (m *MockRandROutputInfoCookie) Reply() (*randr.GetOutputInfoReply, error) {
	return m.reply, m.err
}

type MockRandRCrtcInfoCookie struct {
	reply *randr.GetCrtcInfoReply
	err   error
}

// NewMockRandRCrtcInfoCookie returns a RandR CRTC-info reply for tests.
func NewMockRandRCrtcInfoCookie(reply *randr.GetCrtcInfoReply, err error) RandRCrtcInfoCookie {
	return &MockRandRCrtcInfoCookie{reply: reply, err: err}
}

func (m *MockRandRCrtcInfoCookie) Reply() (*randr.GetCrtcInfoReply, error) {
	return m.reply, m.err
}

type MockRandROutputPrimaryCookie struct {
	reply *randr.GetOutputPrimaryReply
	err   error
}

// NewMockRandROutputPrimaryCookie returns a RandR primary-output reply for tests.
func NewMockRandROutputPrimaryCookie(reply *randr.GetOutputPrimaryReply, err error) RandROutputPrimaryCookie {
	return &MockRandROutputPrimaryCookie{reply: reply, err: err}
}

func (m *MockRandROutputPrimaryCookie) Reply() (*randr.GetOutputPrimaryReply, error) {
	return m.reply, m.err
}

type MockConnection struct {
	mu sync.Mutex

	DefaultScreenFunc             func() *xproto.ScreenInfo
	SetupFunc                     func() *xproto.SetupInfo
	InitRandRFunc                 func() error
	GetScreenResourcesCurrentFunc func(Window xproto.Window) RandRScreenResourcesCookie
	GetOutputInfoFunc             func(Output randr.Output, ConfigTimestamp xproto.Timestamp) RandROutputInfoCookie
	GetCrtcInfoFunc               func(Crtc randr.Crtc, ConfigTimestamp xproto.Timestamp) RandRCrtcInfoCookie
	GetOutputPrimaryFunc          func(Window xproto.Window) RandROutputPrimaryCookie

	InternAtomFunc             func(OnlyIfExists bool, NameLen uint16, Name string) InternAtomCookie
	GetPropertyFunc            func(Delete bool, Window xproto.Window, Property, Type xproto.Atom, LongOffset, LongLength uint32) GetPropertyCookie
	GetGeometryFunc            func(Drawable xproto.Drawable) GetGeometryCookie
	TranslateCoordinatesFunc   func(SrcWindow, DstWindow xproto.Window, SrcX, SrcY int16) TranslateCoordinatesCookie
	QueryPointerFunc           func(Window xproto.Window) QueryPointerCookie
	SendEventCheckedFunc       func(Propagate bool, Destination xproto.Window, EventMask uint32, Event string) SendEventCookie
	MapWindowCheckedFunc       func(Window xproto.Window) MapWindowCookie
	ConfigureWindowCheckedFunc func(Window xproto.Window, ValueMask uint16, ValueList []uint32) ConfigureWindowCookie
	NewIDFunc                  func() (uint32, error)
	GetImageFunc               func(Format byte, Drawable xproto.Drawable, X, Y int16, Width, Height uint16, PlaneMask uint32) GetImageCookie
	FreePixmapFunc             func(Pixmap xproto.Pixmap) FreePixmapCookie
	InitCompositeFunc          func() error
	NameWindowPixmapFunc       func(Window xproto.Window, Pixmap xproto.Pixmap) NameWindowPixmapCookie

	GetKeyboardMappingFunc func(first xproto.Keycode, count byte) GetKeyboardMappingCookie
	FakeInputCheckedFunc   func(eventType byte, detail byte, tm uint32, window xproto.Window, x, y int16, device byte) XTestFakeInputCookie

	LastFakeInput struct {
		EventType byte
		Detail    byte
		Time      uint32
		Window    xproto.Window
		X, Y      int16
		Device    byte
	}
}

func (m *MockConnection) Close() {}
func (m *MockConnection) Sync()  {}
func (m *MockConnection) DefaultScreen() *xproto.ScreenInfo {
	if m.DefaultScreenFunc != nil {
		return m.DefaultScreenFunc()
	}
	return &xproto.ScreenInfo{Root: xproto.Window(1), WidthInPixels: 800, HeightInPixels: 600}
}
func (m *MockConnection) Setup() *xproto.SetupInfo {
	if m.SetupFunc != nil {
		return m.SetupFunc()
	}
	return &xproto.SetupInfo{MinKeycode: 8, MaxKeycode: 255}
}
func (m *MockConnection) InitRandR() error {
	if m.InitRandRFunc != nil {
		return m.InitRandRFunc()
	}
	return nil
}
func (m *MockConnection) GetScreenResourcesCurrent(Window xproto.Window) RandRScreenResourcesCookie {
	if m.GetScreenResourcesCurrentFunc != nil {
		return m.GetScreenResourcesCurrentFunc(Window)
	}
	return &MockRandRScreenResourcesCookie{}
}
func (m *MockConnection) GetOutputInfo(Output randr.Output, ConfigTimestamp xproto.Timestamp) RandROutputInfoCookie {
	if m.GetOutputInfoFunc != nil {
		return m.GetOutputInfoFunc(Output, ConfigTimestamp)
	}
	return &MockRandROutputInfoCookie{}
}
func (m *MockConnection) GetCrtcInfo(Crtc randr.Crtc, ConfigTimestamp xproto.Timestamp) RandRCrtcInfoCookie {
	if m.GetCrtcInfoFunc != nil {
		return m.GetCrtcInfoFunc(Crtc, ConfigTimestamp)
	}
	return &MockRandRCrtcInfoCookie{}
}
func (m *MockConnection) GetOutputPrimary(Window xproto.Window) RandROutputPrimaryCookie {
	if m.GetOutputPrimaryFunc != nil {
		return m.GetOutputPrimaryFunc(Window)
	}
	return &MockRandROutputPrimaryCookie{}
}
func (m *MockConnection) InternAtom(OnlyIfExists bool, NameLen uint16, Name string) InternAtomCookie {
	if m.InternAtomFunc != nil {
		return m.InternAtomFunc(OnlyIfExists, NameLen, Name)
	}
	return &MockInternAtomCookie{reply: &xproto.InternAtomReply{Atom: xproto.Atom(0)}}
}
func (m *MockConnection) GetProperty(Delete bool, Window xproto.Window, Property, Type xproto.Atom, LongOffset, LongLength uint32) GetPropertyCookie {
	if m.GetPropertyFunc != nil {
		return m.GetPropertyFunc(Delete, Window, Property, Type, LongOffset, LongLength)
	}
	return &MockGetPropertyCookie{reply: &xproto.GetPropertyReply{Value: []byte{}, Format: 32}}
}
func (m *MockConnection) GetGeometry(Drawable xproto.Drawable) GetGeometryCookie {
	if m.GetGeometryFunc != nil {
		return m.GetGeometryFunc(Drawable)
	}
	return &MockGetGeometryCookie{reply: &xproto.GetGeometryReply{X: 0, Y: 0, Width: 0, Height: 0}}
}
func (m *MockConnection) TranslateCoordinates(SrcWindow, DstWindow xproto.Window, SrcX, SrcY int16) TranslateCoordinatesCookie {
	if m.TranslateCoordinatesFunc != nil {
		return m.TranslateCoordinatesFunc(SrcWindow, DstWindow, SrcX, SrcY)
	}
	return &MockTranslateCoordinatesCookie{reply: &xproto.TranslateCoordinatesReply{}}
}
func (m *MockConnection) QueryPointer(Window xproto.Window) QueryPointerCookie {
	if m.QueryPointerFunc != nil {
		return m.QueryPointerFunc(Window)
	}
	return &MockQueryPointerCookie{reply: &xproto.QueryPointerReply{}}
}
func (m *MockConnection) SendEventChecked(Propagate bool, Destination xproto.Window, EventMask uint32, Event string) SendEventCookie {
	if m.SendEventCheckedFunc != nil {
		return m.SendEventCheckedFunc(Propagate, Destination, EventMask, Event)
	}
	return &MockCheckCookie{}
}
func (m *MockConnection) MapWindowChecked(Window xproto.Window) MapWindowCookie {
	if m.MapWindowCheckedFunc != nil {
		return m.MapWindowCheckedFunc(Window)
	}
	return &MockCheckCookie{}
}
func (m *MockConnection) ConfigureWindowChecked(Window xproto.Window, ValueMask uint16, ValueList []uint32) ConfigureWindowCookie {
	if m.ConfigureWindowCheckedFunc != nil {
		return m.ConfigureWindowCheckedFunc(Window, ValueMask, ValueList)
	}
	return &MockCheckCookie{}
}
func (m *MockConnection) NewID() (uint32, error) {
	if m.NewIDFunc != nil {
		return m.NewIDFunc()
	}
	return 42, nil
}
func (m *MockConnection) GetImage(Format byte, Drawable xproto.Drawable, X, Y int16, Width, Height uint16, PlaneMask uint32) GetImageCookie {
	if m.GetImageFunc != nil {
		return m.GetImageFunc(Format, Drawable, X, Y, Width, Height, PlaneMask)
	}
	return &XProtoGetImageCookie{cookie: xproto.GetImageCookie{}}
}
func (m *MockConnection) FreePixmap(Pixmap xproto.Pixmap) FreePixmapCookie {
	if m.FreePixmapFunc != nil {
		return m.FreePixmapFunc(Pixmap)
	}
	return &MockCheckCookie{}
}
func (m *MockConnection) InitComposite() error {
	if m.InitCompositeFunc != nil {
		return m.InitCompositeFunc()
	}
	return nil
}
func (m *MockConnection) NameWindowPixmap(Window xproto.Window, Pixmap xproto.Pixmap) NameWindowPixmapCookie {
	if m.NameWindowPixmapFunc != nil {
		return m.NameWindowPixmapFunc(Window, Pixmap)
	}
	return &MockCheckCookie{}
}

func (m *MockConnection) GetKeyboardMapping(first xproto.Keycode, count byte) GetKeyboardMappingCookie {
	if m.GetKeyboardMappingFunc != nil {
		return m.GetKeyboardMappingFunc(first, count)
	}
	return &MockGetKeyboardMappingCookie{reply: &xproto.GetKeyboardMappingReply{KeysymsPerKeycode: 1, Keysyms: []xproto.Keysym{}}}
}

func (m *MockConnection) FakeInputChecked(eventType byte, detail byte, tm uint32, window xproto.Window, x, y int16, device byte) XTestFakeInputCookie {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.LastFakeInput.EventType = eventType
	m.LastFakeInput.Detail = detail
	m.LastFakeInput.Time = tm
	m.LastFakeInput.Window = window
	m.LastFakeInput.X = x
	m.LastFakeInput.Y = y
	m.LastFakeInput.Device = device
	if m.FakeInputCheckedFunc != nil {
		return m.FakeInputCheckedFunc(eventType, detail, tm, window, x, y, device)
	}
	return &MockXTestFakeInputCookie{err: nil}
}

func (m *MockConnection) InitXTest() error { return nil }

func NewMockGetPropertyCookie(rep *xproto.GetPropertyReply) GetPropertyCookie {
	return &MockGetPropertyCookie{reply: rep}
}

func NewMockGetPropertyCookieError(err error) GetPropertyCookie {
	return &MockGetPropertyCookie{err: err}
}

func NewMockGetGeometryCookie(rep *xproto.GetGeometryReply) GetGeometryCookie {
	return &MockGetGeometryCookie{reply: rep}
}

func NewMockTranslateCoordinatesCookie(rep *xproto.TranslateCoordinatesReply) TranslateCoordinatesCookie {
	return &MockTranslateCoordinatesCookie{reply: rep}
}
