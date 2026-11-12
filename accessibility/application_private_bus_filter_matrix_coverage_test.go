//go:build linux

package accessibility

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// Application enumeration and fresh identity discovery must apply the same
// exact bus, path, PID and descriptive title constraints. An ambiguous
// upstream desktop response must not select an arbitrary application.
func TestPrivateATSPIApplicationLookupFilterAndAmbiguityMatrix(t *testing.T) {
	ctx := context.Background()
	backend, _, _ := newSyntheticATSPIBackend(t, false)
	for _, tc := range []struct {
		name   string
		filter ApplicationFilter
		found  bool
	}{
		{"name case insensitive", ApplicationFilter{Name: "  EDITOR  "}, true},
		{"exact bus", ApplicationFilter{Bus: registryName}, true},
		{"exact application path", ApplicationFilter{WindowID: "/org/a11y/atspi/accessible/app"}, true},
		{"title equals accessible name", ApplicationFilter{WindowTitle: "EDITOR"}, true},
		{"title does not match", ApplicationFilter{WindowTitle: "private-unrelated-title"}, false},
		{"wrong exact bus", ApplicationFilter{Bus: "org.example.NotOwned"}, false},
		{"wrong exact path", ApplicationFilter{WindowID: "/org/a11y/atspi/accessible/other"}, false},
		{"name does not match", ApplicationFilter{Name: "terminal"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, method := range []struct {
				name string
				call func(context.Context, ApplicationFilter) (Application, error)
			}{
				{"cached", backend.FindApplication}, {"fresh", backend.FindApplicationFresh},
			} {
				t.Run(method.name, func(t *testing.T) {
					app, err := method.call(ctx, tc.filter)
					if tc.found {
						if err != nil || app.Name != "Editor" {
							t.Fatalf("lookup %+v => %+v %v", tc.filter, app, err)
						}
					} else if !errors.Is(err, ErrNotFound) || app.ID.valid() {
						t.Fatalf("unknown identity exposed: %+v %v", app, err)
					}
				})
			}
		})
	}
	duplicate, _, _ := newSyntheticATSPIBackendWithFrameSetup(t, false, func(root, _ *syntheticATSPINode) { root.children = append(root.children, root.children[0]) })
	for _, method := range []struct {
		name string
		call func(context.Context, ApplicationFilter) (Application, error)
	}{
		{"cached", duplicate.FindApplication}, {"fresh", duplicate.FindApplicationFresh},
	} {
		t.Run("duplicate desktop identity "+method.name, func(t *testing.T) {
			app, err := method.call(ctx, ApplicationFilter{Name: "Editor"})
			if !errors.Is(err, ErrAmbiguous) || app.ID.valid() || !strings.Contains(err.Error(), "2 applications matched") {
				t.Fatalf("ambiguous application selected: %+v %v", app, err)
			}
		})
	}
}
