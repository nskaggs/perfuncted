package accessibility

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

func (b *dbusBackend) Snapshot(ctx context.Context, root NodeID, opts SnapshotOptions) (Snapshot, error) { //nolint:gocyclo // bounded retry, cache, traversal, and publication are one transaction.
	if ctx == nil {
		return Snapshot{}, errors.New("accessibility: nil context")
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	if err := b.connected(); err != nil {
		return Snapshot{}, err
	}
	opts = opts.normalized()
	requestedRoot := root
	for attempt := 0; attempt < 2; attempt++ {
		root = requestedRoot
		if root == (NodeID{}) {
			if !opts.AllowDesktopRoot {
				return Snapshot{}, fmt.Errorf("%w: Snapshot requires an application, window, or root handle", ErrScope)
			}
			root = b.desktop()
		} else if !root.valid() {
			return Snapshot{}, fmt.Errorf("%w: invalid snapshot root", ErrScope)
		}
		if err := b.validateHandle(root); err != nil {
			return Snapshot{}, err
		}
		expectedGeneration := root.Generation
		if root.BusName != registryName {
			if err := b.loadCache(ctx, root.BusName); err != nil && errors.Is(err, ErrStaleGeneration) {
				if requestedRoot == (NodeID{}) {
					continue
				}
				return Snapshot{}, err
			}
		}
		key := snapshotKey(root, opts)
		now := time.Now()
		b.mu.RLock()
		if entry, ok := b.cache[key]; ok && now.Sub(entry.at) < cacheTTL && entry.snapshot.Generation == expectedGeneration {
			snapshot := cloneSnapshot(entry.snapshot)
			b.mu.RUnlock()
			return snapshot, nil
		}
		b.mu.RUnlock()
		walker := snapshotWalker{
			backend: b,
			opts:    opts,
			snapshot: Snapshot{
				Nodes: make([]Node, 0, min(opts.MaxNodes, 1024)),
			},
			seen: make(map[NodeID]struct{}, min(opts.MaxNodes, 1024)),
		}
		rootNode, err := walker.walk(ctx, root, NodeID{}, 0)
		if errors.Is(err, ErrStaleGeneration) || errors.Is(walker.err, ErrStaleGeneration) {
			if requestedRoot == (NodeID{}) {
				continue
			}
			if err == nil {
				err = walker.err
			}
			return Snapshot{}, err
		}
		if err != nil && (ctx.Err() != nil || len(walker.snapshot.Nodes) == 0) {
			return Snapshot{}, err
		}
		walker.snapshot.Root = rootNode
		walker.snapshot.CapturedAt = now
		walker.snapshot.Generation = expectedGeneration
		walker.snapshot.Source = "at-spi"
		walker.snapshot.ProviderErrors = walker.providerErrors
		if generationErr := b.generationError(expectedGeneration); generationErr != nil {
			if requestedRoot == (NodeID{}) {
				continue
			}
			return Snapshot{}, generationErr
		}
		bounded, err := boundSnapshotResponse(walker.snapshot, opts.MaxTotalBytes)
		if err != nil {
			return Snapshot{}, err
		}
		walker.snapshot = bounded
		if err := b.publishSnapshot(key, expectedGeneration, walker.snapshot); err != nil {
			if requestedRoot == (NodeID{}) && errors.Is(err, ErrStaleGeneration) {
				continue
			}
			return Snapshot{}, err
		}
		return walker.snapshot, nil
	}
	return Snapshot{}, fmt.Errorf("%w: bounded retry exhausted", ErrStaleGeneration)
}

func snapshotKey(root NodeID, opts SnapshotOptions) string {
	return root.BusName + "\x00" + root.ObjectPath + "\x00" + strconv.FormatUint(root.Generation, 10) + "\x00" + strconv.Itoa(opts.MaxDepth) + "\x00" + strconv.Itoa(opts.MaxNodes) + "\x00" + strconv.Itoa(opts.MaxTextBytes) + "\x00" + strconv.Itoa(opts.MaxTotalBytes) + "\x00" + strconv.FormatBool(opts.VisibleOnly) + "\x00" + strconv.FormatBool(opts.AllowSensitive) + "\x00" + strconv.FormatBool(opts.AllowDesktopRoot) + "\x00" + strings.Join(opts.SkipRoles, ",")
}

func (b *dbusBackend) generationError(expected uint64) error {
	current := b.Generation()
	if current == expected {
		return nil
	}
	return fmt.Errorf("%w: expected %d, current %d: %w", ErrStaleGeneration, expected, current, ErrStaleNode)
}

func (b *dbusBackend) publishSnapshot(key string, expected uint64, snapshot Snapshot) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed || b.disconnected {
		return ErrDisconnected
	}
	if b.generation != expected {
		return fmt.Errorf("%w: expected %d, current %d: %w", ErrStaleGeneration, expected, b.generation, ErrStaleNode)
	}
	if b.cache == nil {
		b.cache = make(map[string]cachedSnapshot)
	}
	b.cache[key] = cachedSnapshot{at: snapshot.CapturedAt, snapshot: cloneSnapshot(snapshot)}
	return nil
}

func boundSnapshotResponse(snapshot Snapshot, maxBytes int) (Snapshot, error) {
	if maxBytes <= 0 || snapshotJSONSize(snapshot) <= maxBytes {
		return snapshot, nil
	}
	snapshot.Truncated = true
	snapshot.TruncationReasons = appendUnique(snapshot.TruncationReasons, fmt.Sprintf("max total bytes %d", maxBytes))
	for i := range snapshot.Nodes {
		snapshot.Nodes[i] = boundNodeResponse(snapshot.Nodes[i], maxBytes)
	}
	if len(snapshot.Nodes) > 0 {
		snapshot.Root = snapshot.Nodes[0]
	}
	if snapshotJSONSize(snapshot) <= maxBytes {
		return snapshot, nil
	}

	// Binary search for the largest prefix that fits. The snapshotJSONSize
	// estimator is conservative (upper-bound), so every "fits" decision in
	// the search is correct. The final marshal validates the winner.
	originalRoot := snapshot.Root.ID
	low, high, best := 0, len(snapshot.Nodes), -1
	for low <= high {
		middle := low + (high-low)/2
		candidate := snapshot
		candidate.Nodes = append([]Node(nil), snapshot.Nodes[:middle]...)
		if middle == 0 {
			candidate.Root = Node{ID: originalRoot}
		} else {
			candidate.Root = candidate.Nodes[0]
		}
		pruneSnapshotReferences(&candidate)
		if snapshotJSONSize(candidate) <= maxBytes {
			best = middle
			low = middle + 1
			continue
		}
		high = middle - 1
	}
	if best < 0 {
		snapshot.Nodes = nil
		snapshot.Root = Node{ID: originalRoot}
	} else {
		snapshot.Nodes = append([]Node(nil), snapshot.Nodes[:best]...)
		if best == 0 {
			snapshot.Root = Node{ID: originalRoot}
		} else {
			snapshot.Root = snapshot.Nodes[0]
		}
	}
	pruneSnapshotReferences(&snapshot)
	if snapshotJSONSize(snapshot) > maxBytes {
		minimum := snapshot
		minimum.Nodes = nil
		minimum.Root = Node{ID: originalRoot}
		minimum.Warnings = nil
		minimum.ProviderErrors = 0
		pruneSnapshotReferences(&minimum)
		if snapshotJSONSize(minimum) > maxBytes {
			return Snapshot{}, fmt.Errorf("%w: minimum response is %d bytes, limit is %d", ErrResponseBudget, snapshotJSONSize(minimum), maxBytes)
		}
		return minimum, nil
	}
	return snapshot, nil
}

func pruneSnapshotReferences(snapshot *Snapshot) {
	if snapshot == nil {
		return
	}
	retained := make(map[NodeID]struct{}, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		retained[node.ID] = struct{}{}
	}
	for i := range snapshot.Nodes {
		node := &snapshot.Nodes[i]
		children := node.Children[:0]
		for _, child := range node.Children {
			if _, ok := retained[child]; ok {
				children = append(children, child)
			}
		}
		node.Children = children
		for relation, targets := range node.Relations {
			kept := targets[:0]
			for _, target := range targets {
				if _, ok := retained[target]; ok {
					kept = append(kept, target)
				}
			}
			if len(kept) == 0 {
				delete(node.Relations, relation)
			} else {
				node.Relations[relation] = kept
			}
		}
	}
	if len(snapshot.Nodes) > 0 {
		snapshot.Root = snapshot.Nodes[0]
	}
}

func boundNodeResponse(node Node, maxBytes int) Node {
	for estimateNodeJSONSize(node)+60 > maxBytes {
		switch {
		case node.Text != "":
			limit := len(node.Text) / 2
			if limit == len(node.Text) {
				limit--
			}
			node.Text, _ = truncateUTF8(node.Text, limit)
			node.TextTruncated = true
		case node.Attributes != nil:
			node.Attributes = nil
		case node.Relations != nil:
			node.Relations = nil
		case node.Actions != nil:
			node.Actions = nil
		case node.Warnings != nil:
			node.Warnings = nil
		case node.Description != "":
			node.Description = ""
		case node.Name != "":
			node.Name = ""
		case node.Interfaces != nil:
			node.Interfaces = nil
		case node.States != nil:
			node.States = nil
		case node.Children != nil:
			node.Children = nil
		default:
			return node
		}
	}
	return node
}

// estimateNodeJSONSize returns a conservative upper bound for a node's JSON
// representation. String token sizes follow encoding/json's escaping rules;
// fixed structural allowances cover field names, punctuation, and scalar
// values so response-budget enforcement cannot admit an oversized node.
func estimateNodeJSONSize(node Node) int {
	// A zero Node contributes 248 JSON bytes, including two 45-byte NodeID
	// values. This fixed remainder covers its outer fields and zero-valued
	// scalar members; variable NodeID contents are counted separately.
	const fixedNodeStructure = 158
	n := fixedNodeStructure
	n = addJSONSize(n, estimateNodeStringsAndIDsJSONSize(node))
	n = addJSONSize(n, estimateNodeScalarFieldsJSONSize(node))
	n = addJSONSize(n, estimateNodeCollectionsJSONSize(node))
	n = addJSONSize(n, estimateNodeOptionalInfoJSONSize(node))
	return n
}

func estimateNodeStringsAndIDsJSONSize(node Node) int {
	n := 0
	n = addJSONSize(n, estimateStringFieldJSONSize("name", node.Name))
	n = addJSONSize(n, estimateStringFieldJSONSize("description", node.Description))
	n = addJSONSize(n, estimateStringFieldJSONSize("role", node.Role))
	n = addJSONSize(n, estimateStringFieldJSONSize("text", node.Text))
	n = addJSONSize(n, estimateNodeIDJSONSize(node.ID))
	n = addJSONSize(n, estimateNodeIDJSONSize(node.Parent))
	return n
}

func estimateNodeScalarFieldsJSONSize(node Node) int {
	n := 0
	for _, value := range []int{node.Bounds.X, node.Bounds.Y, node.Bounds.Width, node.Bounds.Height, node.ChildCount} {
		n = addJSONSize(n, decimalIntSize(value)-1)
	}
	if node.RoleID != 0 {
		n = addJSONSize(n, estimateJSONMemberSize("roleId", decimalUint32Size(node.RoleID)))
	}
	if node.TextTruncated {
		n = addJSONSize(n, estimateJSONMemberSize("textTruncated", len("true")))
	}
	if node.Redacted {
		n = addJSONSize(n, estimateJSONMemberSize("redacted", len("true")))
	}
	return n
}

func estimateNodeCollectionsJSONSize(node Node) int {
	n := 0
	if len(node.Interfaces) > 0 {
		n = addJSONSize(n, estimateJSONMemberSize("interfaces", estimateStringSliceJSONSize(node.Interfaces)))
	}
	if len(node.States) > 0 {
		n = addJSONSize(n, estimateJSONMemberSize("states", estimateStringSliceJSONSize(node.States)))
	}
	if len(node.Warnings) > 0 {
		n = addJSONSize(n, estimateJSONMemberSize("warnings", estimateStringSliceJSONSize(node.Warnings)))
	}
	if len(node.Children) > 0 {
		n = addJSONSize(n, estimateJSONMemberSize("children", estimateNodeIDSliceJSONSize(node.Children)))
	}
	if len(node.Attributes) > 0 {
		n = addJSONSize(n, estimateJSONMemberSize("attributes", estimateStringMapJSONSize(node.Attributes)))
	}
	if len(node.Relations) > 0 {
		n = addJSONSize(n, estimateJSONMemberSize("relations", estimateRelationsJSONSize(node.Relations)))
	}
	if len(node.Actions) > 0 {
		n = addJSONSize(n, estimateJSONMemberSize("actions", estimateActionsJSONSize(node.Actions)))
	}
	return n
}

func estimateNodeOptionalInfoJSONSize(node Node) int {
	n := 0
	if node.Value != nil {
		n = addJSONSize(n, estimateJSONMemberSize("value", estimateValueInfoJSONSize()))
	}
	if node.Selection != nil {
		n = addJSONSize(n, estimateJSONMemberSize("selection", estimateSelectionInfoJSONSize(*node.Selection)))
	}
	if node.Table != nil {
		n = addJSONSize(n, estimateJSONMemberSize("table", estimateTableInfoJSONSize(*node.Table)))
	}
	if node.Document != nil {
		n = addJSONSize(n, estimateJSONMemberSize("document", estimateDocumentInfoJSONSize(*node.Document)))
	}
	return n
}

// snapshotJSONSize conservatively estimates the total JSON-encoded size of a
// snapshot, including the separately serialized root node and all strings in
// the snapshot envelope. It avoids repeated full-snapshot serialization while
// binary-search truncation enforces MaxTotalBytes.
func snapshotJSONSize(snapshot Snapshot) int {
	// Snapshot{} has a 65-byte envelope outside the root node, nodes value, and
	// timestamp token. The envelope includes all fixed keys and scalar fields.
	const fixedSnapshotStructure = 65
	n := addJSONSize(fixedSnapshotStructure, estimateNodeJSONSize(snapshot.Root))
	n = addJSONSize(n, jsonStringSize(snapshot.CapturedAt.Format(time.RFC3339Nano)))
	switch {
	case snapshot.Nodes == nil:
		n = addJSONSize(n, len("null"))
	case len(snapshot.Nodes) == 0:
		n = addJSONSize(n, 2)
	default:
		n = addJSONSize(n, 2)
		for i, node := range snapshot.Nodes {
			if i > 0 {
				n = addJSONSize(n, 1)
			}
			n = addJSONSize(n, estimateNodeJSONSize(node))
		}
	}
	n = addJSONSize(n, decimalUint64Size(snapshot.Generation)-1)
	if snapshot.ProviderErrors != 0 {
		n = addJSONSize(n, estimateJSONMemberSize("providerErrors", decimalIntSize(snapshot.ProviderErrors)))
	}
	if len(snapshot.TruncationReasons) > 0 {
		n = addJSONSize(n, estimateJSONMemberSize("truncationReasons", estimateStringSliceJSONSize(snapshot.TruncationReasons)))
	}
	if len(snapshot.Warnings) > 0 {
		n = addJSONSize(n, estimateJSONMemberSize("warnings", estimateStringSliceJSONSize(snapshot.Warnings)))
	}
	if snapshot.Source != "" {
		n = addJSONSize(n, estimateStringFieldJSONSize("source", snapshot.Source))
	}
	return n
}

func estimateNodeIDJSONSize(id NodeID) int {
	const fixedNodeIDStructure = 40
	n := fixedNodeIDStructure
	n = addJSONSize(n, jsonStringSize(id.BusName))
	n = addJSONSize(n, jsonStringSize(id.ObjectPath))
	n = addJSONSize(n, decimalUint64Size(id.Generation))
	return n
}

func estimateNodeIDSliceJSONSize(values []NodeID) int {
	n := 2
	for i, value := range values {
		if i > 0 {
			n = addJSONSize(n, 1)
		}
		n = addJSONSize(n, estimateNodeIDJSONSize(value))
	}
	return n
}

func estimateActionsJSONSize(values []Action) int {
	n := 2
	for i, action := range values {
		if i > 0 {
			n = addJSONSize(n, 1)
		}
		n = addJSONSize(n, estimateActionJSONSize(action))
	}
	return n
}

func estimateStringMapJSONSize(values map[string]string) int {
	n := 2
	i := 0
	for key, value := range values {
		if i > 0 {
			n = addJSONSize(n, 1)
		}
		n = addJSONSize(n, jsonStringSize(key))
		n = addJSONSize(n, 1)
		n = addJSONSize(n, jsonStringSize(value))
		i++
	}
	return n
}

func estimateRelationsJSONSize(values map[string][]NodeID) int {
	n := 2
	i := 0
	for key, targets := range values {
		if i > 0 {
			n = addJSONSize(n, 1)
		}
		n = addJSONSize(n, jsonStringSize(key))
		n = addJSONSize(n, 1)
		if targets == nil {
			n = addJSONSize(n, len("null"))
		} else {
			n = addJSONSize(n, estimateNodeIDSliceJSONSize(targets))
		}
		i++
	}
	return n
}

func estimateValueInfoJSONSize() int {
	n := 2
	for i, field := range []string{"current", "minimum", "maximum", "minimumIncrement"} {
		if i > 0 {
			n = addJSONSize(n, 1)
		}
		n = addJSONSize(n, estimateJSONMemberSize(field, 32))
	}
	return n
}

func estimateSelectionInfoJSONSize(value SelectionInfo) int {
	return 2 + estimateJSONMemberSize("selectedChildCount", decimalInt32Size(value.SelectedChildCount))
}

func estimateTableInfoJSONSize(value TableInfo) int {
	n := 2 + estimateJSONMemberSize("rows", decimalInt32Size(value.Rows))
	n = addJSONSize(n, estimateJSONMemberSize("columns", decimalInt32Size(value.Columns)))
	return n
}

func estimateDocumentInfoJSONSize(value DocumentInfo) int {
	n := 2
	count := 0
	if value.Locale != "" {
		n = addJSONSize(n, estimateStringFieldJSONSize("locale", value.Locale))
		count++
	}
	if value.CurrentPageNumber != 0 {
		if count > 0 {
			n = addJSONSize(n, 1)
		}
		n = addJSONSize(n, estimateJSONMemberSize("currentPageNumber", decimalInt32Size(value.CurrentPageNumber)))
		count++
	}
	if value.PageCount != 0 {
		if count > 0 {
			n = addJSONSize(n, 1)
		}
		n = addJSONSize(n, estimateJSONMemberSize("pageCount", decimalInt32Size(value.PageCount)))
	}
	return n
}

func estimateActionJSONSize(action Action) int {
	n := 2 + estimateJSONMemberSize("index", decimalInt32Size(action.Index))
	for _, field := range []struct{ key, value string }{
		{key: "name", value: action.Name},
		{key: "localizedName", value: action.LocalizedName},
		{key: "description", value: action.Description},
		{key: "keyBinding", value: action.KeyBinding},
	} {
		if field.value != "" {
			n = addJSONSize(n, estimateStringFieldJSONSize(field.key, field.value))
		}
	}
	return n
}

func estimateStringFieldJSONSize(key, value string) int {
	if value == "" {
		return 0
	}
	return estimateJSONMemberSize(key, jsonStringSize(value))
}

func estimateJSONMemberSize(key string, valueSize int) int {
	// Include a trailing comma for each member so callers need not know whether
	// it is the final member in the object.
	n := len(key) + 4
	return addJSONSize(n, valueSize)
}

func decimalIntSize(value int) int {
	var buffer [20]byte
	return len(strconv.AppendInt(buffer[:0], int64(value), 10))
}

func decimalInt32Size(value int32) int {
	var buffer [11]byte
	return len(strconv.AppendInt(buffer[:0], int64(value), 10))
}

func decimalUint32Size(value uint32) int {
	var buffer [10]byte
	return len(strconv.AppendUint(buffer[:0], uint64(value), 10))
}

func decimalUint64Size(value uint64) int {
	var buffer [20]byte
	return len(strconv.AppendUint(buffer[:0], value, 10))
}

func estimateStringSliceJSONSize(values []string) int {
	if len(values) == 0 {
		return 0
	}
	n := 2
	for i, value := range values {
		if i > 0 {
			n = addJSONSize(n, 1)
		}
		n = addJSONSize(n, jsonStringSize(value))
	}
	return n
}

func jsonStringSize(value string) int {
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

func addJSONSize(size, addition int) int {
	maxInt := int(^uint(0) >> 1)
	if addition > 0 && size > maxInt-addition {
		return maxInt
	}
	return size + addition
}

func truncateUTF8(value string, maxBytes int) (string, bool) {
	if maxBytes < 0 {
		maxBytes = 0
	}
	if len(value) <= maxBytes {
		return value, false
	}
	value = value[:maxBytes]
	for len(value) > 0 && !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value, true
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func cloneSnapshot(in Snapshot) Snapshot {
	out := in
	out.Nodes = append([]Node(nil), in.Nodes...)
	out.TruncationReasons = append([]string(nil), in.TruncationReasons...)
	out.Warnings = append([]string(nil), in.Warnings...)
	for i := range out.Nodes {
		out.Nodes[i].Interfaces = append([]string(nil), in.Nodes[i].Interfaces...)
		out.Nodes[i].States = append([]string(nil), in.Nodes[i].States...)
		out.Nodes[i].Children = append([]NodeID(nil), in.Nodes[i].Children...)
		out.Nodes[i].Warnings = append([]string(nil), in.Nodes[i].Warnings...)
		out.Nodes[i].Actions = append([]Action(nil), in.Nodes[i].Actions...)
		if in.Nodes[i].Value != nil {
			value := *in.Nodes[i].Value
			out.Nodes[i].Value = &value
		}
		if in.Nodes[i].Selection != nil {
			selection := *in.Nodes[i].Selection
			out.Nodes[i].Selection = &selection
		}
		if in.Nodes[i].Table != nil {
			table := *in.Nodes[i].Table
			out.Nodes[i].Table = &table
		}
		if in.Nodes[i].Document != nil {
			document := *in.Nodes[i].Document
			out.Nodes[i].Document = &document
		}
		if in.Nodes[i].Relations != nil {
			out.Nodes[i].Relations = make(map[string][]NodeID, len(in.Nodes[i].Relations))
			for key, values := range in.Nodes[i].Relations {
				out.Nodes[i].Relations[key] = append([]NodeID(nil), values...)
			}
		}
		if in.Nodes[i].Attributes != nil {
			out.Nodes[i].Attributes = make(map[string]string, len(in.Nodes[i].Attributes))
			for key, value := range in.Nodes[i].Attributes {
				out.Nodes[i].Attributes[key] = value
			}
		}
	}
	if len(out.Nodes) > 0 {
		out.Root = out.Nodes[0]
	}
	return out
}

type snapshotWalker struct {
	backend        snapshotBackend
	opts           SnapshotOptions
	snapshot       Snapshot
	seen           map[NodeID]struct{}
	providerErrors int
	err            error
}

type snapshotBackend interface {
	readNode(context.Context, NodeID, NodeID, int, bool) (Node, error)
	children(context.Context, NodeID) ([]objectRef, error)
	refID(objectRef) NodeID
}

func (w *snapshotWalker) walk(ctx context.Context, id, parent NodeID, depth int) (Node, error) {
	if err := ctx.Err(); err != nil {
		return Node{}, err
	}
	if depth > w.opts.MaxDepth {
		w.markTruncated(fmt.Sprintf("max depth %d", w.opts.MaxDepth))
		return Node{}, fmt.Errorf("accessibility: max depth %d reached", w.opts.MaxDepth)
	}
	if len(w.snapshot.Nodes) >= w.opts.MaxNodes {
		w.markTruncated(fmt.Sprintf("max nodes %d", w.opts.MaxNodes))
		return Node{}, fmt.Errorf("accessibility: max nodes %d reached", w.opts.MaxNodes)
	}
	if _, ok := w.seen[id]; ok {
		return Node{}, nil
	}
	w.seen[id] = struct{}{}
	node, err := w.backend.readNode(ctx, id, parent, w.opts.MaxTextBytes, w.opts.AllowSensitive)
	if err != nil {
		return Node{}, err
	}
	if w.opts.VisibleOnly && depth > 0 && !node.Visible && !node.Showing {
		return Node{}, nil
	}
	if len(node.Warnings) > 0 {
		w.snapshot.Warnings = append(w.snapshot.Warnings, node.Warnings...)
		w.providerErrors += len(node.Warnings)
	}
	nodeIndex := len(w.snapshot.Nodes)
	w.snapshot.Nodes = append(w.snapshot.Nodes, node)
	if node.ChildCount > 0 && !w.opts.skipRole(node.Role) {
		if err := w.walkChildren(ctx, &node, id, depth); err != nil {
			return Node{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return Node{}, err
	}
	w.snapshot.Nodes[nodeIndex] = node
	return node, nil
}

func (o SnapshotOptions) skipRole(role string) bool {
	role = strings.ToLower(strings.TrimSpace(role))
	for _, skipped := range o.SkipRoles {
		if role == skipped {
			return true
		}
	}
	return false
}

func (w *snapshotWalker) walkChildren(ctx context.Context, node *Node, id NodeID, depth int) error {
	children, err := w.backend.children(ctx, id)
	if err != nil {
		if errors.Is(err, ErrStaleGeneration) {
			w.err = err
			return err
		}
		w.snapshot.Warnings = append(w.snapshot.Warnings, fmt.Sprintf("%s: %v", id.ObjectPath, err))
		w.providerErrors++
		return nil
	}
	for _, childRef := range children {
		if len(w.snapshot.Nodes) >= w.opts.MaxNodes {
			w.markTruncated(fmt.Sprintf("max nodes %d", w.opts.MaxNodes))
			return nil
		}
		childID := w.backend.refID(childRef)
		child, childErr := w.walk(ctx, childID, id, depth+1)
		if childErr != nil {
			if ctx.Err() != nil {
				return nil
			}
			if errors.Is(childErr, ErrStaleGeneration) {
				w.err = childErr
				return childErr
			}
			w.snapshot.Warnings = append(w.snapshot.Warnings, childErr.Error())
			w.providerErrors++
			continue
		}
		if child.ID.valid() {
			node.Children = append(node.Children, child.ID)
		}
	}
	return nil
}

func (w *snapshotWalker) markTruncated(reason string) {
	w.snapshot.Truncated = true
	for _, existing := range w.snapshot.TruncationReasons {
		if existing == reason {
			return
		}
	}
	w.snapshot.TruncationReasons = append(w.snapshot.TruncationReasons, reason)
}

func (b *dbusBackend) Find(ctx context.Context, root NodeID, query Query, opts SnapshotOptions) ([]Node, error) {
	snapshot, err := b.Snapshot(ctx, root, opts)
	if err != nil {
		return nil, err
	}
	return FilterSnapshot(snapshot, query), nil
}

// FilterSnapshot returns the snapshot nodes matching query using the same
// case-insensitive substring contract as Backend.Find. It lets callers reuse
// one bounded snapshot for both selection and diagnostic candidates instead
// of paying two AT-SPI traversals.
func FilterSnapshot(snapshot Snapshot, query Query) []Node {
	wantName, wantRole, wantText := strings.ToLower(strings.TrimSpace(query.Name)), strings.ToLower(strings.TrimSpace(query.Role)), strings.ToLower(strings.TrimSpace(query.Text))
	result := make([]Node, 0)
	for _, node := range snapshot.Nodes {
		if wantName != "" && !strings.Contains(strings.ToLower(node.Name), wantName) {
			continue
		}
		if wantRole != "" && !strings.Contains(strings.ToLower(node.Role), wantRole) {
			continue
		}
		if wantText != "" && !strings.Contains(strings.ToLower(node.Text), wantText) {
			continue
		}
		if !matchesStates(node.States, query.States) || !matchesAttributes(node.Attributes, query.Attributes) {
			continue
		}
		result = append(result, node)
	}
	return result
}

func matchesStates(have, want []string) bool {
	for _, requested := range want {
		found := false
		for _, state := range have {
			if strings.EqualFold(strings.TrimSpace(requested), state) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func matchesAttributes(have, want map[string]string) bool {
	for key, expected := range want {
		actual, ok := "", false
		for haveKey, haveValue := range have {
			if strings.EqualFold(haveKey, key) {
				actual, ok = haveValue, true
				break
			}
		}
		if !ok || !strings.EqualFold(actual, expected) {
			return false
		}
	}
	return true
}

func (b *dbusBackend) Focused(ctx context.Context, opts SnapshotOptions) (Node, error) {
	if ctx == nil {
		return Node{}, errors.New("accessibility: nil context")
	}
	if err := ctx.Err(); err != nil {
		return Node{}, err
	}
	opts.AllowDesktopRoot = true
	snapshot, err := b.Snapshot(ctx, NodeID{}, opts)
	if err != nil {
		return Node{}, err
	}
	for _, node := range snapshot.Nodes {
		if node.Focused {
			return node, nil
		}
	}
	return Node{}, ErrNotFound
}

func (b *dbusBackend) AtPoint(ctx context.Context, x, y int) (Node, error) {
	if ctx == nil {
		return Node{}, errors.New("accessibility: nil context")
	}
	if x < -2147483648 || x > 2147483647 || y < -2147483648 || y > 2147483647 {
		return Node{}, errors.New("accessibility: point coordinates out of range")
	}
	obj, err := b.object(b.desktop())
	if err != nil {
		return Node{}, err
	}
	var ref objectRef
	if err := obj.CallWithContext(ctx, componentIface+".GetAccessibleAtPoint", 0, int32(x), int32(y), uint32(0)).Store(&ref); err != nil {
		return Node{}, fmt.Errorf("accessibility: point query: %w", err)
	}
	if ref.null() {
		return Node{}, ErrNotFound
	}
	return b.readNode(ctx, b.refID(ref), NodeID{}, defaultMaxText, false)
}
