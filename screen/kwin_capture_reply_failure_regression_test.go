//go:build linux

package screen

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
	"golang.org/x/sys/unix"
)

type kwinReplyFixture struct {
	dbus.BusObject
	body []any
	hold *os.File
}

func (f *kwinReplyFixture) CallWithContext(_ context.Context, _ string, _ dbus.Flags, args ...any) *dbus.Call {
	if f.body == nil {
		fd, ok := args[len(args)-1].(dbus.UnixFD)
		if !ok {
			return &dbus.Call{Err: errors.New("missing capture descriptor")}
		}
		duplicate, err := unix.Dup(int(fd))
		if err != nil {
			return &dbus.Call{Err: err}
		}
		f.hold = os.NewFile(uintptr(duplicate), "synthetic-kwin-writer")
		return &dbus.Call{Body: []any{map[string]dbus.Variant{}}}
	}
	return &dbus.Call{Body: f.body}
}

func TestKWinCaptureRejectsMalformedReplyAndInterruptedPipe(t *testing.T) {
	for _, tc := range []struct {
		name string
		body []any
		want string
	}{
		{name: "malformed metadata", body: []any{"not metadata"}, want: "store results"},
		{name: "unclosed writer", want: "read pipe"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := &kwinReplyFixture{body: tc.body}
			transport := &kwinDBusTransport{kwin: fixture}
			ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
			defer cancel()
			img, err := transport.CaptureActiveScreen(ctx)
			if fixture.hold != nil {
				defer fixture.hold.Close()
			}
			if img != nil || err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("unusable KWin capture returned image=%v err=%v, want %q", img, err, tc.want)
			}
			if tc.body == nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, os.ErrDeadlineExceeded) {
				t.Fatalf("stalled capture did not report a timeout: %v", err)
			}
		})
	}
}
