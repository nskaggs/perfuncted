package main

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

			got, err := splitShell(tt.line)
			if err != nil {
				t.Fatalf("splitShell(%q) error = %v", tt.line, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("splitShell(%q) = %#v, want %#v", tt.line, got, tt.want)
			}
		})
	}
}
