package perfuncted

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestTraceAndNestedOptionsKeepConfigurationAndRejectTargetCollision(t *testing.T) {
	cfg := &openConfig{}
	target := SessionConfig{}
	for _, option := range []Option{WithNested(target), WithTrace(true), WithTraceDelay(17 * time.Millisecond)} {
		if err := option(cfg); err != nil {
			t.Fatal(err)
		}
	}
	var destination bytes.Buffer
	if err := WithTraceOut(&destination)(cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.target.kind != TargetNested || !cfg.selected || !cfg.trace || cfg.traceDelay != 17*time.Millisecond || cfg.traceOut != &destination {
		t.Fatalf("options lost nested mode or trace settings: %+v", cfg)
	}
	for _, option := range []Option{WithNested(target), WithHeadless(target)} {
		if err := option(cfg); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("duplicate managed target accepted: %v", err)
		}
	}
	if err := WithTrace(false)(cfg); err != nil || cfg.trace {
		t.Fatalf("trace opt-out ignored: %v", err)
	}
}

func TestCompositeWaitConditionPreservesEventWakeupsAndShortCircuits(t *testing.T) {
	ctx := context.Background()
	calls := make([]string, 0, 3)
	condition := func(name string, value bool, err error, pollOnly bool) Condition {
		return conditionFunc{name: name, pollOnly: pollOnly, check: func(context.Context, *Session) (bool, error) { calls = append(calls, name); return value, err }}
	}
	observed := errors.New("synthetic unreadable window state")
	falseCondition := condition("false", false, nil, true)
	broken := condition("broken", false, observed, false)
	trueCondition := condition("true", true, nil, true)
	all := All(falseCondition, broken, trueCondition)
	if all.describe() != "all" || all.pollingOnly() {
		t.Fatal("all condition hid event-capable child")
	}
	if value, err := all.evaluate(ctx, nil); value || err != nil || strings.Join(calls, ",") != "false" {
		t.Fatalf("all did not short-circuit unfulfilled proof: value=%v err=%v calls=%v", value, err, calls)
	}
	calls = nil
	any := Any(broken, trueCondition, falseCondition)
	if any.describe() != "any" || any.pollingOnly() {
		t.Fatal("any condition suppressed event wakeups")
	}
	if value, err := any.evaluate(ctx, nil); !value || err != nil || strings.Join(calls, ",") != "broken,true" {
		t.Fatalf("independent positive proof lost: value=%v err=%v calls=%v", value, err, calls)
	}
	calls = nil
	allBroken := Any(broken, falseCondition)
	if value, err := allBroken.evaluate(ctx, nil); value || !errors.Is(err, observed) {
		t.Fatalf("all alternatives unreadable lost error: value=%v err=%v", value, err)
	}
	negative := Not(falseCondition)
	if negative.describe() != "not false" || !negative.pollingOnly() {
		t.Fatal("not condition changed polling classification")
	}
	if value, err := negative.evaluate(ctx, nil); !value || err != nil {
		t.Fatalf("not false result=%v err=%v", value, err)
	}
	if desc := Not(nil).describe(); desc != "not <nil>" {
		t.Fatalf("nil child described as %q", desc)
	}
	if Not(nil).pollingOnly() {
		t.Fatal("nil child claimed polling readiness")
	}
	if _, err := Not(nil).evaluate(ctx, nil); err == nil {
		t.Fatal("nil child condition evaluated as success")
	}
	if !All(trueCondition, falseCondition).pollingOnly() {
		t.Fatal("poll-only composite classified as event-capable")
	}
}
