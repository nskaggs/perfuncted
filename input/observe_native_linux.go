package input

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// nativeX11Peer accepts only a local, identifiable Xorg-family server.
// XDG_SESSION_TYPE alone is not sufficient: XWayland can inherit misleading
// environment variables. Unknown peers are rejected rather than guessed.
// This does not establish physical-versus-synthetic event provenance.
//
//nolint:gocyclo // Fail-closed local-Xorg peer, credentials and display validation
func nativeX11Peer(display string) error {
	if !strings.HasPrefix(display, ":") {
		return ErrObservationUnsupported
	}
	parts := strings.Split(strings.TrimPrefix(display, ":"), ".")
	if len(parts) > 2 {
		return ErrObservationUnsupported
	}
	if len(parts) == 2 {
		screen, screenErr := strconv.Atoi(parts[1])
		if screenErr != nil || screen < 0 || strconv.Itoa(screen) != parts[1] {
			return ErrObservationUnsupported
		}
	}
	number := parts[0]
	n, err := strconv.Atoi(number)
	if err != nil || n < 0 || n > 65535 || strconv.Itoa(n) != number {
		return ErrObservationUnsupported
	}
	dialer := net.Dialer{Timeout: time.Second}
	conn, err := dialer.Dial("unix", fmt.Sprintf("/tmp/.X11-unix/X%d", n))
	if err != nil {
		return fmt.Errorf("%w: X11 peer: %w", ErrObservationUnavailable, err)
	}
	defer conn.Close()
	return verifyNativeX11PeerConn(conn, os.Readlink)
}

// verifyNativeX11PeerConn isolates Unix-only socket authority from the
// display parser and permits deterministic non-Unix test inputs.
func verifyNativeX11PeerConn(conn net.Conn, readlink func(string) (string, error)) error {
	unix, ok := conn.(*net.UnixConn)
	if !ok {
		return ErrObservationUnsupported
	}
	return verifyNativeX11PeerSocket(unix, readlink)
}

type x11RawSocket interface {
	SyscallConn() (syscall.RawConn, error)
}

func verifyNativeX11PeerSocket(conn x11RawSocket, readlink func(string) (string, error)) error {
	raw, err := conn.SyscallConn()
	if err != nil {
		return fmt.Errorf("%w: X11 peer credentials: %w", ErrObservationUnavailable, err)
	}
	return verifyNativeX11PeerRaw(raw, readlink)
}

func verifyNativeX11PeerRaw(raw syscall.RawConn, readlink func(string) (string, error)) error {
	var cred *syscall.Ucred
	var syscallErr error
	if controlErr := raw.Control(func(fd uintptr) {
		cred, syscallErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); controlErr != nil {
		return fmt.Errorf("%w: X11 peer control: %w", ErrObservationUnavailable, controlErr)
	}
	if credErr := checkNativeX11Cred(cred, syscallErr); credErr != nil {
		return credErr
	}
	exe, err := readlink(fmt.Sprintf("/proc/%d/exe", cred.Pid))
	if err != nil {
		return ErrObservationUnsupported
	}
	switch filepath.Base(exe) {
	case "Xorg", "X", "Xvfb", "Xephyr":
		return nil
	default:
		return ErrObservationUnsupported
	}
}

func checkNativeX11Cred(cred *syscall.Ucred, err error) error {
	if err != nil || cred == nil || cred.Pid <= 0 {
		return ErrObservationUnsupported
	}
	return nil
}
