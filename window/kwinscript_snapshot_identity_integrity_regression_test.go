package window

import "testing"

func TestKWinWindowListRejectsContradictoryCompositorIdentity(t *testing.T) {
	for _, tc := range []struct{ name, payload string }{
		{"two active windows", `[{"id":"one","active":true},{"id":"two","active":true}]`},
		{"negative process id", `[{"id":"one","pid":-1}]`},
		{"negative second process id", `[{"id":"one","pid":0},{"id":"two","pid":-54}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseKWinWindowList(tc.payload)
			if err == nil {
				t.Fatalf("accepted contradictory compositor window snapshot: %+v", got)
			}
		})
	}
}
func TestKWinWindowListAllowsNoActiveWindowAndUnknownPID(t *testing.T) {
	got, err := parseKWinWindowList(`[{"id":"one","pid":0},{"id":"two","pid":17}]`)
	if err != nil || len(got) != 2 || got[0].Active || got[1].Active {
		t.Fatalf("rejected valid unfocused snapshot: %+v err=%v", got, err)
	}
}
