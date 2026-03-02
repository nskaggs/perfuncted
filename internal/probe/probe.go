// Package probe provides a shared type for backend availability reports.
package probe

// Result describes a single backend candidate: whether it can be opened and
// whether Open() would choose it over higher-priority alternatives.
type Result struct {
	Name      string // short human-readable backend name
	Available bool   // true if the backend is usable on this system
	Selected  bool   // true if this is the backend Open() would return
	Reason    string // one-line explanation suitable for display
}

// SelectBest marks the first available result as selected and returns the
// combined list. This implements the "first available wins" priority logic
// used by screen.Probe(), input.Probe(), and window.Probe().
func SelectBest(results []Result) []Result {
	selected := false
	for i := range results {
		if results[i].Available && !selected {
			results[i].Selected = true
			selected = true
		}
	}
	return results
}
