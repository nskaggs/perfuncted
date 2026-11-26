//go:build integration && linux

package integration_test

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/v2/internal/third_party/xgb"
	"github.com/nskaggs/perfuncted/v2/internal/third_party/xgb/xproto"
	"github.com/nskaggs/perfuncted/v2/internal/third_party/xgb/xtest"
)

func certifyReleaseX11InputCommands(t *testing.T, ctx context.Context, s *suite, binary string, conn *xgb.Conn, win, root xproto.Window) {
	t.Helper()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Env = s.rt.EnvList()
		var diagnostic bytes.Buffer
		cmd.Stderr = &diagnostic
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("pf %v: %v: %s", args, err, diagnostic.String())
		}
		return strings.TrimSpace(string(out))
	}
	if err := xproto.ChangeWindowAttributesChecked(conn, win, xproto.CwEventMask, []uint32{xproto.EventMaskButtonPress | xproto.EventMaskButtonRelease}).Check(); err != nil {
		t.Fatal(err)
	}
	if err := xproto.SetInputFocusChecked(conn, xproto.InputFocusPointerRoot, win, xproto.TimeCurrentTime).Check(); err != nil {
		t.Fatal(err)
	}
	setup := xproto.Setup(conn)
	mapping, err := xproto.GetKeyboardMapping(conn, setup.MinKeycode, byte(int(setup.MaxKeycode)-int(setup.MinKeycode)+1)).Reply()
	if err != nil || mapping == nil || mapping.KeysymsPerKeycode == 0 {
		t.Fatalf("native keymap: %v", err)
	}
	var key byte
	for i, sym := range mapping.Keysyms {
		if sym == 0xffc5 {
			key = byte(int(setup.MinKeycode) + i/int(mapping.KeysymsPerKeycode))
			break
		}
	}
	if key == 0 {
		t.Fatal("F8 not mapped")
	}
	// Independent fallback cleanup protects the private display even when the
	// CLI press succeeds but a later assertion or release fails.
	defer func() {
		_ = xtest.FakeInputChecked(conn, xproto.KeyRelease, key, xproto.TimeCurrentTime, root, 0, 0, 0).Check()
		_ = xtest.FakeInputChecked(conn, xproto.ButtonRelease, 1, xproto.TimeCurrentTime, root, 0, 0, 0).Check()
	}()
	for _, pressed := range []bool{true, false} {
		command := "keyup"
		if pressed {
			command = "keydown"
		}
		run("input", command, "f8")
		state, err := xproto.QueryKeymap(conn).Reply()
		if err != nil || state == nil {
			t.Fatalf("native key state: %v", err)
		}
		if got := state.Keys[key/8]&(1<<(key%8)) != 0; got != pressed {
			t.Fatalf("%s did not change native F8 state: %t", command, got)
		}
	}
	for _, pressed := range []bool{true, false} {
		command := "mouseup"
		if pressed {
			command = "mousedown"
		}
		run("input", command, "--button", "1", "--x", "150", "--y", "150")
		state, err := xproto.QueryPointer(conn, root).Reply()
		if err != nil || state == nil {
			t.Fatalf("native button state: %v", err)
		}
		if got := state.Mask&xproto.KeyButMaskButton1 != 0; got != pressed {
			t.Fatalf("%s did not change native button state: %t", command, got)
		}
	}
	// Drain the preceding press/release events before certifying click counts.
	conn.Sync()
	for {
		ev, err := conn.PollForEvent()
		if err != nil {
			t.Fatal(err)
		}
		if ev == nil {
			break
		}
	}
	assertButtons := func(button byte, count int, args ...string) {
		t.Helper()
		run(args...)
		conn.Sync()
		var transitions []string
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			ev, err := conn.PollForEvent()
			if err != nil {
				t.Fatal(err)
			}
			if ev == nil {
				if len(transitions) >= 2*count {
					break
				}
				time.Sleep(10 * time.Millisecond)
				continue
			}
			switch e := ev.(type) {
			case xproto.ButtonPressEvent:
				transitions = append(transitions, fmt.Sprintf("down:%d", e.Detail))
			case xproto.ButtonReleaseEvent:
				transitions = append(transitions, fmt.Sprintf("up:%d", e.Detail))
			}
		}
		want := strings.TrimSpace(strings.Repeat(fmt.Sprintf("down:%d up:%d ", button, button), count))
		if got := strings.Join(transitions, " "); got != want {
			t.Fatalf("native events for %v: %q, want %q", args, got, want)
		}
	}
	assertButtons(1, 1, "input", "click", "--x", "150", "--y", "150")
	assertButtons(1, 1, "input", "click-center", "--rect", "140,140,160,160")
	assertButtons(1, 2, "input", "double-click", "--x", "150", "--y", "150")
	for _, test := range []struct {
		direction string
		button    byte
	}{{"up", 4}, {"down", 5}, {"left", 6}, {"right", 7}} {
		assertButtons(test.button, 2, "input", "scroll", test.direction, "--clicks", "2")
	}
	run("input", "sync")
}
