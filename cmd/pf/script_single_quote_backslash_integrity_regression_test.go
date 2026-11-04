package main

import (
	"reflect"
	"strings"
	"testing"
)

// Single quotes protect every enclosed byte from shell escape interpretation.
// Backslashes in a literal file path, regex, or command must reach the input
// backend unchanged; outside quotes they still escape the following byte.
func TestScriptSingleQuotedBackslashesRemainLiteral(t *testing.T) {
	for _, tc := range []struct {
		name, line string
		want       []string
	}{
		{"path", `input type-literal 'C:\Users\Public\notes.txt'`, []string{"input", "type-literal", `C:\Users\Public\notes.txt`}},
		{"regex", `input type-literal '^\w+\s+\d+$'`, []string{"input", "type-literal", `^\w+\s+\d+$`}},
		{"escaped outside", `input type-literal one\ two`, []string{"input", "type-literal", "one two"}},
		{"literal trailing slash", `input type-literal 'tail\'`, []string{"input", "type-literal", `tail\`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := splitShell(tc.line)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("splitShell(%q) = %#v, %v; want %#v", tc.line, got, err, tc.want)
			}
		})
	}
}

func TestScriptLiteralInputPreservesQuotedBackslashesEndToEnd(t *testing.T) {
	s, input, _, _ := newScriptFixture(t)
	value := `C:\Users\Public\notes.txt`
	if err := s.run(strings.NewReader("input type-literal '" + value + "'\n")); err != nil {
		t.Fatal(err)
	}
	if len(input.Calls) != 1 || input.Calls[0] != "type-literal:"+value {
		t.Fatalf("script changed literal keyboard content: %#v", input.Calls)
	}
}
