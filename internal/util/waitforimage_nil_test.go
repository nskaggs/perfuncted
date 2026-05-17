package util_test

import (
	"testing"
	"time"

	"github.com/nskaggs/perfuncted/internal/util"
)

func TestWaitForImageRejectsTypedNilScreenshotter(t *testing.T) {
	t.Parallel()
	_, ref := waitForImageFixture()
	var sc *countedResolutionScreenshotter
	if _, err := util.WaitForImage(sc, ref, "exact", 200*time.Millisecond); err == nil {
		t.Fatal("WaitForImage succeeded unexpectedly")
	}
}
