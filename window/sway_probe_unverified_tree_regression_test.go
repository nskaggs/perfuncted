package window

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"testing"

	"github.com/nskaggs/perfuncted/internal/env"
)

// A socket that echoes the i3 IPC message type is not sufficient evidence of
// a real Sway compositor. Discovery must validate its GET_TREE body before
// granting a manager that can subsequently issue commands on that socket.
func TestSwayDiscoveryRejectsSocketWithInvalidTreeEvidence(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"malformed JSON", "not-json", false},
		{"empty response", "", false},
		{"untyped JSON", `[]`, false},
		{"missing tree identity", `{}`, false},
		{"zero root ID", `{"id":0,"type":"root"}`, false},
		{"wrong root node type", `{"id":1,"type":"con"}`, false},
		{"plausible Sway root", `{"id":1,"type":"root","nodes":[]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			socket := filepath.Join(t.TempDir(), "synthetic-ipc.sock")
			listener, err := net.Listen("unix", socket)
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			serverErr := make(chan error, 1)
			go func() {
				conn, acceptErr := listener.Accept()
				if acceptErr != nil {
					serverErr <- acceptErr
					return
				}
				defer conn.Close()
				typ, body, readErr := readSwayMessage(conn)
				if readErr == nil && (typ != swayMsgGetTree || len(body) != 0) {
					readErr = errors.New("unexpected discovery request")
				}
				if readErr == nil {
					readErr = writeSwayMessage(conn, swayMsgGetTree, tc.body)
				}
				serverErr <- readErr
			}()
			manager, err := NewSwayManagerRuntimeContext(context.Background(), env.FromEnviron([]string{"SWAYSOCK=" + socket}))
			if tc.valid {
				if err != nil || manager == nil {
					t.Fatalf("valid compositor probe rejected: %+v %v", manager, err)
				}
				if closeErr := manager.Close(); closeErr != nil {
					t.Fatal(closeErr)
				}
			} else if err == nil || manager != nil {
				t.Fatalf("unverified tree accepted as authoritative compositor: %+v %v", manager, err)
			}
			if err := <-serverErr; err != nil {
				t.Fatalf("synthetic compositor server failed: %v", err)
			}
		})
	}
}
