//go:build linux

package input

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Reject invalid portal credentials before writing anything, without
// replacing a previously persisted grant or leaving temporary credentials
// behind. A rejected token cannot silently revoke an existing permission.
func TestPortalRestoreTokenRejectedReplacementPreservesDurableGrant(t *testing.T) {
	for _, tc := range []struct{ name, token string }{
		{"empty", ""},
		{"embedded NUL", "invalid\x00token"},
		{"newline", "invalid\ntoken"},
		{"carriage return", "invalid\rtoken"},
		{"invalid UTF8", string([]byte{0xff, 0xfe})},
		{"oversized", strings.Repeat("x", 4097)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt := portalTokenTestRuntime(t)
			lock, err := lockPortalTokenStore(rt)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Close()
			const previous = "previous-authorized-grant"
			if err := lock.store(previous); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(rt.Get("XDG_STATE_HOME"), "perfuncted")
			if err := lock.store(tc.token); err == nil {
				t.Fatalf("invalid replacement accepted: %q", tc.name)
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".restore-token-") {
					t.Fatalf("invalid replacement left staging file: %s", entry.Name())
				}
			}
			data, err := os.ReadFile(filepath.Join(dir, portalRestoreTokenFile))
			if err != nil || string(data) != previous+"\n" {
				t.Fatalf("invalid replacement altered durable grant: %q err=%v", data, err)
			}
			if got, err := lock.consume(); err != nil || got != previous {
				t.Fatalf("prior grant failed after rejected replacement: token=%q err=%v", got, err)
			}
		})
	}
}

// Maximum-length valid UTF-8 grants must round-trip without truncation,
// while a consumed credential must not be replayable by another reader.
func TestPortalRestoreTokenMaximumLengthRoundTripAndSingleUse(t *testing.T) {
	rt := portalTokenTestRuntime(t)
	lock, err := lockPortalTokenStore(rt)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	token := strings.Repeat("é", 2048)
	if len(token) != 4096 {
		t.Fatalf("test token len=%d", len(token))
	}
	if err := lock.store(token); err != nil {
		t.Fatal(err)
	}
	got, err := lock.consume()
	if err != nil || got != token {
		t.Fatalf("maximum UTF-8 token corrupted: length=%d err=%v", len(got), err)
	}
	if replay, err := lock.consume(); err != nil || replay != "" {
		t.Fatalf("consumed grant was replayable: %q err=%v", replay, err)
	}
}
