package perfuncted

import (
	"context"
	"errors"
	"fmt"
	"image"

	"github.com/nskaggs/perfuncted/find"
)

// FindText recognizes rendered text in one authorized, attested screen region.
// OCR is opt-in: callers must provide a local recognizer explicitly. An empty
// Matches result with nil error means recognition succeeded but the query did
// not occur. OCR confidence is provider-specific and is not a probability.
func (s *ScreenBundle) FindText(ctx context.Context, rect image.Rectangle, query string, options find.TextOptions) (find.TextResult, error) {
	if err := find.ValidateTextOptions(query, options); err != nil {
		return find.TextResult{}, err
	}
	if err := s.checkAvailable("capture"); err != nil {
		return find.TextResult{}, err
	}
	verified, ok := s.backend.(find.VerifiedTemplateScreenshotter)
	if !ok {
		return find.TextResult{}, fmt.Errorf("screen: OCR requires backend-verified capture geometry: %w", find.ErrOCRGeometry)
	}
	s.traceAction("find-text rect=%s", rect)
	operationCtx, timeoutCancel := s.backendContext(ctx, s.session.Timeouts().Medium)
	defer timeoutCancel()
	operationCtx, cancel := context.WithCancel(operationCtx)
	defer cancel()
	stopSessionCancel := context.AfterFunc(s.session.ctx, cancel) //nolint:contextcheck // Closing the Session revokes an in-flight OCR process or post-capture recognition.
	defer stopSessionCancel()
	result, err := find.RecognizeText(operationCtx, verified, rect, query, options)
	if ctxErr := operationCtx.Err(); ctxErr != nil {
		return find.TextResult{}, errors.Join(ctxErr, s.operationError("find-text", err))
	}
	if openErr := s.session.ensureOpen(); openErr != nil {
		return find.TextResult{}, errors.Join(openErr, s.operationError("find-text", err))
	}
	if err != nil {
		return find.TextResult{}, s.operationError("find-text", err)
	}
	return result, nil
}
