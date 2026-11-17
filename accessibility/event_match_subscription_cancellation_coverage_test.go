package accessibility

import (
	"context"
	"errors"
	"testing"

	"github.com/nskaggs/perfuncted/internal/dbusutil"
)

func TestSubscribeEventMatchesHonorsCancellationBeforeRegistration(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	connection, err := dbusutil.SessionBusAddressContext(context.Background(), startTestDBus(t))
	if err != nil {
		t.Fatalf("connect private accessibility bus: %v", err)
	}
	defer connection.Close()

	matches, err := subscribeEventMatches(ctx, connection)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("subscribeEventMatches error = %v, want context cancellation", err)
	}
	if len(matches) != 0 {
		t.Fatalf("canceled subscription retained %d event matches", len(matches))
	}
}

func TestRegisterEventFamiliesReportsCancellationForEmptyCandidateSet(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	registered, err := registerEventFamilies(ctx, nil, "", nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("registerEventFamilies error = %v, want context cancellation", err)
	}
	if len(registered) != 0 {
		t.Fatalf("canceled empty registration returned event families %v", registered)
	}
}
