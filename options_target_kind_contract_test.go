package perfuncted

import "testing"

func TestDesktopTargetKindDefaultsToHostAndPreservesExplicitKind(t *testing.T) {
	if got := (DesktopTarget{}).Kind(); got != TargetHost {
		t.Fatalf("zero target kind = %q, want %q", got, TargetHost)
	}
	for _, kind := range []TargetKind{TargetExplicit, TargetHeadless} {
		if got := (DesktopTarget{kind: kind}).Kind(); got != kind {
			t.Errorf("target kind = %q, want %q", got, kind)
		}
	}
}
