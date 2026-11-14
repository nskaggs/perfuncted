package main

import "testing"

// The environment flag accepts explicit opt-in tokens only. In particular,
// whitespace, case and misleading prefixes must not change its meaning.
func TestEnvBoolExplicitOptInTokens(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  bool
	}{
		{"1", true}, {"TRUE", true}, {" Yes ", true}, {"On", true},
		{"", false}, {"0", false}, {"false", false}, {"off", false},
		{"2", false}, {"trueish", false}, {"yes please", false},
	} {
		t.Run(tc.input, func(t *testing.T) {
			if got := envBool(tc.input); got != tc.want {
				t.Fatalf("envBool(%q) = %t, want %t", tc.input, got, tc.want)
			}
		})
	}
}
