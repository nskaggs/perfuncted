package main

import (
	"context"
	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
	"github.com/nskaggs/perfuncted/window"
	"strings"
	"testing"
)

func TestActivateByTitleVerificationRejectsUnfocusedTarget(t *testing.T) {
	m := &pftest.Manager{Lists: [][]window.Info{{{NativeID: "one", Title: "Test Focus", Active: false}}}}
	_, stderr, code := captureRunIO(t, []string{"window", "activate", "Test Focus", "--verify", "--verify-timeout", "15ms", "--verify-poll", "5ms"}, func(*cliConfig) sessionOpener {
		return func(context.Context) (*perfuncted.Session, error) { return pftest.New(nil, nil, m, nil), nil }
	})
	if code == 0 || !strings.Contains(stderr, "never reported active") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
}
func TestCloseByIDVerificationRequiresWindowDisappearance(t *testing.T) {
	m := &pftest.Manager{Lists: [][]window.Info{{{NativeID: "one", Title: "Dialog"}}}}
	_, stderr, code := captureRunIO(t, []string{"window", "close-id", "one", "--verify", "--verify-timeout", "15ms", "--verify-poll", "5ms"}, func(*cliConfig) sessionOpener {
		return func(context.Context) (*perfuncted.Session, error) { return pftest.New(nil, nil, m, nil), nil }
	})
	if code == 0 || !strings.Contains(stderr, "remains open") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
}

func TestActivateByIDVerificationConfirmsActualFocus(t *testing.T) {
	m := &pftest.Manager{Lists: [][]window.Info{{{NativeID: "id-one", Title: "Duplicate", Active: false}, {NativeID: "id-two", Title: "Duplicate", Active: false}}}}
	_, stderr, code := captureRunIO(t, []string{"window", "activate-id", "id-two", "--verify", "--verify-timeout", "15ms", "--verify-poll", "5ms"}, func(*cliConfig) sessionOpener {
		return func(context.Context) (*perfuncted.Session, error) { return pftest.New(nil, nil, m, nil), nil }
	})
	if code == 0 || !strings.Contains(stderr, "never reported active") {
		t.Fatalf("verify flag ignored: code=%d stderr=%q", code, stderr)
	}
}
