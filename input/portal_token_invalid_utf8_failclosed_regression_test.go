//go:build linux

package input

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPortalRestoreTokenRejectsMalformedUTF8FromProviderAndDisk(t *testing.T) {
	malformed := string([]byte{0xff, 0xfe, 0x80})
	runtime := portalTokenTestRuntime(t)
	lock, err := lockPortalTokenStore(runtime)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := lock.store(malformed); err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("malformed portal token accepted for persistent storage: %v", err)
	}
	tokenPath := filepath.Join(runtime.Get("XDG_STATE_HOME"), "perfuncted", portalRestoreTokenFile)
	if _, err := os.Stat(tokenPath); !os.IsNotExist(err) {
		t.Fatalf("invalid provider token persisted: %v", err)
	}
	if err := os.WriteFile(tokenPath, append([]byte(malformed), '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	value, err := lock.consume()
	if value != "" || err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Fatalf("malformed on-disk token consumed: %q %v", value, err)
	}
	if _, err := os.Stat(tokenPath); !os.IsNotExist(err) {
		t.Fatalf("malformed token not erased after read: %v", err)
	}
}
