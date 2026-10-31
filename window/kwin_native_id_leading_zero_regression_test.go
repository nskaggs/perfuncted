package window

import "testing"

func TestKWinNumericNativeIDPreservesDecimalLeadingZero(t *testing.T) {
	list, err := parseKWinWindowList(`[{"id":"010","title":"decimal ten","width":10,"height":10},{"id":"0x10","title":"hex sixteen","width":10,"height":10}]`)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != 10 || list[1].ID != 16 {
		t.Fatalf("KWin numeric IDs changed value: %#v", list)
	}
}
