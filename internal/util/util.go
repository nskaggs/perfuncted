package util

import (
	"image"
)

// MatchResult is a thin description of a matched template in an image.
type MatchResult struct {
	Match bool
	Score float64
	Rect  image.Rectangle
}
