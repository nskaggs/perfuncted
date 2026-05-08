package output

import (
	"context"
	"fmt"

	"github.com/nskaggs/perfuncted/internal/x11"
)

type X11Lister struct {
	conn *x11.XgbConnection
}

func NewX11Lister(display string) (*X11Lister, error) {
	conn, err := x11.NewXgbConnection(display)
	if err != nil {
		return nil, fmt.Errorf("output/x11: connect to display %q: %w", display, err)
	}
	return &X11Lister{conn: conn}, nil
}

func (l *X11Lister) List(ctx context.Context) ([]Info, error) {
	screen := l.conn.DefaultScreen()
	if screen == nil {
		return nil, fmt.Errorf("output/x11: no default screen")
	}
	return []Info{
		{
			Name:        "x11-root",
			Backend:     "x11",
			Geometry:    Geometry{X: 0, Y: 0, W: int(screen.WidthInPixels), H: int(screen.HeightInPixels)},
			ResolutionW: int(screen.WidthInPixels),
			ResolutionH: int(screen.HeightInPixels),
			Scale:       1,
			Primary:     true,
		},
	}, nil
}

func (l *X11Lister) Close() error {
	if l.conn != nil {
		l.conn.Close()
	}
	return nil
}
