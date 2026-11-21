package main

import (
	"context"
	"image"
	"image/color"
	"testing"
)

// The CLI's opaque predicate must not mistake a fully transparent frame for
// evidence of a visible window or an authorized nontransparent capture.
func TestOpaqueScreenPredicateRejectsTransparentFrames(t *testing.T) {
	predicate, err := screenPredicate(" opaque ")
	if err != nil {
		t.Fatal(err)
	}
	transparent := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	transparent.SetNRGBA(1, 1, color.NRGBA{R: 255, G: 90, B: 50, A: 0})
	if predicate(context.Background(), transparent) {
		t.Fatal("transparent pixels satisfy opaque predicate")
	}
	transparent.SetNRGBA(1, 1, color.NRGBA{R: 255, A: 1})
	if !predicate(context.Background(), transparent) {
		t.Fatal("nonzero alpha pixel not recognized")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if predicate(ctx, transparent) {
		t.Fatal("canceled predicate returned success")
	}
}
