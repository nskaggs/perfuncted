package input

import (
	"context"
	"errors"
	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/dbusutil"
	"github.com/nskaggs/perfuncted/internal/env"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Authorization can disappear after the caller's preflight but before the
// unbuffered writer queue accepts its command. The waiting submit must
// unblock without executing input or reporting successful dispatch.
func TestRemoteDesktopQueuedInputAbortsOnLateAuthorityLoss(t *testing.T) {
	for _, tc := range []struct {
		name   string
		retire func(*RemoteDesktopBackend)
		want   error
	}{
		{name: "portal revoked while waiting", retire: func(b *RemoteDesktopBackend) {
			b.revokedState.Store(&remoteStateError{err: ErrRemoteDesktopRevoked})
			close(b.revoked)
		}, want: ErrRemoteDesktopRevoked},
		{name: "backend stopped while waiting", retire: func(b *RemoteDesktopBackend) { close(b.stop) }, want: ErrRemoteDesktopUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := syntheticQueuedPortal()
			// No receiver is permitted until the retirement signal has arrived.
			// A submit that ignores that signal will hang rather than complete.
			result := make(chan error, 1)
			invoked := make(chan struct{}, 1)
			go func() { result <- b.submit(context.Background(), func() error { invoked <- struct{}{}; return nil }) }()
			tc.retire(b)
			select {
			case err := <-result:
				if !errors.Is(err, tc.want) {
					t.Fatalf("retirement returned %v, expected %v", err, tc.want)
				}
			case <-time.After(time.Second):
				t.Fatal("retired portal command remained blocked on writer")
			}
			select {
			case <-invoked:
				t.Fatal("unauthorized input ran")
			default:
			}
		})
	}
}

// Cancellation must win over a successful writer receipt when the writer
// reports success at the instant its request's authorization expires.
func TestRemoteDesktopQueuedInputLateCanceledReceipt(t *testing.T) {
	b := syntheticQueuedPortal()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	received := make(chan remoteDesktopCommand, 1)
	go func() { received <- <-b.commands }()
	result := make(chan error, 1)
	go func() { result <- b.submit(ctx, func() error { return nil }) }()
	var cmd remoteDesktopCommand
	select {
	case cmd = <-received:
	case <-time.After(time.Second):
		t.Fatal("writer never accepted input")
	}
	cancel()
	cmd.done <- nil
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled receipt falsely succeeded: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled caller blocked on writer acknowledgement")
	}
}

// The same private bus connection can have multiple MatchSignal rules: a
// response handler must not treat an unrelated member as permission denial
// or an authoritative grant even when it arrives on the expected request path.
func TestPortalRequestIgnoresUnrelatedSignalMemberOnSameConnection(t *testing.T) {
	client, provider := startSyntheticPortalBus(t)
	provider.mu.Lock()
	provider.emitUnrelated = true
	provider.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	other := []dbus.MatchOption{
		dbus.WithMatchSender(remoteDesktopBus),
		dbus.WithMatchInterface(portalRequestInterface),
		dbus.WithMatchMember("Unexpected"),
	}
	if err := client.AddMatchSignalContext(ctx, other...); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.RemoveMatchSignalContext(context.Background(), other...) }()
	name, err := portalUniqueName(client.Names())
	if err != nil {
		t.Fatal(err)
	}
	handle, err := portalCreateSession(ctx, client, name)
	if err != nil || handle != syntheticSessionPath {
		t.Fatalf("unrelated member must not override authorized session: handle=%q error=%v", handle, err)
	}
}

// A bus peer must not authorize the caller by fabricating a Request.Response
// at the portal's returned object path. Although normal sessions use narrow
// match rules, godbus dispatches the union of all rules to each signal channel.
func TestPortalRequestRejectsSamePathResponseFromUntrustedBusSender(t *testing.T) {
	client, portal := startSyntheticPortalBus(t)
	pathReady := make(chan dbus.ObjectPath, 1)
	portal.mu.Lock()
	portal.skip = true
	portal.requestPaths = pathReady
	portal.mu.Unlock()
	attacker, err := dbusutil.ConnectContext(context.Background(), portal.busAddress)
	if err != nil {
		t.Fatal(err)
	}
	defer attacker.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Millisecond)
	defer cancel()
	wideRule := []dbus.MatchOption{
		dbus.WithMatchInterface(portalRequestInterface), dbus.WithMatchMember("Response"),
	}
	if lintShadow33 := client.AddMatchSignalContext(ctx, wideRule...); lintShadow33 != nil {
		t.Fatal(lintShadow33)
	}
	defer func() { _ = client.RemoveMatchSignalContext(context.Background(), wideRule...) }()
	name, err := portalUniqueName(client.Names())
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := portalCreateSession(ctx, client, name); done <- err }()
	var path dbus.ObjectPath
	select {
	case path = <-pathReady:
	case <-ctx.Done():
		t.Fatalf("portal method not dispatched: %v", ctx.Err())
	}
	forged := map[string]dbus.Variant{"session_handle": dbus.MakeVariant(syntheticSessionPath)}
	if err := attacker.Emit(path, portalRequestInterface+".Response", uint32(0), forged); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("untrusted peer forged a successful RemoteDesktop authorization")
		}
		if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, ErrRemoteDesktopUnavailable) {
			t.Fatalf("untrusted response leaked unexpected error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("forged response caused portal request to hang")
	}
}

// Every device and interface selected by a live portal EIS session is part
// of its authorization. A compositor pause or removal must retire the whole
// sender; unrelated EIS events must not revoke a different authorization.
func TestPortalSelectedEISDeviceAndInterfaceLossRevokesAuthorization(t *testing.T) {
	for _, tc := range []struct {
		name    string
		message eiMessage
	}{
		{"keyboard device removed", eiMessage{object: 0xff00000000000021, opcode: 0}},
		{"button device paused", eiMessage{object: 0xff00000000000022, opcode: 8}},
		{"scroll device removed", eiMessage{object: 0xff00000000000023, opcode: 0}},
		{"text device paused", eiMessage{object: 0xff00000000000024, opcode: 8}},
		{"pointer device removed", eiMessage{object: 0xff00000000000025, opcode: 0}},
		{"pointer device paused", eiMessage{object: 0xff00000000000025, opcode: 8}},
		{"keyboard interface removed", eiMessage{object: 101, opcode: 0}},
		{"button interface removed", eiMessage{object: 102, opcode: 0}},
		{"scroll interface removed", eiMessage{object: 103, opcode: 0}},
		{"text interface removed", eiMessage{object: 104, opcode: 0}},
		{"pointer interface removed", eiMessage{object: 105, opcode: 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, _, client := newSyntheticRevocableRemoteDesktop(t)
			b.sender.keyboardDev = 0xff00000000000021
			b.sender.buttonDev = 0xff00000000000022
			b.sender.scrollDev = 0xff00000000000023
			b.sender.textDev = 0xff00000000000024
			b.sender.pointers = []eiPointerDevice{{device: 0xff00000000000025, pointer: 105, regions: []eiRegion{{width: 128, height: 128, scale: 1}}}}
			// An event on an unrelated device or a global control object
			// cannot revoke a live portal session.
			client.messages <- eiMessage{object: 0, opcode: 0}
			client.messages <- eiMessage{object: 0xff00000000000090, opcode: 8}
			client.messages <- eiMessage{object: 105, opcode: 3}
			select {
			case <-b.revoked:
				t.Fatal("unrelated EIS event revoked session")
			case <-time.After(5 * time.Millisecond):
			}
			if err := b.stateError(); err != nil {
				t.Fatalf("unrelated event invalidated authorization: %v", err)
			}
			client.messages <- tc.message
			select {
			case <-b.revoked:
			case <-time.After(time.Second):
				t.Fatal("selected device lifecycle loss did not revoke portal")
			}
			if err := b.stateError(); !errors.Is(err, ErrRemoteDesktopRevoked) || !strings.Contains(err.Error(), "EIS session ended") {
				t.Fatalf("wrong revocation after %s: %v", tc.name, err)
			}
			if err := b.KeyDown(context.Background(), "a"); !errors.Is(err, ErrRemoteDesktopRevoked) {
				t.Fatalf("revoked EIS sender accepted input: %v", err)
			}
			select {
			case <-b.writerDone:
			case <-time.After(time.Second):
				t.Fatal("EIS writer remained active after revocation")
			}
		})
	}
}

// Startup is a series of authorization checkpoints, not a best-effort
// fallback. Each failed checkpoint must reject the session, and any session
// already acquired from the trusted portal service must be explicitly closed.
func TestSyntheticPortalStartupStageFailuresDoNotExposeInputAuthority(t *testing.T) { //nolint:gocyclo // Individual authorization checkpoints are one explicitly verified cleanup matrix.
	for _, tc := range []struct {
		name      string
		configure func(*testing.T, *syntheticPortal, env.Runtime)
		persist   bool
		want      error
		message   string
		attempts  int
		closes    int
	}{
		{name: "unsupported portal version", configure: func(_ *testing.T, p *syntheticPortal, _ env.Runtime) { p.getVersion = uint32(1) }, want: ErrRemoteDesktopUnavailable, attempts: 0, closes: 0},
		{name: "wrong version type", configure: func(_ *testing.T, p *syntheticPortal, _ env.Runtime) { p.getVersion = "version 2" }, want: ErrRemoteDesktopUnavailable, attempts: 0, closes: 0},
		{name: "version property read fails", configure: func(_ *testing.T, p *syntheticPortal, _ env.Runtime) {
			p.versionFailure = "synthetic properties failure"
		}, want: ErrRemoteDesktopUnavailable, attempts: 0, closes: 0},
		{name: "insecure restore token directory", configure: func(t *testing.T, _ *syntheticPortal, rt env.Runtime) {
			private := filepath.Join(rt.Get("XDG_STATE_HOME"), "perfuncted")
			if err := os.MkdirAll(private, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(private, 0777); err != nil {
				t.Fatal(err)
			}
		}, persist: true, message: "secure restore-token store", attempts: 0, closes: 0},
		{name: "malformed saved restore token", configure: func(t *testing.T, _ *syntheticPortal, rt env.Runtime) {
			lock, err := lockPortalTokenStore(rt)
			if err != nil {
				t.Fatal(err)
			}
			if err := lock.Close(); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(rt.Get("XDG_STATE_HOME"), "perfuncted", portalRestoreTokenFile)
			if err := os.WriteFile(path, []byte("untrusted credential without newline"), 0600); err != nil {
				t.Fatal(err)
			}
		}, persist: true, message: "consume restore token", attempts: 0, closes: 0},
		{name: "create session rejected", configure: func(_ *testing.T, p *syntheticPortal, _ env.Runtime) { p.createFailure = "synthetic create failed" }, want: ErrRemoteDesktopUnavailable, attempts: 1, closes: 0},
		{name: "invalid session handle", configure: func(_ *testing.T, p *syntheticPortal, _ env.Runtime) {
			p.result = map[string]dbus.Variant{"session_handle": dbus.MakeVariant("")}
		}, want: ErrRemoteDesktopUnavailable, message: "invalid session handle", attempts: 1, closes: 0},
		{name: "select devices denied", configure: func(_ *testing.T, p *syntheticPortal, _ env.Runtime) { p.selectCode = 1 }, want: ErrRemoteDesktopDenied, attempts: 1, closes: 1},
		{name: "start denied", configure: func(_ *testing.T, p *syntheticPortal, _ env.Runtime) { p.startCode = 1 }, want: ErrRemoteDesktopDenied, attempts: 1, closes: 1},
		{name: "missing pointer authority", configure: func(_ *testing.T, p *syntheticPortal, _ env.Runtime) { p.deviceMask = portalDeviceKeyboard }, want: ErrRemoteDesktopDenied, message: "did not authorize keyboard and pointer", attempts: 1, closes: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, portal := startSyntheticPortalBus(t)
			root := privatePortalTokenFixtureDir(t)
			rt := env.FromEnviron([]string{
				"WAYLAND_DISPLAY=/tmp/pf-test-fake-wayland.sock",
				"DBUS_SESSION_BUS_ADDRESS=" + portal.busAddress,
				"XDG_STATE_HOME=" + root,
			})
			portal.mu.Lock()
			if tc.configure != nil {
				tc.configure(t, portal, rt)
			}
			portal.mu.Unlock()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			backend, err := OpenRemoteDesktopContext(ctx, rt, RemoteDesktopOptions{PersistPermission: tc.persist})
			if backend != nil || err == nil {
				if backend != nil {
					_ = backend.Close()
				}
				t.Fatalf("failed checkpoint granted input: backend=%v err=%v", backend, err)
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("checkpoint err=%v, want %v", err, tc.want)
			}
			if tc.message != "" && !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("checkpoint err=%v, want %q", err, tc.message)
			}
			portal.mu.Lock()
			requests, closes := portal.called, portal.closedSessions
			portal.mu.Unlock()
			if requests != tc.attempts || closes != tc.closes {
				t.Fatalf("resource ownership mismatch: createRequests=%d, closeCalls=%d, want %d,%d; error=%v", requests, closes, tc.attempts, tc.closes, err)
			}
		})
	}
}
