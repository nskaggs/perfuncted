package perfuncted

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/accessibility"
)

// The session can be retired independently of the caller's context. A
// provider that ignores cancellation must not publish a sensitive result to
// an operation whose session has already ended.
func TestAccessibilityOwnerSessionCancellationWithholdsLatePrivateResults(t *testing.T) {
	ctx, cancelSession := context.WithCancel(context.Background())
	release := make(chan struct{})
	backend := &ownerQueryBackend{
		ownerLifecycleBackend: newOwnerLifecycleBackend(9, "find"),
		findNodes:             []accessibility.Node{{Name: "private document", ID: accessibility.NodeID{BusName: "org.example.Editor", ObjectPath: "/private"}}},
		findEntered:           make(chan struct{}),
		findRelease:           release,
		ignoreCancel:          true,
	}
	session := &Session{ctx: ctx}
	owner := newAccessibilityBackendOwner(session)
	owner.install(backend)
	defer owner.Close()
	type outcome struct {
		nodes []accessibility.Node
		err   error
	}
	finished := make(chan outcome, 1)
	go func() {
		nodes, err := owner.Find(context.Background(), accessibility.NodeID{}, accessibility.Query{}, accessibility.SnapshotOptions{})
		finished <- outcome{nodes, err}
	}()
	<-backend.findEntered
	cancelSession()
	close(release)
	got := <-finished
	if got.nodes != nil || !errors.Is(got.err, context.Canceled) {
		t.Fatalf("retired session returned sensitive nodes=%+v err=%v", got.nodes, got.err)
	}
}
