package accessibility

import (
	"testing"

	"github.com/godbus/dbus/v5"
)

// Providers use both bare and qualified property names; protecting only the
// exact word "Value" allows alternate current-value properties to disclose
// editor contents in diagnostic and event streams.
func TestATSPIEventValueAliasesNeverPublishSensitivePayload(t *testing.T) {
	for _, property := range []string{"Value", " CurrentValue ", "Accessible-Value", "accessible:value", "Value:Changed", "Text", "PasswordHint", "ProtectedText"} {
		t.Run(property, func(t *testing.T) {
			sig := &dbus.Signal{Sender: "org.example.App", Path: "/editor", Name: "org.a11y.atspi.Event.Object:PropertyChange", Body: []any{property, "private-entry-secret"}}
			if ev := signalEvent(sig); ev.Value != "" {
				t.Fatalf("sensitive %q event exposed %q", property, ev.Value)
			}
		})
	}
	for _, property := range []string{"Name", "Description", "Enabled"} {
		sig := &dbus.Signal{Sender: "org.example.App", Path: "/editor", Name: "org.a11y.atspi.Event.Object:PropertyChange", Body: []any{property, "public-control-label"}}
		if ev := signalEvent(sig); ev.Value != "public-control-label" {
			t.Fatalf("public %q event value unexpectedly redacted", property)
		}
	}
}
