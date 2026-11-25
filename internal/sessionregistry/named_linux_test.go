package sessionregistry

import (
	"context"
	"encoding/json"
	"errors"
	"golang.org/x/sys/unix"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

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
			err := Recover(o.root, o.record.Name)
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
				replacement, err := Reserve(o.record.Name)
				if err != nil {
					t.Fatalf("reserve recovered name: %v", err)
				}
				replacement.Close()
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
			if _, err := Call(ctx, o.root, o.record.Name, "stop"); err == nil {
				t.Fatal("unsafe stop accepted")
			}
			data, err := os.ReadFile(target)
			if err != nil || string(data) != "untouched" {
				t.Fatal("target changed")
			}
		})
	}
}

func TestNamedSessionDuplicateRace(t *testing.T) {
	seed := namedTestReserve(t)
	name := seed.record.Name + "x"
	var wg sync.WaitGroup
	results := make(chan *Owner, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			o, err := Reserve(name)
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
		o.Close()
	}
	if count != 1 {
		t.Fatalf("got %d winners", count)
	}
}

func TestNamedSessionIsolation(t *testing.T) {
	a, b := namedTestReserve(t), namedTestReserve(t)
	if a.record.ID == b.record.ID {
		t.Fatal("duplicate IDs")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	doneA, doneB := make(chan error, 1), make(chan error, 1)
	start := func(o *Owner, suffix string, finished chan error) chan struct{} {
		t.Helper()
		if err := o.Publish(); err != nil {
			t.Fatal(err)
		}
		c, stop := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() {
			defer close(done)
			o.Serve(c, SafeEnv(map[string]string{"XDG_RUNTIME_DIR": "/tmp/" + suffix, "WAYLAND_DISPLAY": "wayland-1", "DBUS_SESSION_BUS_ADDRESS": "unix:path=/tmp/" + suffix + "/bus", "SECRET": "never-export"}), stop, finished)
		}()
		t.Cleanup(func() { stop(); o.listener.Close(); <-done })
		return done
	}
	aDone := start(a, "a", doneA)
	_ = start(b, "b", doneB)
	for _, o := range []*Owner{a, b} {
		r, err := Call(ctx, o.root, o.record.Name, "attach")
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
		r, err := Call(ctx, a.root, a.record.Name, "stop")
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
	r, err := Call(ctx, b.root, b.record.Name, "attach")
	if err != nil || r.Environment["XDG_RUNTIME_DIR"] != "/tmp/b" {
		t.Fatalf("other owner affected: %+v %v", r, err)
	}
}

func namedTestReserve(t *testing.T) *Owner {
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
	o, err := Reserve(name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(o.Close)
	return o
}
