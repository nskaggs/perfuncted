package perfuncted

import (
	"errors"
	"github.com/nskaggs/perfuncted/accessibility"
	"strings"
	"testing"
)

// Provider-originated action outcomes must be reduced to a fixed public
// vocabulary; otherwise arbitrary strings can leak into diagnostic artifacts.
func TestDiagnosticActionOutcomesRejectUnknownProviderValues(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  accessibility.DispatchOutcome
		want string
	}{
		{"accepted", accessibility.DispatchAccepted, "accepted"},
		{"rejected", accessibility.DispatchRejected, "rejected"},
		{"unknown", accessibility.DispatchUnknown, "unknown"},
		{"not sent", accessibility.DispatchNotSent, "not-sent"},
		{"empty", "", "unknown"},
		{"untrusted text", accessibility.DispatchOutcome("private window title /home/account/session"), "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := diagnosticDispatch(tc.raw); got != tc.want {
				t.Fatalf("diagnosticDispatch(%q)=%q, want %q", tc.raw, got, tc.want)
			}
		})
	}
	for _, tc := range []struct {
		raw  ActionOutcomeStatus
		want string
	}{
		{ActionOutcomeNotObserved, "not-observed"},
		{ActionOutcomeVerified, "verified"},
		{ActionOutcomeNotVerified, "not-verified"},
		{"", "not-observed"},
		{"private desktop content", "not-observed"},
	} {
		if got := diagnosticOutcome(tc.raw); got != tc.want {
			t.Fatalf("diagnosticOutcome(%q)=%q, want %q", tc.raw, got, tc.want)
		}
	}
}

// Unknown capability names can originate outside the session's declared
// capabilities; never publish them as an arbitrary startup phase.
func TestStartupCapabilityPhaseUsesFixedVocabulary(t *testing.T) {
	for _, tc := range []struct {
		capability Capability
		phase      StartupPhase
	}{
		{CapabilityScreen, StartupPhaseCapabilityScreen},
		{CapabilityInput, StartupPhaseCapabilityInput},
		{CapabilityWindows, StartupPhaseCapabilityWindows},
		{CapabilityOutputs, StartupPhaseCapabilityOutputs},
		{CapabilityClipboard, StartupPhaseCapabilityClipboard},
		{CapabilityAccessibility, StartupPhaseCapabilityAccessibility},
		{Capability("private-backend-path"), ""},
		{"", ""},
	} {
		if got := capabilityStartupPhase(tc.capability); got != tc.phase {
			t.Fatalf("capabilityStartupPhase(%q)=%q, want %q", tc.capability, got, tc.phase)
		}
	}
}

// An absent clipboard facade is not an authorization to inspect the desktop.
func TestNilClipboardFacadeCannotPassAvailabilityGate(t *testing.T) {
	var clipboard *ClipboardBundle
	if err := clipboard.checkAvailable("get"); !errors.Is(err, ErrNilSession) {
		t.Fatalf("nil clipboard availability: %v, want ErrNilSession", err)
	}
}

// An explicit GOWORK file may be located under private directories and
// include user or project identifiers. A persistent bundle should describe
// build mode, never copy that filesystem path into diagnostic metadata.
func TestDiagnosticBuildInfoRedactsExplicitGoWorkspacePath(t *testing.T) {
	const secret = "/home/private-user/projects/internal-product/workspace.go.work"
	for _, tc := range []struct{ value, want string }{
		{"", "default"},
		{"off", "off"},
		{secret, "on"},
	} {
		t.Run(tc.want+"-"+strings.ReplaceAll(tc.value, "/", "_"), func(t *testing.T) {
			t.Setenv("GOWORK", tc.value)
			info := currentBuildInfo()
			if info.WorkspaceMode != tc.want {
				t.Fatalf("workspace mode=%q, want %q", info.WorkspaceMode, tc.want)
			}
			if strings.Contains(info.WorkspaceMode, secret) {
				t.Fatalf("workspace path exposed in diagnostic: %q", info.WorkspaceMode)
			}
		})
	}
}
