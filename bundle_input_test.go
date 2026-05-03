package perfuncted_test

import (
	"testing"

	"github.com/nskaggs/perfuncted/pftest"
)

func TestInputBundleTypeWithDelayUsesKeyTaps(t *testing.T) {
	inp := &pftest.Inputter{}
	pf := pftest.New(nil, inp, nil, nil)

	if err := pf.Input.TypeWithDelay("A b\n\t", 0); err != nil {
		t.Fatalf("TypeWithDelay: %v", err)
	}

	want := []string{"tap:A", "tap:space", "tap:b", "tap:enter", "tap:tab"}
	if len(inp.Calls) != len(want) {
		t.Fatalf("unexpected call count: got %v want %v", inp.Calls, want)
	}
	for i, call := range want {
		if inp.Calls[i] != call {
			t.Fatalf("call %d = %q, want %q (all calls: %v)", i, inp.Calls[i], call, inp.Calls)
		}
	}
	if typed := inp.Typed(); typed != "" {
		t.Fatalf("TypeWithDelay should not use bulk Type, got typed text %q", typed)
	}
}
