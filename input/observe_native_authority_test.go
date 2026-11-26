package input

import (
	"errors"
	"github.com/nskaggs/perfuncted/v2/internal/testutil"
	"net"
	"os"
	"syscall"
	"testing"
)

type observationRawSocket struct {
	raw syscall.RawConn
	err error
}

func (s observationRawSocket) SyscallConn() (syscall.RawConn, error) { return s.raw, s.err }

type observationRawConn struct {
	err    error
	invoke bool
	fd     uintptr
}

func (r observationRawConn) Control(cb func(uintptr)) error {
	if r.err != nil {
		return r.err
	}
	if r.invoke {
		cb(r.fd)
	}
	return nil
}
func (r observationRawConn) Read(func(uintptr) bool) error  { return nil }
func (r observationRawConn) Write(func(uintptr) bool) error { return nil }

func TestNativeX11PeerCredentialFailures(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	if err := verifyNativeX11PeerConn(left, os.Readlink); !errors.Is(err, ErrObservationUnsupported) {
		t.Fatal(err)
	}

	if err := verifyNativeX11PeerSocket(observationRawSocket{err: os.ErrClosed}, os.Readlink); !errors.Is(err, ErrObservationUnavailable) {
		t.Fatal(err)
	}
	if err := verifyNativeX11PeerRaw(observationRawConn{err: os.ErrPermission}, os.Readlink); !errors.Is(err, ErrObservationUnavailable) {
		t.Fatal(err)
	}
	if err := verifyNativeX11PeerRaw(observationRawConn{}, os.Readlink); !errors.Is(err, ErrObservationUnsupported) {
		t.Fatal(err)
	}
	if err := verifyNativeX11PeerRaw(observationRawConn{invoke: true, fd: ^uintptr(0)}, os.Readlink); !errors.Is(err, ErrObservationUnsupported) {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		cred *syscall.Ucred
		err  error
		want error
	}{
		{nil, nil, ErrObservationUnsupported},
		{&syscall.Ucred{Pid: 0}, nil, ErrObservationUnsupported},
		{&syscall.Ucred{Pid: -1}, nil, ErrObservationUnsupported},
		{&syscall.Ucred{Pid: 12}, os.ErrPermission, ErrObservationUnsupported},
		{&syscall.Ucred{Pid: 12}, nil, nil},
	} {
		if got := checkNativeX11Cred(tc.cred, tc.err); !errors.Is(got, tc.want) {
			t.Fatalf("%+v: got %v expected %v", tc, got, tc.want)
		}
	}
}

func TestNativeX11PeerExecutableAuthority(t *testing.T) {
	path := testutil.ShortSocketPath(t, "peer.sock")
	server, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	client, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	unix, ok := client.(*net.UnixConn)
	if !ok {
		t.Fatalf("unexpected peer type %T", client)
	}
	for _, tc := range []struct {
		label    string
		readlink func(string) (string, error)
		want     error
	}{
		{"xorg", func(string) (string, error) { return "/usr/bin/Xorg", nil }, nil},
		{"xvfb", func(string) (string, error) { return "/usr/bin/Xvfb", nil }, nil},
		{"unknown", func(string) (string, error) { return "/usr/bin/Xwayland", nil }, ErrObservationUnsupported},
		{"missing-proc", func(string) (string, error) { return "", os.ErrNotExist }, ErrObservationUnsupported},
	} {
		t.Run(tc.label, func(t *testing.T) {
			got := verifyNativeX11PeerSocket(unix, tc.readlink)
			if !errors.Is(got, tc.want) {
				t.Fatalf("got %v expected %v", got, tc.want)
			}
		})
	}
}
