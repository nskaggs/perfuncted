package accessibility

import (
	"encoding/json"
	"math"
	"math/rand"
	"strings"
	"testing"
	"time"
)

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
