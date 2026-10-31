package window

import (
	"context"
	"math"
	"testing"
)

// Sway's move position command accepts signed 32-bit geometry. A value that
// cannot be represented by the compositor must not trigger the preceding
// "floating enable" mutation or even the discovery IPC request.
func TestSwayMoveRejectsUnrepresentableCoordinatesBeforeMutatingWindow(t *testing.T) {
	for _, tc := range []struct {
		name string
		x, y int
	}{
		{"huge positive x", math.MaxInt, 0},
		{"huge positive y", 0, math.MaxInt},
		{"huge negative x", math.MinInt, 0},
		{"huge negative y", 0, math.MinInt},
		{"just above signed x", int(math.MaxInt32) + 1, 0},
		{"just below signed y", 0, int(math.MinInt32) - 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, finish := newSyntheticSwayCommands(t, syntheticSwayWindowTree, `[{"success":true}]`)
			err := m.MoveByID(context.Background(), "42", tc.x, tc.y)
			commands, serverErr := finish()
			if err == nil || serverErr != nil || len(commands) != 0 {
				t.Fatalf("unsupported coordinate move (%d,%d) dispatched partial IPC %q: err=%v server=%v", tc.x, tc.y, commands, err, serverErr)
			}
		})
	}
}

func TestSwayMoveAllowsSignedCoordinateBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name string
		x, y int
	}{
		{"minimum signed x", int(math.MinInt32), -1},
		{"maximum signed y", 3, int(math.MaxInt32)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, finish := newSyntheticSwayCommands(t, syntheticSwayWindowTree, `[{"success":true}]`)
			err := m.MoveByID(context.Background(), "42", tc.x, tc.y)
			commands, serverErr := finish()
			if err != nil || serverErr != nil || len(commands) != 3 {
				t.Fatalf("valid signed coordinate move (%d,%d) rejected: err=%v server=%v commands=%q", tc.x, tc.y, err, serverErr, commands)
			}
		})
	}
}
