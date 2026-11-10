package perfuncted

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

// Managed runtime paths are literal. Glob metacharacters in their ancestors
// must not redirect socket discovery to another session or hide a real socket.
func TestSwaySocketUsesLiteralRuntimeDirectory(t *testing.T) {
	root := t.TempDir()
	own := filepath.Join(root, "private[ab]")
	foreign := filepath.Join(root, "privatea")
	for _, dir := range []string{own, foreign} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	listen := func(dir string) (string, net.Listener) {
		path := filepath.Join(dir, "sway-ipc.123.sock")
		listener, err := net.Listen("unix", path)
		if err != nil {
			t.Fatal(err)
		}
		return path, listener
	}
	outside, foreignListener := listen(foreign)
	defer foreignListener.Close()
	expected, ownListener := listen(own)
	defer ownListener.Close()
	session := &Session{infra: &sessionInfra{xdgDir: own}}
	if got := session.SwaySocket(); got != expected {
		t.Fatalf("runtime %q resolved socket %q, want own %q (foreign %q)", own, got, expected, outside)
	}
}

func TestSwaySocketSupportsInvalidGlobCharacterInLiteralRuntime(t *testing.T) {
	root, err := os.MkdirTemp("/var/tmp", "pfsock-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	own := filepath.Join(root, "private[unfinished")
	if err := os.Mkdir(own, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(own, "sway-ipc.local.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	session := &Session{infra: &sessionInfra{xdgDir: own}}
	if got := session.SwaySocket(); got != path {
		t.Fatalf("literal bracket runtime socket=%q want %q", got, path)
	}
}
