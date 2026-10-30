package main

import (
	"math"
	"testing"
)

func TestMaxRegressionBudgetRejectsNonFiniteAndNegativeLimits(t *testing.T) {
	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), -0.1} {
		if validMaxRegression(value) {
			t.Fatalf("accepted unsafe benchmark regression budget %v", value)
		}
	}
	for _, value := range []float64{0, 0.5, 2} {
		if !validMaxRegression(value) {
			t.Fatalf("rejected valid budget %v", value)
		}
	}
}
