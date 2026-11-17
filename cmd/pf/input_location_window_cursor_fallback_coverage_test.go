package main

import (
	"bytes"
	"context"
	"io"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/pftest"
)

type unsupportedPointerLocationInputter struct {
	*pftest.Inputter
}

func (unsupportedPointerLocationInputter) PointerLocation(context.Context) (int, int, error) {
	return 0, 0, perfuncted.ErrUnsupported
}

type cursorPositionWindowManager struct {
	*pftest.Manager
	x, y       int
	closeCalls int
}

func (m *cursorPositionWindowManager) CursorPosition(context.Context) (int, int, error) {
	return m.x, m.y, nil
}

func (*cursorPositionWindowManager) SupportedOperations() []string {
	return []string{"cursor-position"}
}

func (m *cursorPositionWindowManager) Close() error {
	m.closeCalls++
	return m.Manager.Close()
}

func TestInputLocationFallsBackToWindowCursorWhenInjectionBackendCannotReportIt(t *testing.T) {
	inputSession := pftest.New(nil, unsupportedPointerLocationInputter{Inputter: &pftest.Inputter{}}, nil, nil)
	windowManager := &cursorPositionWindowManager{Manager: &pftest.Manager{}, x: 73, y: 118}
	windowSession := pftest.New(nil, nil, windowManager, nil)
	var stdout bytes.Buffer
	cmd := inputCmd(
		func(context.Context) (*perfuncted.Session, error) { return inputSession, nil },
		nil,
		func(context.Context) (*perfuncted.Session, error) { return windowSession, nil },
	)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"location"})
	cmd.SetOut(&stdout)
	cmd.SetErr(io.Discard)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("input location failed with a window cursor backend available: %v", err)
	}
	if got := stdout.String(); got != "73,118\n" {
		t.Fatalf("input location output = %q, want %q", got, "73,118\n")
	}
	if windowManager.closeCalls != 1 {
		t.Fatalf("fallback window session closed %d times, want once", windowManager.closeCalls)
	}
}
