//go:build linux

package dbusutil

import (
	"context"
	"errors"
	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/v2/internal/testutil"
	"net"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDBusPublicContextBoundaries(t *testing.T) {
	if c, err := SessionBusAddressContext(absentDBusContext, "unix:path=/nonexistent"); c != nil || err == nil || !strings.Contains(err.Error(), "nil context") {
		t.Fatalf("session nil = %v %v", c, err)
	}
	if c, err := ConnectContext(absentDBusContext, "unix:path=/nonexistent"); c != nil || err == nil || !strings.Contains(err.Error(), "nil context") {
		t.Fatalf("connect nil = %v %v", c, err)
	}
	if present, err := HasServiceContext(absentDBusContext, nil, "service"); present || err == nil || !strings.Contains(err.Error(), "nil context") {
		t.Fatalf("service nil = %v %v", present, err)
	}
	if present, err := HasServiceContext(context.Background(), nil, "service"); present || err != nil {
		t.Fatalf("service no connection = %v %v", present, err)
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	if c, err := SessionBusAddressContext(ctx, ""); c != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("session expired = %v %v", c, err)
	}
	if c, err := ConnectContext(ctx, "unix:path=/nonexistent"); c != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("connect expired = %v %v", c, err)
	}
	if present, err := HasServiceContext(ctx, nil, "service"); present || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("service expired = %v %v", present, err)
	}
}

func TestDBusHandshakeFailureAndCancellationCleanup(t *testing.T) {
	sentinel := errors.New("authentication refused")
	if err := runHandshakeContext(context.Background(), func() error { return sentinel }, func() error { t.Error("closed successful handshake"); return nil }); !errors.Is(err, sentinel) {
		t.Fatalf("handshake error = %v", err)
	}
	if err := runHandshakeContext(absentDBusContext, func() error { t.Error("executed with nil context"); return nil }, func() error { return nil }); err == nil {
		t.Fatal("nil context accepted")
	}
	started := make(chan struct{})
	release := make(chan struct{})
	var closed atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runHandshakeContext(ctx, func() error { close(started); <-release; return sentinel }, func() error { closed.Add(1); close(release); return nil })
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled handshake = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("handshake did not stop")
	}
	if closed.Load() != 1 {
		t.Fatalf("transport closed %d times", closed.Load())
	}
}

// An intentionally absent context exercises the public nil-context boundary.
var absentDBusContext context.Context

func TestHasServiceNil(t *testing.T) {
	if HasService(nil, "org.freedesktop.DBus") {
		t.Fatal("HasService(nil) = true, want false")
	}
}

func TestHasServiceContextRejectsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if available, err := HasServiceContext(ctx, nil, "org.freedesktop.DBus"); available || !errors.Is(err, context.Canceled) {
		t.Fatalf("HasServiceContext() = (%v, %v), want (false, context.Canceled)", available, err)
	}
}

func TestSessionBusAddressInvalid(t *testing.T) {
	_, err := SessionBusAddress("unix:path=/tmp/nonexistent-dbus-socket-12345")
	if err == nil {
		t.Fatal("SessionBusAddress with invalid path succeeded, want error")
	}
}

func TestSessionBusAddressEmptyReturnsPrivateClosable(t *testing.T) {
	first, err := SessionBusAddress("")
	if err != nil {
		t.Skipf("no session bus available: %v", err)
	}
	defer func() { _ = first.Close() }()
	second, err := SessionBusAddress("")
	if err != nil {
		t.Fatalf("second SessionBusAddress() = %v", err)
	}
	defer func() { _ = second.Close() }()
	if first == second {
		t.Fatal("SessionBusAddress() returned the shared connection; want a private connection safe to Close")
	}
}

func TestRunHandshakeContextBoundsCancellationWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	release := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		result <- runHandshakeContext(ctx, func() error {
			close(started)
			<-release
			return nil
		}, func() error { return nil })
	}()
	<-started
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("handshake cancellation error = %v, want context.Canceled", err)
		}
	case <-time.After(handshakeCleanupTimeout + 200*time.Millisecond):
		t.Fatal("handshake cancellation waited beyond its cleanup bound")
	}
	close(release)
}

func TestDefaultSessionBusConnectionAuthenticatesAndRemainsPrivate(t *testing.T) {
	address := startSyntheticDefaultBus(t)
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", address)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var first, second error
	var available bool
	for ctx.Err() == nil {
		conn, err := SessionBusAddressContext(ctx, "")
		if err == nil {
			available, first = HasServiceContext(ctx, conn, "org.freedesktop.DBus")
			second = conn.Close()
			break
		}
		if errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	if first != nil || second != nil || !available {
		t.Fatalf("private default bus could not make authenticated call: available=%v, call=%v, close=%v", available, first, second)
	}
	firstConn, err := SessionBusAddressContext(ctx, "")
	if err != nil {
		t.Fatalf("reopening private session connection: %v", err)
	}
	secondConn, err := SessionBusAddressContext(ctx, "")
	if err != nil {
		_ = firstConn.Close()
		t.Fatalf("opening independent session connection: %v", err)
	}
	if firstConn == secondConn {
		t.Fatal("private session connections share a client pointer")
	}
	if closeErr := firstConn.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	available, err = HasServiceContext(ctx, secondConn, "org.freedesktop.DBus")
	if err != nil || !available {
		t.Fatalf("closing independent connection disrupted peer: available=%v err=%v", available, err)
	}
	if closeErr := secondConn.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
}

func startSyntheticDefaultBus(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("dbus-daemon"); err != nil {
		t.Skip("synthetic D-Bus daemon unavailable")
	}
	address := "unix:path=" + testutil.ShortSocketPath(t, "session-bus")
	process := exec.Command("dbus-daemon", "--session", "--nofork", "--address="+address)
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = process.Process.Kill(); _ = process.Wait() })
	return address
}

func TestHandshakeCannotAcknowledgeSuccessAfterCallerCanceledWithinReply(t *testing.T) {
	for i := 0; i < 120; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		var closes atomic.Int32
		err := runHandshakeContext(ctx, func() error { cancel(); return nil }, func() error { closes.Add(1); return nil })
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("iteration %d acknowledged successful D-Bus authentication after canceled caller: %v", i, err)
		}
		cancel()
	}
}
func TestHandshakeWithPreCanceledCallerDoesNotAttemptAuthentication(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var handshakes atomic.Int32
	err := runHandshakeContext(ctx, func() error { handshakes.Add(1); return nil }, func() error { return nil })
	if !errors.Is(err, context.Canceled) || handshakes.Load() != 0 {
		t.Fatalf("pre-canceled handshake ran %d times, result %v", handshakes.Load(), err)
	}
}

func TestSessionBusWithoutAddressFailsClosedOnUnreachableDefault(t *testing.T) {
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path="+testutil.ShortSocketPath(t, "missing"))
	conn, err := SessionBusAddressContext(context.Background(), "")
	if conn != nil {
		defer conn.Close()
	}
	if conn != nil || err == nil {
		t.Fatalf("unreachable default bus accepted: conn=%v err=%v", conn, err)
	}
}

func TestConnectContextRejectsPeerThatClosesBeforeAuthentication(t *testing.T) {
	listener, err := net.Listen("unix", testutil.ShortSocketPath(t, "fakebus"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	done := make(chan error, 1)
	go func() {
		peer, e := listener.Accept()
		if e != nil {
			done <- e
			return
		}
		done <- peer.Close()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := ConnectContext(ctx, "unix:path="+listener.Addr().String())
	if conn != nil {
		defer conn.Close()
	}
	if conn != nil || err == nil {
		t.Fatalf("unauthenticated peer accepted: conn=%v err=%v", conn, err)
	}
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-ctx.Done():
		t.Fatalf("fake bus not reached: %v", ctx.Err())
	}
}

func TestHasServiceOnClosedAuthenticatedBusReturnsError(t *testing.T) {
	address := startSyntheticDefaultBus(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*3)
	defer cancel()
	var connErr error
	var conn *dbus.Conn
	// Use the actual authenticated D-Bus connection rather than a mock method
	// result; failed ListNames calls must return errors rather than false success.
	for ctx.Err() == nil {
		c, e := ConnectContext(ctx, address)
		connErr = e
		if e == nil {
			conn = c
			break
		}
		time.Sleep(time.Millisecond * 5)
	}
	if conn == nil {
		t.Fatalf("private bus authentication unavailable: %v", connErr)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	// A closed concrete connection still reaches the D-Bus method-error path.
	present, err := HasServiceContext(context.Background(), conn, "org.freedesktop.DBus")
	if present || err == nil {
		t.Fatalf("dead D-Bus transport advertised service: %v %v", present, err)
	}
}

func TestHandshakeCanceledBetweenSuccessfulResponseAndPublication(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := runHandshakeContext(ctx, func() error { cancel(); return nil }, func() error { return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("late canceled handshake accepted: %v", err)
	}
}

func TestDefaultBusRejectsConnectionClosedBeforeAuthentication(t *testing.T) {
	socket := testutil.ShortSocketPath(t, "fake-default")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path="+socket)
	done := make(chan error, 1)
	go func() {
		peer, e := listener.Accept()
		if e != nil {
			done <- e
			return
		}
		done <- peer.Close()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := SessionBusAddressContext(ctx, "")
	if conn != nil {
		defer conn.Close()
	}
	if conn != nil || err == nil {
		t.Fatalf("incomplete default bus auth succeeded: %v %v", conn, err)
	}
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-ctx.Done():
		t.Fatal("did not reach malformed default bus peer")
	}
}

// This context cancels precisely at the post-handshake Err inspection. It
// models cancellation after the authentication reply but before the API
// publishes a connection, without relying on goroutine scheduling races.
type cancelOnPostHandshakeCheck struct {
	done   <-chan struct{}
	cancel context.CancelFunc
	checks atomic.Int32
}

func (c *cancelOnPostHandshakeCheck) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *cancelOnPostHandshakeCheck) Done() <-chan struct{}       { return c.done }
func (c *cancelOnPostHandshakeCheck) Value(any) any               { return nil }
func (c *cancelOnPostHandshakeCheck) Err() error {
	if c.checks.Add(1) == 2 {
		c.cancel()
	}
	select {
	case <-c.done:
		return context.Canceled
	default:
		return nil
	}
}

func TestHandshakeRejectsCancellationAfterReplyBeforePublication(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	wrapper := &cancelOnPostHandshakeCheck{done: ctx.Done(), cancel: cancel}
	calls := 0
	err := runHandshakeContext(wrapper, func() error { calls++; return nil }, func() error { return nil })
	if calls != 1 || !errors.Is(err, context.Canceled) {
		t.Fatalf("late cancellation not authoritative: calls=%d err=%v", calls, err)
	}
}
