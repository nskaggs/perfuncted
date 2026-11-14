package main

import "testing"

// Diagnostic output must reveal presence, not the value of a secret-bearing setting.
func TestDiagnosticValueRedactsNonemptySettings(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"", ""},
		{"secret-token", "<set>"},
		{" ", "<set>"},
		{"0", "<set>"},
	} {
		if got := diagnosticValue(tc.input); got != tc.want {
			t.Errorf("diagnosticValue(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}
