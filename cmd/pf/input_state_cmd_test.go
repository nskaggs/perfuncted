package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/internal/x11test"
)

type inputStateFailingWriter struct{ err error }

func (w inputStateFailingWriter) Write([]byte) (int, error) { return 0, w.err }

func TestInputStateCLIValidatesBeforeOpeningSession(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"missing selector", nil, "select exactly one"},
		{"multiple selectors", []string{"--key", "shift", "--button", "left"}, "select exactly one"},
		{"invalid key", []string{"--key", "not-a-key"}, "unsupported key"},
		{"invalid lock", []string{"--lock", "compose"}, "unsupported lock state"},
		{"invalid button", []string{"--button", "wheel"}, "invalid mouse button"},
		{"invalid output", []string{"--key", "shift", "--output", "text"}, "unsupported output"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opened := 0
			cmd := inputStateCmd(func(context.Context) (*perfuncted.Session, error) {
				opened++
				return nil, errors.New("unexpected observer open")
			})
			cmd.SetArgs(tc.args)
			cmd.SilenceErrors = true
			cmd.SilenceUsage = true
			cmd.SetOut(&bytes.Buffer{})
			err := cmd.ExecuteContext(context.Background())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v; want %q", err, tc.want)
			}
			if opened != 0 {
				t.Fatalf("opened observer for invalid query %d times", opened)
			}
		})
	}
}

func TestInputStateCLIPropagatesObserverOpenError(t *testing.T) {
	want := errors.New("observer open failed")
	opened := 0
	cmd := inputStateCmd(func(context.Context) (*perfuncted.Session, error) {
		opened++
		return nil, want
	})
	cmd.SetArgs([]string{"--key", "shift"})
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	if err := cmd.ExecuteContext(context.Background()); !errors.Is(err, want) {
		t.Fatalf("input state open error=%v, want %v", err, want)
	}
	if opened != 1 {
		t.Fatalf("observer opens=%d, want one", opened)
	}
}

func TestParseInputStateButton(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  int
	}{
		{"left", 1}, {"middle", 2}, {"right", 3}, {"button4", 4}, {"button5", 5}, {"5", 5},
	} {
		got, err := parseInputStateButton(tc.value)
		if err != nil || got != tc.want {
			t.Errorf("parseInputStateButton(%q) = %d, %v; want %d", tc.value, got, err, tc.want)
		}
	}
	if _, err := parseInputStateButton("wheel"); err == nil {
		t.Fatal("accepted unsupported mouse button")
	}
}

func TestInputStateCLIControlledX11(t *testing.T) {
	if os.Getenv("PF_X11_OBSERVATION_INTEGRATION") != "1" {
		t.Skip("requires isolated Xvfb")
	}
	display, stop, err := x11test.StartXvfb()
	if err != nil {
		t.Fatalf("start owned Xvfb: %v", err)
	}
	t.Cleanup(stop)
	time.Sleep(350 * time.Millisecond)
	t.Setenv("DISPLAY", display)
	t.Setenv("XDG_SESSION_TYPE", "x11")
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("WAYLAND_SOCKET", "")

	for _, tc := range []struct {
		name     string
		args     []string
		kind     string
		wantName string
	}{
		{name: "key", args: []string{"--key", "capslock"}, kind: "key", wantName: "capslock"},
		{name: "lock", args: []string{"--lock", "capslock"}, kind: "lock", wantName: "capslock"},
		{name: "button", args: []string{"--button", "left"}, kind: "button", wantName: "1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			cmd := newRootCmd(defaultOpenPFFactory)
			cmd.SetArgs(append([]string{"input", "state"}, tc.args...))
			cmd.SetOut(&out)
			cmd.SilenceErrors = true
			cmd.SilenceUsage = true
			if err := cmd.ExecuteContext(context.Background()); err != nil {
				t.Fatalf("query %s state: %v", tc.name, err)
			}
			var got input.StateObservation
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatalf("decode JSON state %q: %v", out.String(), err)
			}
			if got.Kind != tc.kind || got.Name != tc.wantName || got.State == "" || got.Provider != "x11-core" || got.Provenance != "unknown" || got.ObservedAt.IsZero() {
				t.Fatalf("unexpected state JSON: %+v", got)
			}
		})
	}

	t.Run("observer query error", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		open := defaultOpenPFFactory(&cliConfig{required: []perfuncted.Capability{perfuncted.CapabilityInputObservation}})
		cmd := inputStateCmd(func(ctx context.Context) (*perfuncted.Session, error) {
			session, err := open(ctx)
			if err == nil {
				cancel()
			}
			return session, err
		})
		cmd.SetArgs([]string{"--key", "capslock"})
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
		if err := cmd.ExecuteContext(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("query error=%v, want context.Canceled", err)
		}
	})

	t.Run("output writer error", func(t *testing.T) {
		want := errors.New("output writer failed")
		cmd := inputStateCmd(defaultOpenPFFactory(&cliConfig{required: []perfuncted.Capability{perfuncted.CapabilityInputObservation}}))
		cmd.SetArgs([]string{"--key", "capslock"})
		cmd.SetOut(inputStateFailingWriter{err: want})
		cmd.SilenceErrors = true
		cmd.SilenceUsage = true
		if err := cmd.ExecuteContext(context.Background()); !errors.Is(err, want) {
			t.Fatalf("encode error=%v, want %v", err, want)
		}
	})
}
