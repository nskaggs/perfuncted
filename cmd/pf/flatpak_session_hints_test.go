package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestFlatpakSessionHintsDescribeSafeFirstUse(t *testing.T) {
	var buf bytes.Buffer
	if err := writeFlatpakSessionHints(&buf); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Flatpak", "grab-region --out -", "pf run -", "--host-dir-ro", "--input-backend remote-desktop"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("sandbox hint does not explain %q: %s", want, buf.String())
		}
	}
}
