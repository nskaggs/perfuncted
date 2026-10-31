package accessibility

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"

	"github.com/godbus/dbus/v5"
	"github.com/nskaggs/perfuncted/internal/dbusutil"
)

// This service implements the actual AT-SPI optional interface methods over
// a private D-Bus daemon; no in-memory callOverride bypasses deserialization.
type optionalATSPINode struct {
	mu                 sync.RWMutex
	values             map[string]any
	fail               map[string]bool
	protected          bool
	cancelRead         context.CancelFunc
	lastAction         int32
	actionCount        int
	lastMutation       string
	lastText           string
	lastStart, lastEnd int32
}

func (p *optionalATSPINode) Get(iface, name string) (dbus.Variant, *dbus.Error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.fail[iface+"."+name] {
		return dbus.Variant{}, dbus.MakeFailedError(errors.New("synthetic property failure"))
	}
	switch name {
	case "Name":
		if p.cancelRead != nil {
			p.cancelRead()
		}
		return dbus.MakeVariant("Records"), nil
	case "Description":
		return dbus.MakeVariant("Records grid"), nil
	case "ChildCount":
		return dbus.MakeVariant(int32(0)), nil
	}
	value, ok := p.values[iface+"."+name]
	if !ok {
		return dbus.Variant{}, dbus.MakeFailedError(errors.New("unimplemented property"))
	}
	return dbus.MakeVariant(value), nil
}
func (*optionalATSPINode) GetRole() (uint32, *dbus.Error) { return 43, nil }
func (*optionalATSPINode) GetInterfaces() ([]string, *dbus.Error) {
	return []string{valueIface, actionIface, selectionIface, tableIface, documentIface}, nil
}
func (p *optionalATSPINode) GetState() ([]uint32, *dbus.Error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.protected {
		return []uint32{0, 1 << 2}, nil
	}
	return []uint32{1 << 8}, nil
}
func (*optionalATSPINode) GetAttributes() (map[string]string, *dbus.Error) {
	return map[string]string{"password": "hidden credential", "public": "safe attribute", "text-value": "sensitive payload"}, nil
}
func (p *optionalATSPINode) GetRelationSet() ([]struct {
	RelationType uint32
	Targets      []objectRef
}, *dbus.Error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.fail["relation"] {
		return nil, dbus.MakeFailedError(errors.New("synthetic relation failure"))
	}
	return []struct {
		RelationType uint32
		Targets      []objectRef
	}{
		{RelationType: 1, Targets: []objectRef{{BusName: registryName, ObjectPath: "/org/a11y/atspi/accessible/label"}, {BusName: registryName, ObjectPath: nullObjectPath}}},
		{RelationType: 1000, Targets: []objectRef{{BusName: registryName, ObjectPath: "/org/a11y/atspi/accessible/ignored"}}},
	}, nil
}
func (p *optionalATSPINode) GetActions() ([]actionMetadataWire, *dbus.Error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.fail["actions"] {
		return nil, dbus.MakeFailedError(errors.New("synthetic action failure"))
	}
	return []actionMetadataWire{{LocalizedName: "Activate", Description: "Activate button", KeyBinding: "Space"}, {LocalizedName: "Cancel", Description: "Cancel operation", KeyBinding: "Esc"}}, nil
}
func (p *optionalATSPINode) GetName(index int32) (string, *dbus.Error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.fail["name"] {
		return "", dbus.MakeFailedError(errors.New("synthetic GetName failure"))
	}
	if index == 0 {
		return "press", nil
	}
	if index == 1 {
		return "cancel", nil
	}
	return "", dbus.MakeFailedError(errors.New("invalid action index"))
}
func (p *optionalATSPINode) GetSelectedChildCount() (int32, *dbus.Error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.fail["selection"] {
		return 0, dbus.MakeFailedError(errors.New("synthetic selection failure"))
	}
	return -3, nil
}

// Synthetic mutation methods run on the actual exported object, not through
// callOverride, so returned success, rejection and side effects cross D-Bus.
func (p *optionalATSPINode) DoAction(index int32) (bool, *dbus.Error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastAction = index
	p.actionCount++
	if p.fail["reject-action"] {
		return false, nil
	}
	if p.fail["error-action"] {
		return false, dbus.MakeFailedError(errors.New("synthetic action refused"))
	}
	return true, nil
}
func (p *optionalATSPINode) Set(iface, key string, v dbus.Variant) *dbus.Error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fail["set-value"] {
		return dbus.MakeFailedError(errors.New("synthetic set refused"))
	}
	p.values[iface+"."+key] = v.Value()
	p.lastMutation = "set-value"
	return nil
}
func (p *optionalATSPINode) DeleteText(start, end int32) (bool, *dbus.Error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastMutation = "delete-text"
	p.lastStart, p.lastEnd = start, end
	if p.fail["reject-delete"] {
		return false, nil
	}
	return true, nil
}
func (p *optionalATSPINode) CopyText(start, end int32) *dbus.Error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastMutation = "copy-text"
	p.lastStart, p.lastEnd = start, end
	return nil
}
func (p *optionalATSPINode) CutText(start, end int32) (bool, *dbus.Error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastMutation = "cut-text"
	p.lastStart, p.lastEnd = start, end
	return true, nil
}
func (p *optionalATSPINode) SetTextContents(value string) (bool, *dbus.Error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastMutation = "set-text"
	p.lastText = value
	return true, nil
}
func (p *optionalATSPINode) InsertText(offset int32, value string, length int32) (bool, *dbus.Error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastMutation = "insert-text"
	p.lastText = value
	p.lastStart, p.lastEnd = offset, length
	return true, nil
}
func newOptionalATSPINode(t *testing.T) (*dbusBackend, NodeID, *optionalATSPINode) {
	t.Helper()
	address := startTestDBus(t)
	server, err := dbusutil.ConnectContext(context.Background(), address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Close() })
	reply, err := server.RequestName(registryName, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("register AT-SPI provider: %v %v", reply, err)
	}
	provider := &optionalATSPINode{values: map[string]any{
		valueIface + ".CurrentValue": float64(27.5), valueIface + ".MinimumValue": float64(1.5),
		valueIface + ".MaximumValue": uint32(100), valueIface + ".MinimumIncrement": int32(2),
		tableIface + ".NRows": int32(12), tableIface + ".NColumns": int32(4),
		documentIface + ".Locale": "en-US", documentIface + ".CurrentPageNumber": int32(2), documentIface + ".PageCount": int32(9),
	}, fail: make(map[string]bool)}
	path := dbus.ObjectPath("/org/a11y/atspi/accessible/optional")
	for _, iface := range []string{accessibleIface, propertiesIface, actionIface, selectionIface, editableTextIface} {
		if lintShadow200 := server.Export(provider, path, iface); lintShadow200 != nil {
			t.Fatalf("export %s: %v", iface, lintShadow200)
		}
	}
	client, err := dbusutil.ConnectContext(context.Background(), address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	b := &dbusBackend{generation: 1, access: client, cache: make(map[string]cachedSnapshot), cacheItems: make(map[NodeID]cacheItem), incarnations: make(map[objectIdentity]uint64), parents: make(map[objectIdentity]objectIdentity), toolkits: make(map[string]string)}
	return b, b.refID(objectRef{BusName: registryName, ObjectPath: path}), provider
}
func TestPrivateATSPIOptionalMetadataNumericConversionAndRelations(t *testing.T) {
	b, id, _ := newOptionalATSPINode(t)
	parent := NodeID{BusName: id.BusName, ObjectPath: "/synthetic/parent", Generation: id.Generation}
	node, err := b.readNodeFresh(context.Background(), id, parent, 40, false)
	if err != nil {
		t.Fatal(err)
	}
	if node.Parent != parent {
		t.Fatalf("fresh accessibility child lost its authoritative parent: got=%+v want=%+v", node.Parent, parent)
	}
	if node.Value == nil || node.Value.Current != 27.5 || node.Value.Minimum != 1.5 || node.Value.Maximum != 100 || node.Value.MinimumIncrement != 2 {
		t.Fatalf("typed value conversion lost provider measurements: %+v", node.Value)
	}
	if node.Selection == nil || node.Selection.SelectedChildCount != 0 {
		t.Fatalf("negative selection count must be clamped: %+v", node.Selection)
	}
	if node.Table == nil || node.Table.Rows != 12 || node.Table.Columns != 4 {
		t.Fatalf("table metadata: %+v", node.Table)
	}
	if node.Document == nil || node.Document.Locale != "en-US" || node.Document.CurrentPageNumber != 2 || node.Document.PageCount != 9 {
		t.Fatalf("document metadata: %+v", node.Document)
	}
	if len(node.Actions) != 2 || node.Actions[0].Name != "press" || node.Actions[0].LocalizedName != "Activate" || node.Actions[1].Name != "cancel" {
		t.Fatalf("localized and machine action names lost: %+v", node.Actions)
	}
	relation := node.Relations["labelled-by"]
	if len(relation) != 1 || relation[0].ObjectPath != "/org/a11y/atspi/accessible/label" || relation[0].Generation != id.Generation {
		t.Fatalf("relation target resolution: %+v", node.Relations)
	}
	if len(node.Relations) != 1 {
		t.Fatalf("unknown or null relation target leaked: %+v", node.Relations)
	}
	if len(node.Warnings) != 0 {
		t.Fatalf("healthy optional provider generated warnings: %v", node.Warnings)
	}
}
func TestPrivateATSPINodeSensitiveOptionalValueCannotLeak(t *testing.T) {
	b, id, provider := newOptionalATSPINode(t)
	provider.mu.Lock()
	provider.protected = true
	provider.mu.Unlock()
	safe, err := b.readNodeFresh(context.Background(), id, NodeID{}, 40, false)
	if err != nil {
		t.Fatal(err)
	}
	if !safe.Redacted || safe.Value != nil || safe.Attributes["password"] != "" || safe.Attributes["text-value"] != "" || safe.Attributes["public"] != "safe attribute" {
		t.Fatalf("protected node leaked optional values: %+v", safe)
	}
	raw, err := b.readNodeFresh(context.Background(), id, NodeID{}, 40, true)
	if err != nil {
		t.Fatal(err)
	}
	if raw.Redacted || raw.Value == nil || raw.Attributes["password"] != "hidden credential" {
		t.Fatalf("trusted provider read lost fields: %+v", raw)
	}
}
func TestPrivateATSPIOptionalProviderFailuresAreIsolatedPerField(t *testing.T) {
	b, id, provider := newOptionalATSPINode(t)
	for _, key := range []string{valueIface + ".MinimumValue", valueIface + ".MaximumValue", valueIface + ".MinimumIncrement", tableIface + ".NRows", documentIface + ".Locale", "name", "selection", "relation"} {
		provider.mu.Lock()
		provider.fail[key] = true
		provider.mu.Unlock()
	}
	node, err := b.readNodeFresh(context.Background(), id, NodeID{}, 40, false)
	if err != nil {
		t.Fatal(err)
	}
	if node.Value == nil || node.Value.Current != 27.5 {
		t.Fatalf("partial value failure removed valid current measurement: %+v", node.Value)
	}
	if node.Table == nil || node.Table.Columns != 4 {
		t.Fatalf("partial table failure removed valid dimensions: %+v", node.Table)
	}
	if node.Document == nil || node.Document.PageCount != 9 {
		t.Fatalf("partial document failure removed valid page count: %+v", node.Document)
	}
	if len(node.Actions) != 2 || node.Actions[0].Name != "" {
		t.Fatalf("partial machine-name failure erased localized metadata: %+v", node.Actions)
	}
	if node.Selection != nil || len(node.Relations) > 0 {
		t.Fatalf("failed optional interfaces published state: %+v", node)
	}
	want := []string{"MinimumValue", "MaximumValue", "MinimumIncrement", "NRows", "GetName", "GetSelectedChildCount", "GetRelationSet"}
	warnings := strings.Join(node.Warnings, "\n")
	for _, name := range want {
		if !strings.Contains(warnings, name) {
			t.Errorf("missing warning for %s: %v", name, node.Warnings)
		}
	}
	provider.mu.Lock()
	provider.fail[valueIface+".CurrentValue"] = true
	provider.mu.Unlock()
	provider.mu.Lock()
	provider.fail["actions"] = true
	provider.mu.Unlock()
	provider.mu.Lock()
	provider.fail[tableIface+".NColumns"] = true
	provider.mu.Unlock()
	provider.mu.Lock()
	provider.fail[documentIface+".CurrentPageNumber"] = true
	provider.mu.Unlock()
	provider.mu.Lock()
	provider.fail[documentIface+".PageCount"] = true
	provider.mu.Unlock()
	node, err = b.readNodeFresh(context.Background(), id, NodeID{}, 40, false)
	if err != nil {
		t.Fatal(err)
	}
	if node.Value != nil || node.Table != nil || node.Document != nil || len(node.Actions) != 0 {
		t.Fatalf("completely failed optional metadata returned stale values: %+v", node)
	}
	if !strings.Contains(strings.Join(node.Warnings, " "), "no readable document properties") {
		t.Fatalf("all-document-properties failure was not reported: %v", node.Warnings)
	}
}
func TestPrivateATSPIPropertyFloatAcceptsOnlyNumericDBusVariants(t *testing.T) {
	b, id, provider := newOptionalATSPINode(t)
	for _, tc := range []struct {
		name    string
		value   any
		want    float64
		allowed bool
	}{
		{"float64", float64(3.25), 3.25, true}, {"int32", int32(-5), -5, true}, {"uint32", uint32(7), 7, true}, {"string", "3.25", 0, false}, {"boolean", true, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider.mu.Lock()
			provider.values[valueIface+".CurrentValue"] = tc.value
			provider.mu.Unlock()
			got := math.NaN()
			err := b.propertyFloat(context.Background(), id, valueIface, "CurrentValue", &got)
			if tc.allowed {
				if err != nil || got != tc.want {
					t.Fatalf("accepted numeric variant %T: %v %v", tc.value, got, err)
				}
			} else if err == nil || !math.IsNaN(got) {
				t.Fatalf("invalid numeric variant %T accepted: %v %v", tc.value, got, err)
			}
		})
	}
	if got := relationName(1); got != "labelled-by" {
		t.Fatalf("labelled-by mapping = %q", got)
	}
	if got := relationName(1000); got != "" {
		t.Fatalf("unknown relation published = %q", got)
	}
}

// A provider can cancel its caller during a partial snapshot; its remaining
// optional-interface lookups must not turn that canceled read into success.
func TestPrivateATSPINodeReadCannotPublishAfterCancellationDuringProviderReply(t *testing.T) {
	b, id, provider := newOptionalATSPINode(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	provider.mu.Lock()
	provider.cancelRead = cancel
	provider.mu.Unlock()
	node, err := b.readNodeFresh(ctx, id, NodeID{}, 40, false)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled D-Bus read produced node %+v and error %v", node, err)
	}
	if node.ID.valid() {
		t.Fatalf("canceled node read published accessible handle %+v", node.ID)
	}
}

func TestPrivateATSPIRealDBusMutationsAreBoundedAndObservable(t *testing.T) {
	b, id, p := newOptionalATSPINode(t)
	ctx := context.Background()
	first, err := b.InvokeDefaultAction(ctx, id)
	if err != nil || first.Index != 0 || first.Name != "press" {
		t.Fatalf("default action %v %v", first, err)
	}
	second, err := b.InvokeActionByName(ctx, id, "cancel")
	if err != nil || second.Index != 1 || second.Name != "cancel" {
		t.Fatalf("named action %v %v", second, err)
	}
	if err := b.InvokeAction(ctx, id, 1); err != nil {
		t.Fatalf("indexed action: %v", err)
	}
	if err := b.InvokeAction(ctx, id, 42); !errors.Is(err, ErrInvalidAction) {
		t.Fatalf("out of range action: %v", err)
	}
	p.mu.RLock()
	n, last := p.actionCount, p.lastAction
	p.mu.RUnlock()
	if n != 3 || last != 1 {
		t.Fatalf("provider executed %d actions ending with %d", n, last)
	}
	if err := b.SetValue(ctx, id, 77.25); err != nil {
		t.Fatalf("value mutation: %v", err)
	}
	p.mu.RLock()
	got := p.values[valueIface+".CurrentValue"]
	p.mu.RUnlock()
	if got != float64(77.25) {
		t.Fatalf("real provider ignored value update: %v", got)
	}
	for _, tc := range []struct {
		name       string
		call       func() error
		last       string
		start, end int32
	}{
		{"delete", func() error { return b.DeleteText(ctx, id, 2, 6) }, "delete-text", 2, 6},
		{"copy", func() error { return b.CopyText(ctx, id, 1, 5) }, "copy-text", 1, 5},
		{"cut", func() error { return b.CutText(ctx, id, 2, 7) }, "cut-text", 2, 7},
		{"insert", func() error { return b.InsertText(ctx, id, 4, "ascii") }, "insert-text", 4, 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); err != nil {
				t.Fatal(err)
			}
			p.mu.RLock()
			last, start, end := p.lastMutation, p.lastStart, p.lastEnd
			p.mu.RUnlock()
			if last != tc.last || start != tc.start || end != tc.end {
				t.Fatalf("provider got %s %d:%d instead of %s %d:%d", last, start, end, tc.last, tc.start, tc.end)
			}
		})
	}
	if err := b.SetTextContents(ctx, id, "synthetic text"); err != nil {
		t.Fatal(err)
	}
	p.mu.RLock()
	text := p.lastText
	p.mu.RUnlock()
	if text != "synthetic text" {
		t.Fatalf("unexpected content %q", text)
	}
}
func TestPrivateATSPIMutationRejectionAndInvalidRangesNeverDispatch(t *testing.T) {
	b, id, p := newOptionalATSPINode(t)
	ctx := context.Background()
	p.mu.Lock()
	p.fail["reject-action"] = true
	p.fail["reject-delete"] = true
	p.fail["set-value"] = true
	p.mu.Unlock()
	action, err := b.InvokeActionByName(ctx, id, "press")
	if action.Name != "press" || !errors.Is(err, ErrMutationRejected) {
		t.Fatalf("rejected action returned %v %v", action, err)
	}
	if err := b.DeleteText(ctx, id, 1, 3); !errors.Is(err, ErrMutationRejected) {
		t.Fatalf("rejected deletion %v", err)
	}
	if err := b.SetValue(ctx, id, 3.5); err == nil {
		t.Fatal("failed D-Bus value update acknowledged")
	}
	p.mu.RLock()
	prev := p.actionCount
	last := p.lastMutation
	p.mu.RUnlock()
	for _, tc := range []struct {
		name string
		call func() error
	}{
		{"negative delete", func() error { return b.DeleteText(ctx, id, -1, 3) }},
		{"reversed copy", func() error { return b.CopyText(ctx, id, 5, 4) }},
		{"negative cut", func() error { return b.CutText(ctx, id, -2, 4) }},
		{"negative insert", func() error { return b.InsertText(ctx, id, -1, "abc") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); err == nil {
				t.Fatal("invalid mutation accepted")
			}
		})
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.actionCount != prev || p.lastMutation != last {
		t.Fatalf("invalid request mutated provider state")
	}
}
