package perfuncted

import (
	"github.com/nskaggs/perfuncted/v2/internal/sessionscreen"
	"image"
)

func translatePointsToImage(points []image.Point, region, bounds image.Rectangle) ([]image.Point, error) {
	return sessionscreen.TranslatePointsToImage(points, region, bounds)
}
