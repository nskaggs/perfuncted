package main

import (
	"context"
	"fmt"
	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/input"
	"github.com/nskaggs/perfuncted/output"
	"github.com/nskaggs/perfuncted/pftest"
	"github.com/nskaggs/perfuncted/window"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type coordinateTestInput struct {
	*pftest.Inputter
	space input.CoordinateSpaceInfo
}

func (i *coordinateTestInput) PointerCoordinateSpace(context.Context) (input.CoordinateSpaceInfo, error) {
	return i.space, nil
}

type outputTestLister struct{ outs []output.Info }

func (l *outputTestLister) List(context.Context) ([]output.Info, error) { return l.outs, nil }
func (l *outputTestLister) Close() error                                { return nil }

func TestScreenPixelsClickActualMappedCoordinates(t *testing.T) {
	in := &coordinateTestInput{Inputter: &pftest.Inputter{}, space: input.CoordinateSpaceInfo{Kind: input.CoordinateSpaceLogical, ScaleX: 1, ScaleY: 1}}
	outs := &outputTestLister{outs: []output.Info{{Available: true, Geometry: output.Geometry{W: 1600, H: 900}, ResolutionW: 1920, ResolutionH: 1080}}}
	out, errOut, code := captureRunIO(t, []string{"input", "click-screen", "--x", "1200", "--y", "600"}, func(*cliConfig) sessionOpener {
		return func(context.Context) (*perfuncted.Session, error) {
			return pftest.NewWithOutputs(nil, in, nil, outs, nil), nil
		}
	})
	if code != 0 {
		t.Fatalf("code=%d output=%q err=%q", code, out, errOut)
	}
	if calls := strings.Join(in.Calls, ","); !strings.Contains(calls, "1000") || !strings.Contains(calls, "500") {
		t.Fatalf("mouse calls=%q, expected logical 1000,500", calls)
	}
}

func TestWindowRelativeClickTargetsStableIDOnly(t *testing.T) {
	in := &coordinateTestInput{Inputter: &pftest.Inputter{}, space: input.CoordinateSpaceInfo{Kind: input.CoordinateSpaceLogical, ScaleX: 1, ScaleY: 1}}
	mgr := &pftest.Manager{Lists: [][]window.Info{{{NativeID: "a", Title: "Duplicate", X: 100, Y: 200, W: 600, H: 300}, {NativeID: "b", Title: "Duplicate", X: 300, Y: 400, W: 200, H: 200}}}}
	out, errOut, code := captureRunIO(t, []string{"input", "click-window", "--id", "b", "--x", "20", "--y", "30"}, func(*cliConfig) sessionOpener {
		return func(context.Context) (*perfuncted.Session, error) { return pftest.New(nil, in, mgr, nil), nil }
	})
	if code != 0 {
		t.Fatalf("code=%d output=%q err=%q", code, out, errOut)
	}
	if calls := strings.Join(in.Calls, ","); !strings.Contains(calls, "320") || !strings.Contains(calls, "430") {
		t.Fatalf("mouse calls=%q expected 320,430", calls)
	}
}

func TestFileChooserRestoresClipboardAfterSuccessAndInputFailure(t *testing.T) {
	file := filepath.Join(t.TempDir(), "book.epub")
	if err := os.WriteFile(file, []byte("epub-test"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{true: "typing-fails", false: "success"}[fail], func(t *testing.T) {
			in := &pftest.Inputter{}
			if fail {
				in.Err = os.ErrPermission
			}
			mgr := &pftest.Manager{Lists: [][]window.Info{{{NativeID: "dialog-1", Title: "File Upload", Active: true}}, {{NativeID: "dialog-1", Title: "File Upload", Active: true}}, {}}}
			cb := &pftest.Clipboard{Text: "prior clipboard"}
			stdout, stderr, code := captureRunIO(t, []string{"input", "select-file", "--id", "dialog-1", "--path", file}, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) { return pftest.New(nil, in, mgr, cb), nil }
			})
			if fail && code == 0 || !fail && code != 0 {
				t.Fatalf("fail=%v code=%d stdout=%q stderr=%q", fail, code, stdout, stderr)
			}
			if cb.Text != "prior clipboard" {
				t.Fatalf("clipboard not restored: %q", cb.Text)
			}
			if len(mgr.Activated) != 1 || mgr.Activated[0] != "dialog-1" {
				t.Errorf("wrong focused target: %v", mgr.Activated)
			}
			if !fail && len(in.Calls) != 2 {
				t.Errorf("expected enter selection + confirmation, calls=%v", in.Calls)
			}
		})
	}
}

func TestFileChooserRefusesToTypeIntoUnfocusedWindow(t *testing.T) {
	file := filepath.Join(t.TempDir(), "test.epub")
	_ = os.WriteFile(file, []byte("test"), 0600)
	in := &pftest.Inputter{}
	mgr := &pftest.Manager{Lists: [][]window.Info{{{NativeID: "dialog-1", Title: "File Upload", Active: false}}}}
	_, _, code := captureRunIO(t, []string{"input", "select-file", "--id", "dialog-1", "--path", file}, func(*cliConfig) sessionOpener {
		return func(context.Context) (*perfuncted.Session, error) {
			return pftest.New(nil, in, mgr, &pftest.Clipboard{Text: "original"}), nil
		}
	})
	if code == 0 || len(in.Calls) != 0 {
		t.Fatalf("unfocused chooser accepted input: code=%d calls=%v", code, in.Calls)
	}
}

func TestScreenClickRejectsUntrustedBackendCoordinateMetadata(t *testing.T) {
	for _, space := range []input.CoordinateSpaceInfo{
		{},
		{Kind: input.CoordinateSpaceLogical, ScaleX: 0, ScaleY: 0},
		{Kind: input.CoordinateSpaceLogical, ScaleX: 2, ScaleY: 2},
		{Kind: input.CoordinateSpacePhysical, ScaleX: math.NaN(), ScaleY: 1},
	} {
		t.Run(fmt.Sprint(space), func(t *testing.T) {
			in := &coordinateTestInput{Inputter: &pftest.Inputter{}, space: space}
			outs := &outputTestLister{outs: []output.Info{{Available: true, Geometry: output.Geometry{W: 1600, H: 900}, ResolutionW: 1920, ResolutionH: 1080}}}
			_, stderr, code := captureRunIO(t, []string{"input", "click-screen", "--x", "600", "--y", "300"}, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					return pftest.NewWithOutputs(nil, in, nil, outs, nil), nil
				}
			})
			if code == 0 || len(in.Calls) > 0 {
				t.Fatalf("invalid coordinate space %+v accepted, code=%d calls=%v stderr=%q", space, code, in.Calls, stderr)
			}
		})
	}
}
