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
