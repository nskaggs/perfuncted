package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/pftest"
	"github.com/nskaggs/perfuncted/window"
)

func TestWindowVerificationRejectsInvalidArguments(t *testing.T) {
	if err := waitWindowActive(context.Background(), nil, 0, time.Millisecond); err == nil {
		t.Fatal("activation verification accepted a nonpositive timeout")
	}
	if err := waitWindowGone(context.Background(), nil, "", time.Second, time.Millisecond); err == nil {
		t.Fatal("disappearance verification accepted an empty ID")
	}
}

func TestWindowVerificationPropagatesBackendErrors(t *testing.T) {
	wantErr := errors.New("synthetic window backend failure")
	manager := &pftest.Manager{Lists: [][]window.Info{{{NativeID: "editor", Title: "Editor"}}}}
	session := pftest.New(nil, nil, manager, nil)
	t.Cleanup(func() { _ = session.Close() })

	target, err := session.Windows.Find(context.Background(), window.Match{TitleExact: "Editor"})
	if err != nil {
		t.Fatalf("find verification target: %v", err)
	}

	manager.Err = wantErr
	if err := waitWindowActive(context.Background(), target, time.Second, time.Millisecond); !errors.Is(err, wantErr) {
		t.Fatalf("activation verification error = %v, want %v", err, wantErr)
	}
	if err := waitWindowGone(context.Background(), session.Windows, "editor", time.Second, time.Millisecond); !errors.Is(err, wantErr) {
		t.Fatalf("disappearance verification error = %v, want %v", err, wantErr)
	}
}
