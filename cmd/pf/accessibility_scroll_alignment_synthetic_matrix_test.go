package main

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/nskaggs/perfuncted"
	"github.com/nskaggs/perfuncted/accessibility"
)

type verifyingScrollAlignment struct {
	cliAutomationFake
	calls []accessibility.ScrollType
}

//nolint:unparam // Synthetic fixture preserves the interface/argument contract.
func (b *verifyingScrollAlignment) ScrollTo(_ context.Context, _ accessibility.NodeID, align accessibility.ScrollType) error {
	b.calls = append(b.calls, align)
	return nil
}

// Every documented alignment must retain its exact AT-SPI numeric meaning
// through local CLI decoding, session dispatch and the backend interface.
func TestAccessibilityRawCLIAlignmentMatrixDispatchesExactEnum(t *testing.T) {
	cases := []struct {
		name string
		want accessibility.ScrollType
	}{
		{"top-left", accessibility.ScrollTopLeft},
		{"bottom-right", accessibility.ScrollBottomRight},
		{"top-edge", accessibility.ScrollTopEdge},
		{"bottom-edge", accessibility.ScrollBottomEdge},
		{"left-edge", accessibility.ScrollLeftEdge},
		{"right-edge", accessibility.ScrollRightEdge},
		{"anywhere", accessibility.ScrollAnyWhere},
		{"any-where", accessibility.ScrollAnyWhere},
		{" RIGHT-EDGE ", accessibility.ScrollRightEdge},
	}
	for _, tc := range cases {
		t.Run(strings.TrimSpace(tc.name), func(t *testing.T) {
			backend := &verifyingScrollAlignment{}
			opens := 0
			args := []string{"a11y", "raw", "--bus", "org.test.App", "--path", "/node", "--generation", "1", "--op", "scroll", "--alignment", tc.name}
			out, stderr, code := captureRunIO(t, args, func(*cliConfig) sessionOpener {
				return func(context.Context) (*perfuncted.Session, error) {
					opens++
					return perfuncted.NewSessionForTesting(nil, nil, nil, nil, nil, backend), nil
				}
			})
			if code != 0 || stderr != "" || strings.TrimSpace(out) != "ok" {
				t.Fatalf("raw scroll %s code=%d out=%q stderr=%q", tc.name, code, out, stderr)
			}
			if opens != 1 || len(backend.calls) != 1 || backend.calls[0] != tc.want {
				t.Fatalf("alignment %q calls=%v opens=%d want %s", tc.name, backend.calls, opens, fmt.Sprint(tc.want))
			}
		})
	}
}
