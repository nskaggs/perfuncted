package perfuncted

import (
	"testing"
	"time"
)

func TestTimeoutProvenanceTracksExplicitAndDefaultFields(t *testing.T) {
	configured := TimeoutPolicy{Medium: 17 * time.Second}
	session := &Session{
		config:          SessionConfig{Timeouts: configured.WithDefaults()},
		timeoutInput:    configured,
		hasTimeoutInput: true,
	}

	got := session.timeoutProvenance()
	for field, want := range map[string]string{
		"short":      "library_default",
		"medium":     "session_config",
		"long":       "library_default",
		"startup":    "library_default",
		"diagnostic": "library_default",
		"poll":       "library_default",
	} {
		if got[field] != want {
			t.Errorf("timeout provenance for %s = %q, want %q", field, got[field], want)
		}
	}
}
