package main

import (
	"testing"
	"time"
)

// Environment trace defaults are evaluated when the CLI is constructed.
// Explicitly disabled trace and invalid/negative delays must never arm a
// debugging delay or trace actions in a subsequent invocation.
func TestRootTraceEnvironmentDefaultsRequireAffirmativeValidValues(t *testing.T) {
	for _, tc := range []struct {
		name, trace, delay string
		wantTrace          bool
		wantDelay          time.Duration
	}{
		{name: "enabled with positive delay", trace: "true", delay: "125ms", wantTrace: true, wantDelay: 125 * time.Millisecond},
		{name: "disabled with negative delay", trace: "false", delay: "-125ms"},
		{name: "disabled with malformed delay", trace: "false", delay: "not-a-duration"},
		{name: "delay alone does not set explicit trace flag", trace: "", delay: "25ms", wantDelay: 25 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("PF_TRACE_ACTIONS", tc.trace)
			t.Setenv("PF_TRACE_DELAY", tc.delay)
			root := newRootCmd(defaultOpenPFFactory)
			trace, err := root.PersistentFlags().GetBool("trace-actions")
			if err != nil {
				t.Fatal(err)
			}
			if trace != tc.wantTrace {
				t.Fatalf("trace-actions default = %v, want %v", trace, tc.wantTrace)
			}
			delay, err := root.PersistentFlags().GetDuration("trace-delay")
			if err != nil {
				t.Fatal(err)
			}
			if delay != tc.wantDelay {
				t.Fatalf("trace-delay default = %s, want %s", delay, tc.wantDelay)
			}
		})
	}
}
