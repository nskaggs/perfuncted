package main

import (
	"github.com/nskaggs/perfuncted/internal/sessionregistry"

	"bytes"
	"context"
	"encoding/json"
	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNamedSessionInvalidNameAndQuote(t *testing.T) {
	for _, name := range []string{"", "../x", "a/b", strings.Repeat("a", 33)} {
		if o, err := sessionregistry.Reserve(name); err == nil {
			o.Close()
			t.Fatalf("accepted %q", name)
		}
	}
	if got := namedShellQuote("a'b"); got != "'a'\"'\"'b'" {
		t.Fatalf("quote: %q", got)
	}
}

func TestNamedCommandsLifecycle(t *testing.T) {
	for _, mode := range []string{"plain", "json"} {
		t.Run(mode, func(t *testing.T) {
			path := t.TempDir()
			if err := os.Chmod(path, 0700); err != nil {
				t.Fatal(err)
			}
			root, err := sessionregistry.OpenDir(unix.AT_FDCWD, path)
			if err != nil {
				t.Fatal(err)
			}
			o, err := sessionregistry.ReserveIn(root, "cli")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(o.Close)
			if err := o.Publish(); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			finished := make(chan error, 1)
			finished <- nil
			done := make(chan struct{})
			go func() {
				defer close(done)
				o.Serve(ctx, sessionregistry.SafeEnv(map[string]string{"XDG_RUNTIME_DIR": "/tmp/a'b", "WAYLAND_DISPLAY": "wayland-1", "DBUS_SESSION_BUS_ADDRESS": "unix:path=/tmp/bus"}), cancel, finished)
			}()
			t.Cleanup(func() { cancel(); _ = o.CloseListener(); <-done })
			// Use a fresh directory descriptor for each invocation.
			run := func(op string, extra ...string) (string, error) {
				t.Helper()
				cmd := &cobra.Command{Use: "session", SilenceUsage: true, SilenceErrors: true}
				cmd.AddCommand(namedCommandsWithRegistry(func() (*os.File, error) {
					return sessionregistry.OpenDir(unix.AT_FDCWD, path)
				})...)
				var out bytes.Buffer
				cmd.SetOut(&out)
				cmd.SetErr(&out)
				args := []string{op, "--output", mode}
				if op != "list" {
					args = append(args, o.Record().Name)
				}
				args = append(args, extra...)
				cmd.SetArgs(args)
				err := cmd.Execute()
				return out.String(), err
			}
			for _, op := range []string{"list", "inspect", "attach"} {
				out, err := run(op)
				if err != nil {
					t.Fatal(op, err)
				}
				if mode == "json" {
					if op == "list" {
						var rs []sessionregistry.Reply
						if err := json.Unmarshal([]byte(out), &rs); err != nil {
							t.Fatal(err)
						}
						found := false
						for _, r := range rs {
							if r.ID == o.Record().ID && r.State == "ready" {
								found = true
							}
						}
						if !found {
							t.Fatal(out)
						}
					} else {
						var r sessionregistry.Reply
						if err := json.Unmarshal([]byte(out), &r); err != nil || r.ID != o.Record().ID || r.State != "ready" {
							t.Fatal(out, err)
						}
						if op == "attach" && len(r.Environment) != 3 {
							t.Fatal(out)
						}
					}
				} else if op == "attach" {
					if !strings.Contains(out, "export XDG_RUNTIME_DIR="+namedShellQuote("/tmp/a'b")) {
						t.Fatal(out)
					}
				} else if !strings.Contains(out, o.Record().ID+" ready") {
					t.Fatal(out)
				}
			}
			out, err := run("attach", "--print-env")
			if mode == "json" && err == nil || mode == "plain" && (err != nil || !strings.Contains(out, "export WAYLAND_DISPLAY=")) {
				t.Fatal(out, err)
			}
			if _, err := run("recover"); err == nil {
				t.Fatal("active recovery succeeded")
			}
			out, err = run("stop")
			if err != nil || !strings.Contains(out, "stopped") {
				t.Fatal(out, err)
			}
			<-done
			if _, err := run("inspect"); err == nil {
				t.Fatal("stopped inspect succeeded")
			}
			// Simulate a crash after publication; no process or runtime is touched.
			rec := o.Record()
			rec.PID = 2147483647
			data, _ := json.Marshal(rec)
			if err := os.WriteFile(filepath.Join(path, "cli", "record"), data, 0600); err != nil {
				t.Fatal(err)
			}
			if err := unix.Flock(int(o.Directory().Fd()), unix.LOCK_UN); err != nil {
				t.Fatal(err)
			}
			out, err = run("recover")
			if err != nil || !strings.Contains(out, "recovered") {
				t.Fatal(out, err)
			}
			for _, op := range []string{"inspect", "attach", "stop", "recover"} {
				if _, err := run(op); err == nil {
					t.Fatal("missing", op)
				}
			}
		})
	}
}

// The real Session.Close path owns the test backend, even though the backend
// itself is synthetic. A stop receipt must follow closure, not merely cancel.
type namedTrackedScreen struct {
	pftest.Screenshotter
	closed  atomic.Int32
	entered chan struct{}
	release chan struct{}
}

func (s *namedTrackedScreen) Close() error {
	if s.closed.Add(1) == 1 && s.entered != nil {
		close(s.entered)
		<-s.release
	}
	return nil
}

func TestNamedStartCloseIsolation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	type instance struct {
		name   string
		screen *namedTrackedScreen
		done   chan error
	}
	start := func(block bool) instance {
		t.Helper()
		d, err := os.MkdirTemp("", "pf134-close-")
		if err != nil {
			t.Fatal(err)
		}
		os.Remove(d)
		name := d[strings.LastIndex(d, "/")+1:]
		backend := &namedTrackedScreen{}
		if block {
			backend.entered = make(chan struct{})
			backend.release = make(chan struct{})
		}
		session := pftest.New(backend, nil, nil, nil)
		cmd := sessionStartCmdWithOpener(func(context.Context, ...perfuncted.Option) (*perfuncted.Session, error) { return session, nil })
		cmd.SetContext(ctx)
		cmd.SetArgs([]string{"--name", name})
		cmd.SetOut(io.Discard)
		w := &sessionStartupSignalWriter{ready: make(chan struct{})}
		cmd.SetErr(w)
		done := make(chan error, 1)
		go func() { done <- cmd.Execute() }()
		select {
		case <-w.ready:
		case err := <-done:
			t.Fatalf("start failed: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		return instance{name, backend, done}
	}
	a := start(true)
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(a.screen.release) }) }
	defer release()
	b := start(false)
	root, err := sessionregistry.Registry()
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	stop := make(chan error, 1)
	go func() {
		r, err := sessionregistry.Call(ctx, root, a.name, "stop")
		if err == nil && r.State != "stopped" {
			err = os.ErrInvalid
		}
		stop <- err
	}()
	select {
	case <-a.screen.entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case err := <-stop:
		t.Fatalf("stop before Session.Close completed: %v", err)
	default:
	}
	if b.screen.closed.Load() != 0 {
		t.Fatal("other backend closed")
	}
	if _, err := sessionregistry.Call(ctx, root, b.name, "attach"); err != nil {
		t.Fatal(err)
	}
	release()
	if err := <-stop; err != nil {
		t.Fatal(err)
	}
	if err := <-a.done; err != nil {
		t.Fatal(err)
	}
	if a.screen.closed.Load() != 1 || b.screen.closed.Load() != 0 {
		t.Fatal("wrong close counts")
	}
	if _, err := sessionregistry.Call(ctx, root, b.name, "stop"); err != nil {
		t.Fatal(err)
	}
	if err := <-b.done; err != nil {
		t.Fatal(err)
	}
	if b.screen.closed.Load() != 1 {
		t.Fatal("other backend not closed exactly once")
	}
}
