//go:build linux

package x11

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jezek/xgb/xproto"
)

// The X11 adapter must be exercised against an actual X protocol peer, not
// only its MockConnection. Xvfb is a disposable, isolated, headless X server;
// no host DISPLAY, credentials, applications, or input devices are accessed.
func startAdapterXvfb(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("Xvfb"); err != nil {
		t.Skip("Xvfb is unavailable")
	}
	cmd := exec.Command("Xvfb", "-displayfd", "1", "-screen", "0", "640x480x24", "-nolisten", "tcp", "-ac")
	var errLog bytes.Buffer
	cmd.Stderr = &errLog
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	type out struct {
		line string
		err  error
	}
	ready := make(chan out, 1)
	go func() { line, err := bufio.NewReader(stdout).ReadString('\n'); ready <- out{line, err} }()
	var num string
	select {
	case result := <-ready:
		if result.err != nil {
			t.Fatalf("headless server failed: %v / %s", result.err, errLog.String())
		}
		num = strings.TrimSpace(result.line)
	case <-time.After(5 * time.Second):
		t.Fatalf("Xvfb failed to allocate private display: %s", errLog.String())
	}
	if _, err := strconv.Atoi(num); err != nil {
		t.Fatalf("Xvfb returned invalid display number %q", num)
	}
	return ":" + num
}

func TestXgbRealHeadlessDisplayReadAndMutationProtocol(t *testing.T) {
	// Never consult the user account's X11 authentication store.
	privateAuth := filepath.Join(t.TempDir(), "private-no-auth")
	if err := os.WriteFile(privateAuth, nil, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XAUTHORITY", privateAuth)
	display := startAdapterXvfb(t)
	var c *XgbConnection
	var err error
	for i := 0; i < 100; i++ {
		c, err = NewXgbConnection(display)
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("unable to connect to dedicated Xvfb display: %v", err)
	}
	defer c.Close()
	setup := c.Setup()
	screen := c.DefaultScreen()
	if setup == nil || screen == nil || screen.WidthInPixels != 640 || screen.HeightInPixels != 480 {
		t.Fatalf("invalid Xvfb root geometry %+v", screen)
	}
	c.Sync()
	if _, lintShadow88 := c.GetGeometry(xproto.Drawable(screen.Root)).Reply(); lintShadow88 != nil {
		t.Fatalf("root geometry: %v", lintShadow88)
	}
	if pointer, lintShadow91 := c.QueryPointer(screen.Root).Reply(); lintShadow91 != nil || pointer == nil {
		t.Fatalf("root pointer: %v %v", pointer, lintShadow91)
	}
	if translated, lintShadow94 := c.TranslateCoordinates(screen.Root, screen.Root, 15, 23).Reply(); lintShadow94 != nil || translated.DstX != 15 || translated.DstY != 23 {
		t.Fatalf("coordinate translation: %+v %v", translated, lintShadow94)
	}
	atom, err := c.InternAtom(false, uint16(len("PF_TEST_PROPERTY")), "PF_TEST_PROPERTY").Reply()
	if err != nil || atom.Atom == 0 {
		t.Fatalf("intern property atom: %+v %v", atom, err)
	}
	if _, lintShadow101 := c.GetProperty(false, screen.Root, atom.Atom, xproto.AtomAny, 0, 32).Reply(); lintShadow101 != nil {
		t.Fatalf("property read: %v", lintShadow101)
	}
	winID, err := c.NewID()
	if err != nil {
		t.Fatal(err)
	}
	if lintShadow108 := xproto.CreateWindowChecked(c.conn, screen.RootDepth, xproto.Window(winID), screen.Root, 10, 10, 100, 75, 0, xproto.WindowClassInputOutput, screen.RootVisual, 0, nil).Check(); lintShadow108 != nil {
		t.Fatalf("create private test window: %v", lintShadow108)
	}
	if lintShadow111 := c.MapWindowChecked(xproto.Window(winID)).Check(); lintShadow111 != nil {
		t.Fatalf("map test window: %v", lintShadow111)
	}
	if lintShadow114 := c.ConfigureWindowChecked(xproto.Window(winID), xproto.ConfigWindowX|xproto.ConfigWindowY|xproto.ConfigWindowWidth, []uint32{23, 34, 125}).Check(); lintShadow114 != nil {
		t.Fatalf("configure test window: %v", lintShadow114)
	}
	geom, err := c.GetGeometry(xproto.Drawable(winID)).Reply()
	if err != nil || geom.Width != 125 || geom.X != 23 || geom.Y != 34 {
		t.Fatalf("applied window geometry %+v %v", geom, err)
	}
	if img, lintShadow121 := c.GetImage(xproto.ImageFormatZPixmap, xproto.Drawable(screen.Root), 0, 0, 2, 2, 0xffffffff).Reply(); lintShadow121 != nil || img == nil {
		t.Fatalf("read back synthetic display framebuffer: %+v %v", img, lintShadow121)
	}
	pixID, err := c.NewID()
	if err != nil {
		t.Fatal(err)
	}
	if lintShadow128 := xproto.CreatePixmapChecked(c.conn, screen.RootDepth, xproto.Pixmap(pixID), xproto.Drawable(screen.Root), 4, 4).Check(); lintShadow128 != nil {
		t.Fatalf("create synthetic pixmap: %v", lintShadow128)
	}
	if lintShadow131 := c.FreePixmap(xproto.Pixmap(pixID)).Check(); lintShadow131 != nil {
		t.Fatalf("free owned pixmap: %v", lintShadow131)
	}
	mapping, err := c.GetKeyboardMapping(setup.MinKeycode, 1).Reply()
	if err != nil || mapping == nil {
		t.Fatalf("keyboard map query: %+v %v", mapping, err)
	}
	if err := c.InitXTest(); err == nil {
		key := byte(setup.MinKeycode)
		if lintShadow140 := c.FakeInputChecked(xproto.KeyPress, key, 0, screen.Root, 0, 0, 0).Check(); lintShadow140 != nil {
			t.Fatalf("XTEST key press: %v", lintShadow140)
		}
		if lintShadow143 := c.FakeInputChecked(xproto.KeyRelease, key, 0, screen.Root, 0, 0, 0).Check(); lintShadow143 != nil {
			t.Fatalf("XTEST key release: %v", lintShadow143)
		}
	} else {
		t.Logf("Xvfb did not advertise XTEST: %v", err)
	}
	if err := c.InitComposite(); err == nil {
		missing, lintShadow150 := c.NewID()
		if lintShadow150 != nil {
			t.Fatal(lintShadow150)
		}
		// An unredirected window is not a valid named pixmap. The checked
		// request must surface a protocol error rather than falsely succeed.
		bad := c.NameWindowPixmap(xproto.Window(0), xproto.Pixmap(missing)).Check()
		if bad == nil || strings.Contains(bad.Error(), "Cannot call 'Check'") {
			t.Fatalf("invalid compositor pixmap did not produce checked protocol error: %v", bad)
		}
	} else {
		t.Logf("Xvfb did not advertise Composite: %v", err)
	}
	// A checked SendEvent is allowed only within this headless server.
	event := make([]byte, 32)
	event[0] = 33 // X11 ClientMessage event type
	event[1] = 8  // data format
	binary.LittleEndian.PutUint32(event[4:8], winID)
	binary.LittleEndian.PutUint32(event[8:12], uint32(atom.Atom))
	if err := c.SendEventChecked(false, xproto.Window(winID), 0, string(event)).Check(); err != nil {
		t.Fatalf("headless client event: %v", err)
	}
	if err := c.InitRandR(); err == nil {
		resources, lintShadow173 := c.GetScreenResourcesCurrent(screen.Root).Reply()
		if lintShadow173 != nil {
			t.Fatalf("RandR resources: %v", lintShadow173)
		}
		if resources != nil {
			if _, lintShadow178 := c.GetOutputPrimary(screen.Root).Reply(); lintShadow178 != nil {
				t.Fatalf("Xvfb RandR primary lookup: %v", lintShadow178)
			}
			if _, lintShadow181 := c.GetOutputInfo(0, resources.ConfigTimestamp).Reply(); lintShadow181 == nil {
				t.Fatal("unknown RandR output returned valid information")
			}
			if _, lintShadow184 := c.GetCrtcInfo(0, resources.ConfigTimestamp).Reply(); lintShadow184 == nil {
				t.Fatal("unknown RandR CRTC returned valid information")
			}
			if len(resources.Outputs) > 0 {
				primary, lintShadow188 := c.GetOutputPrimary(screen.Root).Reply()
				if lintShadow188 != nil {
					t.Fatalf("RandR primary output: %v", lintShadow188)
				}
				_ = primary
				info, lintShadow188 := c.GetOutputInfo(resources.Outputs[0], resources.ConfigTimestamp).Reply()
				if lintShadow188 != nil {
					t.Fatalf("RandR output info: %v", lintShadow188)
				}
				if info != nil && info.Crtc != 0 {
					if _, crtcErr := c.GetCrtcInfo(info.Crtc, resources.ConfigTimestamp).Reply(); crtcErr != nil {
						t.Fatalf("RandR CRTC info: %v", crtcErr)
					}
				}
			}
		}
	} else {
		t.Logf("Xvfb did not advertise RandR: %v", err)
	}
	// The fake desktop is the only recipient of requests and is removed at
	// test cleanup. No host display is used or even consulted.
	if got := os.Getenv("DISPLAY"); got == display {
		t.Fatalf("test unexpectedly inherited its private display from the host: %q", got)
	}
}
