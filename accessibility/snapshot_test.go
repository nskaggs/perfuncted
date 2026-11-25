package accessibility

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/godbus/dbus/v5"
	"math"
	"math/rand"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

// Diagnostics are returned to callers for inspection and JSON serialization.
// Mutating a candidate must never modify the provider snapshot or its other
// candidate results, including nested relation target slices.
func TestSemanticCandidatesDoNotAliasProviderRelationMapsAndTargets(t *testing.T) {
	id := NodeID{BusName: "org.example", ObjectPath: "/node", Generation: 7}
	target := NodeID{BusName: "org.example", ObjectPath: "/label", Generation: 7}
	other := NodeID{BusName: "org.example", ObjectPath: "/other", Generation: 7}
	snapshot := Snapshot{Nodes: []Node{{ID: id, Name: "Editor", Relations: map[string][]NodeID{"labelled-by": {target, other}}}}}
	first := CandidatesForQuery(snapshot, Query{Name: "Editor"}, 1)
	second := CandidatesForQuery(snapshot, Query{Name: "Editor"}, 1)
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("missing candidates: first=%v second=%v", first, second)
	}
	first[0].Relations["labelled-by"][0] = id
	first[0].Relations["labelled-by"] = append(first[0].Relations["labelled-by"], id)
	first[0].Relations["new-relation"] = []NodeID{id}
	if got := snapshot.Nodes[0].Relations["labelled-by"]; len(got) != 2 || got[0] != target || got[1] != other {
		t.Fatalf("mutating diagnostic result rewrote source relation %v", got)
	}
	if _, ok := snapshot.Nodes[0].Relations["new-relation"]; ok {
		t.Fatal("mutating diagnostic added relation to provider snapshot")
	}
	if got := second[0].Relations["labelled-by"]; len(got) != 2 || got[0] != target || got[1] != other {
		t.Fatalf("mutating result corrupted independent diagnostic %v", got)
	}
}

// referenceJSONStringSize is the pre-optimization implementation, kept here so
// the table-driven version can be proven to produce identical sizes.
func referenceJSONStringSize(value string) int {
	n := 2
	for i := 0; i < len(value); {
		switch value[i] {
		case '"', '\\':
			n = addJSONSize(n, 2)
			i++
		case '\b', '\f', '\n', '\r', '\t':
			n = addJSONSize(n, 2)
			i++
		case '<', '>', '&':
			n = addJSONSize(n, 6)
			i++
		default:
			if value[i] < 0x20 {
				n = addJSONSize(n, 6)
				i++
				continue
			}
			if value[i] < utf8.RuneSelf {
				n = addJSONSize(n, 1)
				i++
				continue
			}
			r, size := utf8.DecodeRuneInString(value[i:])
			if r == utf8.RuneError && size == 1 {
				n = addJSONSize(n, 3)
				i++
				continue
			}
			if r == '\u2028' || r == '\u2029' {
				n = addJSONSize(n, 6)
			} else {
				n = addJSONSize(n, size)
			}
			i += size
		}
	}
	return n
}

func TestJSONStringSizeMatchesReferenceForEveryByte(t *testing.T) {
	for b := 0; b < 256; b++ {
		value := string([]byte{byte(b)})
		got, want := jsonStringSize(value), referenceJSONStringSize(value)
		if got != want {
			t.Fatalf("jsonStringSize(%q) = %d, want %d", value, got, want)
		}
	}
}

func TestJSONStringSizeMatchesReferenceOnMixedInput(t *testing.T) {
	rng := rand.New(rand.NewSource(20261002))
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	for range 200000 {
		n := rng.Intn(24)
		buf := make([]byte, n)
		for i := range buf {
			buf[i] = all[rng.Intn(len(all))]
		}
		value := string(buf)
		if got, want := jsonStringSize(value), referenceJSONStringSize(value); got != want {
			t.Fatalf("jsonStringSize(%q) = %d, want %d", value, got, want)
		}
	}
	// Valid multi-byte runes, including the ones that need escaping.
	for r := rune(0x80); r <= utf8.MaxRune; r++ {
		if !utf8.ValidRune(r) {
			continue
		}
		value := fmt.Sprintf("a%cb", r)
		if got, want := jsonStringSize(value), referenceJSONStringSize(value); got != want {
			t.Fatalf("jsonStringSize(%q) = %d, want %d", value, got, want)
		}
	}
	// Long realistic strings.
	for _, s := range []string{
		strings.Repeat("plain ascii text ", 50),
		strings.Repeat(`quote " backslash \ angle < gt > amp & `, 20),
		strings.Repeat("Ünïcödé", 40),
		strings.Repeat("\x00\x01\x1f\x7f\xff", 30),
		strings.Repeat("日本語テキスト", 40),
	} {
		if got, want := jsonStringSize(s), referenceJSONStringSize(s); got != want {
			t.Fatalf("jsonStringSize(long %d bytes) = %d, want %d", len(s), got, want)
		}
	}
}

func benchmarkStrings() []string {
	return []string{
		"org.gnome.Nautilus", // bus name, repeated per node
		"/org/gnome/Nautilus/desktop/window/1234567890/content", // object path
		"Push button", // role
		"Open Folder", // short name
		"Some longer label text that a real accessibility tree carries on a leaf node",
		"Editable text content for a text field that the user can type into and that carries a fair amount of content",
		"https://example.invalid/some/reasonably/long/url/path?query=1&other=2",
		"Ünïcödé ñame with multibyte runes and more text after them to scan",
		"",
	}
}

func BenchmarkJSONStringSize(b *testing.B) {
	values := benchmarkStrings()
	b.ReportAllocs()
	for b.Loop() {
		total := 0
		for _, v := range values {
			total += jsonStringSize(v)
		}
		if total == 0 {
			b.Fatal("zero total")
		}
	}
}

func BenchmarkJSONStringSizeASCIIOnly(b *testing.B) {
	v := "Some longer label text that a real accessibility tree carries on a leaf node"
	b.ReportAllocs()
	for b.Loop() {
		if jsonStringSize(v) == 0 {
			b.Fatal("zero size")
		}
	}
}

func BenchmarkJSONStringSizeRepeatedBusName(b *testing.B) {
	v := "org.gnome.Nautilus"
	b.ReportAllocs()
	for b.Loop() {
		for range 64 {
			_ = jsonStringSize(v)
		}
	}
}

func TestMatchErrorNeverPanicsWhenOptionalCauseMissing(t *testing.T) {
	for _, err := range []*MatchError{
		nil,
		{},
		{Operation: "find button"},
	} {
		func() {
			defer func() {
				if p := recover(); p != nil {
					t.Fatalf("formatting missing-cause accessibility MatchError panicked: %v", p)
				}
			}()
			var asError error = err
			if str := asError.Error(); str == "" {
				t.Fatal("error formatting returned empty diagnostic")
			}
			if str := err.Error(); str == "" {
				t.Fatal("missing error text")
			}
		}()
	}
}
func TestMatchErrorPreservesCauseIdentity(t *testing.T) {
	cause := errors.New("not found")
	err := &MatchError{Operation: "find button", Err: cause}
	if !errors.Is(err, cause) {
		t.Fatalf("cause lost: %v", err)
	}
}

// Outline must route through the same scoped, redacting snapshot as ordinary
// automation. A provider that marks a subtree sensitive may not disclose
// its text through this alternative diagnostic API.
func TestSyntheticATSPIBackendOutlineKeepsSnapshotSecurityAndScope(t *testing.T) {
	backend, root, _ := newSyntheticATSPIBackend(t, true)
	opts := SnapshotOptions{Fresh: true, MaxDepth: 3, MaxNodes: 3}
	outline, err := backend.Outline(context.Background(), root, opts, OutlineOptions{MaxDepth: 2, MaxNodes: 3})
	if err != nil {
		t.Fatal(err)
	}
	if outline.Root.ID != root || outline.Generation != root.Generation || outline.Root.Text != "" {
		t.Fatalf("outline bypassed snapshot privacy or root scope: %+v", outline)
	}
	allowed, err := backend.Outline(context.Background(), root, SnapshotOptions{Fresh: true, AllowSensitive: true}, OutlineOptions{MaxDepth: 3, MaxNodes: 3})
	if err != nil || allowed.Root.Text != "sensitive text" {
		t.Fatalf("explicitly authorized outline: %+v err=%v", allowed, err)
	}
	if _, err := backend.Outline(context.Background(), NodeID{}, opts, OutlineOptions{}); !errors.Is(err, ErrScope) {
		t.Fatalf("unscoped outline bypassed snapshot scope: %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := backend.Outline(canceled, root, opts, OutlineOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled diagnostic outline: %v", err)
	}
	backend.Invalidate(root)
	if _, err := backend.Outline(context.Background(), root, opts, OutlineOptions{}); !errors.Is(err, ErrStaleNode) && !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("outline accepted stale provider object: %v", err)
	}
}

// A malformed AT-SPI parent cycle cannot supply distinct ancestors by
// traversing the same object twice. Ordered ancestor constraints require an
// actual finite chain above the target, not a repeated semantic identity.
func TestSemanticSelectorRejectsRepeatedAncestorFromParentCycle(t *testing.T) {
	targetID := NodeID{BusName: "org.synthetic.App", ObjectPath: "/target", Generation: 7}
	containerID := NodeID{BusName: targetID.BusName, ObjectPath: "/container", Generation: 7}
	intermediateID := NodeID{BusName: targetID.BusName, ObjectPath: "/middle", Generation: 7}
	target := Node{ID: targetID, Role: "entry", Parent: containerID}
	container := Node{ID: containerID, Role: "group", Name: "Compose", Parent: intermediateID}
	middle := Node{ID: intermediateID, Role: "section", Name: "Details", Parent: containerID}
	nodes := map[NodeID]Node{targetID: target, containerID: container, intermediateID: middle}
	repeated := []AncestorSelector{{Role: "group", Name: "Compose"}, {Role: "group", Name: "Compose"}}
	if matchesAncestors(nodes, target, repeated) {
		t.Fatal("cyclic AT-SPI parent chain re-used the same Compose group for two required ancestors")
	}
	if matchesAncestors(nodes, target, []AncestorSelector{{Role: "group", Name: "Compose"}, {Role: "section", Name: "Details"}, {Role: "group", Name: "Compose"}}) {
		t.Fatal("cyclic AT-SPI parent chain re-used an ancestor after visiting a different parent")
	}
	// A real distinct outer group must still authorize repeated constraints.
	outerID := NodeID{BusName: targetID.BusName, ObjectPath: "/outer", Generation: 7}
	middle.Parent = outerID
	nodes[intermediateID] = middle
	nodes[outerID] = Node{ID: outerID, Role: "group", Name: "Compose"}
	if !matchesAncestors(nodes, target, repeated) {
		t.Fatal("two distinct Compose ancestors were rejected")
	}
	if !matchesAncestors(nodes, target, []AncestorSelector{{Role: "group", Name: "Compose"}, {Role: "section", Name: "Details"}, {Role: "group", Name: "Compose"}}) {
		t.Fatal("acyclic ordered ancestor chain was rejected")
	}
}

// Semantic discovery must uphold the same ancestry identity rule when it
// filters a captured graph, not only when the low-level predicate is called.
func TestFilterSnapshotSelectorDoesNotPublishCyclicAncestorMatch(t *testing.T) {
	targetID := NodeID{BusName: "org.synthetic.App", ObjectPath: "/target", Generation: 8}
	groupID := NodeID{BusName: targetID.BusName, ObjectPath: "/group", Generation: 8}
	sectionID := NodeID{BusName: targetID.BusName, ObjectPath: "/section", Generation: 8}
	target := Node{ID: targetID, Role: "entry", Name: "Address", Parent: groupID}
	group := Node{ID: groupID, Role: "group", Name: "Compose", Parent: sectionID}
	section := Node{ID: sectionID, Role: "section", Parent: groupID}
	snapshot := Snapshot{Nodes: []Node{target, group, section}}
	selector := Selector{Role: "entry", Name: "Address", Ancestors: []AncestorSelector{{Role: "group", Name: "Compose"}, {Role: "group", Name: "Compose"}}}
	if got := FilterSnapshotSelector(snapshot, selector); len(got) != 0 {
		t.Fatalf("cyclic parent data published unsupported semantic match: %+v", got)
	}
	outerID := NodeID{BusName: targetID.BusName, ObjectPath: "/outer", Generation: 8}
	snapshot.Nodes[2].Parent = outerID
	snapshot.Nodes = append(snapshot.Nodes, Node{ID: outerID, Role: "group", Name: "Compose"})
	if got := FilterSnapshotSelector(snapshot, selector); len(got) != 1 || got[0].ID != targetID {
		t.Fatalf("finite, distinct ancestor chain could not select target: %+v", got)
	}
}

// The selected object can never be its own ancestor, even when a broken AT-SPI
// provider supplies a self-parent pointer or a cycle back through a container.
func TestFilterSnapshotSelectorDoesNotTreatTargetAsItsOwnAncestor(t *testing.T) {
	targetID := NodeID{BusName: "org.synthetic.App", ObjectPath: "/target", Generation: 8}
	parentID := NodeID{BusName: targetID.BusName, ObjectPath: "/parent", Generation: 8}
	target := Node{ID: targetID, Role: "entry", Name: "Name", Parent: targetID}
	selector := Selector{Role: "entry", Name: "Name", Ancestors: []AncestorSelector{{Role: "entry", Name: "Name"}}}
	if got := FilterSnapshotSelector(Snapshot{Nodes: []Node{target}}, selector); len(got) != 0 {
		t.Fatalf("self-parenting target satisfied its own ancestor constraint: %+v", got)
	}
	target.Parent = parentID
	parent := Node{ID: parentID, Role: "group", Name: "Form", Parent: targetID}
	if got := FilterSnapshotSelector(Snapshot{Nodes: []Node{target, parent}}, selector); len(got) != 0 {
		t.Fatalf("target reappeared as ancestor after a parent cycle: %+v", got)
	}
	// Non-cyclic parent chains must still resolve ordinary semantic selectors.
	parent.Parent = NodeID{}
	selector.Ancestors = []AncestorSelector{{Role: "group", Name: "Form"}}
	if got := FilterSnapshotSelector(Snapshot{Nodes: []Node{target, parent}}, selector); len(got) != 1 || got[0].ID != targetID {
		t.Fatalf("valid parent chain unexpectedly rejected: %+v", got)
	}
}

// A multi-action node has array separators and string escaping on every
// action. Underestimating either permits responses beyond the caller's budget.
func TestSnapshotActionArrayBudgetAccountsForEveryAction(t *testing.T) {
	actions := []Action{
		{Index: 0, Name: "Open", KeyBinding: "Ctrl+O"},
		{Index: 1, Name: "Save & Close", Description: "A \"private\" document <snapshot>"},
		{Index: 2, Name: "Dismiss", LocalizedName: "Fermé"},
	}
	encoded, err := json.Marshal(actions)
	if err != nil {
		t.Fatal(err)
	}
	if estimated := estimateActionsJSONSize(actions); estimated < len(encoded) {
		t.Fatalf("multi-action JSON underestimated: estimated=%d actual=%d", estimated, len(encoded))
	}
	root := NodeID{BusName: "org.example.Editor", ObjectPath: "/app", Generation: 8}
	snapshot := Snapshot{Root: Node{ID: root, Actions: actions}, Nodes: []Node{{ID: root, Actions: actions}}, Generation: 8}
	encoded, err = json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if estimated := snapshotJSONSize(snapshot); estimated < len(encoded) {
		t.Fatalf("snapshot with multiple actions underestimated: estimated=%d actual=%d", estimated, len(encoded))
	}
	limit := len(encoded) - 1
	bounded, err := boundSnapshotResponse(snapshot, limit)
	if err != nil {
		return // A minimum safe response can be larger than the requested budget.
	}
	result, err := json.Marshal(bounded)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) > limit || !bounded.Truncated {
		t.Fatalf("budget exceeded or truncation unreported: bytes=%d limit=%d truncated=%v", len(result), limit, bounded.Truncated)
	}
}

// An absent collection must contribute zero bytes; a nil response must not
// mutate retained nodes or authorize references from a previous snapshot.
func TestSnapshotMissingCollectionsAndNilReferencePruningRemainInert(t *testing.T) {
	if n := estimateStringSliceJSONSize(nil); n != 0 {
		t.Fatalf("nil string slice charged %d bytes", n)
	}
	if n := estimateStringSliceJSONSize([]string{}); n != 0 {
		t.Fatalf("empty string slice charged %d bytes", n)
	}
	var absent *Snapshot
	pruneSnapshotReferences(absent)
	pruneSnapshotReferencesWithRetained(absent, map[NodeID]struct{}{})
	id := NodeID{BusName: "org.example.Editor", ObjectPath: "/app", Generation: 8}
	node := Node{ID: id, Children: []NodeID{{BusName: id.BusName, ObjectPath: "/removed", Generation: id.Generation}}}
	snapshot := Snapshot{Root: node, Nodes: []Node{node}}
	pruneSnapshotReferencesWithRetained(&snapshot, map[NodeID]struct{}{id: {}})
	if len(snapshot.Root.Children) != 0 {
		t.Fatalf("orphan children retained: %+v", snapshot.Root.Children)
	}
}

// benchmarkNodeCount is the tree size the snapshot benchmarks exercise. Real
// application trees routinely reach and exceed this, which is what pushes the
// default 1 MiB response budget into truncation.
const benchmarkNodeCount = 2000

// buildBenchmarkSnapshot produces a tree with the shape of a real application:
// a root, a handful of framed children, and mostly leaf text nodes carrying
// names, roles, states, and bounds.
func buildBenchmarkSnapshot() Snapshot {
	nodeCount := benchmarkNodeCount
	const busName = "org.test.App"
	rootID := NodeID{BusName: busName, ObjectPath: "/app", Generation: 1}

	nodes := make([]Node, 0, nodeCount)
	nodes = append(nodes, Node{
		ID: rootID, Role: "application", Name: "Test Application",
		States: []string{"focused", "visible"}, Bounds: Rect{X: 0, Y: 0, Width: 1920, Height: 1080},
		HasBounds: true, Visible: true, Showing: true, Enabled: true, ChildCount: 1,
	})
	for i := range nodeCount - 1 {
		path := fmt.Sprintf("/app/node-%04d", i)
		id := NodeID{BusName: busName, ObjectPath: path, Generation: 1}
		role := "text"
		if i%17 == 0 {
			role = "push button"
		}
		name := fmt.Sprintf("Label %d with some descriptive text", i)
		nodes = append(nodes, Node{
			ID: id, Parent: rootID, Role: role, Name: name,
			Description: "A longer description string used to estimate serialized size",
			States:      []string{"visible", "showing"},
			Text:        fmt.Sprintf("Some editable text content for node %d that runs on a bit", i),
			Bounds:      Rect{X: i % 1800, Y: i % 900, Width: 320, Height: 48},
			HasBounds:   true,
			Visible:     true,
			Showing:     true,
			Enabled:     true,
			ChildCount:  0,
			Interfaces:  []string{"org.a11y.atspi.Text", "org.a11y.atspi.Component"},
			Attributes:  map[string]string{"toolkit": "gtk4", "version": "4.14"},
			Children:    nil,
		})
	}
	nodes[0].Children = []NodeID{nodes[1].ID}
	return Snapshot{
		Root: nodes[0], Nodes: nodes, Generation: 1,
		CapturedAt: time.Unix(1750000000, 0).UTC(), Source: "provider-cache",
	}
}

// A snapshot that fits the byte budget takes the single-pass path.
func BenchmarkBoundSnapshotResponseWithinBudget(b *testing.B) {
	snapshot := buildBenchmarkSnapshot()
	const budget = 8 << 20
	const maxAllocations = 4500
	allocations := testing.AllocsPerRun(5, func() {
		if _, err := boundSnapshotResponse(snapshot, budget); err != nil {
			b.Fatalf("boundSnapshotResponse allocation check: %v", err)
		}
	})
	if allocations > float64(maxAllocations) {
		b.Fatalf("in-budget snapshot allocations = %.1f, want at most %d", allocations, maxAllocations)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		bounded, err := boundSnapshotResponse(snapshot, budget)
		if err != nil {
			b.Fatalf("boundSnapshotResponse: %v", err)
		}
		if len(bounded.Nodes) != len(snapshot.Nodes) {
			b.Fatalf("nodes = %d, want %d", len(bounded.Nodes), len(snapshot.Nodes))
		}
	}
}

// An over-budget snapshot searches node prefixes and accounts for references
// removed from each candidate.
func BenchmarkBoundSnapshotResponseTruncating(b *testing.B) {
	snapshot := buildBenchmarkSnapshot()
	const budget = 48 << 10
	const maxAllocations = 400
	allocations := testing.AllocsPerRun(5, func() {
		if _, err := boundSnapshotResponse(snapshot, budget); err != nil {
			b.Fatalf("boundSnapshotResponse allocation check: %v", err)
		}
	})
	if allocations > float64(maxAllocations) {
		b.Fatalf("truncated snapshot allocations = %.1f, want at most %d", allocations, maxAllocations)
	}
	projected, err := boundSnapshotResponse(snapshot, budget)
	if err != nil {
		b.Fatalf("boundSnapshotResponse metric sample: %v", err)
	}
	responseBytes := snapshotJSONSize(projected)
	if responseBytes > budget {
		b.Fatalf("truncated response size = %d, want at most %d", responseBytes, budget)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := boundSnapshotResponse(snapshot, budget); err != nil {
			b.Fatalf("boundSnapshotResponse: %v", err)
		}
	}
	b.ReportMetric(float64(len(snapshot.Nodes)), "input_nodes/op")
	b.ReportMetric(float64(len(projected.Nodes)), "returned_nodes/op")
	b.ReportMetric(float64(responseBytes), "response_bytes/op")
}

func BenchmarkSnapshotJSONSize(b *testing.B) {
	snapshot := buildBenchmarkSnapshot()
	b.ReportAllocs()
	for b.Loop() {
		if snapshotJSONSize(snapshot) == 0 {
			b.Fatal("estimated size is zero")
		}
	}
}

func BenchmarkPruneSnapshotReferences(b *testing.B) {
	snapshot := buildBenchmarkSnapshot()
	b.ReportAllocs()
	for b.Loop() {
		candidate := snapshot
		candidate.Nodes = append([]Node(nil), snapshot.Nodes...)
		pruneSnapshotReferences(&candidate)
	}
}

func BenchmarkSnapshotKey(b *testing.B) {
	id := NodeID{BusName: "org.test.App", ObjectPath: "/app", Generation: 3, Incarnation: 1}
	opts := SnapshotOptions{MaxDepth: 20, MaxNodes: 4000, MaxTextBytes: 4096, MaxTotalBytes: 8 << 20}
	b.ReportAllocs()
	for b.Loop() {
		if snapshotKey(id, opts) == "" {
			b.Fatal("empty snapshot key")
		}
	}
}

func TestSyntheticATSPIDesktopSnapshotIsOptInAndCacheOwned(t *testing.T) {
	backend, _, _ := newSyntheticATSPIBackend(t, false)
	ctx := context.Background()

	if _, err := backend.Snapshot(ctx, NodeID{}, SnapshotOptions{}); !errors.Is(err, ErrScope) {
		t.Fatalf("unscoped desktop traversal returned %v", err)
	}

	opts := SnapshotOptions{AllowDesktopRoot: true, MaxNodes: 3}
	snapshot, err := backend.Snapshot(ctx, NodeID{}, opts)
	if err != nil {
		t.Fatalf("opted-in desktop snapshot: %v", err)
	}
	if snapshot.Root.ID.ObjectPath != string(desktopPath) || len(snapshot.Nodes) != 3 || snapshot.Truncated {
		t.Fatalf("desktop snapshot has wrong scope or bounds: root=%+v nodes=%d truncated=%t", snapshot.Root.ID, len(snapshot.Nodes), snapshot.Truncated)
	}
	if snapshot.Nodes[0].ID.ObjectPath != string(desktopPath) || snapshot.Nodes[1].Parent != snapshot.Nodes[0].ID || snapshot.Nodes[2].Parent != snapshot.Nodes[1].ID {
		t.Fatalf("desktop traversal lost parent-linked order: %+v", snapshot.Nodes)
	}

	// Make cache reuse deterministic while preserving the production snapshot
	// captured above. Snapshot results are caller-owned and must not mutate it.
	key := snapshotKey(backend.desktop(), opts.normalized())
	backend.mu.Lock()
	entry, ok := backend.cache[key]
	if !ok {
		backend.mu.Unlock()
		t.Fatal("successful desktop snapshot was not published to cache")
	}
	entry.at = time.Now().Add(time.Second)
	backend.cache[key] = entry
	backend.mu.Unlock()

	snapshot.Nodes[0].Name = "caller mutation"
	snapshot.Nodes[0].Children[0] = NodeID{}
	snapshot.Nodes[2].Attributes["role"] = "caller mutation"
	cached, err := backend.Snapshot(ctx, NodeID{}, opts)
	if err != nil {
		t.Fatalf("cached desktop snapshot: %v", err)
	}
	if cached.Nodes[0].Name != "Desktop" || cached.Nodes[0].Children[0].ObjectPath != "/org/a11y/atspi/accessible/app" || cached.Nodes[2].Attributes["role"] != "editor" {
		t.Fatalf("caller mutation escaped into cached desktop snapshot: %+v", cached.Nodes)
	}
}

func TestBoundNodeResponsePrunesOptionalFieldsWithinBudget(t *testing.T) {
	id := NodeID{BusName: "org.example.Editor", ObjectPath: "/editor", Generation: 1}
	tests := []struct {
		name  string
		apply func(*Node)
		check func(Node) bool
	}{
		{"text", func(n *Node) { n.Text = "界面 content repeated界面 content repeated" }, func(n Node) bool {
			return n.TextTruncated && len(n.Text) < len("界面 content repeated界面 content repeated")
		}},
		{"attributes", func(n *Node) { n.Attributes = map[string]string{"label": "an optional accessible label"} }, func(n Node) bool { return n.Attributes == nil }},
		{"relations", func(n *Node) {
			n.Relations = map[string][]NodeID{"labelled-by": {{BusName: id.BusName, ObjectPath: "/label", Generation: id.Generation}}}
		}, func(n Node) bool { return n.Relations == nil }},
		{"actions", func(n *Node) { n.Actions = []Action{{Index: 0, Name: "activate", Description: "activate editor"}} }, func(n Node) bool { return n.Actions == nil }},
		{"warnings", func(n *Node) { n.Warnings = []string{"provider returned optional metadata warning"} }, func(n Node) bool { return n.Warnings == nil }},
		{"description", func(n *Node) { n.Description = "an optional verbose description" }, func(n Node) bool { return n.Description == "" }},
		{"name", func(n *Node) { n.Name = "an optional verbose accessible name" }, func(n Node) bool { return n.Name == "" }},
		{"interfaces", func(n *Node) { n.Interfaces = []string{"org.example.Interface", "org.example.OptionalInterface"} }, func(n Node) bool { return n.Interfaces == nil }},
		{"states", func(n *Node) { n.States = []string{"enabled", "focused", "visible", "showing"} }, func(n Node) bool { return n.States == nil }},
		{"children", func(n *Node) {
			n.Children = []NodeID{{BusName: id.BusName, ObjectPath: "/child", Generation: id.Generation}}
		}, func(n Node) bool { return n.Children == nil }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := Node{ID: id}
			limitNode := Node{ID: id}
			if tt.name == "text" {
				limitNode.TextTruncated = true
			}
			tt.apply(&node)
			limit := estimateNodeJSONSize(limitNode) + 60
			got := boundNodeResponse(node, limit)
			if !tt.check(got) {
				t.Fatalf("budget pruning retained optional %s: %+v", tt.name, got)
			}
			if got.ID != id {
				t.Fatalf("budget pruning changed stable node identity: %+v", got.ID)
			}
			if estimateNodeJSONSize(got)+60 > limit {
				t.Fatalf("projected node exceeds response budget: size=%d limit=%d", estimateNodeJSONSize(got)+60, limit)
			}
		})
	}
}

func TestATSPIEventsRejectRetiredGeneration(t *testing.T) {
	_, backend, _ := startSyntheticEventBus(t, nil)
	if err := backend.RetireEventsForReopen(context.Background()); err != nil {
		t.Fatalf("retire event generation: %v", err)
	}
	stream, err := backend.Events(context.Background(), EventOptions{})
	if stream != nil || !errors.Is(err, ErrDisconnected) {
		t.Fatalf("retired event generation admitted subscriber: stream=%v err=%v", stream, err)
	}
}

func TestSnapshotRejectsGenerationInvalidatedDuringCacheLoad(t *testing.T) {
	backend, provider := newPrivateCacheBackend(t, syntheticCacheItems())
	root := NodeID{BusName: syntheticCacheService, ObjectPath: "/app", Generation: backend.Generation(), Incarnation: 1}
	opts := SnapshotOptions{}.normalized()
	backend.mu.Lock()
	if backend.cache == nil {
		backend.cache = make(map[string]cachedSnapshot)
	}
	backend.cache[snapshotKey(root, opts)] = cachedSnapshot{
		at: time.Now(),
		snapshot: Snapshot{
			Root:       Node{ID: root, Name: "snapshot from the retired generation"},
			Nodes:      []Node{{ID: root, Name: "snapshot from the retired generation"}},
			Generation: root.Generation,
		},
	}
	backend.mu.Unlock()
	provider.mu.Lock()
	provider.beforeReturn = func() { backend.Invalidate(root) }
	provider.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	snapshot, err := backend.Snapshot(ctx, root, SnapshotOptions{})
	if !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("snapshot during cache generation change = %+v, %v; want stale-generation error", snapshot, err)
	}
	if len(snapshot.Nodes) != 0 || snapshot.Root.Name != "" {
		t.Fatalf("snapshot exposed retired cache data: %+v", snapshot)
	}
	if len(backend.cache) != 0 || len(backend.cacheItems) != 0 || len(backend.cacheApps) != 0 {
		t.Fatalf("generation change retained local or provider cache state: snapshots=%d items=%d apps=%d", len(backend.cache), len(backend.cacheItems), len(backend.cacheApps))
	}
	provider.mu.Lock()
	calls := provider.calls
	provider.mu.Unlock()
	if calls != 1 {
		t.Fatalf("cache provider calls = %d, want exactly one in-flight read", calls)
	}
}

// Snapshot SkipRoles controls whether a subtree is explored. A cache entry
// captured under one set of roles must never be reused by a distinct set of
// skipped roles, even when names themselves contain separators.
func TestSnapshotKeyUniquelyEncodesSkipRoleBoundaries(t *testing.T) {
	root := NodeID{BusName: "org.synthetic.App", ObjectPath: "/application", Generation: 11, Incarnation: 4}
	cases := []struct {
		name  string
		roles []string
	}{
		{"no skips", nil},
		{"single role", []string{"menu"}},
		{"two roles", []string{"menu", "item"}},
		{"comma in role", []string{"menu,item"}},
		{"three roles", []string{"menu", "item", "dialog"}},
		{"two composite roles", []string{"menu,item", "dialog"}},
		{"one composite role", []string{"menu,item,dialog"}},
		{"trailing empty role", []string{"menu", ""}},
		{"single role with comma", []string{"menu,"}},
		{"empty role", []string{""}},
		{"two empty roles", []string{"", ""}},
	}
	keys := map[string]string{}
	for _, tc := range cases {
		key := snapshotKey(root, SnapshotOptions{SkipRoles: tc.roles})
		if prev, ok := keys[key]; ok {
			t.Fatalf("cache key collision between %q and %q: a truncated snapshot could be returned for a different traversal", prev, tc.name)
		}
		keys[key] = tc.name
	}
}

// A cached snapshot contains private accessibility fields. A caller that
// canceled while the backend was validating connectivity must not receive
// that cached data merely because the cache lookup itself is fast.
func TestATSPICachedSnapshotDoesNotPublishAfterCancellationDuringAdmission(t *testing.T) {
	b, root, _ := newSyntheticATSPIBackend(t, false)
	opts := SnapshotOptions{}
	previous, err := b.Snapshot(context.Background(), root, opts)
	if err != nil || len(previous.Nodes) == 0 {
		t.Fatalf("fixture did not populate a real cached snapshot: nodes=%d err=%v", len(previous.Nodes), err)
	}
	b.mu.RLock()
	_, cached := b.cache[snapshotKey(root, opts.normalized())]
	b.mu.RUnlock()
	if !cached {
		t.Fatal("fixture missing cached snapshot entry")
	}
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &observedEventContext{Context: base, firstRead: make(chan struct{})}
	type result struct {
		snapshot Snapshot
		err      error
	}
	out := make(chan result, 1)
	b.mu.Lock()
	go func() { snapshot, err := b.Snapshot(ctx, root, opts); out <- result{snapshot, err} }()
	select {
	case <-ctx.firstRead:
	case <-time.After(time.Second):
		b.mu.Unlock()
		t.Fatal("snapshot admission did not inspect caller cancellation")
	}
	cancel()
	b.mu.Unlock()
	select {
	case value := <-out:
		if !errors.Is(value.err, context.Canceled) || len(value.snapshot.Nodes) != 0 || value.snapshot.Root.Name != "" {
			t.Fatalf("canceled cached snapshot exposed private nodes: root=%q nodes=%d err=%v", value.snapshot.Root.Name, len(value.snapshot.Nodes), value.err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled snapshot did not terminate")
	}
}

type cancelOnContextErrorRead struct {
	context.Context
	cancel    context.CancelFunc
	cancelAt  int
	errorRead int
}

func (c *cancelOnContextErrorRead) Err() error {
	c.errorRead++
	if c.errorRead == c.cancelAt {
		c.cancel()
	}
	return c.Context.Err()
}

func TestATSPICachedSnapshotRejectsCancellationAtReturnBoundary(t *testing.T) {
	backend, root, _ := newSyntheticATSPIBackend(t, false)
	defer backend.Close()
	options := SnapshotOptions{}
	if snapshot, err := backend.Snapshot(context.Background(), root, options); err != nil || len(snapshot.Nodes) == 0 {
		t.Fatalf("populate cached snapshot: nodes=%d err=%v", len(snapshot.Nodes), err)
	}
	key := snapshotKey(root, options.normalized())
	backend.mu.RLock()
	_, cached := backend.cache[key]
	backend.mu.RUnlock()
	if !cached {
		t.Fatal("snapshot fixture did not publish its cache entry")
	}

	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Snapshot checks cancellation during admission, then once more after it
	// clones the cache entry and before it returns that private data.
	ctx := &cancelOnContextErrorRead{Context: base, cancel: cancel, cancelAt: 3}
	snapshot, err := backend.Snapshot(ctx, root, options)
	if !errors.Is(err, context.Canceled) || len(snapshot.Nodes) != 0 || snapshot.Root.ID.valid() {
		t.Fatalf("canceled cache hit exposed snapshot data: nodes=%d root=%+v err=%v", len(snapshot.Nodes), snapshot.Root, err)
	}
	if ctx.errorRead != ctx.cancelAt {
		t.Fatalf("cancellation observed at context error read %d, want return-boundary read %d", ctx.errorRead, ctx.cancelAt)
	}
	backend.mu.RLock()
	_, cached = backend.cache[key]
	backend.mu.RUnlock()
	if !cached {
		t.Fatal("caller cancellation mutated the backend's valid cached snapshot")
	}
}

func TestSnapshotCancellationDuringProviderTraversalDiscardsPartialNodes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend, root, _ := newSyntheticATSPIBackendWithFrameSetup(t, false, func(_, frame *syntheticATSPINode) {
		frame.children = []objectRef{{BusName: registryName, ObjectPath: "/org/a11y/atspi/accessible/child"}}
		frame.childrenHook = func() { cancel() }
	})

	snapshot, err := backend.Snapshot(ctx, root, SnapshotOptions{Fresh: true})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("snapshot after provider-boundary cancellation = %v", err)
	}
	if len(snapshot.Nodes) != 0 || snapshot.Root.ID.valid() {
		t.Fatalf("canceled snapshot exposed partial traversal: %+v", snapshot)
	}
	if len(backend.cache) != 0 {
		t.Fatalf("canceled snapshot published %d cache entries", len(backend.cache))
	}
}

func TestSnapshotDoesNotPublishAfterBackendRetiresDuringTraversal(t *testing.T) {
	var liveBackend atomic.Pointer[dbusBackend]
	var root NodeID
	backend, root, _ := newSyntheticATSPIBackendWithFrameSetup(t, false, func(_, frame *syntheticATSPINode) {
		frame.children = []objectRef{{BusName: registryName, ObjectPath: "/org/a11y/atspi/accessible/child"}}
		frame.childrenHook = func() {
			current := liveBackend.Load()
			_ = current.Close()
		}
	})
	liveBackend.Store(backend)

	snapshot, err := backend.Snapshot(context.Background(), root, SnapshotOptions{Fresh: true})
	if !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("snapshot published after backend retirement: %v", err)
	}
	if len(snapshot.Nodes) != 0 || snapshot.Root.ID.valid() {
		t.Fatalf("retired backend exposed unpublished nodes: %+v", snapshot)
	}
	if len(backend.cache) != 0 {
		t.Fatalf("retired backend published %d cache entries", len(backend.cache))
	}
}

// Fresh snapshots must establish not merely that an added object exists, but
// that its role, state, interfaces, parent and child count correspond to the
// provider's concurrent Cache:AddAccessible signal. Incomplete observations
// must never be presented as verified negative search results.
func TestFreshSnapshotCacheAdditionRejectsMismatchedProviderObservations(t *testing.T) {
	root := NodeID{BusName: "org.example.App", ObjectPath: "/window", Generation: 3}
	child := NodeID{BusName: root.BusName, ObjectPath: "/window/button", Generation: 3}
	item := cacheItem{Object: cacheObjectRef{BusName: root.BusName, ObjectPath: dbus.ObjectPath(child.ObjectPath)}, Application: cacheObjectRef{BusName: root.BusName, ObjectPath: "/app"}, Parent: cacheObjectRef{BusName: root.BusName, ObjectPath: dbus.ObjectPath(root.ObjectPath)}, Role: 43, Name: "Save", Description: "Write document", ChildCount: 0, Interfaces: []string{accessibleIface, actionIface}, States: []uint32{1 << 8}}
	baseParent := Node{ID: root, Role: "window", Children: []NodeID{child}}
	baseChild := Node{ID: child, Parent: root, RoleID: 43, Name: "Save", Description: "Write document", ChildCount: 0, Interfaces: []string{actionIface, accessibleIface}, States: []string{stateName(8)}}
	for _, tc := range []struct {
		name   string
		change func(*Node, *Node, *cacheItem)
		accept bool
	}{
		{"matching complete snapshot", func(*Node, *Node, *cacheItem) {}, true},
		{"missing added child", func(p *Node, _ *Node, _ *cacheItem) { p.Children = nil }, false},
		{"unexpected parent", func(_ *Node, c *Node, _ *cacheItem) {
			c.Parent = NodeID{BusName: root.BusName, ObjectPath: "/other", Generation: 3}
		}, false},
		{"forged name", func(_ *Node, c *Node, _ *cacheItem) { c.Name = "Save As" }, false},
		{"forged description", func(_ *Node, c *Node, _ *cacheItem) { c.Description = "Different" }, false},
		{"mismatched role", func(_ *Node, c *Node, _ *cacheItem) { c.RoleID = 12 }, false},
		{"mismatched interface", func(_ *Node, c *Node, _ *cacheItem) { c.Interfaces = []string{accessibleIface} }, false},
		{"missing state", func(_ *Node, c *Node, _ *cacheItem) { c.States = nil }, false},
		{"extra state", func(_ *Node, c *Node, _ *cacheItem) { c.States = append(c.States, "focused") }, false},
		{"child count mismatch", func(_ *Node, c *Node, _ *cacheItem) { c.ChildCount = 1 }, false},
		{"duplicate interface", func(_ *Node, c *Node, _ *cacheItem) { c.Interfaces = []string{accessibleIface, accessibleIface} }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent, observed, copyItem := baseParent, baseChild, item
			parent.Children = append([]NodeID(nil), baseParent.Children...)
			observed.Interfaces = append([]string(nil), baseChild.Interfaces...)
			observed.States = append([]string(nil), baseChild.States...)
			tc.change(&parent, &observed, &copyItem)
			snap := Snapshot{Root: parent, Nodes: []Node{parent, observed}, Generation: root.Generation}
			change := observationChange{revision: 1, kind: cacheIface + ":AddAccessible", node: objectIdentity{busName: root.BusName, objectPath: child.ObjectPath}, parent: objectIdentity{busName: root.BusName, objectPath: root.ObjectPath}, cacheAdd: true, cacheItem: copyItem}
			b := &dbusBackend{generation: 3, observationRevision: 1, observationHistory: []observationChange{change}}
			err := b.observationChangesError(0, root, snap, true)
			if tc.accept && err != nil {
				t.Fatalf("identical fresh cache addition rejected: %v", err)
			}
			if !tc.accept && !errors.Is(err, ErrObservationChanged) {
				t.Fatalf("inconsistent fresh cache addition accepted: %v", err)
			}
			err = b.observationChangesError(0, root, snap, false)
			if !errors.Is(err, ErrObservationChanged) {
				t.Fatalf("cached observation wrongly accepted concurrent addition: %v", err)
			}
		})
	}
}
func TestSnapshotConcurrentRemovalAndWindowCreationScopeBoundaries(t *testing.T) {
	root := NodeID{BusName: "org.example.App", ObjectPath: "/window", Generation: 5}
	child := NodeID{BusName: root.BusName, ObjectPath: "/window/button", Generation: 5}
	nodes := map[objectIdentity]Node{
		{busName: root.BusName, objectPath: root.ObjectPath}:   {ID: root, Role: "window"},
		{busName: child.BusName, objectPath: child.ObjectPath}: {ID: child, Parent: root, Role: "button"},
	}
	rootIdentity := objectIdentity{busName: root.BusName, objectPath: root.ObjectPath}
	childIdentity := objectIdentity{busName: child.BusName, objectPath: child.ObjectPath}
	foreign := objectIdentity{busName: "org.unrelated.App", objectPath: "/unrelated"}
	for _, tc := range []struct {
		name     string
		change   observationChange
		scope    objectIdentity
		rootRole string
		reject   bool
	}{
		{"observed child removed", observationChange{node: childIdentity, cacheRemove: true}, rootIdentity, "window", true},
		{"observed parent removed", observationChange{node: foreign, parent: rootIdentity, childrenRemove: true}, rootIdentity, "window", true},
		{"observed descendant removed", observationChange{node: foreign, removedChild: childIdentity, childrenRemove: true}, rootIdentity, "window", true},
		{"unknown same-app removal", observationChange{node: objectIdentity{busName: root.BusName, objectPath: "/unknown"}, cacheRemove: true}, rootIdentity, "window", true},
		{"different-app removal", observationChange{node: foreign, cacheRemove: true}, rootIdentity, "window", false},
		{"foreign window creation", observationChange{kind: "Window:Create", node: foreign}, rootIdentity, "application", false},
		{"in-app window creation", observationChange{kind: "Window:Create", node: objectIdentity{busName: root.BusName, objectPath: "/window/new"}}, rootIdentity, "application", true},
		{"in-window scope create unrelated", observationChange{kind: "Window:Create", node: objectIdentity{busName: root.BusName, objectPath: "/window/new"}}, rootIdentity, "window", false},
		{"desktop scope creation", observationChange{kind: "Window:Create", node: foreign}, objectIdentity{busName: registryName, objectPath: string(desktopPath)}, "desktop", true},
		{"unknown changed identity", observationChange{unknown: true}, rootIdentity, "window", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := observationChangeError(tc.change, tc.scope, tc.scope, tc.rootRole, nodes, true)
			if tc.reject && !errors.Is(err, ErrObservationChanged) {
				t.Fatalf("concurrent change was not rejected: %v", err)
			}
			if !tc.reject && err != nil {
				t.Fatalf("unrelated change invalidated independent observation: %v", err)
			}
		})
	}
}

func TestDesktopSnapshotRetriesObservedChangesAndRefusesRepeatedChange(t *testing.T) {
	for _, tc := range []struct {
		name         string
		changeCount  int32
		wantErr      error
		wantRootName string
	}{
		{name: "retries one changed observation", changeCount: 1, wantRootName: "Desktop after change 1"},
		{name: "exhausts after repeated changes", changeCount: 2, wantErr: ErrStaleGeneration},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var backend *dbusBackend
			var reads atomic.Int32
			entered := make(chan struct{})
			release := make(chan struct{})
			backend, _, _ = newSyntheticATSPIBackendWithFrameSetup(t, false, func(desktop, _ *syntheticATSPINode) {
				desktop.childrenHook = func() {
					read := reads.Add(1)
					if read > tc.changeCount {
						return
					}
					if read == 1 {
						close(entered)
						<-release
					}
					desktop.name = fmt.Sprintf("Desktop after change %d", read)
					signal := &dbus.Signal{
						Name:   "org.a11y.atspi.Event.Object.PropertyChange",
						Sender: registryName,
						Path:   desktopPath,
						Body:   []any{"Name", desktop.name},
					}
					backend.prepareEventTransition(signal, signalEvent(signal))
				}
			})

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			result := make(chan struct {
				snapshot Snapshot
				err      error
			}, 1)
			go func() {
				result <- func() (out struct {
					snapshot Snapshot
					err      error
				}) {
					out.snapshot, out.err = backend.Snapshot(ctx, NodeID{}, SnapshotOptions{Fresh: true, AllowDesktopRoot: true})
					return out
				}()
			}()
			select {
			case <-entered:
			case <-ctx.Done():
				close(release)
				t.Fatalf("desktop traversal did not reach its provider boundary: %v", ctx.Err())
			}
			close(release)
			select {
			case got := <-result:
				if tc.wantErr != nil {
					if !errors.Is(got.err, tc.wantErr) {
						t.Fatalf("desktop snapshot error = %v, want %v", got.err, tc.wantErr)
					}
					backend.mu.RLock()
					cacheEntries := len(backend.cache)
					backend.mu.RUnlock()
					if cacheEntries != 0 {
						t.Fatalf("unstable desktop snapshot published cache entries: %d", cacheEntries)
					}
				} else if got.err != nil || got.snapshot.Root.Name != tc.wantRootName {
					t.Fatalf("desktop retry returned root %q, err %v; want %q", got.snapshot.Root.Name, got.err, tc.wantRootName)
				}
				wantReads := tc.changeCount + 1
				if wantReads > 2 {
					wantReads = 2 // Snapshot deliberately bounds desktop-root retries.
				}
				if reads.Load() != wantReads {
					t.Fatalf("desktop children read %d times, want %d attempts", reads.Load(), wantReads)
				}
			case <-ctx.Done():
				t.Fatalf("desktop snapshot did not finish: %v", ctx.Err())
			}
		})
	}
}

// A cached accessibility snapshot contains private provider information.
// Expiration and explicit Fresh reads must replace that entry with real
// provider data rather than returning stale or corrupted cache contents.
func TestATSPISnapshotCacheTTLExpiresAndFreshBypassesUnexpiredEntry(t *testing.T) {
	backend, _, _ := newSyntheticATSPIBackend(t, false)
	root := NodeID{}
	desktop := backend.desktop()
	opts := SnapshotOptions{MaxNodes: 3, MaxDepth: 2, AllowDesktopRoot: true}
	ctx := context.Background()
	first, err := backend.Snapshot(ctx, root, opts)
	if err != nil {
		t.Fatal(err)
	}
	if first.Root.Name != "Desktop" {
		t.Fatalf("unexpected baseline: %q", first.Root.Name)
	}
	key := snapshotKey(desktop, opts.normalized())
	backend.mu.Lock()
	entry, exists := backend.cache[key]
	if !exists {
		backend.mu.Unlock()
		t.Fatal("successful snapshot was not cached")
	}
	// Deliberately poison the cache only; the D-Bus fixture remains immutable
	// so the concurrent test service never races with test mutations.
	entry.snapshot.Nodes[0].Name = "stale cache content"
	entry.at = time.Now().Add(time.Second)
	backend.cache[key] = entry
	backend.mu.Unlock()
	cached, err := backend.Snapshot(ctx, root, opts)
	if err != nil || cached.Root.Name != "stale cache content" {
		t.Fatalf("valid cached entry not reused: %q %v", cached.Root.Name, err)
	}
	backend.mu.Lock()
	entry = backend.cache[key]
	entry.at = time.Now().Add(-2 * cacheTTL)
	backend.cache[key] = entry
	backend.mu.Unlock()
	refreshed, err := backend.Snapshot(ctx, root, opts)
	if err != nil || refreshed.Root.Name != first.Root.Name {
		t.Fatalf("expired cache not refreshed: %q %v", refreshed.Root.Name, err)
	}
	backend.mu.Lock()
	entry = backend.cache[key]
	entry.snapshot.Nodes[0].Name = "stale after refresh"
	entry.at = time.Now().Add(time.Second)
	backend.cache[key] = entry
	backend.mu.Unlock()
	freshOpts := opts
	freshOpts.Fresh = true
	fresh, err := backend.Snapshot(ctx, root, freshOpts)
	if err != nil || fresh.Root.Name != first.Root.Name {
		t.Fatalf("Fresh read returned corrupted cached content: %q %v", fresh.Root.Name, err)
	}
	following, err := backend.Snapshot(ctx, root, opts)
	if err != nil || following.Root.Name != first.Root.Name {
		t.Fatalf("Fresh result did not repair shared cache: %q %v", following.Root.Name, err)
	}
}

func TestExplicitSnapshotRejectsObservedObjectChangeDuringTraversal(t *testing.T) {
	var backend *dbusBackend
	var root NodeID
	backendReady := make(chan struct{})
	backend, root, _ = newSyntheticATSPIBackendWithFrameSetup(t, false, func(_, frame *syntheticATSPINode) {
		frame.children = []objectRef{{BusName: registryName, ObjectPath: dbus.ObjectPath("/org/a11y/atspi/accessible/app")}}
		frame.childrenHook = func() {
			<-backendReady
			signal := &dbus.Signal{
				Name:   "org.a11y.atspi.Event.Object.PropertyChange",
				Sender: registryName,
				Path:   dbus.ObjectPath("/org/a11y/atspi/accessible/editor"),
				Body:   []any{"Name", "Editor changed during snapshot"},
			}
			backend.prepareEventTransition(signal, signalEvent(signal))
		}
	})
	close(backendReady)
	defer backend.Close()

	snapshot, err := backend.Snapshot(context.Background(), root, SnapshotOptions{Fresh: true})
	if !errors.Is(err, ErrObservationChanged) || len(snapshot.Nodes) != 0 || snapshot.Root.ID.valid() {
		t.Fatalf("explicit snapshot exposed data across a provider change: nodes=%d root=%+v err=%v", len(snapshot.Nodes), snapshot.Root, err)
	}
	backend.mu.RLock()
	cacheEntries := len(backend.cache)
	backend.mu.RUnlock()
	if cacheEntries != 0 {
		t.Fatalf("unstable explicit snapshot published %d cache entries", cacheEntries)
	}
}

func TestATSPIFocusedTraversesPrivateBusAndRejectsIncompleteScope(t *testing.T) {
	backend, id, _ := newSyntheticATSPIBackend(t, false)
	node, err := backend.Focused(context.Background(), SnapshotOptions{Fresh: true, MaxNodes: 12, MaxDepth: 5})
	if err != nil || node.ID != id || node.Name != "Editor — document" || !node.Focused {
		t.Fatalf("private AT-SPI focused node=%+v err=%v", node, err)
	}
	if _, lintShadow16 := backend.Focused(context.Background(), SnapshotOptions{Fresh: true, MaxNodes: 1, MaxDepth: 5}); !errors.Is(lintShadow16, ErrIncompleteSnapshot) {
		t.Fatalf("truncated focus observation must report incomplete snapshot: %v", lintShadow16)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, lintShadow21 := backend.Focused(canceled, SnapshotOptions{}); !errors.Is(lintShadow21, context.Canceled) {
		t.Fatalf("canceled Focused=%v", lintShadow21)
	}
	if _, lintShadow24 := backend.Focused(absentFocusedContext, SnapshotOptions{}); lintShadow24 == nil {
		t.Fatal("nil Focused context accepted")
	}
	backend.Invalidate(id)
	fresh, err := backend.Focused(context.Background(), SnapshotOptions{Fresh: true, MaxDepth: 5})
	if err != nil || fresh.ID.Generation <= id.Generation {
		t.Fatalf("fresh focus lookup retained stale generation: %+v %v", fresh, err)
	}
}

func TestATSPIAtPointValidatesCoordinatesAndPrivateProviderFailure(t *testing.T) {
	backend, _, _ := newSyntheticATSPIBackend(t, false)
	if _, err := backend.AtPoint(absentFocusedContext, 0, 0); err == nil {
		t.Fatal("nil context accepted")
	}
	for _, tc := range []struct {
		name string
		x, y int
	}{
		{"x positive overflow", 1 << 40, 0}, {"x negative overflow", -(1 << 40), 0},
		{"y positive overflow", 0, 1 << 40}, {"y negative overflow", 0, -(1 << 40)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := backend.AtPoint(context.Background(), tc.x, tc.y); err == nil || !strings.Contains(err.Error(), "out of range") {
				t.Fatalf("invalid coordinates (%d,%d) accepted: %v", tc.x, tc.y, err)
			}
		})
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := backend.AtPoint(canceled, 10, 15); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled AtPoint=%v", err)
	}
	// The synthetic desktop intentionally does not implement hit-testing.
	// The provider error must not be replaced with a fabricated target.
	node, err := backend.AtPoint(context.Background(), 10, 15)
	if err == nil || node.ID.valid() {
		t.Fatalf("unsupported private bus hit test returned %+v err=%v", node, err)
	}
}

// An intentionally absent context exercises the public nil-context boundary.
var absentFocusedContext context.Context

// This independent historical-format oracle protects the cache identity
// when its allocation strategy changes.
func TestSnapshotKeyEncodingMatchesCacheContract(t *testing.T) {
	roots := []NodeID{
		{BusName: "org.example.App", ObjectPath: "/app", Generation: 7, Incarnation: 2},
		{BusName: "", ObjectPath: "", Generation: ^uint64(0)},
	}
	options := []SnapshotOptions{
		{},
		{MaxDepth: 20, MaxNodes: 4000, MaxTextBytes: 4096, MaxTotalBytes: 8 << 20},
		{MaxDepth: -9, MaxNodes: -2, MaxTextBytes: -1, MaxTotalBytes: -3,
			VisibleOnly: true, AllowSensitive: true, AllowDesktopRoot: true,
			SkipRoles: []string{"button", "menu,item", "écran"}},
	}
	for _, root := range roots {
		for _, opts := range options {
			want := fmt.Sprintf("%s\x00%s\x00%d\x00%d\x00%d\x00%d\x00%d\x00%d\x00%t\x00%t\x00%t\x00",
				root.BusName, root.ObjectPath, root.Generation, effectiveIncarnation(root),
				opts.MaxDepth, opts.MaxNodes, opts.MaxTextBytes, opts.MaxTotalBytes,
				opts.VisibleOnly, opts.AllowSensitive, opts.AllowDesktopRoot)
			for _, role := range opts.SkipRoles {
				want += fmt.Sprintf("%d:%s\x00", len(role), role)
			}
			if got := snapshotKey(root, opts); got != want {
				t.Fatalf("snapshotKey(%+v, %+v) = %q; want %q", root, opts, got, want)
			}
		}
	}
}

func TestSnapshotRejectsUnavailableAndInvalidRequests(t *testing.T) {
	var absent *dbusBackend
	if _, err := absent.Snapshot(context.Background(), NodeID{}, SnapshotOptions{}); !errors.Is(err, ErrDisconnected) {
		t.Fatalf("nil backend snapshot error = %v", err)
	}

	backend, root, _ := newSyntheticATSPIBackend(t, false)
	if _, err := backend.Snapshot(nil, root, SnapshotOptions{}); err == nil || !strings.Contains(err.Error(), "nil context") {
		t.Fatalf("nil-context snapshot error = %v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := backend.Snapshot(canceled, root, SnapshotOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled snapshot error = %v", err)
	}
	invalid := NodeID{BusName: root.BusName, Generation: root.Generation}
	if _, err := backend.Snapshot(context.Background(), invalid, SnapshotOptions{}); !errors.Is(err, ErrScope) {
		t.Fatalf("invalid root snapshot error = %v", err)
	}
	if _, err := backend.Find(context.Background(), invalid, Query{}, SnapshotOptions{}); !errors.Is(err, ErrScope) {
		t.Fatalf("invalid root find error = %v", err)
	}
	backend.Invalidate(root)
	if _, err := backend.Snapshot(context.Background(), root, SnapshotOptions{Fresh: true}); !errors.Is(err, ErrStaleNode) && !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("invalidated root snapshot error = %v", err)
	}
}

func TestSnapshotReturnsProviderFailureAsIncompleteEvidence(t *testing.T) {
	backend, root, _ := newSyntheticATSPIBackendWithFrameSetup(t, false, func(_ *syntheticATSPINode, frame *syntheticATSPINode) {
		frame.children = []objectRef{{BusName: registryName, ObjectPath: "/org/a11y/atspi/accessible/missing"}}
		frame.childrenFailure = "synthetic child provider unavailable"
	})

	snapshot, err := backend.Snapshot(context.Background(), root, SnapshotOptions{Fresh: true})
	if err != nil {
		t.Fatalf("partial provider snapshot should preserve observed root: %v", err)
	}
	if snapshot.Root.ID != root || len(snapshot.Nodes) != 1 || snapshot.ProviderErrors != 1 || len(snapshot.Warnings) != 1 {
		t.Fatalf("provider failure was not represented as partial evidence: %+v", snapshot)
	}
	if err := ValidateSemanticSnapshot(snapshot, Selector{Role: "frame"}); !errors.Is(err, ErrIncompleteSnapshot) {
		t.Fatalf("partial provider snapshot was treated as authoritative: %v", err)
	}
}

func TestSnapshotResponseBudgetRefusesWithoutPublishing(t *testing.T) {
	backend, root, _ := newSyntheticATSPIBackend(t, false)
	opts := SnapshotOptions{Fresh: true, MaxTotalBytes: 1}
	if _, err := backend.Snapshot(context.Background(), root, opts); !errors.Is(err, ErrResponseBudget) {
		t.Fatalf("below-minimum snapshot budget error = %v", err)
	}
	if _, ok := backend.cache[snapshotKey(root, opts.normalized())]; ok {
		t.Fatal("snapshot that exceeded its minimum response budget was published")
	}
}

func TestFindReturnsMatchesFromOneBoundedSnapshot(t *testing.T) {
	backend, root, _ := newSyntheticATSPIBackend(t, false)
	matches, err := backend.Find(context.Background(), root, Query{Name: "document", Role: "frame"}, SnapshotOptions{Fresh: true})
	if err != nil || len(matches) != 1 || matches[0].ID != root {
		t.Fatalf("Find over a fresh snapshot = %+v, %v", matches, err)
	}
}

func TestFocusedPropagatesSnapshotFailureAndReportsNoMatch(t *testing.T) {
	var absent *dbusBackend
	if _, err := absent.Focused(context.Background(), SnapshotOptions{}); !errors.Is(err, ErrDisconnected) {
		t.Fatalf("focused lookup on disconnected backend = %v", err)
	}
	backend, _, _ := newSyntheticATSPIBackendWithFrameSetup(t, false, func(_ *syntheticATSPINode, frame *syntheticATSPINode) {
		frame.states = nil
	})
	if _, err := backend.Focused(context.Background(), SnapshotOptions{Fresh: true}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("focused lookup without a focused node = %v", err)
	}
}

func TestAtPointValidatesCoordinatesAndUsesProviderResult(t *testing.T) {
	var absent *dbusBackend
	if _, err := absent.AtPoint(context.Background(), 0, 0); !errors.Is(err, ErrDisconnected) {
		t.Fatalf("point lookup on disconnected backend = %v", err)
	}
	backend, root, _ := newSyntheticATSPIBackendWithFrameSetup(t, false, func(desktop *syntheticATSPINode, _ *syntheticATSPINode) {
		desktop.pointResult = objectRef{BusName: registryName, ObjectPath: "/org/a11y/atspi/accessible/editor"}
	})
	if _, err := backend.AtPoint(nil, 0, 0); err == nil || !strings.Contains(err.Error(), "nil context") {
		t.Fatalf("nil-context point lookup = %v", err)
	}
	if _, err := backend.AtPoint(context.Background(), int(^uint(0)>>1), 0); err == nil || !strings.Contains(err.Error(), "out of range") {
		t.Fatalf("out-of-range point lookup = %v", err)
	}
	node, err := backend.AtPoint(context.Background(), 10, 15)
	if err != nil || node.ID != root || node.Name != "Editor — document" {
		t.Fatalf("point lookup returned %+v, %v", node, err)
	}
	backend, _, _ = newSyntheticATSPIBackend(t, false)
	if _, err := backend.AtPoint(context.Background(), 1, 1); !errors.Is(err, ErrNotFound) {
		t.Fatalf("null point result = %v", err)
	}
	backend, _, _ = newSyntheticATSPIBackendWithFrameSetup(t, false, func(desktop *syntheticATSPINode, _ *syntheticATSPINode) {
		desktop.pointFailure = "synthetic component failure"
	})
	if _, err := backend.AtPoint(context.Background(), 1, 1); err == nil || !strings.Contains(err.Error(), "point query") {
		t.Fatalf("provider point failure = %v", err)
	}
}

func TestCloneSnapshotOwnsTypedMetadataAndBoundReasonsStayUnique(t *testing.T) {
	root := NodeID{BusName: "org.example.Editor", ObjectPath: "/editor", Generation: 1}
	child := NodeID{BusName: root.BusName, ObjectPath: "/label", Generation: root.Generation}
	in := Snapshot{Root: Node{ID: root}, Nodes: []Node{{
		ID:        root,
		Value:     &ValueInfo{Current: 1, Minimum: 0, Maximum: 10, MinimumIncrement: 0.5},
		Selection: &SelectionInfo{SelectedChildCount: 1},
		Table:     &TableInfo{Rows: 2, Columns: 3},
		Document:  &DocumentInfo{Locale: "en", CurrentPageNumber: 1, PageCount: 2},
		Actions:   []Action{{Name: "activate"}},
		Relations: map[string][]NodeID{"labelled-by": {child}},
	}}}
	out := cloneSnapshot(in)
	out.Nodes[0].Value.Current = 8
	out.Nodes[0].Selection.SelectedChildCount = 5
	out.Nodes[0].Table.Rows = 8
	out.Nodes[0].Document.Locale = "fr"
	out.Nodes[0].Actions[0].Name = "changed"
	out.Nodes[0].Relations["labelled-by"][0] = NodeID{}
	if in.Nodes[0].Value.Current != 1 || in.Nodes[0].Selection.SelectedChildCount != 1 || in.Nodes[0].Table.Rows != 2 || in.Nodes[0].Document.Locale != "en" || in.Nodes[0].Actions[0].Name != "activate" || in.Nodes[0].Relations["labelled-by"][0] != child {
		t.Fatal("snapshot clone shares typed or collection metadata with its source")
	}

	maxBytes := 1024
	snapshot := Snapshot{
		Root:              Node{ID: root, Name: strings.Repeat("long accessible name ", 512)},
		Nodes:             []Node{{ID: root, Name: strings.Repeat("long accessible name ", 512)}},
		TruncationReasons: []string{"max total bytes 1024"},
	}
	bounded, err := boundSnapshotResponse(snapshot, maxBytes)
	if err != nil {
		t.Fatalf("bound oversized snapshot: %v", err)
	}
	if got := snapshotJSONSize(bounded); got > maxBytes {
		t.Fatalf("bounded response has %d bytes, limit %d", got, maxBytes)
	}
	if len(bounded.TruncationReasons) != 1 || bounded.TruncationReasons[0] != "max total bytes 1024" {
		t.Fatalf("response duplicated its truncation reason: %v", bounded.TruncationReasons)
	}
}

func TestSnapshotJSONHelpersSaturateAndPreserveUTF8(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	if got := addJSONSize(maxInt-1, 2); got != maxInt {
		t.Fatalf("overflowing JSON size = %d, want saturated %d", got, maxInt)
	}
	if got := addJSONSize(12, -4); got != 8 {
		t.Fatalf("non-overflowing signed JSON size = %d, want 8", got)
	}
	for _, tc := range []struct {
		value string
		limit int
		want  string
	}{
		{value: "é", limit: 1, want: ""},
		{value: "é", limit: -1, want: ""},
		{value: "é", limit: 2, want: "é"},
	} {
		got, truncated := truncateUTF8(tc.value, tc.limit)
		if got != tc.want || truncated != (len(tc.value) > tc.limit) {
			t.Fatalf("truncateUTF8(%q, %d) = %q, %t", tc.value, tc.limit, got, truncated)
		}
	}
}

// The registry may advance its handle epoch while a desktop snapshot is
// traversing a provider. Explicit handles must fail closed, while a caller
// requesting the current desktop may retry at most once against a new epoch.
func TestSnapshotMidWalkEpochInvalidationRejectsOldRootAndCache(t *testing.T) {
	for _, tc := range []struct {
		name          string
		invalidations int
		explicitRoot  bool
		wantReads     int
		wantError     bool
	}{
		{name: "desktop retries one invalidation", invalidations: 1, wantReads: 2},
		{name: "desktop bounds repeated invalidation", invalidations: 2, wantReads: 2, wantError: true},
		{name: "explicit desktop handle fails closed", invalidations: 1, explicitRoot: true, wantReads: 1, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var backend *dbusBackend
			var live atomic.Pointer[dbusBackend]
			var reads atomic.Int32
			backend, _, _ = newSyntheticATSPIBackendWithFrameSetup(t, false, func(desktop *syntheticATSPINode, _ *syntheticATSPINode) {
				desktop.childrenHook = func() {
					if current := live.Load(); current != nil {
						if reads.Add(1) <= int32(tc.invalidations) {
							current.Invalidate(NodeID{})
						}
					}
				}
			})
			live.Store(backend)
			root := NodeID{}
			if tc.explicitRoot {
				root = backend.desktop()
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			snap, err := backend.Snapshot(ctx, root, SnapshotOptions{Fresh: true, AllowDesktopRoot: true})
			if tc.wantError {
				if !errors.Is(err, ErrStaleGeneration) && !errors.Is(err, ErrStaleNode) {
					t.Fatalf("epoch rollover exposed stale snapshot: err=%v generation=%d", err, snap.Generation)
				}
				if len(snap.Nodes) != 0 {
					t.Fatalf("stale snapshot exposed %d nodes", len(snap.Nodes))
				}
				backend.mu.RLock()
				cacheCount := len(backend.cache)
				backend.mu.RUnlock()
				if cacheCount != 0 {
					t.Fatalf("stale snapshot published %d cache entries", cacheCount)
				}
			} else {
				if err != nil {
					t.Fatalf("desktop retry: %v", err)
				}
				if snap.Generation != backend.Generation() || len(snap.Nodes) == 0 {
					t.Fatalf("retry returned old or empty generation: result=%d current=%d nodes=%d", snap.Generation, backend.Generation(), len(snap.Nodes))
				}
			}
			if reads.Load() != int32(tc.wantReads) {
				t.Fatalf("provider reads=%d want=%d", reads.Load(), tc.wantReads)
			}
		})
	}
}

// Even when no tree nodes fit the response budget, the fallback must preserve
// the requested root identity while stripping provider details and references.
// The fallback's successful result is distinct from an impossible budget.
func TestSnapshotMinimumBudgetFallbackRetainsOnlyRootIdentity(t *testing.T) {
	root := NodeID{BusName: "org.example.Editor", ObjectPath: "/window", Generation: 4, Incarnation: 7}
	child := NodeID{BusName: root.BusName, ObjectPath: "/child", Generation: root.Generation, Incarnation: root.Incarnation}
	source := Snapshot{
		Root:           Node{ID: root, Name: "secret title", Children: []NodeID{child}},
		Nodes:          []Node{{ID: root, Name: "secret title", Children: []NodeID{child}}, {ID: child, Name: "secret child"}},
		Warnings:       []string{"untrusted provider diagnostic"},
		ProviderErrors: 3,
		Generation:     root.Generation,
	}
	minimum, err := minimumSnapshotResponse(source, root, 4096)
	if err != nil {
		t.Fatalf("minimum budget fallback: %v", err)
	}
	if minimum.Root.ID != root || minimum.Root.Name != "" || len(minimum.Root.Children) != 0 || len(minimum.Nodes) != 0 || len(minimum.Warnings) != 0 || minimum.ProviderErrors != 0 {
		t.Fatalf("fallback leaked truncated snapshot content: %+v", minimum)
	}
	if !slices.Equal(source.Warnings, []string{"untrusted provider diagnostic"}) || len(source.Nodes) != 2 || source.Root.Name != "secret title" {
		t.Fatalf("fallback mutated original provider snapshot: %+v", source)
	}
	minimumSize := snapshotJSONSize(minimum)
	if minimumSize > 4096 {
		t.Fatalf("minimum exceeded budget: %d", minimumSize)
	}
	if _, err := minimumSnapshotResponse(source, root, minimumSize-1); !errors.Is(err, ErrResponseBudget) {
		t.Fatalf("impossible minimum budget accepted: %v", err)
	}
}

// A selector must never infer ancestry through a parent ID whose node was
// absent from its snapshot: an unobserved ancestor cannot prove containment.
func TestSelectorMissingAncestorCannotAuthorizeDescendantMatch(t *testing.T) {
	root := NodeID{BusName: "org.example.Editor", ObjectPath: "/root", Generation: 4}
	group := NodeID{BusName: root.BusName, ObjectPath: "/missing-group", Generation: 4}
	leaf := Node{ID: NodeID{BusName: root.BusName, ObjectPath: "/button", Generation: 4}, Parent: group, Name: "Submit", Role: "push-button"}
	byID := map[NodeID]Node{leaf.ID: leaf, root: {ID: root, Role: "frame", Name: "Editor"}}
	constraints := []AncestorSelector{{Role: "frame", Name: "Editor"}}
	if matchesAncestors(byID, leaf, constraints) {
		t.Fatal("unobserved parent chain authorized descendant selector")
	}
	byID[group] = Node{ID: group, Parent: root, Role: "group", Name: "Form"}
	if !matchesAncestors(byID, leaf, constraints) {
		t.Fatal("complete observed parent chain rejected")
	}
	delete(byID, root)
	if matchesAncestors(byID, leaf, constraints) {
		t.Fatal("missing far ancestor authorized descendant selector")
	}
}

// An empty but allocated node slice must serialize as [] rather than null.
// The streaming size estimator must agree with actual JSON output even when
// no nodes are retained after snapshot filtering.
func TestSnapshotEmptyAllocatedNodeListSizeMatchesEncoding(t *testing.T) {
	snapshot := Snapshot{Nodes: make([]Node, 0)}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if snapshotJSONSize(snapshot) != len(encoded) {
		t.Fatalf("empty node list byte estimate=%d JSON=%d: %s", snapshotJSONSize(snapshot), len(encoded), encoded)
	}
}

// Prefix probes may prune references to nodes outside the prefix, while the
// source snapshot retains its original children and relations.
func TestSnapshotProjectionLeavesTheSourceRelationsIntact(t *testing.T) {
	nodes := []Node{
		{ID: NodeID{BusName: "b", ObjectPath: "/0", Generation: 1}},
		{ID: NodeID{BusName: "b", ObjectPath: "/1", Generation: 1}, Attributes: map[string]string{"label": "owned"}, Relations: map[string][]NodeID{
			"labelled-by": {{BusName: "b", ObjectPath: "/0", Generation: 1}},
			"controls":    {{BusName: "b", ObjectPath: "/0", Generation: 1}},
		}},
		{ID: NodeID{BusName: "b", ObjectPath: "/2", Generation: 1}},
		{ID: NodeID{BusName: "b", ObjectPath: "/3", Generation: 1}, Relations: map[string][]NodeID{
			"labelled-by": {{BusName: "b", ObjectPath: "/2", Generation: 1}},
		}},
	}
	nodes[0].Children = []NodeID{{BusName: "b", ObjectPath: "/3", Generation: 1}}
	nodes[1].Relations["flows-to"] = []NodeID{{BusName: "b", ObjectPath: "/3", Generation: 1}}
	source := Snapshot{Root: nodes[0], Nodes: nodes}

	fullSize := snapshotJSONSize(source)
	projected, err := boundSnapshotResponse(source, fullSize-1)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	if len(projected.Nodes) != len(nodes)-1 {
		t.Fatalf("projected node count = %d, want %d", len(projected.Nodes), len(nodes)-1)
	}
	if len(projected.Nodes[0].Children) != 0 {
		t.Fatalf("projected root children = %v, want references outside the prefix pruned", projected.Nodes[0].Children)
	}
	if _, ok := projected.Nodes[1].Relations["flows-to"]; ok {
		t.Fatal("projected relation retained a target outside the prefix")
	}
	projected.Nodes[1].Attributes["label"] = "changed"
	if nodes[1].Attributes["label"] != "owned" {
		t.Fatal("mutating a truncated projection changed the source attributes")
	}

	if len(nodes[1].Relations) != 3 {
		t.Fatalf("source node 1 relations = %v, want all three preserved", nodes[1].Relations)
	}
	if _, ok := nodes[1].Relations["controls"]; !ok {
		t.Fatal("projection deleted a relation from the source snapshot")
	}
	if len(nodes[3].Relations) != 1 {
		t.Fatalf("source node 3 relations = %v, want its relation preserved", nodes[3].Relations)
	}
	if len(nodes[0].Children) != 1 || nodes[0].Children[0].ObjectPath != "/3" {
		t.Fatalf("source root children = %v, want the original reference preserved", nodes[0].Children)
	}
	if len(nodes[1].Relations["flows-to"]) != 1 || nodes[1].Relations["flows-to"][0].ObjectPath != "/3" {
		t.Fatalf("source node 1 flows-to = %v, want the original reference preserved", nodes[1].Relations["flows-to"])
	}
}

// The returned graph must be owned: a caller that mutates it cannot reach back
// into the snapshot it was given.
func TestProjectedSnapshotOwnsItsNodes(t *testing.T) {
	nodes := []Node{
		{
			ID:         NodeID{BusName: "b", ObjectPath: "/0", Generation: 1},
			Interfaces: []string{"root-interface"},
			States:     []string{"root-state"},
			Children:   []NodeID{{BusName: "b", ObjectPath: "/1", Generation: 1}},
			Relations:  map[string][]NodeID{"owns": {{BusName: "b", ObjectPath: "/1", Generation: 1}}},
			Actions:    []Action{{Name: "root-action"}},
			Warnings:   []string{"root-warning"},
		},
		{
			ID:         NodeID{BusName: "b", ObjectPath: "/1", Generation: 1},
			Interfaces: []string{"org.test.Interface"},
			Attributes: map[string]string{"k": "v"},
			States:     []string{"visible"},
			Children:   []NodeID{{BusName: "b", ObjectPath: "/0", Generation: 1}},
			Relations:  map[string][]NodeID{"labelled-by": {{BusName: "b", ObjectPath: "/0", Generation: 1}}},
			Value:      &ValueInfo{Current: 1},
			Actions:    []Action{{Name: "activate"}},
			Selection:  &SelectionInfo{SelectedChildCount: 1},
			Table:      &TableInfo{Rows: 2, Columns: 3},
			Document:   &DocumentInfo{Locale: "en"},
			Warnings:   []string{"warning"},
		},
	}
	source := Snapshot{Nodes: nodes}

	got, err := boundSnapshotResponse(source, 1<<20)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	if len(got.Nodes) != 2 {
		t.Fatalf("projected nodes = %d, want 2", len(got.Nodes))
	}
	mutateProjectedNodeForOwnershipCheck(&got.Nodes[0])
	assertProjectedNeighborUnchanged(t, got.Nodes[1])
	mutateProjectedNodeForOwnershipCheck(&got.Nodes[1])
	assertSourceNodeUnchanged(t, nodes[1])
}

func mutateProjectedNodeForOwnershipCheck(node *Node) {
	node.Interfaces = append(node.Interfaces, "next-interface")
	node.States = append(node.States, "next-state")
	node.Children = append(node.Children, NodeID{BusName: "b", ObjectPath: "/extra", Generation: 1})
	for relation := range node.Relations {
		node.Relations[relation] = append(node.Relations[relation], NodeID{BusName: "b", ObjectPath: "/extra", Generation: 1})
		break
	}
	node.Actions = append(node.Actions, Action{Name: "next-action"})
	node.Warnings = append(node.Warnings, "next-warning")
	if node.Attributes != nil {
		delete(node.Attributes, "k")
	}
	if len(node.Interfaces) > 0 {
		node.Interfaces[0] = "changed"
	}
	if len(node.States) > 0 {
		node.States[0] = "changed"
	}
	if len(node.Children) > 0 {
		node.Children[0].ObjectPath = "/changed"
	}
	if node.Value != nil {
		node.Value.Current = 2
	}
	if len(node.Actions) > 0 {
		node.Actions[0].Name = "changed"
	}
	if node.Selection != nil {
		node.Selection.SelectedChildCount = 2
	}
	if node.Table != nil {
		node.Table.Rows = 4
	}
	if node.Document != nil {
		node.Document.Locale = "changed"
	}
	if len(node.Warnings) > 0 {
		node.Warnings[0] = "changed"
	}
}

func assertProjectedNeighborUnchanged(t *testing.T, node Node) {
	t.Helper()
	if node.Interfaces[0] != "org.test.Interface" || node.States[0] != "visible" ||
		node.Children[0].ObjectPath != "/0" || node.Relations["labelled-by"][0].ObjectPath != "/0" ||
		node.Actions[0].Name != "activate" || node.Warnings[0] != "warning" {
		t.Fatal("appending to one projected node changed a neighboring node")
	}
}

func assertSourceNodeUnchanged(t *testing.T, node Node) {
	t.Helper()
	if len(node.Relations["labelled-by"]) != 1 {
		t.Fatal("mutating the projected graph changed the source relations")
	}
	if node.Attributes["k"] != "v" {
		t.Fatal("mutating the projected graph changed the source attributes")
	}
	if node.Interfaces[0] != "org.test.Interface" || node.States[0] != "visible" || node.Children[0].ObjectPath != "/0" {
		t.Fatal("mutating the projected node changed source collections")
	}
	if node.Value.Current != 1 || node.Actions[0].Name != "activate" || node.Selection.SelectedChildCount != 1 {
		t.Fatal("mutating the projected node changed source value, actions, or selection")
	}
	if node.Table.Rows != 2 || node.Document.Locale != "en" || node.Warnings[0] != "warning" {
		t.Fatal("mutating the projected node changed source table, document, or warnings")
	}
}

func TestBoundSnapshotResponseDoesNotMutateNodesItTrims(t *testing.T) {
	text := "value " + strings.Repeat("long text ", 400)
	source := Snapshot{
		Root: Node{ID: NodeID{BusName: "b", ObjectPath: "/root", Generation: 1}},
		Nodes: []Node{{
			ID:   NodeID{BusName: "b", ObjectPath: "/root", Generation: 1},
			Text: text, Attributes: map[string]string{"label": "source"},
		}},
	}
	projected, err := boundSnapshotResponse(source, 1024)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	if len(projected.Nodes) != 0 || projected.Root.ID != source.Root.ID {
		t.Fatalf("projected minimal response = %+v, want the original root identity without oversized nodes", projected)
	}
	if source.Nodes[0].Text != text || source.Nodes[0].TextTruncated || source.Nodes[0].Attributes["label"] != "source" {
		t.Fatal("trimming the projection changed the source node")
	}
}

func TestProjectedSnapshotOwnsRootWithoutNodes(t *testing.T) {
	source := Snapshot{Root: Node{
		ID:         NodeID{BusName: "b", ObjectPath: "/root", Generation: 1},
		Attributes: map[string]string{"label": "owned"},
	}}

	projected, err := boundSnapshotResponse(source, 1<<20)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	projected.Root.Attributes["label"] = "changed"
	if source.Root.Attributes["label"] != "owned" {
		t.Fatal("mutating a projection without nodes changed the source root")
	}
}

// Publishing a snapshot is the last authorization boundary. An object that
// disappeared, an inaccessible backend, or an intervening provider event must
// prevent the snapshot from becoming a reusable cache entry.
func TestSnapshotPublicationRejectsLostBackendRevokedIdentityAndChangedObservations(t *testing.T) {
	root := NodeID{BusName: "org.synthetic.Editor", ObjectPath: "/window", Generation: 7}
	rootIdentity := objectIdentity{busName: root.BusName, objectPath: root.ObjectPath}
	observation := observationChange{revision: 4, node: rootIdentity, kind: "org.a11y.atspi.Event.Object:PropertyChange"}
	for _, tc := range []struct {
		name      string
		configure func(*dbusBackend)
		want      error
	}{
		{"backend closed", func(b *dbusBackend) { b.closed = true }, ErrDisconnected},
		{"provider disconnected", func(b *dbusBackend) { b.disconnected = true }, ErrDisconnected},
		{"object path reincarnated", func(b *dbusBackend) { b.incarnations = map[objectIdentity]uint64{rootIdentity: 2} }, ErrStaleNode},
		{"intervening property change", func(b *dbusBackend) {
			b.observationRevision = 4
			b.observationHistory = []observationChange{observation}
		}, ErrObservationChanged},
		{"unverifiable history", func(b *dbusBackend) { b.observationRevision = 4; b.observationFloor = 4 }, ErrObservationChanged},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &dbusBackend{generation: 7, observationRevision: 3}
			tc.configure(b)
			snap := Snapshot{Root: Node{ID: root, Name: "stale editor"}, Nodes: []Node{{ID: root, Name: "stale editor"}}, Generation: 7, CapturedAt: time.Now()}
			err := b.publishSnapshot("candidate", 7, 3, root, true, snap)
			if !errors.Is(err, tc.want) {
				t.Fatalf("unverifiable snapshot publication returned %v, want %v", err, tc.want)
			}
			if len(b.cache) != 0 {
				t.Fatalf("unverifiable snapshot entered reusable cache: %+v", b.cache)
			}
		})
	}
}

func TestSnapshotPublishedCacheOwnsProviderData(t *testing.T) {
	root := NodeID{BusName: "org.synthetic.Editor", ObjectPath: "/window", Generation: 7}
	child := NodeID{BusName: root.BusName, ObjectPath: "/window/confirm", Generation: 7}
	b := &dbusBackend{generation: 7}
	snap := Snapshot{Root: Node{ID: root, Name: "Editor"}, Nodes: []Node{
		{ID: root, Name: "Editor", Children: []NodeID{child}},
		{ID: child, Name: "Confirm", Attributes: map[string]string{"action": "confirm"}, States: []string{"enabled"}},
	}, Generation: 7, CapturedAt: time.Now()}
	if err := b.publishSnapshot("verified", 7, 0, root, true, snap); err != nil {
		t.Fatalf("verified snapshot rejected: %v", err)
	}
	snap.Nodes[0].Children[0] = NodeID{}
	snap.Nodes[1].Attributes["action"] = "cancel"
	snap.Nodes[1].States[0] = "disabled"
	cached, ok := b.cache["verified"]
	if !ok || len(cached.snapshot.Nodes) != 2 {
		t.Fatalf("verified cache missing: %+v", cached)
	}
	if cached.snapshot.Nodes[0].Children[0] != child || cached.snapshot.Nodes[1].Attributes["action"] != "confirm" || cached.snapshot.Nodes[1].States[0] != "enabled" {
		t.Fatalf("published snapshot aliases mutable provider data: %+v", cached.snapshot.Nodes)
	}
}

// Fresh application and window lookups cannot trust a finite event history
// after its relevant entries have been evicted. Empty intervals, by contrast,
// are safe even when older unrelated events remain in the ring buffer.
func TestWindowResolutionRejectsExpiredObservationHistoryWithoutLeakingCandidates(t *testing.T) {
	desktop := NodeID{BusName: registryName, ObjectPath: string(desktopPath), Generation: 5}
	app := Application{Node: Node{ID: NodeID{BusName: "org.synthetic.Editor", ObjectPath: "/app", Generation: 5}}}
	window := Node{ID: NodeID{BusName: app.ID.BusName, ObjectPath: "/app/window", Generation: 5}, Role: "frame", Name: "Editor"}
	for _, tc := range []struct {
		name     string
		backend  *dbusBackend
		expected uint64
		want     error
	}{
		{"unavailable backend", nil, 1, ErrDisconnected},
		{"evicted history", &dbusBackend{generation: 5, observationRevision: 8, observationFloor: 7}, 6, ErrObservationChanged},
		{"matching revision", &dbusBackend{generation: 5, observationRevision: 8, observationFloor: 7}, 8, nil},
		{"only preceding events", &dbusBackend{generation: 5, observationRevision: 8, observationHistory: []observationChange{{revision: 3, unknown: true}}}, 8, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.backend.windowResolutionChangesError(tc.expected, desktop, []Application{app}, []Node{window})
			if !errors.Is(err, tc.want) {
				t.Fatalf("window resolution consistency error=%v, want %v", err, tc.want)
			}
		})
	}
}

// A fresh resolution reads an observation revision before gathering provider
// identities. Earlier events are already represented by those identities;
// treating them as concurrent changes would reject an otherwise coherent
// result. Later relevant events must still invalidate that same result.
func TestFreshResolutionOnlyInvalidatesForChangesAfterCapturedRevision(t *testing.T) {
	const generation = 5
	desktop := NodeID{BusName: registryName, ObjectPath: "/desktop", Generation: generation}
	app := Node{ID: NodeID{BusName: "org.example.App", ObjectPath: "/app", Generation: generation}}
	window := Node{ID: NodeID{BusName: app.ID.BusName, ObjectPath: "/window", Generation: generation}, Role: "frame"}
	desktopIdentity := objectIdentity{busName: desktop.BusName, objectPath: desktop.ObjectPath}
	windowIdentity := objectIdentity{busName: window.ID.BusName, objectPath: window.ID.ObjectPath}
	foreign := objectIdentity{busName: "org.other.App", objectPath: "/other"}
	for _, tc := range []struct {
		name        string
		later       observationChange
		wantChanged bool
	}{
		{name: "older unknown event ignored", later: observationChange{node: foreign}},
		{name: "later observed window change invalidates", later: observationChange{node: windowIdentity}, wantChanged: true},
		{name: "later desktop change invalidates", later: observationChange{node: desktopIdentity}, wantChanged: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := &dbusBackend{generation: generation, observationRevision: 3, observationHistory: []observationChange{
				{revision: 1, unknown: true},
				{revision: 2, unknown: true},
				{revision: 3, cacheRemove: true, node: desktopIdentity},
			}}
			backend.observationHistory = append(backend.observationHistory, observationChange{revision: 4, node: tc.later.node, parent: tc.later.parent, cacheRemove: tc.later.cacheRemove})
			backend.observationRevision = 4
			expected := uint64(3)
			// Window candidates resolved from app ownership may be invalidated by
			// candidate changes or desktop application list changes, not foreign apps.
			winErr := backend.windowResolutionChangesError(expected, desktop, []Application{{Node: app}}, []Node{window})
			if errors.Is(winErr, ErrObservationChanged) != tc.wantChanged || (winErr != nil && !tc.wantChanged) {
				t.Fatalf("window resolution history validation=%v changed=%t", winErr, tc.wantChanged)
			}
			// Application resolution observes only app and desktop identities.
			appErr := backend.applicationResolutionChangesError(expected, desktop, []Node{app})
			wantAppChanged := tc.later.node == desktopIdentity
			if errors.Is(appErr, ErrObservationChanged) != wantAppChanged || (appErr != nil && !wantAppChanged) {
				t.Fatalf("application resolution history validation=%v changed=%t", appErr, wantAppChanged)
			}
		})
	}
}

// A named/role/label/ancestor selector must never claim that a target is
// absent when the AT-SPI fields required to find it were unreadable. Optional
// metadata failures, however, must not block unrelated semantic discovery.
func TestSnapshotWarningCompletenessAcrossSelectorDependencies(t *testing.T) {
	root := NodeID{BusName: "org.synthetic.App", ObjectPath: "/app", Generation: 8}
	input := NodeID{BusName: root.BusName, ObjectPath: "/app/field", Generation: 8}
	for _, tc := range []struct {
		name, warning string
		selector      Selector
		complete      bool
	}{
		{"name-required", accessibleIface + ".Name: synthetic unavailable", Selector{Name: "Confirm"}, false},
		{"name-label-required", accessibleIface + ".Name: synthetic unavailable", Selector{Label: "Account"}, false},
		{"name-ancestor-required", accessibleIface + ".Name: synthetic unavailable", Selector{Ancestors: []AncestorSelector{{Name: "Dialog"}}}, false},
		{"name-not-required", accessibleIface + ".Name: synthetic unavailable", Selector{Role: "entry"}, true},
		{"role-required", accessibleIface + ".GetRole: synthetic unavailable", Selector{Role: "entry"}, false},
		{"role-ancestor-required", accessibleIface + ".GetRole: synthetic unavailable", Selector{Ancestors: []AncestorSelector{{Role: "dialog"}}}, false},
		{"role-not-required", accessibleIface + ".GetRole: synthetic unavailable", Selector{Name: "Confirm"}, true},
		{"states-required", accessibleIface + ".GetState: synthetic unavailable", Selector{States: []string{"focused"}}, false},
		{"states-not-required", accessibleIface + ".GetState: synthetic unavailable", Selector{Role: "entry"}, true},
		{"attributes-required", accessibleIface + ".GetAttributes: synthetic unavailable", Selector{Attributes: map[string]string{"data-id": "42"}}, false},
		{"attributes-not-required", accessibleIface + ".GetAttributes: synthetic unavailable", Selector{Role: "entry"}, true},
		{"label-relation-required", accessibleIface + ".GetRelationSet: synthetic unavailable", Selector{Label: "Account"}, false},
		{"label-relation-not-required", accessibleIface + ".GetRelationSet: synthetic unavailable", Selector{Role: "entry"}, true},
		{"text-required", textIface + ": synthetic unavailable", Selector{Text: "Confirm"}, false},
		{"text-label-required", textIface + ": synthetic unavailable", Selector{Label: "Account"}, false},
		{"text-not-required", textIface + ": synthetic unavailable", Selector{Role: "entry"}, true},
		{"interface-required-for-text", accessibleIface + ".GetInterfaces: synthetic unavailable", Selector{Text: "Confirm"}, false},
		{"interface-irrelevant", accessibleIface + ".GetInterfaces: synthetic unavailable", Selector{Role: "entry"}, true},
		{"child-count-always-required", accessibleIface + ".ChildCount: synthetic unavailable", Selector{Role: "entry"}, false},
		{"action-optional", actionIface + ".GetActions: synthetic unavailable", Selector{Role: "entry"}, true},
		{"value-optional", valueIface + ".MinimumValue: synthetic unavailable", Selector{Role: "entry"}, true},
		{"selection-optional", selectionIface + ".GetSelectedChildCount: synthetic unavailable", Selector{Role: "entry"}, true},
		{"table-optional", tableIface + ".NRows: synthetic unavailable", Selector{Role: "entry"}, true},
		{"document-optional", documentIface + ".PageCount: synthetic unavailable", Selector{Role: "entry"}, true},
		{"unclassified-provider-warning", "org.synthetic.UnknownMethod: synthetic unavailable", Selector{Role: "entry"}, false},
		{"malformed-unknown-warning", "provider returned nonsense", Selector{Role: "entry"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := Snapshot{Root: Node{ID: root, Role: "application"}, Nodes: []Node{{ID: root, Role: "application"}, {ID: input, Parent: root, Role: "entry", Warnings: []string{tc.warning}}}, Warnings: []string{tc.warning}, ProviderErrors: 1}
			err := ValidateSemanticSnapshot(snapshot, tc.selector)
			if tc.complete && err != nil {
				t.Fatalf("optional failure %q incorrectly blocks independent selection: %v", tc.warning, err)
			}
			if !tc.complete && !errors.Is(err, ErrIncompleteSnapshot) {
				t.Fatalf("required failure %q not reported as incomplete: %v", tc.warning, err)
			}
		})
	}
}
func TestSnapshotGlobalTraversalWarningsCannotBeMistakenForBenignNodeMetadata(t *testing.T) {
	id := NodeID{BusName: "org.synthetic.App", ObjectPath: "/root", Generation: 6}
	for _, tc := range []struct {
		name                         string
		nodeWarnings, globalWarnings []string
		providerErrors               int
		wantIncomplete               bool
	}{
		{"matching optional warning", []string{actionIface + ".GetActions: error"}, []string{actionIface + ".GetActions: error"}, 1, false},
		{"node warning not collected", []string{actionIface + ".GetActions: error"}, nil, 0, true},
		{"unknown global warning", nil, []string{"child traversal failed"}, 1, true},
		{"provider count mismatch", nil, []string{actionIface + ".GetActions: error"}, 2, true},
		{"extra unrelated optional warning", nil, []string{actionIface + ".GetActions: error"}, 1, false},
		{"truncated without reasons", nil, nil, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := Snapshot{Root: Node{ID: id}, Nodes: []Node{{ID: id, Warnings: tc.nodeWarnings}}, Warnings: tc.globalWarnings, ProviderErrors: tc.providerErrors}
			if strings.HasPrefix(tc.name, "truncated") {
				snapshot.Truncated = true
			}
			err := ValidateSemanticSnapshot(snapshot, Selector{Role: "entry"})
			if tc.wantIncomplete && !errors.Is(err, ErrIncompleteSnapshot) {
				t.Fatalf("traversal incomplete but reported verified: %v", err)
			}
			if !tc.wantIncomplete && err != nil {
				t.Fatalf("benign optional warning blocked selection: %v", err)
			}
		})
	}
}

func TestSnapshotJSONSizeNeverUndercountsAdversarialSnapshots(t *testing.T) {
	random := rand.New(rand.NewSource(0x5a17))
	parts := []string{
		"quote \" slash \\ control \x00\b\f\n\r\t",
		"Unicode: café Ελληνικά 日本語 😀 \u2028 \u2029 <>&",
		string([]byte{0xff, 0xc0, 'x'}),
		strings.Repeat(string([]byte{0xff}), 2048),
		strings.Repeat("long-quoted-\\-text-😀", 2048),
	}
	for i, value := range parts {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal string case %d: %v", i, err)
		}
		if estimated := jsonStringSize(value); estimated < len(encoded) {
			t.Fatalf("string case %d estimate %d undercounts JSON size %d", i, estimated, len(encoded))
		}
	}
	maxInt := int(^uint(0) >> 1)
	minInt := -maxInt - 1
	for i := 0; i < 160; i++ {
		choose := func() string {
			value := parts[random.Intn(len(parts))]
			if random.Intn(2) == 0 {
				return value
			}
			return value + parts[random.Intn(len(parts))] + string(rune(0x1f600+random.Intn(64)))
		}
		rootID := NodeID{BusName: choose(), ObjectPath: "/root/" + choose(), Generation: ^uint64(0)}
		childID := NodeID{BusName: choose(), ObjectPath: "/child/" + choose(), Generation: uint64(random.Int63())}
		node := Node{
			ID:            childID,
			Parent:        rootID,
			Name:          choose(),
			Description:   choose(),
			Role:          choose(),
			RoleID:        ^uint32(0),
			Interfaces:    []string{choose(), choose()},
			Attributes:    map[string]string{choose(): choose(), "escaped\"key": choose()},
			States:        []string{choose(), choose(), choose()},
			Text:          choose(),
			TextTruncated: true,
			Bounds:        Rect{X: minInt, Y: maxInt, Width: maxInt, Height: minInt},
			HasBounds:     true,
			ChildCount:    maxInt,
			Children:      []NodeID{rootID, childID},
			Relations:     map[string][]NodeID{choose(): {rootID, childID}, "flows-to": {childID}},
			Value:         &ValueInfo{Current: math.MaxFloat64, Minimum: -math.MaxFloat64, Maximum: math.MaxFloat64, MinimumIncrement: math.SmallestNonzeroFloat64},
			Actions: []Action{{
				Index:         math.MaxInt32,
				Name:          choose(),
				LocalizedName: choose(),
				Description:   choose(),
				KeyBinding:    choose(),
			}},
			Selection: &SelectionInfo{SelectedChildCount: math.MaxInt32},
			Table:     &TableInfo{Rows: math.MaxInt32, Columns: math.MaxInt32},
			Document:  &DocumentInfo{Locale: choose(), CurrentPageNumber: math.MaxInt32, PageCount: math.MaxInt32},
			Focused:   true,
			Visible:   true,
			Showing:   true,
			Enabled:   true,
			Redacted:  true,
			Warnings:  []string{choose(), choose()},
		}
		root := node
		root.ID = rootID
		root.Parent = NodeID{}
		snapshot := Snapshot{
			Root:              root,
			Nodes:             []Node{root, node},
			Truncated:         true,
			TruncationReasons: []string{choose(), choose()},
			ProviderErrors:    maxInt,
			Warnings:          []string{choose(), choose()},
			Generation:        ^uint64(0),
			CapturedAt:        time.Date(9999, time.December, 31, 23, 59, 59, 999999999, time.FixedZone("+14", 14*60*60)),
			Source:            choose(),
		}
		encoded, err := json.Marshal(snapshot)
		if err != nil {
			t.Fatalf("marshal generated snapshot %d: %v", i, err)
		}
		if estimated := snapshotJSONSize(snapshot); estimated < len(encoded) {
			t.Fatalf("snapshot %d estimate %d undercounts JSON size %d", i, estimated, len(encoded))
		}
		if i%16 == 0 {
			sparse := Snapshot{Root: Node{ID: rootID}, Nodes: []Node{{ID: rootID}}, Generation: uint64(i), CapturedAt: time.Time{}}
			sparseJSON, marshalErr := json.Marshal(sparse)
			if marshalErr != nil {
				t.Fatalf("marshal sparse snapshot %d: %v", i, marshalErr)
			}
			if sparseEstimate := snapshotJSONSize(sparse); sparseEstimate < len(sparseJSON) {
				t.Fatalf("sparse snapshot %d estimate %d undercounts JSON size %d", i, sparseEstimate, len(sparseJSON))
			}
		}
	}
}

func TestSnapshotPrefixEstimateMatchesPrunedSnapshot(t *testing.T) {
	ids := []NodeID{
		{BusName: "b", ObjectPath: "/0", Generation: 1},
		{BusName: "b", ObjectPath: "/1", Generation: 1},
		{BusName: "b", ObjectPath: "/2", Generation: 1},
		{BusName: "b", ObjectPath: "/3", Generation: 1},
	}
	nodes := []Node{
		{
			ID: ids[0], Children: []NodeID{ids[1], ids[3]},
			Relations: map[string][]NodeID{"owns": {ids[1], ids[3]}, "empty": nil},
		},
		{
			ID: ids[1], Parent: ids[0], Attributes: map[string]string{"quote\"key": "value"},
			Children: []NodeID{ids[0], ids[2]}, Relations: map[string][]NodeID{"controls": {ids[3], ids[1]}},
		},
		{ID: ids[2], Parent: ids[1], Relations: map[string][]NodeID{"labelled-by": {ids[0]}}},
		{ID: ids[3], Parent: ids[2], Relations: map[string][]NodeID{"flows-to": {ids[2]}}},
	}
	snapshot := Snapshot{
		Root: nodes[0], Nodes: nodes, Truncated: true,
		TruncationReasons: []string{"bounded fixture"}, Warnings: []string{"warning"},
		Generation: 4, CapturedAt: time.Unix(1750000000, 0).UTC(), Source: "fixture",
	}
	nodeSizes := make([]int, len(nodes))
	prefixNodeSizes := make([]int, len(nodes)+1)
	for i, node := range nodes {
		nodeSizes[i] = estimateNodeJSONSize(node)
		prefixNodeSizes[i+1] = addJSONSize(prefixNodeSizes[i], nodeSizes[i])
	}
	retained := make(map[NodeID]struct{}, len(nodes))
	baseSize := snapshotJSONBaseSize(snapshot)
	for count := 0; count <= len(nodes); count++ {
		clear(retained)
		for _, node := range nodes[:count] {
			retained[node.ID] = struct{}{}
		}
		got := snapshotJSONSizeForPrefix(snapshot, count, nodeSizes, prefixNodeSizes, baseSize, retained, ids[0])

		candidate := snapshot
		candidate.Nodes = cloneSnapshotNodes(nodes[:count])
		if count == 0 {
			candidate.Root = Node{ID: ids[0]}
		} else {
			candidate.Root = candidate.Nodes[0]
		}
		pruneSnapshotReferences(&candidate)
		if want := snapshotJSONSize(candidate); got != want {
			t.Fatalf("prefix %d estimate = %d, pruned snapshot estimate = %d", count, got, want)
		}
	}
}

func TestMaximumPotentialSnapshotPrefixContainsLargestFittingPrefix(t *testing.T) {
	ids := []NodeID{
		{BusName: "b", ObjectPath: "/root", Generation: 1},
		{BusName: "b", ObjectPath: "/one", Generation: 1},
		{BusName: "b", ObjectPath: "/two", Generation: 1},
		{BusName: "b", ObjectPath: "/three", Generation: 1},
	}
	nodes := []Node{
		{ID: ids[0], Children: []NodeID{ids[1], ids[3]}},
		{ID: ids[1], Parent: ids[0], Relations: map[string][]NodeID{"controls": {ids[3]}}},
		{ID: ids[2], Parent: ids[0], Name: "an independent node"},
		{ID: ids[3], Parent: ids[0], Attributes: map[string]string{"label": "a longer target"}},
	}
	snapshot := Snapshot{Root: nodes[0], Nodes: nodes, Truncated: true, Generation: 1, CapturedAt: time.Unix(1750000000, 0).UTC()}
	nodeSizes := make([]int, len(nodes))
	for i, node := range nodes {
		nodeSizes[i] = estimateNodeJSONSize(node)
	}
	baseSize := snapshotJSONBaseSize(snapshot)
	prefixSizes := make([]int, len(nodes)+1)
	for count := range len(prefixSizes) {
		candidate := snapshot
		candidate.Nodes = cloneSnapshotNodes(nodes[:count])
		if count == 0 {
			candidate.Root = Node{ID: ids[0]}
		} else {
			candidate.Root = candidate.Nodes[0]
		}
		pruneSnapshotReferences(&candidate)
		prefixSizes[count] = snapshotJSONSize(candidate)
	}
	budgets := []int{
		prefixSizes[0],
		prefixSizes[1] - 1,
		prefixSizes[1],
		prefixSizes[2] - 1,
		prefixSizes[2],
		prefixSizes[len(nodes)],
	}
	for _, budget := range budgets {
		limit := maximumPotentialSnapshotPrefix(snapshot, nodeSizes, baseSize, budget, ids[0])
		largestFit := -1
		for count, size := range prefixSizes {
			if size <= budget {
				largestFit = count
			}
		}
		if limit < largestFit {
			t.Fatalf("budget %d potential limit = %d, below largest fitting prefix %d", budget, limit, largestFit)
		}
		if limit < len(nodes) {
			minimumNodesSize := 0
			minimumRootSize := estimateNodeJSONSize(Node{ID: ids[0]})
			for i, node := range nodes[:limit+1] {
				minimumSize := estimateNodeJSONSizeAfterReferencePruning(node, nodeSizes[i], nil)
				minimumNodesSize = addJSONSize(minimumNodesSize, minimumSize)
				if i == 0 {
					minimumRootSize = minimumSize
				}
			}
			beyondLimit := snapshotJSONSizeFromBase(baseSize, minimumRootSize, minimumNodesSize, limit+1, false)
			if beyondLimit <= budget {
				t.Fatalf("budget %d potential limit %d leaves prefix %d under the limit at %d bytes", budget, limit, limit+1, beyondLimit)
			}
		}
	}
}

// A one-byte or multibyte character must still make progress when snapshot
// bounding halves nonempty text. No invalid UTF-8 or private original text
// may escape when the remaining response budget admits only the empty node.
func TestSnapshotNodeBudgetDropsShortestTextWithoutInvalidUTF8(t *testing.T) {
	for _, tc := range []struct{ name, value string }{
		{name: "single byte", value: "x"},
		{name: "multibyte rune", value: "é"},
		{name: "astral rune", value: "😀"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := Node{Text: tc.value}
			budget := estimateNodeJSONSize(Node{}) + 60
			if estimateNodeJSONSize(original)+60 <= budget {
				t.Fatalf("test did not require truncation: value=%q budget=%d", tc.value, budget)
			}
			bounded := boundNodeResponse(original, budget)
			if bounded.Text != "" || !bounded.TextTruncated || !utf8.ValidString(bounded.Text) {
				t.Fatalf("short text escaped response budget: original=%q bounded=%+v", tc.value, bounded)
			}
			if original.Text != tc.value || original.TextTruncated {
				t.Fatalf("bounding mutated input node: %+v", original)
			}
			// The minimum node including its TextTruncated marker may still
			// exceed this intentionally impossible budget. The caller's
			// minimum-response fallback handles that case separately.
		})
	}
}

// A caller-supplied response budget must not override the absolute snapshot
// ceiling, and canonical role filters must not mutate caller-owned inputs.
func TestSnapshotOptionsClampTotalResponseAndCopyRoleFilters(t *testing.T) {
	roles := []string{"  ALERT ", "button", "Alert", "", "BUTTON"}
	opts := SnapshotOptions{
		MaxDepth:      absMaxDepth + 1,
		MaxNodes:      absMaxNodes + 1,
		MaxTextBytes:  absMaxText + 1,
		MaxTotalBytes: absMaxTotal + 1,
		SkipRoles:     roles,
	}
	normalized := opts.normalized()
	if normalized.MaxDepth != absMaxDepth || normalized.MaxNodes != absMaxNodes ||
		normalized.MaxTextBytes != absMaxText || normalized.MaxTotalBytes != absMaxTotal {
		t.Fatalf("oversized snapshot limits escaped caps: %+v", normalized)
	}
	if want := []string{"alert", "button"}; !reflect.DeepEqual(normalized.SkipRoles, want) {
		t.Fatalf("role filter normalization = %#v, want %#v", normalized.SkipRoles, want)
	}
	if want := []string{"  ALERT ", "button", "Alert", "", "BUTTON"}; !reflect.DeepEqual(roles, want) {
		t.Fatalf("normalizing snapshot options mutated caller-owned filters: %#v", roles)
	}
	if got := (SnapshotOptions{MaxTotalBytes: -1}).normalized().MaxTotalBytes; got != defaultMaxTotal {
		t.Fatalf("negative total budget default = %d, want %d", got, defaultMaxTotal)
	}
}

// An absent byte limit must not expose cached source storage through a
// response. Caller mutations are permitted only on the owned projection.
func TestSnapshotUnboundedResponseClonesOwnedNodesAndRoot(t *testing.T) {
	for _, budget := range []int{0, -1} {
		t.Run(map[int]string{0: "zero means unlimited", -1: "negative means unlimited"}[budget], func(t *testing.T) {
			original := Snapshot{Root: Node{Name: "original-root", Children: []NodeID{{BusName: "org.test", ObjectPath: "/child"}}}, Nodes: []Node{
				{Name: "source", Children: []NodeID{{BusName: "org.test", ObjectPath: "/child"}}, Interfaces: []string{"Accessible"}},
				{Name: "child", Interfaces: []string{"Action"}},
			}}
			got, err := boundSnapshotResponse(original, budget)
			if err != nil || len(got.Nodes) != 2 || got.Root.Name != "source" || got.Truncated {
				t.Fatalf("unbounded response lost original nodes: %+v err=%v", got, err)
			}
			got.Nodes[0].Name = "mutated"
			got.Nodes[0].Children[0].ObjectPath = "/changed"
			got.Nodes[1].Interfaces[0] = "changed"
			got.Root.Name = "mutated-root"
			if original.Nodes[0].Name != "source" || original.Nodes[0].Children[0].ObjectPath != "/child" || original.Nodes[1].Interfaces[0] != "Action" || original.Root.Name != "original-root" {
				t.Fatalf("unbounded response aliased source snapshot: %+v", original)
			}
		})
	}
}

// A removed candidate can be relevant even when no application identity was
// resolved for its bus. Retaining it would allow stale window selection.
func TestFreshWindowResolutionRejectsRemovedOrphanCandidate(t *testing.T) {
	desktop := objectIdentity{busName: registryName, objectPath: "/desktop"}
	orphan := objectIdentity{busName: "org.example.Orphan", objectPath: "/window"}
	observed := map[objectIdentity]Node{orphan: {ID: NodeID{BusName: orphan.busName, ObjectPath: orphan.objectPath}}}
	change := observationChange{childrenRemove: true, node: objectIdentity{busName: "org.other", objectPath: "/parent"}, parent: objectIdentity{busName: "org.other", objectPath: "/parent"}, removedChild: orphan}
	if err := windowResolutionChangeError(change, desktop, nil, observed); !errors.Is(err, ErrObservationChanged) {
		t.Fatalf("removed orphan window still eligible for fresh resolution: %v", err)
	}
	change.removedChild = objectIdentity{busName: "org.other", objectPath: "/unrelated"}
	if err := windowResolutionChangeError(change, desktop, nil, observed); err != nil {
		t.Fatalf("unrelated removed child invalidated orphan candidate: %v", err)
	}
}

func TestSnapshotObservationChangesNilBackendIsDisconnected(t *testing.T) {
	var backend *dbusBackend
	if err := backend.observationChangesError(1, NodeID{}, Snapshot{}, true); !errors.Is(err, ErrDisconnected) {
		t.Fatalf("nil observation backend error=%v", err)
	}
}

type syntheticTreeReader struct {
	nodes         map[string]Node
	edges         map[string][]objectRef
	readErr       map[string]error
	childErr      map[string]error
	reads         []string
	childReads    []string
	parents       map[string]NodeID
	freshReads    int
	freshChildren int
	afterRead     func()
	beforeRef     func()
}

func (r *syntheticTreeReader) refID(ref objectRef) NodeID {
	if r.beforeRef != nil {
		r.beforeRef()
	}
	return NodeID{BusName: ref.BusName, ObjectPath: string(ref.ObjectPath), Generation: 7}
}
func (r *syntheticTreeReader) readNode(ctx context.Context, id, parent NodeID, _ int, _ bool) (Node, error) {
	if err := ctx.Err(); err != nil {
		return Node{}, err
	}
	r.reads = append(r.reads, id.ObjectPath)
	if err := r.readErr[id.ObjectPath]; err != nil {
		return Node{}, err
	}
	node, ok := r.nodes[id.ObjectPath]
	if !ok {
		return Node{}, fmt.Errorf("no synthetic AT-SPI node %s", id.ObjectPath)
	}
	node.ID = id
	node.Parent = parent
	if r.afterRead != nil {
		r.afterRead()
	}
	return node, nil
}
func (r *syntheticTreeReader) children(ctx context.Context, id NodeID) ([]objectRef, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.childReads = append(r.childReads, id.ObjectPath)
	if err := r.childErr[id.ObjectPath]; err != nil {
		return nil, err
	}
	return r.edges[id.ObjectPath], nil
}
func (r *syntheticTreeReader) readNodeFresh(ctx context.Context, id, parent NodeID, limit int, sensitive bool) (Node, error) {
	r.freshReads++
	return r.readNode(ctx, id, parent, limit, sensitive)
}
func (r *syntheticTreeReader) childrenFresh(ctx context.Context, id NodeID) ([]objectRef, error) {
	r.freshChildren++
	return r.children(ctx, id)
}
func (r *syntheticTreeReader) recordObjectParent(id, parent NodeID) {
	if r.parents == nil {
		r.parents = map[string]NodeID{}
	}
	r.parents[id.ObjectPath] = parent
}
func newSyntheticTreeWalker(opts SnapshotOptions) *snapshotWalker {
	root := objectRef{BusName: "org.synthetic.App", ObjectPath: "/root"}
	folder := objectRef{BusName: root.BusName, ObjectPath: "/folder"}
	visible := objectRef{BusName: root.BusName, ObjectPath: "/visible"}
	hidden := objectRef{BusName: root.BusName, ObjectPath: "/hidden"}
	r := &syntheticTreeReader{
		nodes: map[string]Node{
			"/root":    {Name: "App", Role: "application", ChildCount: 3, Visible: true, Showing: true},
			"/folder":  {Name: "Folder", Role: "group", ChildCount: 1, Visible: true, Showing: true},
			"/visible": {Name: "Save", Role: "button", Visible: true, Showing: true},
			"/hidden":  {Name: "secret", Role: "button", Visible: false, Showing: false},
		},
		readErr: map[string]error{}, childErr: map[string]error{},
		edges: map[string][]objectRef{"/root": {folder, visible, hidden}, "/folder": {visible}},
	}
	return &snapshotWalker{backend: r, opts: opts, snapshot: Snapshot{Nodes: make([]Node, 0)}, seen: make(map[NodeID]struct{})}
}
func runSyntheticWalk(w *snapshotWalker) (Node, error) {
	return w.walk(context.Background(), NodeID{BusName: "org.synthetic.App", ObjectPath: "/root", Generation: 7}, NodeID{}, 0)
}

type snapshotReaderWithoutFreshMethods struct{ reader *syntheticTreeReader }

func (r snapshotReaderWithoutFreshMethods) readNode(ctx context.Context, id, parent NodeID, limit int, sensitive bool) (Node, error) {
	return r.reader.readNode(ctx, id, parent, limit, sensitive)
}
func (r snapshotReaderWithoutFreshMethods) children(ctx context.Context, id NodeID) ([]objectRef, error) {
	return r.reader.children(ctx, id)
}
func (r snapshotReaderWithoutFreshMethods) refID(ref objectRef) NodeID { return r.reader.refID(ref) }

func TestSnapshotWalkerFreshFallsBackWhenReaderHasNoFreshAPI(t *testing.T) {
	w := newSyntheticTreeWalker(SnapshotOptions{Fresh: true, MaxNodes: 8, MaxDepth: 8})
	reader := w.backend.(*syntheticTreeReader) //nolint:errcheck // Color models and synthetic fixture builders guarantee this concrete type.
	w.backend = snapshotReaderWithoutFreshMethods{reader: reader}
	root, err := runSyntheticWalk(w)
	if err != nil || root.ID.ObjectPath != "/root" || len(w.snapshot.Nodes) != 4 {
		t.Fatalf("fresh traversal without specialized reader = root %+v, nodes %d, err %v", root, len(w.snapshot.Nodes), err)
	}
	if reader.freshReads != 0 || reader.freshChildren != 0 || len(reader.parents) != 0 {
		t.Fatalf("fallback invoked unavailable fresh/parent APIs: reads=%d children=%d parents=%v", reader.freshReads, reader.freshChildren, reader.parents)
	}
}

func TestSnapshotWalkerRejectsEntryAtNodeLimit(t *testing.T) {
	w := newSyntheticTreeWalker(SnapshotOptions{MaxNodes: 1, MaxDepth: 8})
	id := NodeID{BusName: "org.synthetic.App", ObjectPath: "/root", Generation: 7}
	w.snapshot.Nodes = []Node{{ID: id}}
	if _, err := w.walk(context.Background(), id, NodeID{}, 0); err == nil || !w.snapshot.Truncated {
		t.Fatalf("walker accepted a node beyond its bound: err=%v snapshot=%+v", err, w.snapshot)
	}
}

func TestSnapshotWalkerPropagatesCancellationAfterProviderRead(t *testing.T) {
	w := newSyntheticTreeWalker(SnapshotOptions{MaxNodes: 8, MaxDepth: 8})
	reader := w.backend.(*syntheticTreeReader) //nolint:errcheck // Color models and synthetic fixture builders guarantee this concrete type.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader.afterRead = cancel
	id := NodeID{BusName: "org.synthetic.App", ObjectPath: "/root", Generation: 7}
	if _, err := w.walk(ctx, id, NodeID{}, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation during provider response was hidden: %v", err)
	}
	if len(w.snapshot.Nodes) != 1 {
		t.Fatalf("canceled traversal continued to child objects: %+v", w.snapshot.Nodes)
	}
}

func TestSnapshotWalkerPropagatesCancellationAtChildBoundary(t *testing.T) {
	w := newSyntheticTreeWalker(SnapshotOptions{MaxNodes: 8, MaxDepth: 8})
	reader := w.backend.(*syntheticTreeReader) //nolint:errcheck // Color models and synthetic fixture builders guarantee this concrete type.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader.beforeRef = cancel
	id := NodeID{BusName: "org.synthetic.App", ObjectPath: "/root", Generation: 7}
	if _, err := w.walk(ctx, id, NodeID{}, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation before child read was hidden: %v", err)
	}
	if len(w.snapshot.Nodes) != 1 {
		t.Fatalf("canceled traversal committed a child: %+v", w.snapshot.Nodes)
	}
}

func TestSnapshotWalkerPromotesNodeWarningsToProviderErrors(t *testing.T) {
	w := newSyntheticTreeWalker(SnapshotOptions{MaxNodes: 8, MaxDepth: 8})
	reader := w.backend.(*syntheticTreeReader) //nolint:errcheck // Color models and synthetic fixture builders guarantee this concrete type.
	reader.nodes["/root"] = Node{
		Name: "App", Role: "application", ChildCount: 3,
		Warnings: []string{"AT-SPI omitted one optional root property"},
	}
	root, err := runSyntheticWalk(w)
	if err != nil || root.ID.ObjectPath != "/root" {
		t.Fatalf("warning-bearing root traversal = %+v, %v", root, err)
	}
	if w.providerErrors != 1 || len(w.snapshot.Warnings) != 1 || w.snapshot.Warnings[0] != "AT-SPI omitted one optional root property" {
		t.Fatalf("node warning was not included in snapshot completeness evidence: %+v, errors=%d", w.snapshot, w.providerErrors)
	}
}
func TestSnapshotWalkerDetectsDuplicateLinksAndPreservesParentHandles(t *testing.T) {
	w := newSyntheticTreeWalker(SnapshotOptions{MaxNodes: 8, MaxDepth: 8, MaxTextBytes: 100})
	root, err := runSyntheticWalk(w)
	if err != nil || len(w.snapshot.Nodes) != 4 || len(root.Children) != 2 {
		t.Fatalf("cyclic/duplicate graph mishandled: %+v err=%v nodes=%+v", root, err, w.snapshot.Nodes)
	}
	if w.snapshot.Truncated || w.providerErrors != 0 {
		t.Fatalf("duplicate links caused false warning %+v", w.snapshot)
	}
	reader, ok := w.backend.(*syntheticTreeReader)
	if !ok {
		t.Fatalf("unexpected synthetic tree backend %T", w.backend)
	}
	if len(reader.reads) != 4 || reader.parents["/visible"].ObjectPath != "/folder" {
		t.Fatalf("node re-read or parent changed: reads=%v parents=%v", reader.reads, reader.parents)
	}
	for _, node := range w.snapshot.Nodes {
		if node.ID.ObjectPath == "/visible" && node.Parent.ObjectPath != "/folder" {
			t.Fatalf("duplicate node parent replaced by later link: %+v", node)
		}
	}
}
func TestSnapshotWalkerRedactsHiddenSubtreeAndRespectsFreshReads(t *testing.T) {
	w := newSyntheticTreeWalker(SnapshotOptions{MaxNodes: 8, MaxDepth: 8, VisibleOnly: true, Fresh: true})
	root, err := runSyntheticWalk(w)
	if err != nil || len(w.snapshot.Nodes) != 3 || len(root.Children) != 1 {
		t.Fatalf("hidden node leaked into bounded visible traversal: root=%+v nodes=%+v err=%v", root, w.snapshot.Nodes, err)
	}
	reader, ok := w.backend.(*syntheticTreeReader)
	if !ok {
		t.Fatalf("unexpected synthetic tree backend %T", w.backend)
	}
	if reader.freshReads != 4 || reader.freshChildren != 2 {
		t.Fatalf("fresh option skipped true fresh provider: read=%d children=%d", reader.freshReads, reader.freshChildren)
	}
	if w.snapshot.Truncated {
		t.Fatal("visible filtering incorrectly reported truncation")
	}
}
func TestSnapshotWalkerBoundedDepthNodesRolesAndMismatch(t *testing.T) {
	for _, tc := range []struct {
		name       string
		opts       SnapshotOptions
		wantReason string
		wantCount  int
	}{
		{"max depth", SnapshotOptions{MaxNodes: 8, MaxDepth: 0}, "max depth", 1},
		{"max nodes", SnapshotOptions{MaxNodes: 2, MaxDepth: 8}, "max nodes", 2},
		{"skip nested role", SnapshotOptions{MaxNodes: 8, MaxDepth: 8, SkipRoles: []string{"application"}}, "subtree below role", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newSyntheticTreeWalker(tc.opts)
			_, err := runSyntheticWalk(w)
			if err != nil || !w.snapshot.Truncated || len(w.snapshot.Nodes) != tc.wantCount || len(w.snapshot.TruncationReasons) == 0 || !strings.Contains(strings.Join(w.snapshot.TruncationReasons, " "), tc.wantReason) {
				t.Fatalf("bounds: nodes=%d truncated=%v reasons=%v err=%v", len(w.snapshot.Nodes), w.snapshot.Truncated, w.snapshot.TruncationReasons, err)
			}
		})
	}
	w := newSyntheticTreeWalker(SnapshotOptions{MaxNodes: 8, MaxDepth: 8})
	reader, ok := w.backend.(*syntheticTreeReader)
	if !ok {
		t.Fatalf("unexpected synthetic tree backend %T", w.backend)
	}
	reader.nodes["/root"] = Node{Name: "App", Role: "application", ChildCount: 4}
	_, err := runSyntheticWalk(w)
	if err != nil || !w.snapshot.Truncated || !strings.Contains(strings.Join(w.snapshot.TruncationReasons, " "), "child count mismatch") {
		t.Fatalf("missing child mismatch warning: %+v err=%v", w.snapshot, err)
	}
}
func TestSnapshotWalkerRecoversPartialErrorsButNeverStaleGeneration(t *testing.T) {
	for _, tc := range []struct {
		name         string
		childFailure bool
		stale        bool
	}{
		{"node provider error", false, false},
		{"child enumeration error", true, false},
		{"stale node", false, true},
		{"stale children", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := newSyntheticTreeWalker(SnapshotOptions{MaxNodes: 8, MaxDepth: 8})
			reader, ok := w.backend.(*syntheticTreeReader)
			if !ok {
				t.Fatalf("unexpected synthetic tree backend %T", w.backend)
			}
			failure := errors.New("AT-SPI synthetic provider disconnected")
			if tc.stale {
				failure = ErrStaleGeneration
			}
			if tc.childFailure {
				reader.childErr["/folder"] = failure
			} else {
				reader.readErr["/folder"] = failure
			}
			_, err := runSyntheticWalk(w)
			if tc.stale {
				if !errors.Is(err, ErrStaleGeneration) {
					t.Fatalf("stale backend silently recovered: %+v err=%v", w.snapshot, err)
				}
			} else if err != nil || w.providerErrors == 0 || len(w.snapshot.Warnings) == 0 {
				t.Fatalf("non-stale provider failure not reported: %+v err=%v", w.snapshot, err)
			}
		})
	}
}
func TestSnapshotWalkerRejectsCanceledReadsBeforeAnyBackendWork(t *testing.T) {
	w := newSyntheticTreeWalker(SnapshotOptions{MaxNodes: 8, MaxDepth: 8})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	id := NodeID{BusName: "org.synthetic.App", ObjectPath: "/root", Generation: 7}
	if _, err := w.walk(ctx, id, NodeID{}, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled snapshot walked: %v", err)
	}
	reader, ok := w.backend.(*syntheticTreeReader)
	if !ok {
		t.Fatalf("unexpected synthetic tree backend %T", w.backend)
	}
	if len(reader.reads) != 0 {
		t.Fatal("canceled request reached backend")
	}
}

// An AT-SPI property read failure must invalidate a selector that depends on
// that property, but an unrelated optional interface failure must not make
// an otherwise authoritative name or role match ambiguous.
func TestSnapshotSemanticWarningSelectorDependencyMatrix(t *testing.T) {
	cases := []struct {
		name, warning  string
		selector       Selector
		affects, known bool
	}{
		{"name selector requires name", accessibleIface + ".Name: missing", Selector{Name: "Editor"}, true, true},
		{"ancestor name requires name", accessibleIface + ".Name: missing", Selector{Ancestors: []AncestorSelector{{Name: "Dialog"}}}, true, true},
		{"unrelated name failure", accessibleIface + ".Name: missing", Selector{Role: "button"}, false, true},
		{"role selector requires role", accessibleIface + ".GetRole: missing", Selector{Role: "button"}, true, true},
		{"ancestor role requires role", accessibleIface + ".GetRole: missing", Selector{Ancestors: []AncestorSelector{{Role: "dialog"}}}, true, true},
		{"unrelated role failure", accessibleIface + ".GetRole: missing", Selector{Name: "Editor"}, false, true},
		{"state selector", accessibleIface + ".GetState: missing", Selector{States: []string{"focused"}}, true, true},
		{"unrelated state", accessibleIface + ".GetState: missing", Selector{Name: "Editor"}, false, true},
		{"child count always relevant", accessibleIface + ".ChildCount: missing", Selector{Name: "Editor"}, true, true},
		{"interfaces required by text", accessibleIface + ".GetInterfaces: missing", Selector{Text: "hello"}, true, true},
		{"interfaces unrelated to role", accessibleIface + ".GetInterfaces: missing", Selector{Role: "button"}, false, true},
		{"attribute selector", accessibleIface + ".GetAttributes: missing", Selector{Attributes: map[string]string{"type": "email"}}, true, true},
		{"unrelated attributes", accessibleIface + ".GetAttributes: missing", Selector{Name: "Editor"}, false, true},
		{"relations needed for label", accessibleIface + ".GetRelationSet: missing", Selector{Label: "Address"}, true, true},
		{"unrelated relations", accessibleIface + ".GetRelationSet: missing", Selector{Role: "button"}, false, true},
		{"text needed by label", textIface + ": missing", Selector{Label: "Address"}, true, true},
		{"unrelated text", textIface + ": missing", Selector{Role: "button"}, false, true},
		{"optional value failure", valueIface + ".GetCurrentValue: missing", Selector{Name: "Editor"}, false, true},
		{"optional action failure", actionIface + ".GetActions: missing", Selector{Role: "button"}, false, true},
		{"optional table failure", tableIface + ".GetRowCount: missing", Selector{Name: "Editor"}, false, true},
		{"unknown provider failure", "org.example.Provider.Failure: missing", Selector{Name: "Editor"}, true, false},
		{"warning missing separator", "unstructured failure", Selector{Name: "Editor"}, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := semanticWarningAffectsSelector(tc.warning, tc.selector); got != tc.affects {
				t.Fatalf("warning %q selector %+v affects=%v want=%v", tc.warning, tc.selector, got, tc.affects)
			}
			if got := knownNodeWarning(tc.warning); got != tc.known {
				t.Fatalf("warning %q known=%v want=%v", tc.warning, got, tc.known)
			}
		})
	}
}
