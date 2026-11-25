package workflow

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Run executes script lines in order using an existing lifecycle and the caller's buffered reader.
func Run(ctx context.Context, reader *bufio.Reader, lifecycle *Lifecycle, dispatch func(int, []string) error) error {
	step := 0
	for lineNo := 1; ; lineNo++ {
		lineBytes, readErr := reader.ReadSlice('\n')
		if errors.Is(readErr, io.EOF) && len(lineBytes) == 0 {
			return nil
		}
		if errors.Is(readErr, bufio.ErrBufferFull) {
			return fmt.Errorf("script line %d: token too long", lineNo)
		}
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return readErr
		}
		if ctx != nil {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if err := runLine(lifecycle, dispatch, lineNo, string(lineBytes), &step); err != nil {
			return err
		}
	}
}

func runLine(lifecycle *Lifecycle, dispatch func(int, []string) error, lineNo int, raw string, step *int) error {
	line := strings.TrimSpace(raw)
	if line == "" || strings.HasPrefix(line, "#") {
		return nil
	}
	toks, err := SplitShell(line)
	if err != nil {
		return fmt.Errorf("script line %d: %w", lineNo, err)
	}
	if len(toks) == 0 {
		return nil
	}
	*step++
	lifecycle.BeginStep(*step, "waiting")
	if err := lifecycle.Checkpoint(); err != nil {
		return err
	}
	lifecycle.BeginStep(*step, "running")
	if err := dispatch(lineNo, toks); err != nil {
		lifecycle.BeginStep(*step, "failed")
		return err
	}
	lifecycle.CompleteStep(*step)
	return nil
}
