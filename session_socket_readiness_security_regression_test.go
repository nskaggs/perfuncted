package perfuncted

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestManagedSessionSocketReadinessRejectsFilesDirectoriesAndSymlinks(t *testing.T) {
	root := t.TempDir()
	regular := filepath.Join(root, "regular")
	if err := os.WriteFile(regular, []byte("not a socket"), 0600); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, "directory")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "symlink")
	socket := filepath.Join(root, "socket")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := os.Symlink(socket, link); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, path string
		ready      bool
	}{
		{"regular file", regular, false},
		{"directory", directory, false},
		{"symlink to socket", link, false},
		{"real unix socket", socket, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := waitForSocket(context.Background(), tc.path, 1, time.Millisecond)
			if tc.ready && err != nil {
				t.Fatalf("real socket rejected: %v", err)
			}
			if !tc.ready && err == nil {
				t.Fatal("non-socket filesystem entry spoofed readiness")
			}
		})
	}
}
