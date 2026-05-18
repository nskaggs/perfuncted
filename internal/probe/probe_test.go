package probe

import "testing"

func TestSelectBestMarksFirstAvailable(t *testing.T) {
	t.Parallel()

	got := SelectBest([]Result{
		{Name: "first", Available: false},
		{Name: "second", Available: true},
		{Name: "third", Available: true},
	})

	if got[0].Selected {
		t.Fatalf("first selected = true, want false")
	}
	if !got[1].Selected {
		t.Fatalf("second selected = false, want true")
	}
	if got[2].Selected {
		t.Fatalf("third selected = true, want false")
	}
}

func TestSelectBestLeavesUnavailableUnselected(t *testing.T) {
	t.Parallel()

	got := SelectBest([]Result{
		{Name: "first"},
		{Name: "second"},
	})

	for _, result := range got {
		if result.Selected {
			t.Fatalf("%s selected = true, want false", result.Name)
		}
	}
}

func TestSelectBestPreservesExistingSelectedFlags(t *testing.T) {
	t.Parallel()

	got := SelectBest([]Result{
		{Name: "forced", Selected: true},
		{Name: "available", Available: true},
	})

	if !got[0].Selected {
		t.Fatalf("existing selected flag was cleared")
	}
	if !got[1].Selected {
		t.Fatalf("first available result was not selected")
	}
}
