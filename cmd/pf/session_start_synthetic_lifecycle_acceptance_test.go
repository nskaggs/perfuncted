package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
)

type sessionStartupSignalWriter struct {
	mu    sync.Mutex
	buf   bytes.Buffer
	ready chan struct{}
	once  sync.Once
}

func (w *sessionStartupSignalWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.buf.Write(p)
	if strings.Contains(w.buf.String(), "press Ctrl+C") {
		w.once.Do(func() { close(w.ready) })
	}
	return n, err
}
func (w *sessionStartupSignalWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func TestSyntheticSessionStartEmitsEnvironmentAndWaitsForCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session := pftest.New(nil, nil, nil, nil)
	opened := 0
	opener := func(got context.Context, options ...perfuncted.Option) (*perfuncted.Session, error) {
		opened++
		if got != ctx || len(options) != 1 {
			t.Fatalf("invalid startup context or options: ctx=%v options=%d", got, len(options))
		}
		return session, nil
	}
	cmd := sessionStartCmdWithOpener(opener)
	cmd.SetContext(ctx)
	var stdout bytes.Buffer
	stderr := &sessionStartupSignalWriter{ready: make(chan struct{})}
	cmd.SetOut(&stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"--res-x", "640", "--res-y", "480", "--sway-config", "/tmp/synthetic-config"})
	done := make(chan error, 1)
	go func() { done <- cmd.Execute() }()
	// The session is required to stay alive after reporting readiness; wait
	// for an explicit signal before cancellation rather than racing on a buffer.
	select {
	case <-stderr.ready:
	case err := <-done:
		t.Fatalf("synthetic session exited before readiness: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("session did not report readiness")
	}
	select {
	case err := <-done:
		t.Fatalf("session exited without cancellation: %v", err)
	default:
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if opened != 1 {
		t.Fatalf("startup opened %d sessions", opened)
	}
	for _, value := range []string{"export XDG_RUNTIME_DIR=", "export WAYLAND_DISPLAY=", "export DBUS_SESSION_BUS_ADDRESS="} {
		if !strings.Contains(stdout.String(), value) {
			t.Fatalf("missing startup export %q in %q", value, stdout.String())
		}
	}
	for _, value := range []string{"running", "press Ctrl+C", "stopping", "stopped"} {
		if !strings.Contains(stderr.String(), value) {
			t.Fatalf("missing session lifecycle %q in %q", value, stderr.String())
		}
	}
}

func TestSyntheticSessionStartReportsOpenAndOutputFailures(t *testing.T) {
	wanted := errors.New("synthetic desktop startup failed")
	cmd := sessionStartCmdWithOpener(func(context.Context, ...perfuncted.Option) (*perfuncted.Session, error) { return nil, wanted })
	if err := cmd.Execute(); !errors.Is(err, wanted) {
		t.Fatalf("startup failure lost: %v", err)
	}
	for _, stage := range []struct {
		name string
		out  io.Writer
		err  io.Writer
	}{
		{"stdout 1", &infoFailingWriter{max: 1}, io.Discard},
		{"stdout 2", &infoFailingWriter{max: 2}, io.Discard},
		{"stdout 3", &infoFailingWriter{max: 3}, io.Discard},
		{"stderr 1", io.Discard, &infoFailingWriter{max: 1}},
		{"stderr 2", io.Discard, &infoFailingWriter{max: 2}},
		{"stderr 3", io.Discard, &infoFailingWriter{max: 3}},
	} {
		t.Run(stage.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			cmd := sessionStartCmdWithOpener(func(context.Context, ...perfuncted.Option) (*perfuncted.Session, error) {
				return pftest.New(nil, nil, nil, nil), nil
			})
			cmd.SetContext(ctx)
			cmd.SetOut(stage.out)
			cmd.SetErr(stage.err)
			if err := cmd.Execute(); !errors.Is(err, io.ErrClosedPipe) {
				t.Fatalf("startup swallowed writer failure: %v", err)
			}
		})
	}
}
