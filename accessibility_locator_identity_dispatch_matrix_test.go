package perfuncted

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
)

func TestLocatorScopeIdentityNeverAcceptsDifferentAuthoritativeTargets(t *testing.T) {
	editor := locatorIdentity{windowID: "17", pid: 101, appID: "org.editor", busName: "org.editor.Bus", name: "Editor"}
	for _, tc := range []struct {
		name          string
		first, second locatorIdentity
		same          bool
	}{
		{"same authoritative identifiers", editor, editor, true},
		{"different managed window", editor, locatorIdentity{windowID: "18", pid: 101, appID: "org.editor", busName: "org.editor.Bus"}, false},
		{"different bus owner", editor, locatorIdentity{windowID: "17", pid: 101, appID: "org.editor", busName: "org.editor.New"}, false},
		{"different process", editor, locatorIdentity{windowID: "17", pid: 102, appID: "org.editor", busName: "org.editor.Bus"}, false},
		{"same process different app", editor, locatorIdentity{windowID: "17", pid: 101, appID: "org.other", busName: "org.editor.Bus"}, false},
		{"optional app absent", editor, locatorIdentity{windowID: "17", pid: 101, busName: "org.editor.Bus"}, true},
		{"app fallback agreement", locatorIdentity{appID: "org.editor"}, locatorIdentity{appID: "org.editor"}, true},
		{"app fallback mismatch", locatorIdentity{appID: "org.editor"}, locatorIdentity{appID: "org.other"}, false},
		{"bus fallback agreement", locatorIdentity{busName: "org.editor.Bus"}, locatorIdentity{busName: "org.editor.Bus"}, true},
		{"bus fallback mismatch", locatorIdentity{busName: "org.editor.Bus"}, locatorIdentity{busName: "org.other.Bus"}, false},
		{"name fallback case insensitive", locatorIdentity{name: " Editor "}, locatorIdentity{name: "editor"}, true},
		{"name fallback mismatch", locatorIdentity{name: "Editor"}, locatorIdentity{name: "Browser"}, false},
		{"no identity evidence", locatorIdentity{}, locatorIdentity{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := sameLocatorScope(tc.first, tc.second)
			if got != tc.same {
				t.Fatalf("sameLocatorScope(%+v,%+v)=%v, want %v", tc.first, tc.second, got, tc.same)
			}
		})
	}
}

func TestLocatorDispatchReceiptClassificationAndSelectionAreFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want accessibility.DispatchOutcome
	}{
		{"successful", nil, accessibility.DispatchAccepted},
		{"stale generation", fmtWrap(accessibility.ErrStaleGeneration), accessibility.DispatchNotSent},
		{"stale node", fmtWrap(accessibility.ErrStaleNode), accessibility.DispatchNotSent},
		{"unsupported", fmtWrap(accessibility.ErrUnsupported), accessibility.DispatchNotSent},
		{"rejected mutation", fmtWrap(accessibility.ErrMutationRejected), accessibility.DispatchRejected},
		{"uncertain transport", errors.New("provider dropped the reply"), accessibility.DispatchUnknown},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := textDispatchOutcome(tc.err); got != tc.want {
				t.Fatalf("dispatch=%v want=%v", got, tc.want)
			}
		})
	}
	target := accessibility.Node{ID: accessibility.NodeID{BusName: "org.editor.Bus", ObjectPath: "/target", Generation: 5}, Name: "Save"}
	for _, tc := range []struct {
		name  string
		nodes []accessibility.Node
		want  error
	}{
		{"no result", nil, accessibility.ErrNotFound},
		{"unique result", []accessibility.Node{target}, nil},
		{"ambiguous result", []accessibility.Node{target, target}, accessibility.ErrAmbiguous},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node, err := uniqueLocatorMatch(tc.nodes)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error=%v want=%v", err, tc.want)
			}
			if tc.want == nil && !reflect.DeepEqual(node, target) {
				t.Fatalf("node=%+v want=%+v", node, target)
			}
			if tc.want != nil && node.ID.BusName != "" {
				t.Fatalf("error leaked target %+v", node)
			}
		})
	}
}

func fmtWrap(err error) error { return errors.Join(errors.New("outer"), err) }

func TestLocatorScopeRootApplicationAndInvalidScopes(t *testing.T) {
	loc, _ := newLocatorWaitFixture(t, 1, false)
	root, identity, err := func() (accessibility.NodeID, locatorIdentity, error) {
		r, i, e := loc.scopeRoot(context.Background())
		return r, i, e
	}()
	if err != nil || root.ObjectPath != "/application" || identity.name != "Editor" || identity.busName != "org.synthetic.Editor" {
		t.Fatalf("application root=%+v identity=%+v error=%v", root, identity, err)
	}
	bad := *loc
	for _, tc := range []struct {
		name   string
		target *AccessibilityLocator
		want   error
	}{
		{"nil locator", nil, ErrUnavailable},
		{"missing bundle", &AccessibilityLocator{}, ErrUnavailable},
		{"missing scope", &bad, ErrInvalidArgument},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "missing scope" {
				bad.scope.kind = 0
			}
			_, _, err := tc.target.scopeRoot(context.Background())
			if !errors.Is(err, tc.want) {
				t.Fatalf("scope error=%v want=%v", err, tc.want)
			}
		})
	}
}

func TestLocatorConstructorsCopySelectorIntentAndTrimManagedWindowID(t *testing.T) {
	base := accessibility.Selector{States: []string{"focused"}, Attributes: map[string]string{"role": "button"}, Ancestors: []accessibility.AncestorSelector{{Name: "Editor"}}}
	b := &AccessibilityBundle{}
	loc := b.LocatorForWindow("  17  ", base, accessibility.SnapshotOptions{})
	base.States[0] = "untrusted"
	base.Attributes["role"] = "untrusted"
	base.Ancestors[0].Name = "untrusted"
	if loc.scope.windowID != "17" || loc.selector.States[0] != "focused" || loc.selector.Attributes["role"] != "button" || loc.selector.Ancestors[0].Name != "Editor" {
		t.Fatalf("locator reused caller-owned selector or untrimmed handle: %+v", loc)
	}
	app := b.LocatorForApplication(accessibility.ApplicationFilter{Name: "Editor"}, base, accessibility.SnapshotOptions{})
	if app.scope.kind != locatorApplicationScope || app.scope.application.Name != "Editor" {
		t.Fatalf("application constructor=%+v", app.scope)
	}
}
