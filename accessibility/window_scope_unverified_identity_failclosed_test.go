package accessibility

import (
	"context"
	"errors"
	"testing"
)

// A managed-window identifier is not an AT-SPI identity. Both cached and
// freshly observed correlation must reject requests without independent
// evidence before making any provider calls or claiming a scope root.
func TestWindowCorrelationRefusesAbsentAndUncorroboratedIdentityWithoutProvider(t *testing.T) {
	backend := &dbusBackend{generation: 13}
	for _, tc := range []struct {
		name   string
		target WindowTarget
		want   error
	}{
		{"missing identity", WindowTarget{Title: "Editor", PID: 42}, ErrNotFound},
		{"whitespace identity", WindowTarget{ID: " \t", Title: "Editor", PID: 42}, ErrNotFound},
		{"ID alone", WindowTarget{ID: "managed-window"}, ErrUnsupportedCorrelation},
		{"ID and app name only", WindowTarget{ID: "managed-window", AppID: "org.example.Editor"}, ErrUnsupportedCorrelation},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, fresh := range []bool{false, true} {
				var scope WindowScope
				var err error
				if fresh {
					scope, err = backend.ResolveWindowFresh(context.Background(), tc.target)
				} else {
					scope, err = backend.ResolveWindow(context.Background(), tc.target)
				}
				if !errors.Is(err, tc.want) || scope.Root.BusName != "" || scope.Root.ObjectPath != "" {
					t.Fatalf("fresh=%v target=%+v scope=%+v err=%v want=%v", fresh, tc.target, scope, err, tc.want)
				}
			}
		})
	}
}
