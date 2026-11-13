package perfuncted

import "testing"

// A semantic target must not be treated as unchanged when its PID/bus/app
// identity disappears and only a non-unique human-readable name remains.
func TestLocatorIdentityRejectsAuthoritativeEvidenceDowngrade(t *testing.T) {
	initial := locatorIdentity{pid: 412, busName: ":1.412", appID: "org.editor.App", name: "Editor"}
	cases := []struct {
		name    string
		current locatorIdentity
		same    bool
	}{
		{"unchanged strong identity", initial, true},
		{"PID retained while optional app missing", locatorIdentity{pid: 412, name: "Editor"}, true},
		{"PID and bus lost, name retained", locatorIdentity{name: "Editor"}, false},
		{"PID lost, same app id", locatorIdentity{appID: "org.editor.App", name: "Editor"}, true},
		{"PID lost, different app id", locatorIdentity{appID: "org.other.App", name: "Editor"}, false},
		{"bus lost, name retained", locatorIdentity{name: "Editor", appID: "org.editor.App"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sameLocatorScope(initial, tc.current); got != tc.same {
				t.Fatalf("identity downgrade accepted=%v want=%v initial=%+v current=%+v", got, tc.same, initial, tc.current)
			}
		})
	}
}
