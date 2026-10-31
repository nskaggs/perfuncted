package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/accessibility"
)

type recordingATSPIMutations struct {
	cliAutomationFake
	calls   []string
	failure error
}

func (b *recordingATSPIMutations) ScrollToPoint(_ context.Context, _ accessibility.NodeID, coord accessibility.CoordType, x, y int) error {
	b.calls = append(b.calls, fmt.Sprintf("scroll-point:%d:%d:%d", coord, x, y))
	return b.failure
}
func (b *recordingATSPIMutations) SetPosition(_ context.Context, _ accessibility.NodeID, x, y int, coord accessibility.CoordType) error {
	b.calls = append(b.calls, fmt.Sprintf("position:%d:%d:%d", coord, x, y))
	return b.failure
}
func (b *recordingATSPIMutations) SetSize(_ context.Context, _ accessibility.NodeID, w, h int) error {
	b.calls = append(b.calls, fmt.Sprintf("size:%d:%d", w, h))
	return b.failure
}
func (b *recordingATSPIMutations) SetExtents(_ context.Context, _ accessibility.NodeID, x, y, w, h int, coord accessibility.CoordType) error {
	b.calls = append(b.calls, fmt.Sprintf("extents:%d:%d:%d:%d:%d", coord, x, y, w, h))
	return b.failure
}
func (b *recordingATSPIMutations) InsertText(_ context.Context, _ accessibility.NodeID, offset int32, value string) error {
	b.calls = append(b.calls, fmt.Sprintf("insert:%d:%s", offset, value))
	return b.failure
}
func (b *recordingATSPIMutations) DeleteText(_ context.Context, _ accessibility.NodeID, start, end int32) error {
	b.calls = append(b.calls, fmt.Sprintf("delete:%d:%d", start, end))
	return b.failure
}
func (b *recordingATSPIMutations) CopyText(_ context.Context, _ accessibility.NodeID, start, end int32) error {
	b.calls = append(b.calls, fmt.Sprintf("copy:%d:%d", start, end))
	return b.failure
}
func (b *recordingATSPIMutations) CutText(_ context.Context, _ accessibility.NodeID, start, end int32) error {
	b.calls = append(b.calls, fmt.Sprintf("cut:%d:%d", start, end))
	return b.failure
}
func (b *recordingATSPIMutations) PasteText(_ context.Context, _ accessibility.NodeID, position int32) error {
	b.calls = append(b.calls, fmt.Sprintf("paste:%d", position))
	return b.failure
}
func (b *recordingATSPIMutations) SetCaretOffset(_ context.Context, _ accessibility.NodeID, offset int32) error {
	b.calls = append(b.calls, fmt.Sprintf("caret:%d", offset))
	return b.failure
}
func (b *recordingATSPIMutations) SetTextSelections(_ context.Context, _ accessibility.NodeID, selections []accessibility.DocumentTextSelection) error {
	b.calls = append(b.calls, fmt.Sprintf("selection-count:%d", len(selections)))
	return b.failure
}
func (b *recordingATSPIMutations) DeselectChild(_ context.Context, _ accessibility.NodeID, index int32) error {
	b.calls = append(b.calls, fmt.Sprintf("deselect-child:%d", index))
	return b.failure
}
func (b *recordingATSPIMutations) DeselectSelectedChild(_ context.Context, _ accessibility.NodeID) error {
	b.calls = append(b.calls, "deselect-selected-child")
	return b.failure
}

func TestAccessibilityRawCLIRoutesEveryRemainingMutationToCorrectBackendMethod(t *testing.T) {
	base := []string{"a11y", "raw", "--bus", "org.test.App", "--path", "/node", "--generation", "1"}
	cases := []struct {
		name, op string
		args     []string
		want     string
	}{
		{"scroll point in window coordinates", "scroll-to-point", []string{"--coordinate-space", "window", "--x", "123", "--y", "456"}, "scroll-point:1:123:456"},
		{"scroll point in screen coordinates", "scroll-to-point", []string{"--coordinate-space", "screen", "--x", "13", "--y", "25"}, "scroll-point:0:13:25"},
		{"position in parent coordinates", "set-position", []string{"--coordinate-space", "parent", "--x", "9", "--y", "7"}, "position:2:9:7"},
		{"size", "set-size", []string{"--width", "111", "--height", "222"}, "size:111:222"},
		{"extents", "set-extents", []string{"--coordinate-space", "window", "--x", "1", "--y", "2", "--width", "3", "--height", "4"}, "extents:1:1:2:3:4"},
		{"insert range-text overrides text", "insert-text", []string{"--offset", "6", "--text", "ignored", "--range-text", "actual"}, "insert:6:actual"},
		{"insert text fallback", "insert-text", []string{"--offset", "7", "--text", "fallback"}, "insert:7:fallback"},
		{"delete", "delete-text", []string{"--start", "2", "--end", "8"}, "delete:2:8"},
		{"copy", "copy-text", []string{"--start", "5", "--end", "15"}, "copy:5:15"},
		{"cut", "cut-text", []string{"--start", "9", "--end", "19"}, "cut:9:19"},
		{"paste", "paste-text", []string{"--position", "22"}, "paste:22"},
		{"caret", "set-caret", []string{"--offset", "18"}, "caret:18"},
		{"document selections empty", "set-document-text-selections", []string{"--selections", "[]"}, "selection-count:0"},
		{"deselect child", "deselect-child", []string{"--index", "7"}, "deselect-child:7"},
		{"deselect selected child", "deselect-selected-child", nil, "deselect-selected-child"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			backend := &recordingATSPIMutations{}
			args := append(append(append([]string{}, base...), "--op", tc.op), tc.args...)
			out, errOut, exit := captureRunIO(t, args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					return perfuncted.NewSessionForTesting(nil, nil, nil, nil, nil, backend), nil
				}
			})
			if exit != 0 || strings.TrimSpace(out) != "ok" || errOut != "" {
				t.Fatalf("raw %v exit=%d stdout=%q stderr=%q", args, exit, out, errOut)
			}
			if len(backend.calls) != 1 || backend.calls[0] != tc.want {
				t.Fatalf("raw %s dispatched %v, want %s", tc.name, backend.calls, tc.want)
			}
		})
	}
}
func TestAccessibilityRawCLIRejectsUnknownOrMalformedTypedParameters(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"unknown op", []string{"--op", "nonexistent"}, "unknown raw op"},
		{"bad coordinate type", []string{"--op", "scroll-to-point", "--coordinate-space", "planet"}, "unknown coordinate space"},
		{"bad position coordinate", []string{"--op", "set-position", "--coordinate-space", "planet"}, "unknown coordinate space"},
		{"bad extents coordinate", []string{"--op", "set-extents", "--coordinate-space", "planet"}, "unknown coordinate space"},
		{"bad scroll alignment", []string{"--op", "scroll", "--alignment", "invalid"}, "unknown alignment"},
		{"bad JSON selection", []string{"--op", "set-document-text-selections", "--selections", "{"}, "invalid --selections JSON"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"a11y", "raw", "--bus", "org.test.App", "--path", "/node", "--generation", "1"}, tc.args...)
			out, errOut, code := captureRunIO(t, args, openCLIWithAutomation)
			if code == 0 || out != "" || !strings.Contains(errOut, tc.want) {
				t.Fatalf("raw %v code=%d stdout=%q stderr=%q", args, code, out, errOut)
			}
		})
	}
}

type atspiFailWriter struct{ err error }

func (w atspiFailWriter) Write([]byte) (int, error) { return 0, w.err }
func TestAccessibilityHumanAndJSONOutputPropagateWriterFailures(t *testing.T) {
	failure := errors.New("synthetic output stream closed")
	id := accessibility.NodeID{BusName: "org.test.App", ObjectPath: "/node", Generation: 9}
	types := []any{
		[]accessibility.Application{{Node: accessibility.Node{ID: id, Name: "Editor", Role: "application"}}},
		accessibility.Node{ID: id, Name: "Editor", Role: "entry"},
		[]accessibility.Node{{ID: id, Name: "Editor", Role: "entry"}},
		accessibility.Snapshot{Root: accessibility.Node{ID: id}, Generation: 9},
		accessibility.Outline{Generation: 9, Truncated: true},
		accessibility.Action{Index: 3, Name: "activate"},
		perfuncted.AccessibilityActionReceipt{Node: accessibility.Node{ID: id}, Generation: 9},
		"default response",
	}
	for i, value := range types {
		t.Run(fmt.Sprintf("type-%d", i), func(t *testing.T) {
			if err := accessibilityOutput(atspiFailWriter{err: failure}, false, value); !errors.Is(err, failure) {
				t.Fatalf("human output dropped writer failure for %T: %v", value, err)
			}
			if err := accessibilityOutput(atspiFailWriter{err: failure}, true, value); !errors.Is(err, failure) {
				t.Fatalf("JSON output dropped writer failure for %T: %v", value, err)
			}
			var buf bytes.Buffer
			if err := accessibilityOutput(&buf, false, value); err != nil {
				t.Fatalf("human output of %T failed: %v", value, err)
			}
			if buf.Len() == 0 && i != 0 {
				t.Fatalf("human output of %T silently empty", value)
			}
		})
	}
}
