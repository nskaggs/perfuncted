package main

import (
	"io"
	"strings"
	"testing"
)

func TestGateRejectsMissingSkippedFailedAndMalformedEvidence(t *testing.T) {
	const pass = "{\"Action\":\"pass\",\"Test\":\"TestRequired\"}\n"
	for _, tc := range []struct {
		name, events string
		wantError    bool
	}{
		{"pass", pass, false},
		{"missing", "{\"Action\":\"pass\",\"Test\":\"TestUnrelated\"}\n", true},
		{"skipped", "{\"Action\":\"skip\",\"Test\":\"TestRequired\"}\n", true},
		{"skipped subtest", pass + "{\"Action\":\"skip\",\"Test\":\"TestRequired/subtest\"}\n", true},
		{"failed", pass + "{\"Action\":\"fail\",\"Test\":\"TestRequired/subtest\"}\n", true},
		{"malformed", pass + "not JSON\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := verifyEvents(strings.NewReader(tc.events), io.Discard, map[string]bool{"TestRequired": false})
			if (err != nil) != tc.wantError {
				t.Fatalf("verification error=%v wantError=%t", err, tc.wantError)
			}
		})
	}
}
func TestRequiredContractsDeduplicatesAndIgnoresLiveLanes(t *testing.T) {
	required, err := requiredContracts("a\tcontract:TestA/TestB\nb\tcontract:TestA\nc\tmatrix:TestLive")
	if err != nil || len(required) != 2 {
		t.Fatalf("required=%v err=%v", required, err)
	}
	if _, err := requiredContracts("a\tmatrix:TestLive"); err == nil {
		t.Fatal("empty contract lane accepted")
	}
}
