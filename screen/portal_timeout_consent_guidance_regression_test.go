package screen

import (
	"strings"
	"testing"
	"time"
)

func TestPortalScreenshotTimeoutExplainsConsentAndReadOnlyAlternative(t *testing.T) {
	err := portalScreenshotTimeoutError(15*time.Second, portalProtocolTimeout, 15*time.Second)
	if err == nil {
		t.Fatal("portal timeout must be an error")
	}
	for _, want := range []string{
		"timed out waiting for screenshot",
		"policy/protocol 15s/30s, effective 15s",
		"screenshot permission",
		"pf output list",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("timeout %q missing %q", err, want)
		}
	}
}
