package accessibility

import (
	"context"
	"fmt"
	"strings"
)

// Candidate is compact context for a successful, rejected, ambiguous, or
// missing semantic match. It deliberately reuses snapshot data rather than
// issuing another unbounded tree walk.
type Candidate struct {
	ID         NodeID              `json:"id"`
	Role       string              `json:"role,omitempty"`
	Name       string              `json:"name,omitempty"`
	States     []string            `json:"states,omitempty"`
	Actions    []Action            `json:"actions,omitempty"`
	Bounds     Rect                `json:"bounds"`
	HasBounds  bool                `json:"hasBounds"`
	Visible    bool                `json:"visible"`
	Showing    bool                `json:"showing"`
	Enabled    bool                `json:"enabled"`
	Breadcrumb []string            `json:"breadcrumb,omitempty"`
	Relations  map[string][]NodeID `json:"relations,omitempty"`
	Rejection  string              `json:"rejection,omitempty"`
}

// MatchError carries machine-readable candidate context while preserving
// errors.Is checks against ErrNotFound or ErrAmbiguous.
type MatchError struct {
	Operation  string      `json:"operation"`
	Err        error       `json:"-"`
	Candidates []Candidate `json:"candidates,omitempty"`
}

func (e *MatchError) Error() string {
	if e == nil {
		return "accessibility: match failed"
	}
	if e.Operation == "" {
		return e.Err.Error()
	}
	return fmt.Sprintf("accessibility: %s: %v", e.Operation, e.Err)
}

func (e *MatchError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// OutlineOptions bounds the derived semantic outline.
type OutlineOptions struct {
	MaxDepth int `json:"maxDepth,omitempty"`
	MaxNodes int `json:"maxNodes,omitempty"`
}

func (o OutlineOptions) normalized() OutlineOptions {
	if o.MaxDepth <= 0 || o.MaxDepth > absMaxDepth {
		o.MaxDepth = 8
	}
	if o.MaxNodes <= 0 || o.MaxNodes > absMaxNodes {
		o.MaxNodes = 512
	}
	return o
}

// OutlineNode is a compact, hierarchical view derived from Snapshot.
type OutlineNode struct {
	ID        NodeID        `json:"id"`
	Role      string        `json:"role,omitempty"`
	Name      string        `json:"name,omitempty"`
	Text      string        `json:"text,omitempty"`
	States    []string      `json:"states,omitempty"`
	Actions   []Action      `json:"actions,omitempty"`
	Bounds    Rect          `json:"bounds"`
	HasBounds bool          `json:"hasBounds"`
	Children  []OutlineNode `json:"children,omitempty"`
}

// Outline is the bounded semantic view and retains snapshot generation and
// warnings so callers can reason about freshness and partial results.
type Outline struct {
	Root       OutlineNode `json:"root"`
	Generation uint64      `json:"generation"`
	Truncated  bool        `json:"truncated"`
	Warnings   []string    `json:"warnings,omitempty"`
}

// BuildOutline derives a compact semantic outline without introducing a
// second accessibility model.
func BuildOutline(snapshot Snapshot, options OutlineOptions) Outline {
	options = options.normalized()
	byID := make(map[NodeID]Node, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		byID[node.ID] = node
	}
	children := make(map[NodeID][]NodeID, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		for _, child := range node.Children {
			children[node.ID] = append(children[node.ID], child)
		}
	}
	out := Outline{Generation: snapshot.Generation, Truncated: snapshot.Truncated, Warnings: append([]string(nil), snapshot.Warnings...)}
	var walk func(NodeID, int) OutlineNode
	count := 0
	walk = func(id NodeID, depth int) OutlineNode {
		node := byID[id]
		count++
		result := OutlineNode{ID: node.ID, Role: node.Role, Name: node.Name, Text: node.Text, States: append([]string(nil), node.States...), Actions: append([]Action(nil), node.Actions...), Bounds: node.Bounds, HasBounds: node.HasBounds}
		if depth >= options.MaxDepth {
			if len(children[id]) > 0 {
				out.Truncated = true
			}
			return result
		}
		for _, child := range children[id] {
			if count >= options.MaxNodes {
				out.Truncated = true
				break
			}
			if _, ok := byID[child]; !ok {
				continue
			}
			result.Children = append(result.Children, walk(child, depth+1))
		}
		return result
	}
	if snapshot.Root.ID.valid() {
		out.Root = walk(snapshot.Root.ID, 0)
	}
	return out
}

// CandidatesForQuery returns bounded contextual candidates from an existing
// snapshot. Successful ordinary queries do not pay this diagnostic cost.
func CandidatesForQuery(snapshot Snapshot, query Query, limit int) []Candidate {
	if limit <= 0 || limit > 128 {
		limit = 32
	}
	byID := make(map[NodeID]Node, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		byID[node.ID] = node
	}
	result := make([]Candidate, 0, min(limit, len(snapshot.Nodes)))
	for _, node := range snapshot.Nodes {
		if !candidateNearQuery(node, query) {
			continue
		}
		candidate := candidateFromNode(node)
		for parent := node.Parent; parent.valid(); {
			ancestor, ok := byID[parent]
			if !ok {
				break
			}
			label := strings.TrimSpace(ancestor.Role)
			if ancestor.Name != "" {
				label += ": " + ancestor.Name
			}
			if label != "" {
				candidate.Breadcrumb = append([]string{label}, candidate.Breadcrumb...)
			}
			parent = ancestor.Parent
		}
		result = append(result, candidate)
		if len(result) >= limit {
			break
		}
	}
	return result
}

func candidateNearQuery(node Node, query Query) bool {
	if query.Name == "" && query.Role == "" && query.Text == "" && len(query.States) == 0 && len(query.Attributes) == 0 {
		return true
	}
	return (query.Name != "" && strings.Contains(strings.ToLower(node.Name), strings.ToLower(query.Name))) ||
		(query.Role != "" && strings.Contains(strings.ToLower(node.Role), strings.ToLower(query.Role))) ||
		(query.Text != "" && strings.Contains(strings.ToLower(node.Text), strings.ToLower(query.Text)))
}

func candidateFromNode(node Node) Candidate {
	return Candidate{ID: node.ID, Role: node.Role, Name: node.Name, States: append([]string(nil), node.States...), Actions: append([]Action(nil), node.Actions...), Bounds: node.Bounds, HasBounds: node.HasBounds, Visible: node.Visible, Showing: node.Showing, Enabled: node.Enabled, Relations: node.Relations}
}

// Outline captures an existing scoped snapshot through the native backend.
func (b *dbusBackend) Outline(ctx context.Context, root NodeID, snapshotOptions SnapshotOptions, outlineOptions OutlineOptions) (Outline, error) {
	snapshot, err := b.Snapshot(ctx, root, snapshotOptions)
	if err != nil {
		return Outline{}, err
	}
	return BuildOutline(snapshot, outlineOptions), nil
}

var _ interface {
	Outline(context.Context, NodeID, SnapshotOptions, OutlineOptions) (Outline, error)
} = (*dbusBackend)(nil)
