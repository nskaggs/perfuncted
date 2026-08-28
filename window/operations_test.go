package window

import "testing"

func TestSupportedOperationsIncludeSessionOperations(t *testing.T) {
	operations := (&X11Backend{}).SupportedOperations()
	for _, want := range []string{"info", "active-title"} {
		found := false
		for _, operation := range operations {
			if operation == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("SupportedOperations() = %v, missing %s", operations, want)
		}
	}
}
