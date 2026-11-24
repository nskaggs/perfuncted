package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/input"
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

func TestInputLocationJSONDoesNotFallback(t *testing.T) {
	s := pftest.New(nil, unsupportedPointerLocationInputter{Inputter: &pftest.Inputter{}}, nil, nil)
	var stdout bytes.Buffer
	cmd := inputCmd(func(context.Context) (*perfuncted.Session, error) { return s, nil }, nil, func(context.Context) (*perfuncted.Session, error) {
		t.Fatal("unexpected window fallback")
		return nil, nil
	})
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"location", "--output", "json"})
	cmd.SetOut(&stdout)
	cmd.SetErr(io.Discard)
	if err := cmd.Execute(); !errors.Is(err, perfuncted.ErrUnsupported) || stdout.Len() != 0 {
		t.Fatalf("got %q, %v", stdout.String(), err)
	}
}

type observedPointerInputter struct {
	*pftest.Inputter
	result input.PointerObservation
}

func (p observedPointerInputter) ObservePointer(context.Context) (input.PointerObservation, error) {
	return p.result, nil
}

func TestInputLocationJSONObservation(t *testing.T) {
	want := input.PointerObservation{
		X: -42, Y: 0,
		CoordinateSpace: input.CoordinateSpaceInfo{Kind: input.CoordinateSpaceLogical, ScaleX: 1, ScaleY: 1},
		Provider:        "gnome-native",
		ObservedAt:      time.Date(2026, 11, 24, 15, 0, 0, 123, time.UTC),
	}
	s := pftest.New(nil, observedPointerInputter{Inputter: &pftest.Inputter{}, result: want}, nil, nil)
	var stdout bytes.Buffer
	cmd := inputCmd(func(context.Context) (*perfuncted.Session, error) { return s, nil }, nil, func(context.Context) (*perfuncted.Session, error) { t.Fatal("unexpected fallback"); return nil, nil })
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"location", "--output", "json"})
	cmd.SetOut(&stdout)
	cmd.SetErr(io.Discard)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var got input.PointerObservation
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}
