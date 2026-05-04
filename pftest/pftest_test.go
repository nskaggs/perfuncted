package pftest_test

import (
	"context"
	"image"
	"image/color"
	"testing"

	"github.com/nskaggs/perfuncted/pftest"
)

func TestNewAssemblesAllBackends(t *testing.T) {
	sc := &pftest.Screenshotter{
		Frames: []image.Image{pftest.SolidImage(8, 8, color.RGBA{255, 0, 0, 255})},
	}
	inp := &pftest.Inputter{}
	mgr := &pftest.Manager{}
	cb := &pftest.Clipboard{Text: "hi"}

	pf := pftest.New(sc, inp, mgr, cb)
	if pf == nil {
		t.Fatal("New returned nil")
	}

	// Exercise each bundle through the assembled Perfuncted.
	if _, err := pf.Screen.GrabContext(context.Background(), image.Rectangle{}); err != nil {
		t.Errorf("GrabContext: %v", err)
	}
	if err := pf.Input.Type("{enter}"); err != nil {
		t.Errorf("Type: %v", err)
	}
	if got, err := pf.Clipboard.Get(); err != nil || got != "hi" {
		t.Errorf("Clipboard.Get: %q, %v", got, err)
	}
}

func TestNewNilBackends(t *testing.T) {
	pf := pftest.New(nil, nil, nil, nil)
	if pf == nil {
		t.Fatal("New returned nil")
	}
	// All bundles are zero-valued; operations should return errors, not panic.
	if _, err := pf.Screen.GrabContext(context.Background(), image.Rectangle{}); err == nil {
		t.Error("expected error for nil screen")
	}
	if err := pf.Input.Type("{ctrl+s}"); err == nil {
		t.Error("expected error for nil inputter")
	}
	if _, err := pf.Clipboard.Get(); err == nil {
		t.Error("expected error for nil clipboard")
	}
}
