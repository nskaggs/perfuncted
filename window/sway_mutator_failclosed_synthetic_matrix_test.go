package window

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestSwayMutatorsDoNotSendCommandsForUnresolvedWindowIdentities(t *testing.T) {
	for _, tc := range []struct {
		name   string
		invoke func(*SwayManager) error
	}{
		{"move", func(m *SwayManager) error { return m.MoveByID(context.Background(), "999", 20, 25) }},
		{"resize", func(m *SwayManager) error { return m.ResizeByID(context.Background(), "999", 480, 320) }},
		{"close", func(m *SwayManager) error { return m.CloseWindowByID(context.Background(), "999") }},
		{"minimize", func(m *SwayManager) error { return m.MinimizeByID(context.Background(), "999") }},
		{"fullscreen", func(m *SwayManager) error { return m.FullscreenByID(context.Background(), "999") }},
		{"unfullscreen", func(m *SwayManager) error { return m.UnfullscreenByID(context.Background(), "999") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, finish := newSyntheticSwayCommands(t, syntheticSwayWindowTree, `[{"success":true}]`)
			err := tc.invoke(m)
			calls, serverErr := finish()
			if !errors.Is(err, ErrWindowNotFound) || serverErr != nil || !reflect.DeepEqual(calls, []string{"GET_TREE"}) {
				t.Fatalf("unresolved target operation: err=%v cmds=%q server=%v", err, calls, serverErr)
			}
		})
	}
}

func TestSwayCompoundMutationsStopAfterRejectedFloatingStep(t *testing.T) {
	for _, tc := range []struct {
		name   string
		invoke func(*SwayManager) error
	}{
		{"move", func(m *SwayManager) error { return m.MoveByID(context.Background(), "42", 20, 25) }},
		{"resize", func(m *SwayManager) error { return m.ResizeByID(context.Background(), "42", 480, 320) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, finish := newSyntheticSwayCommands(t, syntheticSwayWindowTree, `[{"success":false,"error":"synthetic compositor rejected floating"}]`)
			err := tc.invoke(m)
			commands, serverErr := finish()
			expected := []string{"GET_TREE", "[con_id=42] floating enable"}
			if err == nil || serverErr != nil || !reflect.DeepEqual(commands, expected) {
				t.Fatalf("rejected first mutation continued operation: err=%v commands=%v server=%v", err, commands, serverErr)
			}
		})
	}
}
