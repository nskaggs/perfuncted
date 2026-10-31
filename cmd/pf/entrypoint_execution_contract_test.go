package main

import (
	"context"
	"testing"
)

// Exercise the same entrypoint main delegates to, without calling os.Exit.
// Help is entirely local; an unknown command must fail before attempting
// session acquisition or authentication to any desktop backend.
func TestPFEntrypointDistinguishesSuccessfulHelpAndUnsupportedCommand(t *testing.T) {
	for _, tc := range []struct {
		name string
		ctx  context.Context
		args []string
		want int
	}{
		{"normal help", context.Background(), []string{"--help"}, 0},
		{"nil context help", nil, []string{"--help"}, 0},
		{"unknown command", context.Background(), []string{"not-a-real-command"}, 1},
		{"invalid flag", context.Background(), []string{"--not-a-real-flag"}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if code := run(tc.ctx, tc.args); code != tc.want {
				t.Fatalf("cli %q exit status=%d want %d", tc.args, code, tc.want)
			}
		})
	}
}
