package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgb/xtest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/internal/x11test"
	"github.com/nskaggs/perfuncted/pftest"
)

func TestInputHotkeyCLIValidatesBeforeOpeningSession(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"missing", nil, "choose exactly one"},
		{"bad output", []string{"--combo", "F8", "--output", "text"}, "unsupported output"},
		{"bad button", []string{"--button", "wheel"}, "invalid mouse button"},
		{"both filters", []string{"--button", "left", "--combo", "F8"}, "choose exactly one"},
		{"bad chord", []string{"--combo", "Ctrl+"}, "unsupported key"},
		{"bad event", []string{"--combo", "F8", "--event", "held"}, "unsupported transition"},
		{"malformed flag", []string{"--combo", "F8", "--timeout", "bad"}, "invalid argument"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called := 0
			cmd := inputHotkeyCmd(func(context.Context) (*perfuncted.Session, error) {
				called++
				return nil, errors.New("unexpected open")
			})
			cmd.SetArgs(append([]string{"listen"}, tc.args...))
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
			cmd.SetOut(&bytes.Buffer{})
			err := cmd.ExecuteContext(context.Background())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v; want %q", err, tc.want)
			}
			if called != 0 {
				t.Fatalf("opened backend for invalid CLI filter (%d calls)", called)
			}
		})
	}
}

func TestInputHotkeyCLISelectsObservationOnly(t *testing.T) {
	cases := []struct{ args []string }{
		{[]string{"--combo", "F8", "--event", "press"}},
		{[]string{"--button", "left", "--event", "both"}},
		{[]string{"--button", "middle", "--event", "release"}},
		{[]string{"--button", "right"}},
		{[]string{"--button", "1"}},
		{[]string{"--button", "2"}},
		{[]string{"--button", "3"}},
	}
	sentinel := errors.New("observation opener selected")
	for i, tc := range cases {
		t.Run(fmt.Sprintf("valid-%d", i), func(t *testing.T) {
			called := 0
			cmd := inputHotkeyCmd(func(context.Context) (*perfuncted.Session, error) { called++; return nil, sentinel })
			cmd.SetArgs(append([]string{"listen"}, tc.args...))
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
			cmd.SetOut(&bytes.Buffer{})
			if err := cmd.ExecuteContext(context.Background()); !errors.Is(err, sentinel) {
				t.Fatal(err)
			}
			if called != 1 {
				t.Fatalf("observer opened %d times", called)
			}
		})
	}
}

func TestInputHotkeyCLICanceledAndTimedOutContext(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		ctx   context.Context
		flags []string
		want  error
	}{
		{canceled, []string{"--combo", "F8"}, context.Canceled},
		{context.Background(), []string{"--combo", "F8", "--timeout", "10ms"}, context.DeadlineExceeded},
	} {
		cmd := inputHotkeyCmd(func(ctx context.Context) (*perfuncted.Session, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		})
		cmd.SetArgs(append([]string{"listen"}, tc.flags...))
		cmd.SilenceUsage = true
		cmd.SilenceErrors = true
		if err := cmd.ExecuteContext(tc.ctx); !errors.Is(err, tc.want) {
			t.Fatal(err)
		}
	}
}

// TestInputHotkeyCLIControlledX11 exercises the actual Session, command,
// JSON streaming and cancellation using synthetic input in isolated Xvfb.
// It does not establish physical-input provenance on a native Xorg desktop.
func TestInputHotkeyCLIControlledX11(t *testing.T) {
	if os.Getenv("PF_X11_OBSERVATION_INTEGRATION") != "1" {
		t.Skip("requires isolated Xvfb")
	}
	display, stop, err := x11test.StartXvfbNoReset()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	t.Setenv("DISPLAY", display)
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("XDG_SESSION_TYPE", "x11")
	sender, err := xgb.NewConnDisplay(display)
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	if err := xtest.Init(sender); err != nil {
		t.Fatal(err)
	}
	// F8 is typically X11 keysym 0xffc5; derive the actual mapped keycode.
	reply, err := xproto.GetKeyboardMapping(sender, xproto.Setup(sender).MinKeycode,
		byte(int(xproto.Setup(sender).MaxKeycode)-int(xproto.Setup(sender).MinKeycode)+1)).Reply()
	if err != nil {
		t.Fatal(err)
	}
	key := byte(0)
	stride := int(reply.KeysymsPerKeycode)
	for i := 0; i+stride <= len(reply.Keysyms); i += stride {
		for _, sym := range reply.Keysyms[i : i+stride] {
			if uint32(sym) == 0xffc5 {
				key = byte(int(xproto.Setup(sender).MinKeycode) + i/stride)
				break
			}
		}
		if key != 0 {
			break
		}
	}
	if key == 0 {
		t.Fatal("F8 absent from isolated keymap")
	}
	root := xproto.Setup(sender).DefaultScreen(sender).Root
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var out bytes.Buffer
	cmd := newRootCmd(defaultOpenPFFactory)
	cmd.SetArgs([]string{"input", "hotkey", "listen", "--combo", "F8", "--event", "both"})
	cmd.SetOut(&out)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	finished := make(chan error, 1)
	go func() { finished <- cmd.ExecuteContext(ctx) }()
	// Generate multiple candidate transitions after registration has time to
	// finish; no unbounded retries or production input injection.
	time.Sleep(200 * time.Millisecond)
	for j := 0; j < 5; j++ {
		for _, kind := range []byte{xproto.KeyPress, xproto.KeyRelease} {
			if err := xtest.FakeInputChecked(sender, kind, key, xproto.TimeCurrentTime, root, 0, 0, 0).Check(); err != nil {
				t.Fatal(err)
			}
		}
		time.Sleep(60 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-finished:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("CLI streaming listener did not cancel")
	}
	output := out.String()
	if !strings.Contains(output, `"source":"x11-xi2"`) ||
		!strings.Contains(output, `"transition":"press"`) ||
		!strings.Contains(output, `"transition":"release"`) {
		t.Fatalf("missing JSON hotkey transitions: %q", output)
	}
}

type cliObservationSub struct {
	events chan input.ObservationEvent
	err    error
}

func (s *cliObservationSub) Events() <-chan input.ObservationEvent { return s.events }
func (s *cliObservationSub) Err() error                            { return s.err }
func (s *cliObservationSub) Close() error                          { return nil }

type cliFailedWriter struct{ err error }

func (w cliFailedWriter) Write([]byte) (int, error) { return 0, w.err }

func TestInputHotkeyStreamContract(t *testing.T) {
	t.Run("cancel", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		sub := &cliObservationSub{events: make(chan input.ObservationEvent)}
		if err := streamInputObservation(ctx, sub, &bytes.Buffer{}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("deadline", func(t *testing.T) {
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancel()
		sub := &cliObservationSub{events: make(chan input.ObservationEvent)}
		if err := streamInputObservation(ctx, sub, &bytes.Buffer{}); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
	})
	for _, tc := range []struct {
		name  string
		cause error
		want  error
	}{
		{"normal", nil, nil},
		{"subscription-cancel", context.Canceled, nil},
		{"event-loss", input.ErrObservationLoss, input.ErrObservationLoss},
	} {
		t.Run(tc.name, func(t *testing.T) {
			events := make(chan input.ObservationEvent)
			close(events)
			sub := &cliObservationSub{events: events, err: tc.cause}
			err := streamInputObservation(context.Background(), sub, &bytes.Buffer{})
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
		})
	}
	t.Run("json", func(t *testing.T) {
		events := make(chan input.ObservationEvent, 2)
		events <- input.ObservationEvent{Kind: "hotkey", ID: "F8", Transition: "press", Source: "x11-xi2"}
		events <- input.ObservationEvent{Kind: "hotkey", ID: "F8", Transition: "release", Source: "x11-xi2"}
		close(events)
		var buf bytes.Buffer
		if err := streamInputObservation(context.Background(), &cliObservationSub{events: events}, &buf); err != nil {
			t.Fatal(err)
		}
		if strings.Count(buf.String(), "x11-xi2") != 2 {
			t.Fatal(buf.String())
		}
	})
	t.Run("writer-error", func(t *testing.T) {
		events := make(chan input.ObservationEvent, 1)
		events <- input.ObservationEvent{Kind: "hotkey", ID: "F8", Transition: "press"}
		sentinel := errors.New("failed writer")
		err := streamInputObservation(context.Background(), &cliObservationSub{events: events}, cliFailedWriter{err: sentinel})
		if !errors.Is(err, sentinel) {
			t.Fatal(err)
		}
	})
}

func TestInputHotkeyCLIRejectsSessionWithoutObserver(t *testing.T) {
	cmd := newRootCmd(func(*cliConfig) sessionOpener {
		return func(context.Context) (*perfuncted.Session, error) { return pftest.New(nil, nil, nil, nil), nil }
	})
	cmd.SetArgs([]string{"input", "hotkey", "listen", "--combo", "F8"})
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	if err := cmd.ExecuteContext(context.Background()); err == nil {
		t.Fatal("CLI accepted unavailable observation capability")
	}
}
