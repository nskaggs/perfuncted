package window

import "testing"

func TestKWinWindowListPreservesActiveStateForStableIDVerification(t *testing.T) {
	data := `[{"id":"{active-uuid}","title":"active","active":true,"x":0,"y":0,"width":600,"height":300},{"id":"{inactive-uuid}","title":"inactive","active":false,"x":200,"y":100,"width":500,"height":250}]`
	got, err := parseKWinWindowList(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !got[0].Active || got[1].Active {
		t.Fatalf("lost actual focus state: %+v", got)
	}
}
