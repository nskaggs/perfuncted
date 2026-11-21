//go:build linux

package screen

import (
	"context"
	"os"
	"strings"
	"testing"
)

// A compositor capture whose read descriptor was already closed cannot be
// reported as a successful, empty screenshot. Propagate the pipe deadline
// setup failure while retaining the underlying closed-descriptor identity.
func TestDrainKWinPipeRejectsClosedReadDescriptor(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	pixels, err := drainKWinPipe(context.Background(), reader)
	if len(pixels) != 0 || err == nil || !strings.Contains(err.Error(), "pipe deadline") || !strings.Contains(err.Error(), "closed file") {
		t.Fatalf("closed KWin capture pipe: pixels=%d err=%v", len(pixels), err)
	}
}
