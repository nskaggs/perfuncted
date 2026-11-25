package util

import (
	"errors"
	"testing"
)

func TestIsNilHandlesAllNilableKindsAndOpaqueValues(t *testing.T) {
	var p *int
	var m map[string]int
	var s []byte
	var ch chan int
	var fn func()
	var it interface{} = p
	for _, tc := range []struct {
		name  string
		value any
		nil   bool
	}{
		{"untyped nil", nil, true},
		{"typed pointer", p, true},
		{"typed map", m, true},
		{"typed slice", s, true},
		{"typed channel", ch, true},
		{"typed function", fn, true},
		{"nil under interface", it, true},
		{"integer", 0, false},
		{"string", "", false},
		{"array", [2]int{}, false},
		{"concrete pointer", new(int), false},
		{"concrete slice", []int{}, false},
		{"concrete map", map[string]int{}, false},
		{"concrete function", func() {}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsNil(tc.value); got != tc.nil {
				t.Fatalf("IsNil(%T)=%t want %t", tc.value, got, tc.nil)
			}
			err := CheckAvailable("device", tc.value)
			if tc.nil && (!errors.Is(err, ErrNotAvailable) || err.Error() != "device: not available") {
				t.Fatalf("typed nil error: %v", err)
			}
			if !tc.nil && err != nil {
				t.Fatalf("valid resource error: %v", err)
			}
		})
	}
}
