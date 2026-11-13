package accessibility

import (
	"context"
	"testing"
)

// PIDHint is still an ownership boundary when the compositor cannot supply a
// primary PID. Neither a matching title nor an application name may promote
// a window from a different process into an authorized candidate.
func TestWindowApplicationPIDHintDoesNotDowngradeOwnershipProof(t *testing.T) {
	for _, tc := range []struct {
		name   string
		app    Application
		target WindowTarget
		want   bool
	}{
		{"matching PID hint with divergent app id", Application{Node: Node{Name: "Firefox"}, PID: 42}, WindowTarget{PIDHint: 42, AppID: "org.mozilla.Firefox", Title: "Browser Console"}, true},
		{"mismatching PID hint even with title", Application{Node: Node{Name: "Browser Console"}, PID: 43}, WindowTarget{PIDHint: 42, Title: "Browser Console"}, false},
		{"missing PID cannot satisfy hint", Application{Node: Node{Name: "Browser Console"}}, WindowTarget{PIDHint: 42, Title: "Browser Console"}, false},
		{"primary PID overrides conflicting hint", Application{Node: Node{Name: "Firefox"}, PID: 44}, WindowTarget{PID: 44, PIDHint: 42, Title: "Browser Console"}, true},
		{"primary PID rejects matching hint only", Application{Node: Node{Name: "Firefox"}, PID: 42}, WindowTarget{PID: 44, PIDHint: 42, Title: "Browser Console"}, false},
		{"app ID alone requires identity match", Application{Node: Node{Name: "Firefox", Description: "org.mozilla.Firefox"}, PID: 42}, WindowTarget{AppID: "org.mozilla.Firefox"}, true},
		{"app ID alone rejects unrelated name", Application{Node: Node{Name: "Text Editor"}, PID: 42}, WindowTarget{AppID: "org.mozilla.Firefox"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := windowAppMatches(tc.app, tc.target); got != tc.want {
				t.Fatalf("app=%+v target=%+v matched=%v want=%v", tc.app, tc.target, got, tc.want)
			}
		})
	}
}

// Candidate discovery must reject mismatching process ownership before
// contacting any accessibility backend, including for an active/title-matched
// window that otherwise appears highly credible.
func TestWindowCandidatesSkipUnownedApplicationsBeforeBackendAccess(t *testing.T) {
	var backend *dbusBackend
	apps := []Application{
		{Node: Node{ID: NodeID{BusName: "org.unknown", ObjectPath: "/application", Generation: 1}, Name: "Browser Console"}, PID: 99},
		{Node: Node{ID: NodeID{BusName: "org.missing", ObjectPath: "/application", Generation: 1}, Name: "Browser Console"}, PID: 0},
	}
	target := WindowTarget{ID: "window1", PIDHint: 42, Title: "Browser Console", Active: true}
	candidates := backend.windowCandidates(context.Background(), target, apps)
	if len(candidates) != 0 {
		t.Fatalf("unowned candidates exposed: %+v", candidates)
	}
}
