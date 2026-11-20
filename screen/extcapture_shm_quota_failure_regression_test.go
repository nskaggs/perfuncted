//go:build linux

package screen

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/wl"
)

func TestExtCaptureSharedMemoryQuotaFailureDoesNotPublishPixels(t *testing.T) {
	const childEnv = "PF_TEST_EXT_CAPTURE_QUOTA"
	if os.Getenv(childEnv) != "1" {
		args := []string{"-test.run=^TestExtCaptureSharedMemoryQuotaFailureDoesNotPublishPixels$", "-test.timeout=10s"}
		if coverageDir := flag.Lookup("test.gocoverdir"); coverageDir != nil && coverageDir.Value.String() != "" {
			args = append(args, "-test.gocoverdir="+coverageDir.Value.String())
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], args...)
		cmd.Env = append(os.Environ(), childEnv+"=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated capture quota test: %v\n%s", err, output)
		}
		return
	}

	dir, err := os.MkdirTemp("/var/tmp", "pf-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	t.Setenv("XDG_RUNTIME_DIR", dir)
	socket := filepath.Join(dir, "wayland")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	serverDone := make(chan error, 1)
	go func() {
		conn, err := listener.AcceptUnix()
		if err != nil {
			serverDone <- err
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		request := make([]byte, 12)
		if _, err := io.ReadFull(conn, request); err != nil {
			serverDone <- err
			return
		}
		for _, event := range []struct {
			sender, opcode uint32
			body           []byte
		}{
			{3, 0, wlUint32Payload(2, 2)},
			{3, 1, wlUint32Payload(0)},
			{wl.Uint32(request[8:]), 0, nil},
		} {
			if err := writeExtTestEvent(conn, event.sender, event.opcode, event.body); err != nil {
				serverDone <- err
				return
			}
		}
		var header [8]byte
		_, err = io.ReadFull(conn, header[:])
		switch {
		case errors.Is(err, io.EOF):
			serverDone <- nil
		case err != nil:
			serverDone <- err
		default:
			serverDone <- fmt.Errorf("allocation failure sent Wayland request for object %d", wl.Uint32(header[:4]))
		}
	}()

	wlctx, err := wl.Connect(socket)
	if err != nil {
		t.Fatal(err)
	}
	shm := &wl.Shm{}
	session := &wlRawProxy{}
	wlctx.Register(shm)
	wlctx.Register(session)
	backend := &ExtCaptureBackend{session: &wl.Session{Sock: socket, Ctx: wlctx, Display: wl.NewDisplay(wlctx)}, shm: shm, sessProxy: session}
	signal.Ignore(syscall.SIGXFSZ)
	var original syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_FSIZE, &original); err != nil {
		t.Fatal(err)
	}
	limited := original
	limited.Cur = 0
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &limited); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	hash, captureErr := backend.GrabFullHash(ctx)
	cancel()
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &original); err != nil {
		t.Fatal(err)
	}
	if hash != 0 || !errors.Is(captureErr, syscall.EFBIG) || !strings.Contains(captureErr.Error(), "shm file") {
		t.Fatalf("quota failure returned hash=%08x err=%v", hash, captureErr)
	}
	if backend.cachedFd != nil || backend.cachedBuf != nil {
		t.Fatal("failed allocation retained shared memory")
	}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-serverDone:
		if err != nil {
			t.Fatalf("synthetic compositor: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("compositor did not observe connection closure")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "perfuncted-shm-") {
			t.Fatalf("failed allocation leaked shared memory file %q", entry.Name())
		}
	}
}
