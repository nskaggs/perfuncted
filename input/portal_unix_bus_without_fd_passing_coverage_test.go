package input

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/env"
)

func TestOpenRemoteDesktopRejectsSessionBusWithoutUnixFDs(t *testing.T) {
	_, portal := startSyntheticPortalBus(t)
	address, negotiationRejected := startPortalUnixFDRejectingProxy(t, portal.busAddress)
	runtime := env.FromEnviron([]string{
		"WAYLAND_DISPLAY=/tmp/synthetic-wayland.sock",
		"DBUS_SESSION_BUS_ADDRESS=" + address,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	inputter, err := OpenRemoteDesktopContext(ctx, runtime, RemoteDesktopOptions{})
	if inputter != nil {
		t.Fatalf("session bus without descriptor passing returned inputter %T", inputter)
	}
	if err == nil || !errors.Is(err, ErrRemoteDesktopUnavailable) || !strings.Contains(err.Error(), "session bus does not support descriptor passing") {
		t.Fatalf("session bus without descriptor passing error = %v", err)
	}
	select {
	case <-negotiationRejected:
	default:
		t.Fatal("D-Bus client did not negotiate descriptor passing with the test bus")
	}
	portal.mu.Lock()
	calls := portal.called
	portal.mu.Unlock()
	if calls != 0 {
		t.Fatalf("portal received %d authorization requests before descriptor capability rejection", calls)
	}
}

func startPortalUnixFDRejectingProxy(t *testing.T, upstreamAddress string) (string, <-chan struct{}) {
	t.Helper()
	upstreamPath, ok := strings.CutPrefix(upstreamAddress, "unix:path=")
	if !ok || upstreamPath == "" {
		t.Fatalf("synthetic portal bus address %q is not a Unix path", upstreamAddress)
	}
	path := shortPrivateSocketPath(t, "portal-no-fd.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	negotiationRejected := make(chan struct{}, 1)
	proxyDone := make(chan error, 1)
	go func() {
		client, err := listener.AcceptUnix()
		if err != nil {
			proxyDone <- err
			return
		}
		upstream, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: upstreamPath, Net: "unix"})
		if err != nil {
			_ = client.Close()
			proxyDone <- err
			return
		}
		proxyDone <- rejectPortalUnixFDNegotiation(client, upstream, negotiationRejected)
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		select {
		case err := <-proxyDone:
			if err != nil && !portalProxyClosed(err) {
				t.Errorf("D-Bus Unix-FD proxy: %v", err)
			}
		case <-time.After(time.Second):
			t.Error("D-Bus Unix-FD proxy did not stop after the client closed")
		}
	})
	return "unix:path=" + path, negotiationRejected
}

func rejectPortalUnixFDNegotiation(client, upstream *net.UnixConn, rejected chan<- struct{}) (proxyErr error) {
	serverReplies := make(chan error, 1)
	go func() {
		_, err := io.Copy(client, upstream)
		serverReplies <- err
	}()
	defer func() {
		_ = client.Close()
		_ = upstream.Close()
		if err := <-serverReplies; proxyErr == nil && err != nil && !portalProxyClosed(err) {
			proxyErr = err
		}
	}()

	reader := bufio.NewReader(client)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if portalProxyClosed(err) {
				return nil
			}
			return err
		}
		command := strings.TrimSpace(line)
		if command == "NEGOTIATE_UNIX_FD" {
			if _, err := io.WriteString(client, "ERROR Unix descriptor passing unavailable\r\n"); err != nil {
				return err
			}
			rejected <- struct{}{}
			continue
		}
		if _, err := io.WriteString(upstream, line); err != nil {
			return err
		}
		if command == "BEGIN" {
			_, err := io.Copy(upstream, reader)
			if portalProxyClosed(err) {
				return nil
			}
			return err
		}
	}
}

func portalProxyClosed(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.EPIPE)
}
