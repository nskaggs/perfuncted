//go:build linux

package window

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/nskaggs/perfuncted/internal/third_party/xgb"
	"github.com/nskaggs/perfuncted/internal/third_party/xgb/xproto"
	"github.com/nskaggs/perfuncted/internal/x11"
	"github.com/nskaggs/perfuncted/internal/x11test"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

func TestX11ActiveTitleDoesNotPublishTitleAfterCancellationDuringMetadataRead(t *testing.T) {
	for _, cancelAt := range []string{"active window", "title"} {
		t.Run(cancelAt, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			b, conn := newStubX11Backend(t, true, "Private title")
			old := conn.GetPropertyFunc
			conn.GetPropertyFunc = func(del bool, w xproto.Window, p, typ xproto.Atom, off, length uint32) x11.GetPropertyCookie {
				if (cancelAt == "active window" && p == b.atomNetActiveWindow) || (cancelAt == "title" && p == b.atomNetWMName) {
					cancel()
				}
				if p == b.atomNetActiveWindow {
					return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: []byte{50, 0, 0, 0}})
				}
				return old(del, w, p, typ, off, length)
			}
			title, err := b.ActiveTitle(ctx)
			if title != "" || !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled active title disclosed title=%q err=%v", title, err)
			}
		})
	}
}

// The root _NET_ACTIVE_WINDOW property is exactly one 32-bit WINDOW. A
// malformed property cannot claim focus for an arbitrary window ID.
func TestX11ActiveWindowRejectsMalformedProperty(t *testing.T) {
	for _, tc := range []struct {
		name   string
		format byte
		value  []byte
	}{
		{"8 bit property", 8, []byte{50, 0, 0, 0}},
		{"16 bit property", 16, []byte{50, 0, 0, 0}},
		{"extra window ID", 32, []byte{50, 0, 0, 0, 51, 0, 0, 0}},
		{"short property", 32, []byte{50, 0, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, conn := newStubX11Backend(t, false, "")
			conn.GetPropertyFunc = func(_ bool, _ xproto.Window, property, kind xproto.Atom, _, _ uint32) x11.GetPropertyCookie {
				if property != b.atomNetActiveWindow || kind != xproto.AtomWindow {
					t.Fatalf("unexpected property/type %d/%d", property, kind)
				}
				return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: tc.format, Value: tc.value})
			}
			win, err := b.activeWindow()
			if err != nil || win != 0 {
				t.Fatalf("malformed active-window property reported window=%d err=%v", win, err)
			}
		})
	}
}

type failingX11AtomCookie struct{ err error }

func (c failingX11AtomCookie) Reply() (*xproto.InternAtomReply, error) {
	return nil, c.err
}

func TestX11WindowMutationsStopWhenAtomLookupFails(t *testing.T) {
	lookupErr := errors.New("synthetic atom lookup transport failure")
	for _, tc := range []struct {
		name, failedAtom string
		minimize         bool
	}{
		{name: "close delete atom", failedAtom: "WM_DELETE_WINDOW"},
		{name: "close protocols atom", failedAtom: "WM_PROTOCOLS"},
		{name: "minimize state atom", failedAtom: "WM_CHANGE_STATE", minimize: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, conn := newStubX11Backend(t, false, "")
			var lookups []string
			var sentEvents int
			conn.InternAtomFunc = func(_ bool, _ uint16, atom string) x11.InternAtomCookie {
				lookups = append(lookups, atom)
				if atom == tc.failedAtom {
					return failingX11AtomCookie{err: lookupErr}
				}
				return cancelAwareAtomReply{}
			}
			conn.SendEventCheckedFunc = func(_ bool, _ xproto.Window, _ uint32, _ string) x11.SendEventCookie {
				sentEvents++
				return &x11.MockCheckCookie{}
			}

			var err error
			if tc.minimize {
				err = b.MinimizeByID(context.Background(), "50")
			} else {
				err = b.CloseWindowByID(context.Background(), "50")
			}
			if !errors.Is(err, lookupErr) || !strings.Contains(err.Error(), tc.failedAtom) {
				t.Fatalf("mutation error = %v, want wrapped %s lookup error", err, tc.failedAtom)
			}
			if sentEvents != 0 {
				t.Fatalf("mutation sent %d X11 events after atom lookup failed", sentEvents)
			}
			if len(lookups) == 0 || lookups[len(lookups)-1] != tc.failedAtom {
				t.Fatalf("atom lookups = %v, want failure at %s", lookups, tc.failedAtom)
			}
			if tc.failedAtom == "WM_DELETE_WINDOW" && len(lookups) != 1 {
				t.Fatalf("close looked up atoms after delete-atom failure: %v", lookups)
			}
		})
	}
}

type nilX11AtomCookie struct{}

func (nilX11AtomCookie) Reply() (*xproto.InternAtomReply, error) { return nil, nil }

// An absent atom reply is a protocol failure, not a valid reply with Atom 0.
// Reject it without sending a close/minimize mutation or panicking.
func TestX11WindowMutationsRejectMissingAtomReplies(t *testing.T) {
	for _, tc := range []struct {
		name, failAt string
		minimize     bool
	}{
		{"close missing delete atom", "WM_DELETE_WINDOW", false},
		{"close missing protocols atom", "WM_PROTOCOLS", false},
		{"minimize missing change state atom", "WM_CHANGE_STATE", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, conn := newStubX11Backend(t, false, "")
			events := 0
			conn.InternAtomFunc = func(_ bool, _ uint16, name string) x11.InternAtomCookie {
				if name == tc.failAt {
					return nilX11AtomCookie{}
				}
				return cancelAwareAtomReply{}
			}
			conn.SendEventCheckedFunc = func(_ bool, _ xproto.Window, _ uint32, _ string) x11.SendEventCookie {
				events++
				return &x11.MockCheckCookie{}
			}
			var err error
			if tc.minimize {
				err = b.MinimizeByID(context.Background(), "50")
			} else {
				err = b.CloseWindowByID(context.Background(), "50")
			}
			if err == nil || !strings.Contains(err.Error(), tc.failAt) || events != 0 {
				t.Fatalf("nil atom reply mishandled: err=%v events=%d", err, events)
			}
		})
	}
}

// ICCCM WM_CLASS contains two NUL-terminated STRING (Latin-1) fields.
// Incorrect wire format or unterminated/extra fields cannot be trusted as
// the window's instance and resource class.
func TestX11WindowClassValidatesICCCMWireEncoding(t *testing.T) {
	for _, tc := range []struct {
		name            string
		format          byte
		payload         []byte
		instance, class string
	}{
		{"latin1 class", 8, []byte{'C', 'a', 'f', 0xe9, 0, 'E', 'd', 'i', 't', 0xe9, 0}, "Café", "Edité"},
		{"invalid 32 bit format", 32, []byte("inst\x00Class\x00"), "", ""},
		{"missing final terminator", 8, []byte("inst\x00Class"), "", ""},
		{"missing separator", 8, []byte("instClass"), "", ""},
		{"extra hidden field", 8, []byte("inst\x00Class\x00Spoofed\x00"), "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, conn := newStubX11Backend(t, false, "")
			conn.GetPropertyFunc = func(_ bool, _ xproto.Window, property, typ xproto.Atom, _, _ uint32) x11.GetPropertyCookie {
				if property != xproto.AtomWmClass || typ != xproto.AtomString {
					t.Fatalf("wrong WM_CLASS query: %d %d", property, typ)
				}
				return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: tc.format, Type: xproto.AtomString, Value: tc.payload})
			}
			instance, class := b.windowClass(50)
			if instance != tc.instance || class != tc.class || !utf8.ValidString(instance) || !utf8.ValidString(class) {
				t.Fatalf("WM_CLASS=(%q,%q) want (%q,%q)", instance, class, tc.instance, tc.class)
			}
		})
	}
}

// A WINDOW list is an array of complete 32-bit IDs. A server declaring
// trailing bytes that cannot contain a whole ID must not produce a successful,
// partial enumeration, including when its first reply declares Format 0.
func TestX11ClientListRejectsInconsistentRemainder(t *testing.T) {
	for _, tc := range []struct {
		name       string
		format     byte
		bytesAfter uint32
		payload    []byte
	}{
		{"unaligned trailing bytes", 32, 2, []byte{50, 0, 0, 0}},
		{"unset format but trailing data", 0, 4, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, conn := newStubX11Backend(t, false, "")
			calls := 0
			conn.GetPropertyFunc = func(_ bool, _ xproto.Window, p, _ xproto.Atom, _, _ uint32) x11.GetPropertyCookie {
				if p != b.atomNetClientList {
					return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{})
				}
				calls++
				if calls > 1 {
					t.Fatal("read another chunk despite invalid prior reply")
				}
				return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: tc.format, Value: tc.payload, BytesAfter: tc.bytesAfter})
			}
			result, err := b.List(context.Background())
			if err == nil || len(result) != 0 || !strings.Contains(err.Error(), "_NET_CLIENT_LIST") {
				t.Fatalf("invalid list was accepted: result=%v err=%v", result, err)
			}
		})
	}
}

// An X server is permitted to close or malfunction while the client list is
// read. Neither a nil reply nor canceled pagination can publish an incomplete
// window list as if it were authoritative.
func TestX11ClientListNilPropertyReplyNeverPublishesWindows(t *testing.T) {
	b, conn := newStubX11Backend(t, false, "")
	calls := 0
	conn.GetPropertyFunc = func(_ bool, _ xproto.Window, property, _ xproto.Atom, _, _ uint32) x11.GetPropertyCookie {
		if property != b.atomNetClientList {
			t.Fatalf("queried unrelated property %d", property)
		}
		calls++
		return x11.NewMockGetPropertyCookie(nil)
	}
	windows, err := b.List(context.Background())
	if len(windows) != 0 || err == nil || !strings.Contains(err.Error(), "nil _NET_CLIENT_LIST reply") {
		t.Fatalf("nil property reply published windows=%+v err=%v", windows, err)
	}
	if calls != 1 {
		t.Fatalf("nil reply retried %d times", calls)
	}
}

func TestX11ClientListCanceledDuringSecondPageDiscardsFirstPage(t *testing.T) {
	b, conn := newStubX11Backend(t, false, "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	conn.GetPropertyFunc = func(_ bool, _ xproto.Window, property, _ xproto.Atom, offset, _ uint32) x11.GetPropertyCookie {
		if property != b.atomNetClientList {
			t.Fatalf("queried unrelated property %d", property)
		}
		calls++
		if calls > 2 {
			t.Fatalf("canceled enumeration continued to call %d", calls)
		}
		if calls == 1 {
			if offset != 0 {
				t.Fatalf("first page began at offset %d", offset)
			}
			data := make([]byte, 4)
			binary.LittleEndian.PutUint32(data, 50)
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: data, BytesAfter: 4})
		}
		if offset != 1 {
			t.Fatalf("second page offset %d", offset)
		}
		cancel() // transport finishes but authorization expired before accepting the reply
		data := make([]byte, 4)
		binary.LittleEndian.PutUint32(data, 51)
		return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: data})
	}
	windows, err := b.List(ctx)
	if len(windows) != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled paginated list published windows=%+v err=%v", windows, err)
	}
	if calls != 2 {
		t.Fatalf("canceled pagination made %d property requests", calls)
	}
}

// _NET_CLIENT_LIST can exceed the first GetProperty chunk. The server signals
// remaining elements through BytesAfter; silently returning only the first
// chunk produces an incomplete snapshot and hides windows.
func TestX11ClientListReadsEveryPropertyChunk(t *testing.T) {
	b, conn := newStubX11Backend(t, true, "Sample")
	original := conn.GetPropertyFunc
	var offsets []uint32
	conn.GetPropertyFunc = func(del bool, w xproto.Window, p, kind xproto.Atom, offset, length uint32) x11.GetPropertyCookie {
		if p != b.atomNetClientList {
			return original(del, w, p, kind, offset, length)
		}
		offsets = append(offsets, offset)
		if offset == 0 {
			data := make([]byte, 4)
			binary.LittleEndian.PutUint32(data, 50)
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: data, BytesAfter: 4})
		}
		if offset == 1 {
			data := make([]byte, 4)
			binary.LittleEndian.PutUint32(data, 51)
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: data})
		}
		t.Fatalf("unexpected property offset %d", offset)
		return nil
	}
	infos, err := b.List(context.Background())
	if err != nil || len(infos) != 2 || infos[0].ID != 50 || infos[1].ID != 51 {
		t.Fatalf("incomplete client list: infos=%v err=%v offsets=%v", infos, err, offsets)
	}
}

// A server that reports remaining bytes without returning any payload must
// fail instead of looping indefinitely or reporting an incomplete list.
func TestX11ClientListRejectsNonProgressingPropertyChunks(t *testing.T) {
	b, conn := newStubX11Backend(t, false, "")
	calls := 0
	conn.GetPropertyFunc = func(_ bool, _ xproto.Window, p, _ xproto.Atom, _, _ uint32) x11.GetPropertyCookie {
		if p != b.atomNetClientList {
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{})
		}
		calls++
		if calls > 2 {
			t.Fatal("pagination looped without progress")
		}
		return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, BytesAfter: 4})
	}
	infos, err := b.List(context.Background())
	if err == nil || len(infos) != 0 {
		t.Fatalf("non-progressing client list accepted: infos=%v err=%v", infos, err)
	}
}

// Duplicate IDs must not evade the hard property-size budget. BytesAfter
// reveals the total raw size on the first reply even when the decoded list
// contains only one unique ID.
func TestX11ClientListRejectsOversizedRawPropertyBeforePagination(t *testing.T) {
	b, conn := newStubX11Backend(t, false, "")
	calls := 0
	conn.GetPropertyFunc = func(_ bool, _ xproto.Window, property, _ xproto.Atom, offset, _ uint32) x11.GetPropertyCookie {
		if property != b.atomNetClientList {
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{})
		}
		calls++
		if calls > 1 {
			t.Fatalf("oversized list performed %d requests", calls)
		}
		if offset != 0 {
			t.Fatalf("unexpected offset %d", offset)
		}
		v := make([]byte, 4)
		binary.LittleEndian.PutUint32(v, 50)
		return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: v, BytesAfter: 4 * (1 << 20)})
	}
	infos, err := b.List(context.Background())
	if err == nil || len(infos) != 0 || !strings.Contains(err.Error(), "exceeds supported size") {
		t.Fatalf("oversized property not rejected: infos=%v err=%v calls=%d", infos, err, calls)
	}
	if calls != 1 {
		t.Fatalf("unexpected property calls %d", calls)
	}
}

// A snapshot cannot be trusted when the remaining-byte count fails to
// decrease after consuming data. Otherwise a changing or broken property can
// force up to a million round trips while never yielding a consistent list.
func TestX11ClientListRejectsStalledRemainingBytes(t *testing.T) {
	b, conn := newStubX11Backend(t, false, "")
	calls := 0
	conn.GetPropertyFunc = func(_ bool, _ xproto.Window, p, _ xproto.Atom, offset, _ uint32) x11.GetPropertyCookie {
		if p != b.atomNetClientList {
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{})
		}
		calls++
		if calls > 2 {
			t.Fatal("stalled list continued requesting chunks")
		}
		if offset != uint32(calls-1) {
			t.Fatalf("wrong chunk offset %d at call %d", offset, calls)
		}
		data := make([]byte, 4)
		binary.LittleEndian.PutUint32(data, uint32(49+calls))
		return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: data, BytesAfter: 4})
	}
	infos, err := b.List(context.Background())
	if err == nil || len(infos) != 0 || !strings.Contains(err.Error(), "_NET_CLIENT_LIST") {
		t.Fatalf("stalled list accepted: infos=%v err=%v", infos, err)
	}
	if calls != 2 {
		t.Fatalf("expected detection after second chunk, got %d calls", calls)
	}
}

func TestX11CloseDoesNotSendEventWhenAtomLookupCancelsRequest(t *testing.T) {
	for _, stage := range []string{"WM_DELETE_WINDOW", "WM_PROTOCOLS"} {
		t.Run(stage, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			b, conn := newStubX11Backend(t, false, "")
			calls := 0
			events := 0
			conn.InternAtomFunc = func(_ bool, _ uint16, name string) x11.InternAtomCookie {
				calls++
				if name == stage {
					cancel()
				}
				return cancelAwareAtomReply{}
			}
			conn.SendEventCheckedFunc = func(_ bool, _ xproto.Window, _ uint32, _ string) x11.SendEventCookie {
				events++
				return &x11.MockCheckCookie{}
			}
			err := b.CloseWindowByID(ctx, "50")
			if !errors.Is(err, context.Canceled) || events != 0 {
				t.Fatalf("canceled close continued: err=%v events=%d atomLookups=%d", err, events, calls)
			}
			if stage == "WM_DELETE_WINDOW" && calls != 1 {
				t.Fatalf("canceled after first atom lookup but made %d lookups", calls)
			}
		})
	}
}

type cancelAwareAtomReply struct{}

func (cancelAwareAtomReply) Reply() (*xproto.InternAtomReply, error) {
	return &xproto.InternAtomReply{Atom: 42}, nil
}

// _NET_CLIENT_LIST is a set of managed windows, not an instruction to
// process a window multiple times if a faulty WM repeats its identifier.
func TestX11IterateWindowsDeduplicatesRepeatedClientIDs(t *testing.T) {
	b, conn := newStubX11Backend(t, false, "")
	old := conn.GetPropertyFunc
	conn.GetPropertyFunc = func(del bool, w xproto.Window, p, kind xproto.Atom, offset, length uint32) x11.GetPropertyCookie {
		if p == b.atomNetClientList {
			data := make([]byte, 12)
			for i, id := range []uint32{50, 51, 50} {
				binary.LittleEndian.PutUint32(data[i*4:], id)
			}
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: data})
		}
		return old(del, w, p, kind, offset, length)
	}
	windows, err := b.List(context.Background())
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(windows) != 2 || windows[0].ID != 50 || windows[1].ID != 51 {
		t.Fatalf("duplicate client IDs were emitted: %+v", windows)
	}
}

// _NET_FRAME_EXTENTS uses CARDINAL, not the property atom as its value type.
// A compliant X server returns no value for a mismatched GetProperty type,
// which otherwise drops the decoration offsets from window geometry.
func TestX11WindowGeometryRequestsCardinalFrameExtents(t *testing.T) {
	b, conn := newStubX11Backend(t, true, "Test")
	conn.GetPropertyFunc = func(_ bool, _ xproto.Window, property, kind xproto.Atom, _, _ uint32) x11.GetPropertyCookie {
		switch property {
		case b.atomMotifWMHints:
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 0})
		case b.atomNetFrameExtent:
			if kind != xproto.AtomCardinal {
				return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 0, Type: xproto.AtomCardinal})
			}
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Type: xproto.AtomCardinal, Value: []byte{5, 0, 0, 0, 7, 0, 0, 0, 11, 0, 0, 0, 13, 0, 0, 0}})
		default:
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 0})
		}
	}
	x, y, w, h := b.windowGeometry(50)
	if x != 5 || y != 9 || w != 812 || h != 624 {
		t.Fatalf("decoration extents absent: geometry=(%d,%d,%d,%d) want (5,9,812,624)", x, y, w, h)
	}
}

// EWMH frame extents describe a small decoration around a window. An
// untrusted 32-bit CARDINAL such as 0xffffffff must not expand the returned
// geometry into billions of pixels or negative coordinates.
func TestX11FrameExtentsRejectOutOfRangeDecorationValues(t *testing.T) {
	for _, tc := range []struct {
		name    string
		extents [4]uint32
	}{
		{"huge left", [4]uint32{^uint32(0), 0, 0, 0}},
		{"huge right", [4]uint32{0, ^uint32(0), 0, 0}},
		{"huge top", [4]uint32{0, 0, ^uint32(0), 0}},
		{"huge bottom", [4]uint32{0, 0, 0, ^uint32(0)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, conn := newStubX11Backend(t, true, "Test")
			conn.GetPropertyFunc = func(_ bool, _ xproto.Window, p, _ xproto.Atom, _, _ uint32) x11.GetPropertyCookie {
				if p == b.atomNetFrameExtent {
					buf := make([]byte, 16)
					for i, v := range tc.extents {
						binary.LittleEndian.PutUint32(buf[i*4:], v)
					}
					return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Type: xproto.AtomCardinal, Value: buf})
				}
				return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{})
			}
			x, y, w, h := b.windowGeometry(50)
			if x != 10 || y != 20 || w != 800 || h != 600 {
				t.Fatalf("untrusted frame extents changed geometry to (%d,%d,%d,%d)", x, y, w, h)
			}
		})
	}
}

func TestX11ManagerHeadlessEWMHDiscoveryGeometryAndActions(t *testing.T) {
	privateAuth := filepath.Join(t.TempDir(), "isolated-auth")
	if err := os.WriteFile(privateAuth, nil, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XAUTHORITY", privateAuth)
	display, stop, err := x11test.StartXvfb()
	if err != nil {
		t.Skipf("disposable Xvfb display unavailable: %v", err)
	}
	t.Cleanup(stop)
	raw, err := xgb.NewConnDisplay(display)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	screen := xproto.Setup(raw).DefaultScreen(raw)
	if screen == nil {
		t.Fatal("Xvfb has no root display")
	}
	winID, err := raw.NewId()
	if err != nil {
		t.Fatal(err)
	}
	win := xproto.Window(winID)
	if lintShadow45 := xproto.CreateWindowChecked(raw, screen.RootDepth, win, screen.Root, 11, 17, 130, 90, 0, xproto.WindowClassInputOutput, screen.RootVisual, 0, nil).Check(); lintShadow45 != nil {
		t.Fatal(lintShadow45)
	}
	defer xproto.DestroyWindow(raw, win)
	if lintShadow49 := xproto.MapWindowChecked(raw, win).Check(); lintShadow49 != nil {
		t.Fatal(lintShadow49)
	}
	backend, err := NewX11Backend(display)
	if err != nil {
		t.Fatalf("open X11 manager on synthetic desktop: %v", err)
	}
	defer backend.Close()
	setProperty := func(w xproto.Window, p, k xproto.Atom, format byte, items uint32, data []byte) {
		t.Helper()
		if lintShadow59 := xproto.ChangePropertyChecked(raw, xproto.PropModeReplace, w, p, k, format, items, data).Check(); lintShadow59 != nil {
			t.Fatalf("set X11 root/window property %d: %v", p, lintShadow59)
		}
	}
	atomBytes := func(ids ...uint32) []byte {
		buf := make([]byte, 4*len(ids))
		for i, id := range ids {
			binary.LittleEndian.PutUint32(buf[i*4:], id)
		}
		return buf
	}
	setProperty(screen.Root, backend.atomNetClientList, xproto.AtomWindow, 32, 1, atomBytes(winID))
	setProperty(screen.Root, backend.atomNetActiveWindow, xproto.AtomWindow, 32, 1, atomBytes(winID))
	setProperty(win, backend.atomNetWMName, backend.atomUTF8String, 8, 11, []byte("Test Editor"))
	setProperty(win, xproto.AtomWmClass, xproto.AtomString, 8, uint32(len("pf-test\x00PFTest\x00")), []byte("pf-test\x00PFTest\x00"))
	setProperty(win, backend.atomNetWMPID, xproto.AtomCardinal, 32, 1, atomBytes(1234))
	setProperty(win, backend.atomNetWMState, xproto.AtomAtom, 32, 3, atomBytes(uint32(backend.atomNetWMStateMaximizedVert), uint32(backend.atomNetWMStateMaximizedHorz), uint32(backend.atomNetWMStateFullscreen)))
	ctx := context.Background()
	windows, err := backend.List(ctx)
	if err != nil || len(windows) != 1 {
		t.Fatalf("real X11 root enumeration: %+v %v", windows, err)
	}
	info := windows[0]
	if info.ID != uint64(winID) || info.Title != "Test Editor" || info.AppID != "pf-test" || info.Class != "PFTest" || info.PID != 1234 || !info.Active || !info.Maximized || !info.Fullscreen || info.Minimized {
		t.Fatalf("EWMH window metadata was lost: %+v", info)
	}
	title, err := backend.ActiveTitle(ctx)
	if err != nil || title != "Test Editor" {
		t.Fatalf("root active window: %q %v", title, err)
	}
	key := strconv.FormatUint(uint64(winID), 10)
	fresh, err := backend.InfoByID(ctx, key)
	if err != nil || fresh.ID != uint64(winID) {
		t.Fatalf("InfoByID %s: %+v %v", key, fresh, err)
	}
	if _, lintShadow94 := backend.InfoByID(ctx, "999999"); !errors.Is(lintShadow94, ErrWindowNotFound) {
		t.Fatalf("unknown window accepted: %v", lintShadow94)
	}
	if lintShadow97 := backend.MoveByID(ctx, key, 71, 53); lintShadow97 != nil {
		t.Fatalf("real X11 move failed: %v", lintShadow97)
	}
	if lintShadow100 := backend.ResizeByID(ctx, key, 215, 145); lintShadow100 != nil {
		t.Fatalf("real X11 resize failed: %v", lintShadow100)
	}
	geom, err := xproto.GetGeometry(raw, xproto.Drawable(win)).Reply()
	if err != nil || geom.X != 71 || geom.Y != 53 || geom.Width != 215 || geom.Height != 145 {
		t.Fatalf("server did not apply requested geometry: %+v %v", geom, err)
	}
	for _, action := range []struct {
		name string
		call func(context.Context, string) error
	}{
		{"activate", backend.ActivateByID}, {"minimize", backend.MinimizeByID}, {"maximize", backend.MaximizeByID},
		{"fullscreen", backend.FullscreenByID}, {"unfullscreen", backend.UnfullscreenByID}, {"restore", backend.RestoreByID}, {"close", backend.CloseWindowByID},
	} {
		t.Run(action.name, func(t *testing.T) {
			if err := action.call(ctx, key); err != nil {
				t.Fatalf("EWMH request could not be serialized: %v", err)
			}
		})
	}
	if err := backend.Sync(ctx); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if ops := backend.SupportedOperations(); len(ops) < 10 {
		t.Fatalf("missing X11 control capabilities: %v", ops)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := backend.MoveByID(canceled, key, 1, 2); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled movement: %v", err)
	}
	if err := backend.ResizeByID(ctx, key, 0, 10); err == nil {
		t.Fatal("invalid X11 resize was sent")
	}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.List(ctx); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("closed X11 manager accepted discovery: %v", err)
	}
	if err := backend.Close(); err != nil {
		t.Fatalf("repeat close: %v", err)
	}
}

// Window 0 is X11 None, not an addressable client. The window manager's
// client list must not advertise it as a real window or mark it active.
func TestX11IterateWindowsSkipsInvalidZeroClientID(t *testing.T) {
	b, conn := newStubX11Backend(t, false, "")
	prev := conn.GetPropertyFunc
	conn.GetPropertyFunc = func(del bool, w xproto.Window, p, typ xproto.Atom, offset, length uint32) x11.GetPropertyCookie {
		if p == b.atomNetClientList {
			v := make([]byte, 8)
			binary.LittleEndian.PutUint32(v[4:], 50)
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: v})
		}
		return prev(del, w, p, typ, offset, length)
	}
	windows, err := b.List(context.Background())
	if err != nil {
		t.Fatalf("window listing failed: %v", err)
	}
	if len(windows) != 1 || windows[0].ID != 50 {
		t.Fatalf("client list returned invalid window: %+v", windows)
	}
}

type observedX11IteratorContext struct {
	context.Context //nolint:containedctx // The wrapper exposes the initial cancellation check for synchronization.
	checked         chan struct{}
	once            sync.Once
}

func (c *observedX11IteratorContext) Err() error {
	err := c.Context.Err()
	c.once.Do(func() { close(c.checked) })
	return err
}

func TestX11IteratorCancellationWhileWaitingForAdmission(t *testing.T) {
	backend, conn := newStubX11Backend(t, true, "private window")
	reads := 0
	conn.GetPropertyFunc = func(bool, xproto.Window, xproto.Atom, xproto.Atom, uint32, uint32) x11.GetPropertyCookie {
		reads++
		return x11.NewMockGetPropertyCookie(nil)
	}
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &observedX11IteratorContext{Context: parent, checked: make(chan struct{})}
	result := make(chan error, 1)
	backend.lifecycleMu.Lock()
	go func() {
		for info, err := range backend.IterateWindows(ctx) {
			if info != (Info{}) {
				result <- errors.New("canceled iterator disclosed a window")
				return
			}
			result <- err
			return
		}
		result <- errors.New("canceled iterator yielded no error")
	}()
	select {
	case <-ctx.checked:
		cancel()
		backend.lifecycleMu.Unlock()
	case <-time.After(time.Second):
		cancel()
		backend.lifecycleMu.Unlock()
		t.Fatal("iterator did not reach initial cancellation check")
	}
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("iterator returned %v, want cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled iterator did not finish")
	}
	if reads != 0 {
		t.Fatalf("canceled iterator made %d X11 property requests", reads)
	}
}

// A lazy X11 consumer that stops after one result must not access metadata of
// subsequent windows. Those subsequent windows can belong to other clients.
func TestX11IteratorConsumerStopDoesNotReadFollowingWindow(t *testing.T) {
	b, conn := newStubX11Backend(t, true, "first-window")
	original := conn.GetPropertyFunc
	const first = xproto.Window(50)
	const second = xproto.Window(51)
	secondReads := 0
	conn.GetPropertyFunc = func(del bool, w xproto.Window, p, tp xproto.Atom, offset, count uint32) x11.GetPropertyCookie {
		if p == b.atomNetClientList {
			data := make([]byte, 8)
			binary.LittleEndian.PutUint32(data[:4], uint32(first))
			binary.LittleEndian.PutUint32(data[4:], uint32(second))
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: data})
		}
		if w == second {
			secondReads++
		}
		return original(del, w, p, tp, offset, count)
	}
	seen := 0
	for info, err := range b.IterateWindows(context.Background()) {
		if err != nil || info.ID != uint64(first) {
			t.Fatalf("first window=%+v err=%v", info, err)
		}
		seen++
		break
	}
	if seen != 1 || secondReads != 0 {
		t.Fatalf("iterator read unrequested client: seen=%d reads=%d", seen, secondReads)
	}
}

// Once an observer cancels after seeing the first window, the iterator must
// not disclose the next window in its already-fetched client-list snapshot.
func TestX11IteratorCancellationAfterFirstYieldStopsMetadata(t *testing.T) {
	b, conn := newStubX11Backend(t, true, "first-window")
	original := conn.GetPropertyFunc
	const second = xproto.Window(51)
	secondReads := 0
	conn.GetPropertyFunc = func(del bool, w xproto.Window, p, tp xproto.Atom, offset, count uint32) x11.GetPropertyCookie {
		if p == b.atomNetClientList {
			data := make([]byte, 8)
			binary.LittleEndian.PutUint32(data[:4], 50)
			binary.LittleEndian.PutUint32(data[4:], uint32(second))
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: data})
		}
		if w == second {
			secondReads++
		}
		return original(del, w, p, tp, offset, count)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	seen, failures := 0, 0
	for info, err := range b.IterateWindows(ctx) {
		if seen == 0 {
			if err != nil || info.ID != 50 {
				t.Fatalf("first window=%+v err=%v", info, err)
			}
			cancel()
			seen++
			continue
		}
		if !errors.Is(err, context.Canceled) || info.ID != 0 {
			t.Fatalf("canceled iterator disclosed window: %+v %v", info, err)
		}
		failures++
	}
	if seen != 1 || failures != 1 || secondReads != 0 {
		t.Fatalf("late cancellation: windows=%d errors=%d private reads=%d", seen, failures, secondReads)
	}
}

func TestX11IteratorStopsWhenClientListReplyCancelsRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b, conn := newStubX11Backend(t, false, "")
	conn.GetPropertyFunc = func(bool, xproto.Window, xproto.Atom, xproto.Atom, uint32, uint32) x11.GetPropertyCookie {
		cancel()
		return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 0})
	}
	windows, err := b.List(ctx)
	if !errors.Is(err, context.Canceled) || len(windows) != 0 {
		t.Fatalf("canceled client-list read reported success: windows=%#v err=%v", windows, err)
	}
}

func TestX11IteratorDoesNotPublishWindowAfterMetadataReadCancelsRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b, conn := newStubX11Backend(t, true, "private-X11-title")
	original := conn.GetPropertyFunc
	conn.GetPropertyFunc = func(del bool, w xproto.Window, p, tp xproto.Atom, off, count uint32) x11.GetPropertyCookie {
		if p == b.atomNetWMName {
			cancel()
		}
		return original(del, w, p, tp, off, count)
	}
	windows, err := b.List(ctx)
	if !errors.Is(err, context.Canceled) || len(windows) != 0 {
		t.Fatalf("canceled metadata read disclosed private window: windows=%#v err=%v", windows, err)
	}
}

// A request canceled while resolving WM_CHANGE_STATE must not minimize a
// window after the caller has withdrawn the operation.
func TestX11MinimizeDoesNotSendEventAfterAtomLookupCancelsRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b, conn := newStubX11Backend(t, false, "")
	atomCalls := 0
	events := 0
	conn.InternAtomFunc = func(_ bool, _ uint16, name string) x11.InternAtomCookie {
		atomCalls++
		if name != "WM_CHANGE_STATE" {
			t.Fatalf("unexpected atom lookup %q", name)
		}
		cancel()
		return minimizeAtomReply{}
	}
	conn.SendEventCheckedFunc = func(_ bool, _ xproto.Window, _ uint32, _ string) x11.SendEventCookie {
		events++
		return &x11.MockCheckCookie{}
	}
	err := b.MinimizeByID(ctx, "50")
	if !errors.Is(err, context.Canceled) || events != 0 || atomCalls != 1 {
		t.Fatalf("canceled minimize performed mutation: err=%v events=%d atomCalls=%d", err, events, atomCalls)
	}
}

type minimizeAtomReply struct{}

func (minimizeAtomReply) Reply() (*xproto.InternAtomReply, error) {
	return &xproto.InternAtomReply{Atom: 42}, nil
}

// Motif hints are exactly five 32-bit values. An incorrectly formatted reply
// must not authorize stripping decoration from the window geometry.
func TestX11MotifHintsRejectMalformedPropertyFormats(t *testing.T) {
	for _, tc := range []struct {
		name   string
		format byte
	}{
		{"8-bit", 8}, {"16-bit", 16}, {"unset", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, conn := newStubX11Backend(t, false, "")
			conn.GetPropertyFunc = func(_ bool, _ xproto.Window, property, typ xproto.Atom, _, _ uint32) x11.GetPropertyCookie {
				if property != b.atomMotifWMHints || typ != b.atomMotifWMHints {
					t.Fatalf("unexpected hint property %d type %d", property, typ)
				}
				data := make([]byte, 20)
				data[0] = 2 // decorations flag set
				// decorations value 0 means no decorations, but only in valid 32-bit hints.
				return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: tc.format, Value: data})
			}
			if !b.windowHasDecoration(50) {
				t.Fatal("malformed Motif hints hid decorations")
			}
		})
	}
}

// X11 checked mutations may finish during a request's cancellation. Their
// acknowledged transport success must not override the withdrawn operation.
func TestX11MutationsReportCancellationDuringCheckedRequest(t *testing.T) {
	cases := []struct {
		name string
		run  func(*X11Backend, context.Context) error
	}{
		{"activate", func(b *X11Backend, ctx context.Context) error { return b.ActivateByID(ctx, "50") }},
		{"move", func(b *X11Backend, ctx context.Context) error { return b.MoveByID(ctx, "50", 1, 2) }},
		{"resize", func(b *X11Backend, ctx context.Context) error { return b.ResizeByID(ctx, "50", 200, 100) }},
		{"maximize", func(b *X11Backend, ctx context.Context) error { return b.MaximizeByID(ctx, "50") }},
		{"fullscreen", func(b *X11Backend, ctx context.Context) error { return b.FullscreenByID(ctx, "50") }},
		{"unfullscreen", func(b *X11Backend, ctx context.Context) error { return b.UnfullscreenByID(ctx, "50") }},
		{"close", func(b *X11Backend, ctx context.Context) error { return b.CloseWindowByID(ctx, "50") }},
		{"minimize", func(b *X11Backend, ctx context.Context) error { return b.MinimizeByID(ctx, "50") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			b, conn := newStubX11Backend(t, false, "")
			calls := 0
			conn.SendEventCheckedFunc = func(bool, xproto.Window, uint32, string) x11.SendEventCookie {
				calls++
				cancel()
				return &x11.MockCheckCookie{}
			}
			conn.ConfigureWindowCheckedFunc = func(xproto.Window, uint16, []uint32) x11.ConfigureWindowCookie {
				calls++
				cancel()
				return &x11.MockCheckCookie{}
			}
			err := tc.run(b, ctx)
			if !errors.Is(err, context.Canceled) || calls != 1 {
				t.Fatalf("late cancellation not reported: err=%v calls=%d", err, calls)
			}
		})
	}
}

// An X11 round trip may return a missing reply without a transport error.
// Geometry discovery must degrade safely instead of panicking.
func TestX11GeometryHandlesMissingGeometryAndTranslationReplies(t *testing.T) {
	for _, tc := range []struct {
		name                       string
		nilGeometry                bool
		wantX, wantY, wantW, wantH int
	}{
		{"missing geometry reply", true, 0, 0, 0, 0},
		{"missing translated coordinates", false, 10, 20, 800, 600},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, conn := newStubX11Backend(t, false, "")
			if tc.nilGeometry {
				conn.GetGeometryFunc = func(xproto.Drawable) x11.GetGeometryCookie { return x11.NewMockGetGeometryCookie(nil) }
			} else {
				conn.GetGeometryFunc = func(xproto.Drawable) x11.GetGeometryCookie {
					return x11.NewMockGetGeometryCookie(&xproto.GetGeometryReply{X: 10, Y: 20, Width: 800, Height: 600})
				}
				conn.TranslateCoordinatesFunc = func(xproto.Window, xproto.Window, int16, int16) x11.TranslateCoordinatesCookie {
					return x11.NewMockTranslateCoordinatesCookie(nil)
				}
			}
			x, y, w, h := b.windowGeometry(50)
			if x != tc.wantX || y != tc.wantY || w != tc.wantW || h != tc.wantH {
				t.Fatalf("geometry (%d,%d,%d,%d) want (%d,%d,%d,%d)", x, y, w, h, tc.wantX, tc.wantY, tc.wantW, tc.wantH)
			}
		})
	}
}

type x11OperationErrorCookie struct{ err error }

func (c x11OperationErrorCookie) Check() error { return c.err }

func TestX11OperationsRejectInvalidIDsAndPreserveFirstFailure(t *testing.T) {
	t.Run("fullscreen rejects invalid ID before transport", func(t *testing.T) {
		b, conn := newStubX11Backend(t, false, "")
		sent := 0
		conn.SendEventCheckedFunc = func(bool, xproto.Window, uint32, string) x11.SendEventCookie {
			sent++
			return &x11.MockCheckCookie{}
		}
		if err := b.FullscreenByID(context.Background(), "bad-window-id"); err == nil {
			t.Fatal("FullscreenByID accepted an invalid native ID")
		}
		if sent != 0 {
			t.Fatalf("FullscreenByID sent %d events for an invalid ID", sent)
		}
	})

	t.Run("unfullscreen rejects invalid ID before transport", func(t *testing.T) {
		b, conn := newStubX11Backend(t, false, "")
		sent := 0
		conn.SendEventCheckedFunc = func(bool, xproto.Window, uint32, string) x11.SendEventCookie {
			sent++
			return &x11.MockCheckCookie{}
		}
		if err := b.UnfullscreenByID(context.Background(), "bad-window-id"); err == nil {
			t.Fatal("UnfullscreenByID accepted an invalid native ID")
		}
		if sent != 0 {
			t.Fatalf("UnfullscreenByID sent %d events for an invalid ID", sent)
		}
	})

	t.Run("restore returns first checked event error without mapping", func(t *testing.T) {
		wantErr := errors.New("synthetic state event rejected")
		b, conn := newStubX11Backend(t, false, "")
		sent, mapped := 0, 0
		conn.SendEventCheckedFunc = func(bool, xproto.Window, uint32, string) x11.SendEventCookie {
			sent++
			return x11OperationErrorCookie{err: wantErr}
		}
		conn.MapWindowCheckedFunc = func(xproto.Window) x11.MapWindowCookie {
			mapped++
			return &x11.MockCheckCookie{}
		}
		if err := b.RestoreByID(context.Background(), "50"); !errors.Is(err, wantErr) {
			t.Fatalf("RestoreByID error=%v, want checked-event error", err)
		}
		if sent != 1 || mapped != 0 {
			t.Fatalf("RestoreByID continued after failed state event: sent=%d mapped=%d", sent, mapped)
		}
	})

	t.Run("info rejects invalid ID before listing", func(t *testing.T) {
		b, conn := newStubX11Backend(t, false, "")
		queries := 0
		conn.GetPropertyFunc = func(bool, xproto.Window, xproto.Atom, xproto.Atom, uint32, uint32) x11.GetPropertyCookie {
			queries++
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32})
		}
		if info, err := b.InfoByID(context.Background(), "bad-window-id"); err == nil || info != (Info{}) {
			t.Fatalf("InfoByID returned info=%+v err=%v for an invalid native ID", info, err)
		}
		if queries != 0 {
			t.Fatalf("InfoByID made %d X11 queries for an invalid ID", queries)
		}
	})

	t.Run("info preserves list query failure", func(t *testing.T) {
		wantErr := errors.New("synthetic client-list transport failure")
		b, conn := newStubX11Backend(t, false, "")
		queries := 0
		conn.GetPropertyFunc = func(_ bool, _ xproto.Window, property, _ xproto.Atom, _, _ uint32) x11.GetPropertyCookie {
			if property == b.atomNetClientList {
				queries++
				return x11.NewMockGetPropertyCookieError(wantErr)
			}
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32})
		}
		if info, err := b.InfoByID(context.Background(), "50"); !errors.Is(err, wantErr) || info != (Info{}) {
			t.Fatalf("InfoByID returned info=%+v err=%v, want client-list transport failure", info, err)
		}
		if queries != 1 {
			t.Fatalf("InfoByID made %d client-list queries after the failure", queries)
		}
	})
}

// _NET_WM_PID has type CARDINAL under EWMH. XGetWindowProperty returns
// no value for a caller that requests a different property type.
func TestX11WindowPIDRequestsEWMHCardinalPropertyType(t *testing.T) {
	b, conn := newStubX11Backend(t, false, "")
	var requestedType xproto.Atom
	conn.GetPropertyFunc = func(_ bool, _ xproto.Window, property, typ xproto.Atom, _, _ uint32) x11.GetPropertyCookie {
		if property != b.atomNetWMPID {
			t.Fatalf("requested wrong property %d", property)
		}
		requestedType = typ
		if typ != xproto.AtomCardinal {
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 0, Type: xproto.AtomCardinal})
		}
		return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Type: xproto.AtomCardinal, Value: []byte{0xd2, 0x04, 0, 0}})
	}
	if pid := b.windowPID(50); pid != 1234 {
		t.Fatalf("_NET_WM_PID returned %d, requested type=%d; want CARDINAL", pid, requestedType)
	}
}

// A process ID must be a positive signed Linux PID encoded as exactly one
// 32-bit EWMH CARDINAL. Non-32-bit or out-of-range properties cannot be
// promoted into the public window identity metadata.
func TestX11WindowPIDRejectsMalformedOrOutOfRangeProperty(t *testing.T) {
	for _, tc := range []struct {
		name   string
		format byte
		data   []byte
	}{
		{"8 bit format", 8, []byte{17, 0, 0, 0}},
		{"16 bit format", 16, []byte{17, 0, 0, 0}},
		{"zero pid", 32, []byte{0, 0, 0, 0}},
		{"negative signed pid", 32, []byte{0xff, 0xff, 0xff, 0xff}},
		{"sign bit set", 32, []byte{0, 0, 0, 0x80}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, conn := newStubX11Backend(t, false, "")
			conn.GetPropertyFunc = func(_ bool, _ xproto.Window, property, kind xproto.Atom, _, _ uint32) x11.GetPropertyCookie {
				if property != b.atomNetWMPID || kind != xproto.AtomCardinal {
					t.Fatalf("wrong PID lookup: property=%d kind=%d", property, kind)
				}
				return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Type: xproto.AtomCardinal, Format: tc.format, Value: tc.data})
			}
			if got := b.windowPID(50); got != 0 {
				t.Fatalf("malformed PID property became %d", got)
			}
		})
	}
}

// Restore first clears maximization and then maps the window. If the caller
// cancels while the first mutation's X11 event completes, the second mutation
// must not be sent after the request is withdrawn.
func TestX11RestoreSkipsMapAfterCancellationDuringStateEvent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b, conn := newStubX11Backend(t, false, "")
	stateEvents := 0
	mapCalls := 0
	conn.SendEventCheckedFunc = func(_ bool, _ xproto.Window, _ uint32, _ string) x11.SendEventCookie {
		stateEvents++
		cancel()
		return &x11.MockCheckCookie{}
	}
	conn.MapWindowCheckedFunc = func(_ xproto.Window) x11.MapWindowCookie {
		mapCalls++
		return &x11.MockCheckCookie{}
	}
	err := b.RestoreByID(ctx, "50")
	if !errors.Is(err, context.Canceled) || stateEvents != 1 || mapCalls != 0 {
		t.Fatalf("restore continued after cancellation: err=%v events=%d mapCalls=%d", err, stateEvents, mapCalls)
	}
}

// A checked map can finish after the request is withdrawn. Restore should
// report that cancellation instead of confirming a completed operation.
func TestX11RestoreReportsCancellationDuringFinalMapRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b, conn := newStubX11Backend(t, false, "")
	stateCalls, mapCalls := 0, 0
	conn.SendEventCheckedFunc = func(bool, xproto.Window, uint32, string) x11.SendEventCookie {
		stateCalls++
		return &x11.MockCheckCookie{}
	}
	conn.MapWindowCheckedFunc = func(xproto.Window) x11.MapWindowCookie {
		mapCalls++
		cancel()
		return &x11.MockCheckCookie{}
	}
	err := b.RestoreByID(ctx, "50")
	if !errors.Is(err, context.Canceled) || stateCalls != 1 || mapCalls != 1 {
		t.Fatalf("restore confirmed cancellation as success: err=%v state=%d map=%d", err, stateCalls, mapCalls)
	}
}

// The client list may be empty and the final active-window metadata round
// trip may cancel. A requested InfoByID should return cancellation instead
// of incorrectly classifying the window as absent.
func TestX11InfoByIDReportsCancellationAfterEmptyClientList(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b, conn := newStubX11Backend(t, false, "")
	original := conn.GetPropertyFunc
	conn.GetPropertyFunc = func(del bool, w xproto.Window, p, typ xproto.Atom, offset, length uint32) x11.GetPropertyCookie {
		if p == b.atomNetActiveWindow {
			cancel()
		}
		return original(del, w, p, typ, offset, length)
	}
	info, err := b.InfoByID(ctx, "50")
	if !errors.Is(err, context.Canceled) || info != (Info{}) {
		t.Fatalf("late cancellation classified as not-found: info=%+v err=%v", info, err)
	}
}

// A malformed _NET_WM_STATE property must not claim fullscreen, minimized,
// or maximized state from a partial or incorrectly typed atom list.
func TestX11WindowStateRejectsMalformedProperty(t *testing.T) {
	for _, tc := range []struct {
		name   string
		format byte
		value  []byte
	}{
		{"8 bit state", 8, []byte{23, 0, 0, 0}},
		{"trailing partial atom", 32, []byte{23, 0, 0, 0, 99}},
		{"16 bit state", 16, []byte{23, 0, 0, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, conn := newStubX11Backend(t, false, "")
			conn.GetPropertyFunc = func(_ bool, _ xproto.Window, property, kind xproto.Atom, _, _ uint32) x11.GetPropertyCookie {
				if property != b.atomNetWMState || kind != xproto.AtomAtom {
					t.Fatalf("unexpected property/type %d/%d", property, kind)
				}
				v := append([]byte(nil), tc.value...)
				v[0] = byte(b.atomNetWMStateFullscreen)
				return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: tc.format, Value: v})
			}
			_, _, fullscreen := b.windowState(50)
			if fullscreen {
				t.Fatalf("malformed _NET_WM_STATE returned fullscreen=true")
			}
		})
	}
}

type cancelDuringX11SyncConnection struct {
	x11.Connection
	cancel context.CancelFunc
	calls  int
}

func (c *cancelDuringX11SyncConnection) Sync() { c.calls++; c.cancel() }

// Sync must not report success when the caller cancels while its blocking
// X11 round trip completes, even if the transport method has no error return.
func TestX11SyncReportsCancellationDuringTransportRoundTrip(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b, _ := newStubX11Backend(t, false, "")
	conn := &cancelDuringX11SyncConnection{Connection: b.conn, cancel: cancel}
	b.conn = conn
	err := b.Sync(ctx)
	if !errors.Is(err, context.Canceled) || conn.calls != 1 {
		t.Fatalf("sync acknowledged canceled request: err=%v calls=%d", err, conn.calls)
	}
}

// newStubX11Backend creates an X11Backend backed by a shared MockConnection with
// pre-set atom values.  Tests that need to inspect or override specific methods
// receive the *x11.MockConnection directly so they can set Func hooks without
// any type-assertion.
func newStubX11Backend(t *testing.T, withWindow bool, title string) (*X11Backend, *x11.MockConnection) {
	t.Helper()
	conn := &x11.MockConnection{}
	// InternAtom(false) creates a named atom and must return a nonzero ID.
	// Preserve that wire contract for successful action tests.
	conn.InternAtomFunc = func(bool, uint16, string) x11.InternAtomCookie {
		return cancelAwareAtomReply{}
	}
	b := &X11Backend{
		conn:                        conn,
		root:                        1,
		atomNetClientList:           10,
		atomNetActiveWindow:         11,
		atomNetFrameExtent:          12,
		atomNetWMName:               13,
		atomNetWMState:              20,
		atomNetWMStateHidden:        21,
		atomNetWMStateMaximizedVert: 22,
		atomNetWMStateMaximizedHorz: 23,
		atomNetWMPID:                14,
		atomMotifWMHints:            15,
		atomUTF8String:              16,
	}
	conn.DefaultScreenFunc = func() *xproto.ScreenInfo {
		return &xproto.ScreenInfo{Root: b.root, WidthInPixels: 1920, HeightInPixels: 1080}
	}
	// Default: return an empty client list (Format 32, zero bytes) so that
	// IterateWindows/findByTitle return "not found" rather than a format error.
	conn.GetPropertyFunc = func(d bool, w xproto.Window, p, tp xproto.Atom, lo, ll uint32) x11.GetPropertyCookie {
		return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: []byte{}})
	}
	if withWindow {
		wid := xproto.Window(50)
		titleBytes := []byte(title)
		conn.GetPropertyFunc = func(d bool, w xproto.Window, p, tp xproto.Atom, lo, ll uint32) x11.GetPropertyCookie {
			switch p {
			case b.atomNetClientList:
				return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: []byte{byte(wid), byte(wid >> 8), byte(wid >> 16), byte(wid >> 24)}})
			case b.atomNetWMName:
				return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 8, Value: titleBytes})
			case b.atomNetWMPID:
				return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: []byte{1, 0, 0, 0}})
			case b.atomNetActiveWindow:
				return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: []byte{}})
			case b.atomNetFrameExtent, b.atomMotifWMHints:
				return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: []byte{}})
			}
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: []byte{}})
		}
		conn.GetGeometryFunc = func(d xproto.Drawable) x11.GetGeometryCookie {
			return x11.NewMockGetGeometryCookie(&xproto.GetGeometryReply{X: 10, Y: 20, Width: 800, Height: 600})
		}
		conn.TranslateCoordinatesFunc = func(s, d xproto.Window, sx, sy int16) x11.TranslateCoordinatesCookie {
			return x11.NewMockTranslateCoordinatesCookie(&xproto.TranslateCoordinatesReply{DstX: 10, DstY: 20})
		}
	}
	return b, conn
}

func TestX11Backend_New_NoDisplay(t *testing.T) {
	orig := os.Getenv("DISPLAY")
	os.Unsetenv("DISPLAY")
	defer os.Setenv("DISPLAY", orig)
	_, err := NewX11Backend("")
	if err == nil || !strings.Contains(err.Error(), "window/x11: connect to display") {
		t.Fatalf("NewX11Backend() error = %v, want connection error", err)
	}
}

func TestX11Backend_List(t *testing.T) {
	b, _ := newStubX11Backend(t, true, "Test Window")
	wins, err := b.List(context.Background())
	if err != nil {
		t.Fatalf("List() unexpected error: %v", err)
	}
	if len(wins) != 1 || wins[0].Title != "Test Window" {
		t.Errorf("List() = %+v, want 1 window titled %q", wins, "Test Window")
	}
}

func TestX11Backend_List_Empty(t *testing.T) {
	b, _ := newStubX11Backend(t, false, "")
	wins, err := b.List(context.Background())
	if err != nil {
		t.Fatalf("List() unexpected error: %v", err)
	}
	if len(wins) != 0 {
		t.Errorf("List() expected 0 windows, got %d", len(wins))
	}
}

func TestX11Backend_List_GetPropertyError(t *testing.T) {
	b, conn := newStubX11Backend(t, false, "")
	conn.GetPropertyFunc = func(d bool, w xproto.Window, p, tp xproto.Atom, lo, ll uint32) x11.GetPropertyCookie {
		return x11.NewMockGetPropertyCookieError(fmt.Errorf("simulated error"))
	}
	_, err := b.List(context.Background())
	if err == nil || !strings.Contains(err.Error(), "get _NET_CLIENT_LIST") {
		t.Errorf("List() error = %v, want error containing %q", err, "get _NET_CLIENT_LIST")
	}
}

func TestX11Backend_List_UnexpectedFormat(t *testing.T) {
	b, conn := newStubX11Backend(t, false, "")
	conn.GetPropertyFunc = func(d bool, w xproto.Window, p, tp xproto.Atom, lo, ll uint32) x11.GetPropertyCookie {
		return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 8, Value: []byte{1, 2, 3}})
	}
	_, err := b.List(context.Background())
	if err == nil || !strings.Contains(err.Error(), "unexpected _NET_CLIENT_LIST format") {
		t.Errorf("List() error = %v, want error containing format", err)
	}
}

func TestX11Backend_List_MalformedPayloadLength(t *testing.T) {
	b, conn := newStubX11Backend(t, false, "")
	conn.GetPropertyFunc = func(d bool, w xproto.Window, p, tp xproto.Atom, lo, ll uint32) x11.GetPropertyCookie {
		return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: []byte{1, 2, 3}})
	}
	_, err := b.List(context.Background())
	if err == nil || !strings.Contains(err.Error(), "malformed _NET_CLIENT_LIST payload length") {
		t.Errorf("List() error = %v, want malformed payload error", err)
	}
}

// runX11ActionTest is a table-helper for ID-based actions that send an event.
func runX11ActionTest(t *testing.T, name string, action func(*X11Backend, context.Context, string) error) {
	t.Helper()
	t.Run("valid ID", func(t *testing.T) {
		var events []string
		b, conn := newStubX11Backend(t, true, name)
		conn.SendEventCheckedFunc = func(p bool, dest xproto.Window, mask uint32, ev string) x11.SendEventCookie {
			events = append(events, ev)
			return &x11.MockCheckCookie{}
		}
		if err := action(b, context.Background(), "50"); err != nil {
			t.Errorf("%s() unexpected error: %v", name, err)
		}
		if len(events) == 0 {
			t.Errorf("%s() did not send event", name)
		}
	})
	t.Run("invalid ID", func(t *testing.T) {
		b, _ := newStubX11Backend(t, false, "")
		err := action(b, context.Background(), "not-a-number")
		if err == nil {
			t.Errorf("%s() error = nil, want invalid ID error", name)
		}
	})
}

func TestX11Backend_ActivateByID(t *testing.T) {
	runX11ActionTest(t, "ActivateByID", func(b *X11Backend, ctx context.Context, id string) error {
		return b.ActivateByID(ctx, id)
	})
}

func TestX11Backend_CloseWindowByID(t *testing.T) {
	runX11ActionTest(t, "CloseWindowByID", func(b *X11Backend, ctx context.Context, id string) error {
		return b.CloseWindowByID(ctx, id)
	})
}

func TestX11Backend_MinimizeByID(t *testing.T) {
	runX11ActionTest(t, "MinimizeByID", func(b *X11Backend, ctx context.Context, id string) error {
		return b.MinimizeByID(ctx, id)
	})
}

func TestX11Backend_MaximizeByID(t *testing.T) {
	runX11ActionTest(t, "MaximizeByID", func(b *X11Backend, ctx context.Context, id string) error {
		return b.MaximizeByID(ctx, id)
	})
}

func TestX11Backend_RestoreByID(t *testing.T) {
	t.Run("valid ID", func(t *testing.T) {
		var events []string
		var mapped []xproto.Window
		b, conn := newStubX11Backend(t, true, "RestoreByID")
		conn.SendEventCheckedFunc = func(p bool, dest xproto.Window, mask uint32, ev string) x11.SendEventCookie {
			events = append(events, ev)
			return &x11.MockCheckCookie{}
		}
		conn.MapWindowCheckedFunc = func(w xproto.Window) x11.MapWindowCookie {
			mapped = append(mapped, w)
			return &x11.MockCheckCookie{}
		}
		if err := b.RestoreByID(context.Background(), "50"); err != nil {
			t.Errorf("RestoreByID() unexpected error: %v", err)
		}
		if len(events) == 0 {
			t.Errorf("RestoreByID() did not send event")
		}
		if len(mapped) == 0 {
			t.Errorf("RestoreByID() did not map window")
		}
	})
	t.Run("invalid ID", func(t *testing.T) {
		b, _ := newStubX11Backend(t, false, "")
		if err := b.RestoreByID(context.Background(), "not-a-number"); err == nil {
			t.Error("RestoreByID() error = nil, want invalid ID error")
		}
	})
}

func TestX11Backend_MoveByID(t *testing.T) { //nolint:dupl
	t.Run("valid ID", func(t *testing.T) {
		var cfg []struct {
			mask   uint16
			values []uint32
		}
		b, conn := newStubX11Backend(t, true, "MoveMe")
		conn.ConfigureWindowCheckedFunc = func(w xproto.Window, mask uint16, vals []uint32) x11.ConfigureWindowCookie {
			cfg = append(cfg, struct {
				mask   uint16
				values []uint32
			}{mask, vals})
			return &x11.MockCheckCookie{}
		}
		if err := b.MoveByID(context.Background(), "50", 100, 200); err != nil {
			t.Errorf("MoveByID() unexpected error: %v", err)
		}
		if len(cfg) == 0 {
			t.Errorf("MoveByID() did not configure window")
		}
	})
	t.Run("invalid ID", func(t *testing.T) {
		b, _ := newStubX11Backend(t, false, "")
		if err := b.MoveByID(context.Background(), "not-a-number", 100, 200); err == nil {
			t.Error("MoveByID() error = nil, want invalid ID error")
		}
	})
}

func TestX11Backend_ResizeByID(t *testing.T) { //nolint:dupl
	t.Run("valid ID", func(t *testing.T) {
		var cfg []struct {
			mask   uint16
			values []uint32
		}
		b, conn := newStubX11Backend(t, true, "ResizeMe")
		conn.ConfigureWindowCheckedFunc = func(w xproto.Window, mask uint16, vals []uint32) x11.ConfigureWindowCookie {
			cfg = append(cfg, struct {
				mask   uint16
				values []uint32
			}{mask, vals})
			return &x11.MockCheckCookie{}
		}
		if err := b.ResizeByID(context.Background(), "50", 1024, 768); err != nil {
			t.Errorf("ResizeByID() unexpected error: %v", err)
		}
		if len(cfg) == 0 {
			t.Errorf("ResizeByID() did not configure window")
		}
	})
	t.Run("invalid ID", func(t *testing.T) {
		b, _ := newStubX11Backend(t, false, "")
		if err := b.ResizeByID(context.Background(), "not-a-number", 1024, 768); err == nil {
			t.Error("ResizeByID() error = nil, want invalid ID error")
		}
	})
}

func TestX11Backend_ActiveTitle(t *testing.T) {
	b, conn := newStubX11Backend(t, true, "Active Window")
	conn.GetPropertyFunc = func(d bool, w xproto.Window, p, tp xproto.Atom, lo, ll uint32) x11.GetPropertyCookie {
		if p == b.atomNetActiveWindow {
			wid := xproto.Window(50)
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: []byte{byte(wid), byte(wid >> 8), byte(wid >> 16), byte(wid >> 24)}})
		}
		if p == b.atomNetWMName {
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 8, Value: []byte("Active Window")})
		}
		return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: []byte{}})
	}
	title, err := b.ActiveTitle(context.Background())
	if err != nil {
		t.Fatalf("ActiveTitle() unexpected error: %v", err)
	}
	if title != "Active Window" {
		t.Errorf("ActiveTitle() = %q, want %q", title, "Active Window")
	}
}

func TestX11Backend_ActiveTitle_NoActive(t *testing.T) {
	b, _ := newStubX11Backend(t, false, "")
	title, err := b.ActiveTitle(context.Background())
	if err != nil {
		t.Fatalf("ActiveTitle() unexpected error: %v", err)
	}
	if title != "" {
		t.Errorf("ActiveTitle() = %q, want empty", title)
	}
}

func TestX11Backend_windowTitle(t *testing.T) {
	b, conn := newStubX11Backend(t, false, "")
	conn.GetPropertyFunc = func(d bool, w xproto.Window, p, tp xproto.Atom, lo, ll uint32) x11.GetPropertyCookie {
		if p == b.atomNetWMName {
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 8, Value: []byte("UTF8 Title")})
		}
		return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: []byte{}})
	}
	if title := b.windowTitle(50); title != "UTF8 Title" {
		t.Errorf("windowTitle() = %q, want %q", title, "UTF8 Title")
	}
}

func TestX11Backend_windowPID(t *testing.T) {
	b, conn := newStubX11Backend(t, false, "")
	conn.GetPropertyFunc = func(d bool, w xproto.Window, p, tp xproto.Atom, lo, ll uint32) x11.GetPropertyCookie {
		if p == b.atomNetWMPID {
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: []byte{42, 0, 0, 0}})
		}
		return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: []byte{}})
	}
	if pid := b.windowPID(50); pid != 42 {
		t.Errorf("windowPID() = %d, want 42", pid)
	}
}

func TestX11Backend_windowPID_NoPID(t *testing.T) {
	b, _ := newStubX11Backend(t, false, "")
	if pid := b.windowPID(50); pid != 0 {
		t.Errorf("windowPID() = %d, want 0", pid)
	}
}

func TestX11Backend_windowClass(t *testing.T) {
	b, conn := newStubX11Backend(t, false, "")
	conn.GetPropertyFunc = func(d bool, w xproto.Window, p, tp xproto.Atom, lo, ll uint32) x11.GetPropertyCookie {
		if p == xproto.AtomWmClass {
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 8, Value: []byte("org.example\x00Example\x00")})
		}
		return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: []byte{}})
	}
	appID, class := b.windowClass(50)
	if appID != "org.example" || class != "Example" {
		t.Fatalf("windowClass() = (%q, %q), want (%q, %q)", appID, class, "org.example", "Example")
	}
}

func TestX11Backend_windowHasDecoration(t *testing.T) {
	b, conn := newStubX11Backend(t, false, "")
	conn.GetPropertyFunc = func(d bool, w xproto.Window, p, tp xproto.Atom, lo, ll uint32) x11.GetPropertyCookie {
		if p == b.atomMotifWMHints {
			data := make([]byte, 20)
			data[0] = 2 // MwmHintsDecorations flag
			data[8] = 1 // decorations = 1
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: data})
		}
		return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: []byte{}})
	}
	if !b.windowHasDecoration(50) {
		t.Errorf("windowHasDecoration() = false, want true")
	}
}

func TestX11Backend_windowHasDecoration_NoDecoration(t *testing.T) {
	b, conn := newStubX11Backend(t, false, "")
	conn.GetPropertyFunc = func(d bool, w xproto.Window, p, tp xproto.Atom, lo, ll uint32) x11.GetPropertyCookie {
		if p == b.atomMotifWMHints {
			data := make([]byte, 20)
			data[0] = 2 // MwmHintsDecorations flag
			data[8] = 0 // decorations = 0
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: data})
		}
		return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: []byte{}})
	}
	if b.windowHasDecoration(50) {
		t.Errorf("windowHasDecoration() = true, want false")
	}
}

func TestX11Backend_windowGeometry(t *testing.T) {
	b, conn := newStubX11Backend(t, false, "")
	conn.GetGeometryFunc = func(d xproto.Drawable) x11.GetGeometryCookie {
		return x11.NewMockGetGeometryCookie(&xproto.GetGeometryReply{X: 10, Y: 20, Width: 800, Height: 600})
	}
	conn.TranslateCoordinatesFunc = func(s, d xproto.Window, x, y int16) x11.TranslateCoordinatesCookie {
		return x11.NewMockTranslateCoordinatesCookie(&xproto.TranslateCoordinatesReply{DstX: 10, DstY: 20})
	}
	x, y, w, h := b.windowGeometry(50)
	if x != 10 || y != 20 || w != 800 || h != 600 {
		t.Errorf("windowGeometry() = (%d, %d, %d, %d), want (10, 20, 800, 600)", x, y, w, h)
	}
}

func TestX11Backend_Close(t *testing.T) {
	b, _ := newStubX11Backend(t, false, "")
	if err := b.Close(); err != nil {
		t.Errorf("Close() unexpected error: %v", err)
	}
}

type trackingX11Connection struct {
	*x11.MockConnection
	closeOnce func()
	closed    chan struct{}
}

func (c *trackingX11Connection) Close() {
	c.closeOnce()
}

type blockedGetPropertyCookie struct {
	started chan struct{}
	closed  <-chan struct{}
}

func (c *blockedGetPropertyCookie) Reply() (*xproto.GetPropertyReply, error) {
	close(c.started)
	<-c.closed
	return nil, net.ErrClosed
}

func TestX11BackendCloseUnblocksBlockedReply(t *testing.T) {
	mock := &x11.MockConnection{}
	conn := &trackingX11Connection{
		MockConnection: mock,
		closed:         make(chan struct{}),
	}
	conn.closeOnce = sync.OnceFunc(func() { close(conn.closed) })
	defer conn.Close()
	b := &X11Backend{conn: conn, root: 1, atomNetActiveWindow: 2}
	replyStarted := make(chan struct{})
	mock.GetPropertyFunc = func(bool, xproto.Window, xproto.Atom, xproto.Atom, uint32, uint32) x11.GetPropertyCookie {
		return &blockedGetPropertyCookie{started: replyStarted, closed: conn.closed}
	}

	operationDone := make(chan error, 1)
	go func() {
		_, err := b.ActiveTitle(context.Background())
		operationDone <- err
	}()
	<-replyStarted

	closeDone := make(chan error, 1)
	go func() { closeDone <- b.Close() }()
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Close did not unblock the blocked Reply")
	}

	select {
	case err := <-operationDone:
		if !errors.Is(err, net.ErrClosed) {
			t.Fatalf("ActiveTitle error = %v, want net.ErrClosed", err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked ActiveTitle did not exit after Close")
	}
}

func TestX11BackendRejectsOperationsAfterClose(t *testing.T) {
	b, _ := newStubX11Backend(t, false, "")
	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	tests := []struct {
		name string
		run  func() error
	}{
		{name: "List", run: func() error {
			_, err := b.List(context.Background())
			return err
		}},
		{name: "IterateWindows", run: func() error {
			for _, err := range b.IterateWindows(context.Background()) {
				return err
			}
			return nil
		}},
		{name: "ActiveTitle", run: func() error {
			_, err := b.ActiveTitle(context.Background())
			return err
		}},
		{name: "Sync", run: func() error { return b.Sync(context.Background()) }},
		{name: "ActivateByID", run: func() error { return b.ActivateByID(context.Background(), "50") }},
		{name: "MoveByID", run: func() error { return b.MoveByID(context.Background(), "50", 1, 2) }},
		{name: "ResizeByID", run: func() error { return b.ResizeByID(context.Background(), "50", 1, 2) }},
		{name: "CloseWindowByID", run: func() error {
			return b.CloseWindowByID(context.Background(), "50")
		}},
		{name: "MinimizeByID", run: func() error { return b.MinimizeByID(context.Background(), "50") }},
		{name: "MaximizeByID", run: func() error { return b.MaximizeByID(context.Background(), "50") }},
		{name: "FullscreenByID", run: func() error {
			return b.FullscreenByID(context.Background(), "50")
		}},
		{name: "UnfullscreenByID", run: func() error {
			return b.UnfullscreenByID(context.Background(), "50")
		}},
		{name: "RestoreByID", run: func() error { return b.RestoreByID(context.Background(), "50") }},
		{name: "InfoByID", run: func() error {
			_, err := b.InfoByID(context.Background(), "50")
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.run(); !errors.Is(err, net.ErrClosed) {
				t.Fatalf("error = %v, want net.ErrClosed", err)
			}
		})
	}
}

func TestX11Backend_activeWindow(t *testing.T) {
	b, conn := newStubX11Backend(t, false, "")
	// Test with active window
	conn.GetPropertyFunc = func(d bool, w xproto.Window, p, t xproto.Atom, lo, ll uint32) x11.GetPropertyCookie {
		if p == b.atomNetActiveWindow {
			wid := xproto.Window(50)
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: []byte{byte(wid), byte(wid >> 8), byte(wid >> 16), byte(wid >> 24)}})
		}
		return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: []byte{}})
	}
	win, err := b.activeWindow()
	if err != nil {
		t.Fatalf("activeWindow() unexpected error: %v", err)
	}
	if win != xproto.Window(50) {
		t.Errorf("activeWindow() = %d, want 50", win)
	}
}

func TestX11Backend_activeWindow_NoActive(t *testing.T) {
	b, conn := newStubX11Backend(t, false, "")
	conn.GetPropertyFunc = func(d bool, w xproto.Window, p, t xproto.Atom, lo, ll uint32) x11.GetPropertyCookie {
		return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: []byte{}})
	}
	win, err := b.activeWindow()
	if err != nil {
		t.Fatalf("activeWindow() unexpected error: %v", err)
	}
	if win != 0 {
		t.Errorf("activeWindow() = %d, want 0", win)
	}
}

func TestX11Backend_activeWindow_Error(t *testing.T) {
	b, conn := newStubX11Backend(t, false, "")
	conn.GetPropertyFunc = func(d bool, w xproto.Window, p, t xproto.Atom, lo, ll uint32) x11.GetPropertyCookie {
		return x11.NewMockGetPropertyCookieError(fmt.Errorf("connection error"))
	}
	_, err := b.activeWindow()
	if err == nil {
		t.Fatal("activeWindow() expected error, got nil")
	}
}

func TestX11Backend_windowState_Minimized(t *testing.T) {
	b, conn := newStubX11Backend(t, false, "")
	conn.GetPropertyFunc = func(d bool, w xproto.Window, p, t xproto.Atom, lo, ll uint32) x11.GetPropertyCookie {
		if p == b.atomNetWMState {
			// Return _NET_WM_STATE_HIDDEN in the state
			atomBytes := []byte{
				byte(b.atomNetWMStateHidden), byte(b.atomNetWMStateHidden >> 8),
				byte(b.atomNetWMStateHidden >> 16), byte(b.atomNetWMStateHidden >> 24),
			}
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: atomBytes})
		}
		return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: []byte{}})
	}
	minimized, maximized, _ := b.windowState(50)
	if !minimized {
		t.Errorf("windowState() minimized = false, want true")
	}
	if maximized {
		t.Errorf("windowState() maximized = true, want false")
	}
}

func TestX11Backend_windowState_Maximized(t *testing.T) {
	b, conn := newStubX11Backend(t, false, "")
	conn.GetPropertyFunc = func(d bool, w xproto.Window, p, t xproto.Atom, lo, ll uint32) x11.GetPropertyCookie {
		if p == b.atomNetWMState {
			// Return both maximized vert and horz (8 bytes = 2 atoms * 4 bytes each)
			atomBytes := make([]byte, 8)
			// _NET_WM_STATE_MAXIMIZED_VERT
			atomBytes[0] = byte(b.atomNetWMStateMaximizedVert)
			atomBytes[1] = byte(b.atomNetWMStateMaximizedVert >> 8)
			atomBytes[2] = byte(b.atomNetWMStateMaximizedVert >> 16)
			atomBytes[3] = byte(b.atomNetWMStateMaximizedVert >> 24)
			// _NET_WM_STATE_MAXIMIZED_HORZ
			atomBytes[4] = byte(b.atomNetWMStateMaximizedHorz)
			atomBytes[5] = byte(b.atomNetWMStateMaximizedHorz >> 8)
			atomBytes[6] = byte(b.atomNetWMStateMaximizedHorz >> 16)
			atomBytes[7] = byte(b.atomNetWMStateMaximizedHorz >> 24)
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: atomBytes})
		}
		return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: []byte{}})
	}
	minimized, maximized, _ := b.windowState(50)
	if minimized {
		t.Errorf("windowState() minimized = true, want false")
	}
	if !maximized {
		t.Errorf("windowState() maximized = false, want true")
	}
}

func TestX11Backend_windowState_OneMaximizedAxisIsNotFullyMaximized(t *testing.T) {
	b, conn := newStubX11Backend(t, false, "")
	conn.GetPropertyFunc = func(d bool, w xproto.Window, p, t xproto.Atom, lo, ll uint32) x11.GetPropertyCookie {
		if p == b.atomNetWMState {
			atomBytes := make([]byte, 4)
			atomBytes[0] = byte(b.atomNetWMStateMaximizedVert)
			atomBytes[1] = byte(b.atomNetWMStateMaximizedVert >> 8)
			atomBytes[2] = byte(b.atomNetWMStateMaximizedVert >> 16)
			atomBytes[3] = byte(b.atomNetWMStateMaximizedVert >> 24)
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: atomBytes})
		}
		return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: []byte{}})
	}
	_, maximized, _ := b.windowState(50)
	if maximized {
		t.Fatal("windowState() reported a one-axis maximize as fully maximized")
	}
}

func TestX11Backend_windowState_Fullscreen(t *testing.T) {
	b, conn := newStubX11Backend(t, false, "")
	conn.GetPropertyFunc = func(d bool, w xproto.Window, p, t xproto.Atom, lo, ll uint32) x11.GetPropertyCookie {
		if p == b.atomNetWMState {
			atomBytes := make([]byte, 4)
			atomBytes[0] = byte(b.atomNetWMStateFullscreen)
			atomBytes[1] = byte(b.atomNetWMStateFullscreen >> 8)
			atomBytes[2] = byte(b.atomNetWMStateFullscreen >> 16)
			atomBytes[3] = byte(b.atomNetWMStateFullscreen >> 24)
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: atomBytes})
		}
		return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: []byte{}})
	}
	minimized, maximized, fullscreen := b.windowState(50)
	if minimized {
		t.Errorf("windowState() minimized = true, want false")
	}
	if maximized {
		t.Errorf("windowState() maximized = true, want false")
	}
	if !fullscreen {
		t.Errorf("windowState() fullscreen = false, want true")
	}
}

// encodeWMStateAtoms encodes three atom uint32 values into a 12-byte little-endian slice
// suitable for a _NET_WM_STATE property reply.
func encodeWMStateAtoms(a, b, c xproto.Atom) []byte {
	atomBytes := make([]byte, 12)
	atomBytes[0] = byte(a)
	atomBytes[1] = byte(a >> 8)
	atomBytes[2] = byte(a >> 16)
	atomBytes[3] = byte(a >> 24)
	atomBytes[4] = byte(b)
	atomBytes[5] = byte(b >> 8)
	atomBytes[6] = byte(b >> 16)
	atomBytes[7] = byte(b >> 24)
	atomBytes[8] = byte(c)
	atomBytes[9] = byte(c >> 8)
	atomBytes[10] = byte(c >> 16)
	atomBytes[11] = byte(c >> 24)
	return atomBytes
}

func TestX11Backend_windowState_Both(t *testing.T) {
	b, conn := newStubX11Backend(t, false, "")
	conn.GetPropertyFunc = func(d bool, w xproto.Window, p, t xproto.Atom, lo, ll uint32) x11.GetPropertyCookie {
		if p == b.atomNetWMState {
			atomBytes := encodeWMStateAtoms(b.atomNetWMStateHidden, b.atomNetWMStateMaximizedVert, b.atomNetWMStateMaximizedHorz)
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: atomBytes})
		}
		return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: []byte{}})
	}
	minimized, maximized, _ := b.windowState(50)
	if !minimized {
		t.Errorf("windowState() minimized = false, want true")
	}
	if !maximized {
		t.Errorf("windowState() maximized = false, want true")
	}
}

func TestX11Backend_windowState_Error(t *testing.T) {
	b, conn := newStubX11Backend(t, false, "")
	conn.GetPropertyFunc = func(d bool, w xproto.Window, p, t xproto.Atom, lo, ll uint32) x11.GetPropertyCookie {
		if p == b.atomNetWMState {
			return x11.NewMockGetPropertyCookieError(fmt.Errorf("connection error"))
		}
		return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: []byte{}})
	}
	minimized, maximized, _ := b.windowState(50)
	if minimized || maximized {
		t.Errorf("windowState() with error should return false, false, got %v, %v", minimized, maximized)
	}
}

func TestX11Backend_IterateWindows_MinimizedMaximized(t *testing.T) {
	b, conn := newStubX11Backend(t, true, "Test Window")
	// Override getPropertyFn to also handle atomNetWMState
	origFn := conn.GetPropertyFunc
	conn.GetPropertyFunc = func(d bool, w xproto.Window, p, t xproto.Atom, lo, ll uint32) x11.GetPropertyCookie {
		if p == b.atomNetWMState {
			atomBytes := encodeWMStateAtoms(b.atomNetWMStateHidden, b.atomNetWMStateMaximizedVert, b.atomNetWMStateMaximizedHorz)
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: atomBytes})
		}
		// Call original function for other properties
		return origFn(d, w, p, t, lo, ll)
	}
	ctx := context.Background()
	var found Info
	for info, err := range b.IterateWindows(ctx) {
		if err != nil {
			t.Fatalf("IterateWindows() error: %v", err)
		}
		found = info
	}
	if !found.Minimized {
		t.Errorf("IterateWindows() Minimized = false, want true")
	}
	if !found.Maximized {
		t.Errorf("IterateWindows() Maximized = false, want true")
	}
}

// A malformed application-owned _NET_WM_NAME must not become public metadata:
// invalid UTF-8 and embedded NUL can confuse consumers matching window titles.
// Legacy WM_NAME is ISO-8859-1, not UTF-8, so non-ASCII bytes must decode.
func TestX11WindowTitleRejectsMalformedUTF8AndDecodesLatin1Fallback(t *testing.T) {
	for _, tc := range []struct {
		name           string
		modern, legacy []byte
		want           string
	}{
		{"invalid utf8", []byte{0xff, 0xfe}, []byte("Fallback"), "Fallback"},
		{"embedded nul", []byte("Trusted\x00Forged"), []byte("Fallback"), "Fallback"},
		{"latin1 fallback", nil, []byte{'C', 'a', 'f', 0xe9}, "Café"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, conn := newStubX11Backend(t, false, "")
			conn.GetPropertyFunc = func(_ bool, _ xproto.Window, property, kind xproto.Atom, _, _ uint32) x11.GetPropertyCookie {
				switch property {
				case b.atomNetWMName:
					if kind != b.atomUTF8String {
						t.Fatalf("wrong modern title type %d", kind)
					}
					return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 8, Value: tc.modern})
				case xproto.AtomWmName:
					if kind != xproto.AtomString {
						t.Fatalf("wrong legacy title type %d", kind)
					}
					return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 8, Value: tc.legacy})
				default:
					t.Fatalf("unexpected property %d", property)
					return nil
				}
			}
			got := b.windowTitle(50)
			if got != tc.want || !utf8.ValidString(got) {
				t.Fatalf("title=%q (valid UTF8=%v) want %q", got, utf8.ValidString(got), tc.want)
			}
		})
	}
}

// BytesAfter marks an incomplete GetProperty result, even if the first chunk
// has the expected shape. Never expose a partial active ID or process ID.
func TestX11ScalarMetadataRejectsTruncatedReplies(t *testing.T) {
	for _, tc := range []struct {
		name   string
		active bool
	}{
		{"active window", true}, {"process ID", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, conn := newStubX11Backend(t, false, "")
			conn.GetPropertyFunc = func(_ bool, _ xproto.Window, p, _ xproto.Atom, _, _ uint32) x11.GetPropertyCookie {
				want := b.atomNetWMPID
				if tc.active {
					want = b.atomNetActiveWindow
				}
				if p != want {
					t.Fatalf("unexpected property %d", p)
				}
				v := make([]byte, 4)
				binary.LittleEndian.PutUint32(v, 50)
				return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: v, BytesAfter: 4})
			}
			if tc.active {
				got, err := b.activeWindow()
				if err != nil || got != 0 {
					t.Fatalf("truncated active window got=%d err=%v", got, err)
				}
			} else if got := b.windowPID(50); got != 0 {
				t.Fatalf("truncated PID got=%d", got)
			}
		})
	}
}

// Incomplete hints must not suppress decorations or enlarge geometry.
func TestX11DecorationsRejectTruncatedReplies(t *testing.T) {
	t.Run("motif hints", func(t *testing.T) {
		b, conn := newStubX11Backend(t, false, "")
		conn.GetPropertyFunc = func(_ bool, _ xproto.Window, p, _ xproto.Atom, _, _ uint32) x11.GetPropertyCookie {
			if p != b.atomMotifWMHints {
				t.Fatalf("unexpected property %d", p)
			}
			v := make([]byte, 20)
			binary.LittleEndian.PutUint32(v, 2)
			return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: v, BytesAfter: 4})
		}
		if !b.windowHasDecoration(50) {
			t.Fatal("truncated Motif hints suppressed window decorations")
		}
	})
	t.Run("frame extents", func(t *testing.T) {
		b, conn := newStubX11Backend(t, true, "Window")
		prev := conn.GetPropertyFunc
		conn.GetPropertyFunc = func(del bool, w xproto.Window, p, typ xproto.Atom, offset, length uint32) x11.GetPropertyCookie {
			if p == b.atomNetFrameExtent {
				v := make([]byte, 16)
				for i, e := range []uint32{14, 9, 30, 19} {
					binary.LittleEndian.PutUint32(v[i*4:], e)
				}
				return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: v, BytesAfter: 4})
			}
			return prev(del, w, p, typ, offset, length)
		}
		x, y, width, height := b.windowGeometry(50)
		if x != 10 || y != 20 || width != 800 || height != 600 {
			t.Fatalf("truncated frame extents changed geometry (%d,%d,%d,%d)", x, y, width, height)
		}
	})
}

// A GetProperty response with BytesAfter set is only a prefix. Do not expose
// a supposedly complete EWMH state or WM_CLASS from such a reply.
func TestX11WindowStateRejectsTruncatedAtomList(t *testing.T) {
	b, conn := newStubX11Backend(t, false, "")
	conn.GetPropertyFunc = func(_ bool, _ xproto.Window, p, _ xproto.Atom, _, _ uint32) x11.GetPropertyCookie {
		if p != b.atomNetWMState {
			t.Fatalf("unexpected property %d", p)
		}
		v := make([]byte, 4)
		binary.LittleEndian.PutUint32(v, uint32(b.atomNetWMStateFullscreen))
		return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 32, Value: v, BytesAfter: 4})
	}
	minimized, maximized, fullscreen := b.windowState(50)
	if minimized || maximized || fullscreen {
		t.Fatalf("truncated atom list exposed state: %v %v %v", minimized, maximized, fullscreen)
	}
}

func TestX11WindowClassRejectsTruncatedProperty(t *testing.T) {
	b, conn := newStubX11Backend(t, false, "")
	conn.GetPropertyFunc = func(_ bool, _ xproto.Window, p, typ xproto.Atom, _, _ uint32) x11.GetPropertyCookie {
		if p != xproto.AtomWmClass || typ != xproto.AtomString {
			t.Fatalf("unexpected property/type %d/%d", p, typ)
		}
		return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 8, Value: []byte("trusted\x00TrustedApp\x00"), BytesAfter: 4})
	}
	instance, class := b.windowClass(50)
	if instance != "" || class != "" {
		t.Fatalf("truncated WM_CLASS exposed %q/%q", instance, class)
	}
}

// BytesAfter means GetProperty returned only a prefix. A partial title must
// never be returned as if it were the complete application-provided metadata.
func TestX11WindowTitleRejectsTruncatedProperties(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		modernAfter, legacyAfter uint32
		want                     string
	}{
		{"truncated modern uses complete legacy", 4, 0, "Complete legacy"},
		{"truncated modern and legacy are not published", 4, 4, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, conn := newStubX11Backend(t, false, "")
			conn.GetPropertyFunc = func(_ bool, _ xproto.Window, p, _ xproto.Atom, _, _ uint32) x11.GetPropertyCookie {
				switch p {
				case b.atomNetWMName:
					return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 8, Value: []byte("Prefix"), BytesAfter: tc.modernAfter})
				case xproto.AtomWmName:
					return x11.NewMockGetPropertyCookie(&xproto.GetPropertyReply{Format: 8, Value: []byte("Complete legacy"), BytesAfter: tc.legacyAfter})
				default:
					t.Fatalf("unexpected title property %d", p)
					return nil
				}
			}
			if got := b.windowTitle(50); got != tc.want {
				t.Fatalf("truncated title got %q want %q", got, tc.want)
			}
		})
	}
}

type zeroX11AtomCookie struct{}

func (zeroX11AtomCookie) Reply() (*xproto.InternAtomReply, error) {
	return &xproto.InternAtomReply{Atom: xproto.AtomNone}, nil
}

// A successful InternAtom(onlyIfExists=false) must return a nonzero atom ID.
// AtomNone cannot be used in client-message types or protocol payloads.
func TestX11WindowMutationsRejectZeroAtomReplies(t *testing.T) {
	for _, tc := range []struct {
		name, failAt string
		minimize     bool
	}{
		{"close delete atom none", "WM_DELETE_WINDOW", false},
		{"close protocols atom none", "WM_PROTOCOLS", false},
		{"minimize change state atom none", "WM_CHANGE_STATE", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, conn := newStubX11Backend(t, false, "")
			events := 0
			conn.InternAtomFunc = func(_ bool, _ uint16, name string) x11.InternAtomCookie {
				if name == tc.failAt {
					return zeroX11AtomCookie{}
				}
				return cancelAwareAtomReply{}
			}
			conn.SendEventCheckedFunc = func(_ bool, _ xproto.Window, _ uint32, _ string) x11.SendEventCookie {
				events++
				return &x11.MockCheckCookie{}
			}
			var err error
			if tc.minimize {
				err = b.MinimizeByID(context.Background(), "50")
			} else {
				err = b.CloseWindowByID(context.Background(), "50")
			}
			if err == nil || !strings.Contains(err.Error(), tc.failAt) || events != 0 {
				t.Fatalf("zero atom reply mishandled: err=%v events=%d", err, events)
			}
		})
	}
}

// X11 WindowNone (0) is not an addressable client. Reject it at the shared
// handle parser so no mutation can target the invalid protocol identifier.
func TestX11WindowHandleRejectsNone(t *testing.T) {
	if got, err := x11WindowID("0"); err == nil || got != 0 {
		t.Fatalf("invalid X11 window handle accepted: id=%d err=%v", got, err)
	}
	b, _ := newStubX11Backend(t, false, "")
	for _, tc := range []struct {
		name   string
		mutate func() error
	}{
		{"activate", func() error { return b.ActivateByID(context.Background(), "0") }},
		{"move", func() error { return b.MoveByID(context.Background(), "0", 1, 1) }},
		{"resize", func() error { return b.ResizeByID(context.Background(), "0", 100, 100) }},
		{"close", func() error { return b.CloseWindowByID(context.Background(), "0") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.mutate(); err == nil || !strings.Contains(err.Error(), "window") {
				t.Fatalf("invalid zero ID mutation returned %v", err)
			}
		})
	}
}
