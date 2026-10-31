package perfuncted

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/accessibility"
)

type ownerQueryBackend struct {
	*ownerLifecycleBackend
	findRoot                          accessibility.NodeID
	findQuery                         accessibility.Query
	findOpts                          accessibility.SnapshotOptions
	focusedOpts                       accessibility.SnapshotOptions
	pointX, pointY                    int
	findCalls, focusCalls, pointCalls int
	findNodes                         []accessibility.Node
	selected                          accessibility.Node
	fail                              error
	findEntered                       chan struct{}
	findRelease                       <-chan struct{}
	ignoreCancel                      bool
}

func (b *ownerQueryBackend) Find(ctx context.Context, root accessibility.NodeID, query accessibility.Query, opts accessibility.SnapshotOptions) ([]accessibility.Node, error) {
	if b.findEntered != nil {
		close(b.findEntered)
	}
	if b.findRelease != nil {
		<-b.findRelease
	}
	b.findCalls++
	b.findRoot = root
	b.findQuery = query
	b.findOpts = opts
	if !b.ignoreCancel {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	return b.findNodes, b.fail
}
func (b *ownerQueryBackend) Focused(ctx context.Context, opts accessibility.SnapshotOptions) (accessibility.Node, error) {
	b.focusCalls++
	b.focusedOpts = opts
	if err := ctx.Err(); err != nil {
		return accessibility.Node{}, err
	}
	return b.selected, b.fail
}
func (b *ownerQueryBackend) AtPoint(ctx context.Context, x, y int) (accessibility.Node, error) {
	b.pointCalls++
	b.pointX = x
	b.pointY = y
	if err := ctx.Err(); err != nil {
		return accessibility.Node{}, err
	}
	return b.selected, b.fail
}
func TestAccessibilityOwnerQueriesForwardExactInputsAndResults(t *testing.T) {
	id := accessibility.NodeID{BusName: "org.example.Editor", ObjectPath: "/root", Generation: 5}
	found := accessibility.Node{ID: accessibility.NodeID{BusName: id.BusName, ObjectPath: "/save", Generation: 5}, Name: "Save", Role: "button"}
	backend := &ownerQueryBackend{ownerLifecycleBackend: newOwnerLifecycleBackend(5, "find", "focused", "at-point"), findNodes: []accessibility.Node{found}, selected: found}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
	defer session.Close()
	owner := accessibilityOwnerForTest(t, session)
	query := accessibility.Query{Name: "Save", Role: "button"}
	opts := accessibility.SnapshotOptions{Fresh: true, MaxDepth: 2, MaxNodes: 5}
	nodes, err := owner.Find(context.Background(), id, query, opts)
	if err != nil || !reflect.DeepEqual(nodes, []accessibility.Node{found}) || backend.findCalls != 1 || backend.findRoot != id || !reflect.DeepEqual(backend.findQuery, query) || !reflect.DeepEqual(backend.findOpts, opts) {
		t.Fatalf("Find did not forward exact query: nodes=%+v err=%v backend=%+v", nodes, err, backend)
	}
	selected, err := owner.Focused(context.Background(), opts)
	if err != nil || !reflect.DeepEqual(selected, found) || backend.focusCalls != 1 || !reflect.DeepEqual(backend.focusedOpts, opts) {
		t.Fatalf("Focused result mismatch: %+v %v", selected, err)
	}
	hit, err := owner.AtPoint(context.Background(), -25, 900)
	if err != nil || !reflect.DeepEqual(hit, found) || backend.pointCalls != 1 || backend.pointX != -25 || backend.pointY != 900 {
		t.Fatalf("AtPoint result mismatch: %+v %v", hit, err)
	}
	operations := owner.SupportedOperations()
	if len(operations) != 3 || !reflect.DeepEqual(operations, []string{"find", "focused", "at-point"}) {
		t.Fatalf("owner operations incorrect: %v", operations)
	}
	operations[0] = "corrupt"
	if got := owner.SupportedOperations(); got[0] != "find" {
		t.Fatalf("caller mutated backend operations: %v", got)
	}
}
func TestAccessibilityOwnerQueriesPreserveErrorsAndRejectClosedOrCanceledSessions(t *testing.T) {
	id := accessibility.NodeID{BusName: "org.example.Editor", ObjectPath: "/root", Generation: 4}
	providerErr := errors.New("synthetic AT-SPI scope read failed")
	backend := &ownerQueryBackend{ownerLifecycleBackend: newOwnerLifecycleBackend(4), fail: providerErr}
	session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
	owner := accessibilityOwnerForTest(t, session)
	ctx := context.Background()
	if _, err := owner.Find(ctx, id, accessibility.Query{}, accessibility.SnapshotOptions{}); !errors.Is(err, providerErr) {
		t.Fatalf("Find provider failure=%v", err)
	}
	if _, err := owner.Focused(ctx, accessibility.SnapshotOptions{}); !errors.Is(err, providerErr) {
		t.Fatalf("Focused provider failure=%v", err)
	}
	if _, err := owner.AtPoint(ctx, 0, 0); !errors.Is(err, providerErr) {
		t.Fatalf("AtPoint provider failure=%v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, op := range []struct {
		name string
		call func(context.Context) error
	}{
		{"Find", func(ctx context.Context) error {
			_, err := owner.Find(ctx, id, accessibility.Query{}, accessibility.SnapshotOptions{})
			return err
		}},
		{"Focused", func(ctx context.Context) error {
			_, err := owner.Focused(ctx, accessibility.SnapshotOptions{})
			return err
		}},
		{"AtPoint", func(ctx context.Context) error { _, err := owner.AtPoint(ctx, 4, 5); return err }},
	} {
		t.Run(op.name, func(t *testing.T) {
			if err := op.call(canceled); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled query reported %v", err)
			}
		})
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	for _, op := range []struct {
		name string
		call func() error
	}{
		{"Find", func() error {
			_, err := owner.Find(ctx, id, accessibility.Query{}, accessibility.SnapshotOptions{})
			return err
		}},
		{"Focused", func() error { _, err := owner.Focused(ctx, accessibility.SnapshotOptions{}); return err }},
		{"AtPoint", func() error { _, err := owner.AtPoint(ctx, 4, 5); return err }},
	} {
		t.Run("closed_"+op.name, func(t *testing.T) {
			if err := op.call(); !errors.Is(err, accessibility.ErrDisconnected) {
				t.Fatalf("closed backend allowed query: %v", err)
			}
		})
	}
	if got := owner.SupportedOperations(); len(got) != 0 {
		t.Fatalf("closed owner exposed operations: %v", got)
	}
}

func TestAccessibilityOwnerRejectsLateSuccessAfterCallerCancellation(t *testing.T) {
	backend := &ownerQueryBackend{ownerLifecycleBackend: newOwnerLifecycleBackend(3), findEntered: make(chan struct{}), ignoreCancel: true}
	release := make(chan struct{})
	backend.findRelease = release
	session := NewSessionForTesting(nil, nil, nil, nil, nil, backend)
	defer session.Close()
	owner := accessibilityOwnerForTest(t, session)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := owner.Find(ctx, accessibility.NodeID{}, accessibility.Query{}, accessibility.SnapshotOptions{})
		done <- err
	}()
	<-backend.findEntered
	cancel()
	close(release)
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("owner reported late successful query after cancellation: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("blocked query never completed")
	}
}
