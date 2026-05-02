package x11

import (
"sync"

"github.com/jezek/xgb/xproto"
)

// Minimal mock connection for unit tests. Tests can set the Func fields to
// customize behavior.
type MockGetPropertyCookie struct {
reply *xproto.GetPropertyReply
}

func (m *MockGetPropertyCookie) Reply() (*xproto.GetPropertyReply, error) { return m.reply, nil }

type MockGetKeyboardMappingCookie struct {
reply *xproto.GetKeyboardMappingReply
}

func (m *MockGetKeyboardMappingCookie) Reply() (*xproto.GetKeyboardMappingReply, error) {
return m.reply, nil
}

type MockCheckCookie struct{}

func (m *MockCheckCookie) Check() error { return nil }

type MockGetGeometryCookie struct {
reply *xproto.GetGeometryReply
}

func (m *MockGetGeometryCookie) Reply() (*xproto.GetGeometryReply, error) { return m.reply, nil }

// Minimal XTest fake-input cookie implementation
type MockXTestFakeInputCookie struct{ err error }

func (m *MockXTestFakeInputCookie) Check() error { return m.err }

// MockInternAtomCookie implements InternAtomCookie for tests.
type MockInternAtomCookie struct{
reply *xproto.InternAtomReply
}
func (m *MockInternAtomCookie) Reply() (*xproto.InternAtomReply, error) { return m.reply, nil }

// MockConnection implements the Connection interface with user-provided hooks.
type MockConnection struct {
mu sync.Mutex

DefaultScreenFunc func() *xproto.ScreenInfo
SetupFunc         func() *xproto.SetupInfo

InternAtomFunc             func(OnlyIfExists bool, NameLen uint16, Name string) InternAtomCookie
GetPropertyFunc            func(Delete bool, Window xproto.Window, Property, Type xproto.Atom, LongOffset, LongLength uint32) GetPropertyCookie
GetGeometryFunc            func(Drawable xproto.Drawable) GetGeometryCookie
TranslateCoordinatesFunc   func(SrcWindow, DstWindow xproto.Window, SrcX, SrcY int16) TranslateCoordinatesCookie
SendEventCheckedFunc       func(Propagate bool, Destination xproto.Window, EventMask uint32, Event string) SendEventCookie
MapWindowCheckedFunc       func(Window xproto.Window) MapWindowCookie
ConfigureWindowCheckedFunc func(Window xproto.Window, ValueMask uint16, ValueList []uint32) ConfigureWindowCookie
NewIdFunc                  func() (uint32, error)
GetImageFunc               func(Format byte, Drawable xproto.Drawable, X, Y int16, Width, Height uint16, PlaneMask uint32) GetImageCookie
FreePixmapFunc             func(Pixmap xproto.Pixmap) FreePixmapCookie
InitCompositeFunc          func() error
NameWindowPixmapFunc       func(Window xproto.Window, Pixmap xproto.Pixmap) NameWindowPixmapCookie

GetKeyboardMappingFunc func(first xproto.Keycode, count byte) GetKeyboardMappingCookie
FakeInputCheckedFunc   func(eventType byte, detail byte, tm uint32, window xproto.Window, x, y int16, device byte) XTestFakeInputCookie

// helpers for tests
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
return &MockGetPropertyCookie{reply: &xproto.GetPropertyReply{Value: []byte{}, Format: 8}}
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
return &XProtoTranslateCoordinatesCookie{cookie: xproto.TranslateCoordinatesCookie{}}
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
func (m *MockConnection) NewId() (uint32, error) {
if m.NewIdFunc != nil {
return m.NewIdFunc()
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
// default empty mapping
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
