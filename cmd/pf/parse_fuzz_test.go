package main

import "testing"

func FuzzParseRect(f *testing.F) {
	for _, seed := range []string{"0,0,10,10", " 1, 2, 3, 4 ", "", "bad", "1,2,3"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		_, _ = parseRect(s)
	})
}

func FuzzParseDuration(f *testing.F) {
	for _, seed := range []string{"", "1s", "250ms", "bad", "-1s"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		_, _ = parseDuration(s, 0)
	})
}

func FuzzParseHash(f *testing.F) {
	for _, seed := range []string{"0", "0x1234abcd", "1234abcd", "bad", ""} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		_, _ = parseHash(s)
	})
}
