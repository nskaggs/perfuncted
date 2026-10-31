package accessibility

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

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
}

func (r *syntheticTreeReader) refID(ref objectRef) NodeID {
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
