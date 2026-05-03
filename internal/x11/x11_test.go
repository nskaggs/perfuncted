package x11

import (
	"errors"
	"testing"

	"github.com/jezek/xgb/xproto"
)

var errTest = errors.New("test error")

// ── Cookie wrapper tests ──────────────────────────────────────────────────────

func TestMockGetPropertyCookie_Reply(t *testing.T) {
	reply := &xproto.GetPropertyReply{Format: 32, Value: []byte{1, 2, 3}}
	c := &MockGetPropertyCookie{reply: reply}
	got, err := c.Reply()
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if got.Format != 32 || len(got.Value) != 3 {
		t.Errorf("unexpected reply: %+v", got)
	}
}

func TestMockGetPropertyCookie_Error(t *testing.T) {
	c := &MockGetPropertyCookie{err: errTest}
	_, err := c.Reply()
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestMockGetKeyboardMappingCookie_Reply(t *testing.T) {
	reply := &xproto.GetKeyboardMappingReply{KeysymsPerKeycode: 2, Keysyms: []xproto.Keysym{0x61, 0x62}}
	c := &MockGetKeyboardMappingCookie{reply: reply}
	got, err := c.Reply()
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if got.KeysymsPerKeycode != 2 || len(got.Keysyms) != 2 {
		t.Errorf("unexpected reply: %+v", got)
	}
}

func TestMockGetGeometryCookie_Reply(t *testing.T) {
	reply := &xproto.GetGeometryReply{X: 10, Y: 20, Width: 800, Height: 600}
	c := &MockGetGeometryCookie{reply: reply}
	got, err := c.Reply()
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if got.X != 10 || got.Y != 20 || got.Width != 800 || got.Height != 600 {
		t.Errorf("unexpected reply: %+v", got)
	}
}

func TestMockTranslateCoordinatesCookie_Reply(t *testing.T) {
	reply := &xproto.TranslateCoordinatesReply{}
	c := &MockTranslateCoordinatesCookie{reply: reply}
	_, err := c.Reply()
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
}

func TestMockXTestFakeInputCookie_Check(t *testing.T) {
	c := &MockXTestFakeInputCookie{}
	if err := c.Check(); err != nil {
		t.Fatalf("Check: %v", err)
	}
}

func TestMockXTestFakeInputCookie_CheckError(t *testing.T) {
	c := &MockXTestFakeInputCookie{err: errTest}
	if err := c.Check(); err == nil {
		t.Fatal("expected error")
	}
}

func TestMockInternAtomCookie_Reply(t *testing.T) {
	reply := &xproto.InternAtomReply{Atom: xproto.Atom(42)}
	c := &MockInternAtomCookie{reply: reply}
	got, err := c.Reply()
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if got.Atom != 42 {
		t.Errorf("atom = %d, want 42", got.Atom)
	}
}

func TestMockGetImageCookie_Reply(t *testing.T) {
	reply := &xproto.GetImageReply{}
	c := &MockGetImageCookie{reply: reply}
	got, err := c.Reply()
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if got != reply {
		t.Errorf("unexpected reply: %+v", got)
	}
}

func TestMockGetImageCookie_Error(t *testing.T) {
	c := &MockGetImageCookie{err: errTest}
	_, err := c.Reply()
	if err == nil {
		t.Fatal("expected error")
	}
}

// ── MockConnection default behaviour ──────────────────────────────────────────

func TestMockConnection_Defaults(t *testing.T) {
	m := &MockConnection{}

	// Close should be a no-op.
	m.Close()

	// DefaultScreen returns a sensible default.
	screen := m.DefaultScreen()
	if screen.Root == 0 {
		t.Error("DefaultScreen.Root should be non-zero")
	}
	if screen.WidthInPixels != 800 || screen.HeightInPixels != 600 {
		t.Errorf("DefaultScreen dimensions = %dx%d, want 800x600",
			screen.WidthInPixels, screen.HeightInPixels)
	}

	// Setup returns a sensible default.
	setup := m.Setup()
	if setup.MinKeycode != 8 || setup.MaxKeycode != 255 {
		t.Errorf("Setup keycodes = %d-%d, want 8-255", setup.MinKeycode, setup.MaxKeycode)
	}

	// NewId returns default 42.
	id, err := m.NewId()
	if err != nil {
		t.Fatalf("NewId: %v", err)
	}
	if id != 42 {
		t.Errorf("NewId = %d, want 42", id)
	}

	// InitComposite and InitXTest succeed by default.
	if err := m.InitComposite(); err != nil {
		t.Errorf("InitComposite: %v", err)
	}
	if err := m.InitXTest(); err != nil {
		t.Errorf("InitXTest: %v", err)
	}
}

func TestMockConnection_FakeInputRecordsCall(t *testing.T) {
	m := &MockConnection{}
	c := m.FakeInputChecked(1, 2, 3, 4, 5, 6, 7)
	if err := c.Check(); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if m.LastFakeInput.EventType != 1 {
		t.Errorf("EventType = %d, want 1", m.LastFakeInput.EventType)
	}
	if m.LastFakeInput.Detail != 2 {
		t.Errorf("Detail = %d, want 2", m.LastFakeInput.Detail)
	}
	if m.LastFakeInput.Time != 3 {
		t.Errorf("Time = %d, want 3", m.LastFakeInput.Time)
	}
	if m.LastFakeInput.Window != 4 {
		t.Errorf("Window = %d, want 4", m.LastFakeInput.Window)
	}
	if m.LastFakeInput.X != 5 || m.LastFakeInput.Y != 6 {
		t.Errorf("X,Y = %d,%d, want 5,6", m.LastFakeInput.X, m.LastFakeInput.Y)
	}
	if m.LastFakeInput.Device != 7 {
		t.Errorf("Device = %d, want 7", m.LastFakeInput.Device)
	}
}

func TestMockConnection_GetKeyboardMappingDefault(t *testing.T) {
	m := &MockConnection{}
	c := m.GetKeyboardMapping(8, 248)
	reply, err := c.Reply()
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply.KeysymsPerKeycode != 1 {
		t.Errorf("KeysymsPerKeycode = %d, want 1", reply.KeysymsPerKeycode)
	}
}

func TestMockConnection_GetPropertyDefault(t *testing.T) {
	m := &MockConnection{}
	c := m.GetProperty(false, 0, 0, 0, 0, 0)
	reply, err := c.Reply()
	if err != nil {
		t.Fatalf("Reply: %v", err)
	}
	if reply.Format != 32 {
		t.Errorf("Format = %d, want 32", reply.Format)
	}
}

// ── MockConnection satisfies Connection interface ─────────────────────────────

var _ Connection = (*MockConnection)(nil)
