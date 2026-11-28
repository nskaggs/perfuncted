package workflow

import (
	"reflect"
	"testing"
)

func TestSplitShellPreservesEmptyQuotedArgs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		line string
		want []string
	}{
		{
			name: "empty double quoted arg",
			line: `input type ""`,
			want: []string{"input", "type", ""},
		},
		{
			name: "empty single quoted arg",
			line: `window find title=''`,
			want: []string{"window", "find", "title="},
		},
		{
			name: "multiple empty args",
			line: `input type "" ''`,
			want: []string{"input", "type", "", ""},
		},
		{
			name: "quoted empty prefix keeps token",
			line: `input type ""suffix`,
			want: []string{"input", "type", "suffix"},
		},
		{
			name: "quoted empty suffix keeps token",
			line: `input type prefix""`,
			want: []string{"input", "type", "prefix"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := SplitShell(tt.line)
			if err != nil {
				t.Fatalf("SplitShell(%q) error = %v", tt.line, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("SplitShell(%q) = %#v, want %#v", tt.line, got, tt.want)
			}
		})
	}
}

func TestScriptRunnerQuotingPreservesEmptyAndEscapedArguments(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  []string
		bad   bool
	}{
		{"input type 'two words'", []string{"input", "type", "two words"}, false},
		{`input type ""`, []string{"input", "type", ""}, false},
		{`input type hello\ world`, []string{"input", "type", "hello world"}, false},
		{`input type "one 'inner' word"`, []string{"input", "type", "one 'inner' word"}, false},
		{`input type 'one "inner" word'`, []string{"input", "type", "one \"inner\" word"}, false},
		{`input type "unterminated`, nil, true},
		{`input type trailing\`, nil, true},
	} {
		got, err := SplitShell(tc.input)
		if (err != nil) != tc.bad || (!tc.bad && !reflect.DeepEqual(got, tc.want)) {
			t.Errorf("SplitShell(%q)=%#v,%v; want %#v error=%v", tc.input, got, err, tc.want, tc.bad)
		}
	}
}

// Single quotes protect every enclosed byte from shell escape interpretation.
// Backslashes in a literal file path, regex, or command must reach the input
// backend unchanged; outside quotes they still escape the following byte.
func TestScriptSingleQuotedBackslashesRemainLiteral(t *testing.T) {
	for _, tc := range []struct {
		name, line string
		want       []string
	}{
		{"path", `input type 'C:\Users\Public\notes.txt'`, []string{"input", "type", `C:\Users\Public\notes.txt`}},
		{"regex", `input type '^\w+\s+\d+$'`, []string{"input", "type", `^\w+\s+\d+$`}},
		{"escaped outside", `input type one\ two`, []string{"input", "type", "one two"}},
		{"literal trailing slash", `input type 'tail\'`, []string{"input", "type", `tail\`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SplitShell(tc.line)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("SplitShell(%q) = %#v, %v; want %#v", tc.line, got, err, tc.want)
			}
		})
	}
}
