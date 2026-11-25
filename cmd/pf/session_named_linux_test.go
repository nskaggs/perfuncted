package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func namedTestReserve(t *testing.T) *namedOwner {
	t.Helper()
	var id [8]byte
	// Derive a valid unique name from a temporary basename.
	d, err := os.MkdirTemp("", "pf134-")
	if err != nil {
		t.Fatal(err)
	}
	os.Remove(d)
	_ = id
	name := d[strings.LastIndex(d, "/")+1:]
	o, err := namedReserve(name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(o.close)
	return o
}

func TestNamedSessionIsolation(t *testing.T) {
	a, b := namedTestReserve(t), namedTestReserve(t)
	if a.record.ID == b.record.ID {
		t.Fatal("duplicate IDs")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	doneA, doneB := make(chan error, 1), make(chan error, 1)
	start := func(o *namedOwner, suffix string, finished chan error) chan struct{} {
		t.Helper()
		if err := o.publish(); err != nil {
			t.Fatal(err)
		}
		c, stop := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() {
			defer close(done)
			o.serve(c, namedSafeEnv(map[string]string{"XDG_RUNTIME_DIR": "/tmp/" + suffix, "WAYLAND_DISPLAY": "wayland-1", "DBUS_SESSION_BUS_ADDRESS": "unix:path=/tmp/" + suffix + "/bus", "SECRET": "never-export"}), stop, finished)
		}()
		t.Cleanup(func() { stop(); o.listener.Close(); <-done })
		return done
	}
	aDone := start(a, "a", doneA)
	_ = start(b, "b", doneB)
	for _, o := range []*namedOwner{a, b} {
		r, err := namedCall(ctx, o.root, o.record.Name, "attach")
		if err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(r)
		if len(r.Environment) != 3 || strings.Contains(string(data), "SECRET") || strings.Contains(string(data), "never-export") {
			t.Fatalf("unsafe environment: %s", data)
		}
	}
	stopped := make(chan error, 1)
	go func() {
		r, err := namedCall(ctx, a.root, a.record.Name, "stop")
		if err == nil && r.State != "stopped" {
			err = os.ErrInvalid
		}
		stopped <- err
	}()
	select {
	case err := <-stopped:
		t.Fatalf("early stop result: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	doneA <- nil
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
	<-aDone
	r, err := namedCall(ctx, b.root, b.record.Name, "attach")
	if err != nil || r.Environment["XDG_RUNTIME_DIR"] != "/tmp/b" {
		t.Fatalf("other owner affected: %+v %v", r, err)
	}
}

func TestNamedSessionDuplicateRace(t *testing.T) {
	seed := namedTestReserve(t)
	name := seed.record.Name + "x"
	var wg sync.WaitGroup
	results := make(chan *namedOwner, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			o, err := namedReserve(name)
			if err == nil {
				results <- o
			}
		}()
	}
	wg.Wait()
	close(results)
	count := 0
	for o := range results {
		count++
		o.close()
	}
	if count != 1 {
		t.Fatalf("got %d winners", count)
	}
}

func TestNamedSessionUnsafeRecords(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "fifo", "pid-reuse"} {
		t.Run(kind, func(t *testing.T) {
			o := namedTestReserve(t)
			path := namedFDPath(o.dir, "record")
			target := t.TempDir() + "/target"
			if err := os.WriteFile(target, []byte("untouched"), 0600); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "symlink":
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(target, path); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				if err := unix.Mkfifo(path, 0600); err != nil {
					t.Fatal(err)
				}
			case "pid-reuse":
				r := o.record
				r.Start = "0"
				data, _ := json.Marshal(r)
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if _, err := namedCall(ctx, o.root, o.record.Name, "stop"); err == nil {
				t.Fatal("unsafe stop accepted")
			}
			data, err := os.ReadFile(target)
			if err != nil || string(data) != "untouched" {
				t.Fatal("target changed")
			}
		})
	}
}

func TestNamedSessionInvalidNameAndQuote(t *testing.T) {
	for _, name := range []string{"", "../x", "a/b", strings.Repeat("a", 33)} {
		if o, err := namedReserve(name); err == nil {
			o.close()
			t.Fatalf("accepted %q", name)
		}
	}
	if got := namedShellQuote("a'b"); got != "'a'\"'\"'b'" {
		t.Fatalf("quote: %q", got)
	}
}

func TestNamedRecovery(t *testing.T) {
	for _, tc := range []string{"empty", "partial", "published", "active", "live", "mismatch", "pid-reuse", "malformed", "unknown", "symlink", "hardlink", "fifo"} {
		t.Run(tc, func(t *testing.T) {
			o := namedTestReserve(t)
			path := namedFDPath(o.dir, "record")
			rec := o.record
			rec.PID = 2147483647
			writeRecord := func() {
				t.Helper()
				data, _ := json.Marshal(rec)
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			target := t.TempDir() + "/target"
			if err := os.WriteFile(target, []byte("keep"), 0600); err != nil {
				t.Fatal(err)
			}
			switch tc {
			case "partial":
				if err := os.WriteFile(namedFDPath(o.dir, "record.tmp"), []byte("{"), 0600); err != nil {
					t.Fatal(err)
				}
			case "published":
				writeRecord()
			case "live":
				rec = o.record
				writeRecord()
			case "mismatch":
				rec.Name = "wrong"
				writeRecord()
			case "pid-reuse":
				// The current test process stands in for the unrelated live
				// process that inherited the recorded owner's PID.
				rec = o.record
				rec.Start = "1"
				if rec.Start == o.record.Start {
					rec.Start = "2"
				}
				writeRecord()
			case "malformed":
				if err := os.WriteFile(path, []byte("{"), 0600); err != nil {
					t.Fatal(err)
				}
			case "unknown":
				writeRecord()
				if err := os.WriteFile(namedFDPath(o.dir, "unknown"), nil, 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(target, path); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				if err := unix.Mkfifo(path, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if tc != "active" {
				if err := unix.Flock(int(o.dir.Fd()), unix.LOCK_UN); err != nil {
					t.Fatal(err)
				}
			}
			err := namedRecover(o.root, o.record.Name)
			wantOK := tc == "empty" || tc == "partial" || tc == "published" || tc == "pid-reuse"
			if (err == nil) != wantOK {
				t.Fatalf("recover: %v, want success %v", err, wantOK)
			}
			var st unix.Stat_t
			err = unix.Fstatat(int(o.root.Fd()), o.record.Name, &st, unix.AT_SYMLINK_NOFOLLOW)
			if wantOK && !errors.Is(err, unix.ENOENT) || !wantOK && err != nil {
				t.Fatalf("reservation state: %v", err)
			}
			if tc == "pid-reuse" {
				start, err := namedProcStart(rec.PID)
				if err != nil || start != o.record.Start {
					t.Fatalf("unrelated process identity changed: %q, %v", start, err)
				}
				// The name must be immediately reusable.
				replacement, err := namedReserve(o.record.Name)
				if err != nil {
					t.Fatalf("reserve recovered name: %v", err)
				}
				replacement.close()
			}
			data, err := os.ReadFile(target)
			if err != nil || string(data) != "keep" {
				t.Fatal("external target changed")
			}
			for _, entry := range []string{"record.tmp", "unknown"} {
				_ = unix.Unlinkat(int(o.dir.Fd()), entry, 0)
			}
		})
	}
}

func TestNamedCommandsLifecycle(t *testing.T) {
	for _, mode := range []string{"plain", "json"} {
		t.Run(mode, func(t *testing.T) {
			path := t.TempDir()
			if err := os.Chmod(path, 0700); err != nil {
				t.Fatal(err)
			}
			root, err := namedOpenDir(unix.AT_FDCWD, path)
			if err != nil {
				t.Fatal(err)
			}
			o, err := namedReserveIn(root, "cli")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(o.close)
			if err := o.publish(); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			finished := make(chan error, 1)
			finished <- nil
			done := make(chan struct{})
			go func() {
				defer close(done)
				o.serve(ctx, namedSafeEnv(map[string]string{"XDG_RUNTIME_DIR": "/tmp/a'b", "WAYLAND_DISPLAY": "wayland-1", "DBUS_SESSION_BUS_ADDRESS": "unix:path=/tmp/bus"}), cancel, finished)
			}()
			t.Cleanup(func() { cancel(); o.listener.Close(); <-done })
			// Use a fresh directory descriptor for each invocation.
			run := func(op string, extra ...string) (string, error) {
				t.Helper()
				cmd := &cobra.Command{Use: "session", SilenceUsage: true, SilenceErrors: true}
				cmd.AddCommand(namedCommandsWithRegistry(func() (*os.File, error) {
					return namedOpenDir(int(o.root.Fd()), ".")
				})...)
				var out bytes.Buffer
				cmd.SetOut(&out)
				cmd.SetErr(&out)
				args := []string{op, "--output", mode}
				if op != "list" {
					args = append(args, o.record.Name)
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
						var rs []namedReply
						if err := json.Unmarshal([]byte(out), &rs); err != nil {
							t.Fatal(err)
						}
						found := false
						for _, r := range rs {
							if r.ID == o.record.ID && r.State == "ready" {
								found = true
							}
						}
						if !found {
							t.Fatal(out)
						}
					} else {
						var r namedReply
						if err := json.Unmarshal([]byte(out), &r); err != nil || r.ID != o.record.ID || r.State != "ready" {
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
				} else if !strings.Contains(out, o.record.ID+" ready") {
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
			rec := o.record
			rec.PID = 2147483647
			data, _ := json.Marshal(rec)
			if err := os.WriteFile(namedFDPath(o.dir, "record"), data, 0600); err != nil {
				t.Fatal(err)
			}
			if err := unix.Flock(int(o.dir.Fd()), unix.LOCK_UN); err != nil {
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
	root, err := namedRegistry()
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	stop := make(chan error, 1)
	go func() {
		r, err := namedCall(ctx, root, a.name, "stop")
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
	if _, err := namedCall(ctx, root, b.name, "attach"); err != nil {
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
	if _, err := namedCall(ctx, root, b.name, "stop"); err != nil {
		t.Fatal(err)
	}
	if err := <-b.done; err != nil {
		t.Fatal(err)
	}
	if b.screen.closed.Load() != 1 {
		t.Fatal("other backend not closed exactly once")
	}
}
