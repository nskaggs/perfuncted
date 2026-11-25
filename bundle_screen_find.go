package perfuncted

import (
	"context"
	"errors"
	"fmt"
	"image"

	"github.com/nskaggs/perfuncted/find"
)

// findScreenValue shares the session-bound lifetime and result authority checks
// of screen recognition operations. Callers retain their validation, capability
// admission, backend selection, and tracing order.
func findScreenValue[T any](ctx context.Context, s *ScreenBundle, operation string, recognize func(context.Context) (T, error)) (T, error) {
	var zero T
	operationCtx, timeoutCancel := s.backendContext(ctx, s.session.Timeouts().Medium)
	defer timeoutCancel()
	operationCtx, cancel := context.WithCancel(operationCtx)
	defer cancel()
	stopSessionCancel := context.AfterFunc(s.session.ctx, cancel) //nolint:contextcheck // Session closure independently revokes recognition.
	defer stopSessionCancel()
	result, err := recognize(operationCtx)
	if ctxErr := operationCtx.Err(); ctxErr != nil {
		return zero, errors.Join(ctxErr, s.operationError(operation, err))
	}
	if openErr := s.session.ensureOpen(); openErr != nil {
		return zero, errors.Join(openErr, s.operationError(operation, err))
	}
	if err != nil {
		return zero, s.operationError(operation, err)
	}
	return result, nil
}

// FindTemplate searches rect for the best match of reference whose similarity
// score is at least minConfidence. The returned rectangle is in desktop
// coordinates, including when the backend returns a uniformly upscaled image.
// It requires a backend that can attest complete capture coverage; unsupported
// or clipped live captures return find.ErrUnverifiedCaptureGeometry.
//
// minConfidence uses the normalized RGB mean-absolute-difference score defined
// by find.MatchTemplate. It is a similarity measure, not a probability.
// For exact pixel matching, use find.LocateExact.
func (s *ScreenBundle) FindTemplate(
	ctx context.Context,
	rect image.Rectangle,
	reference image.Image,
	minConfidence float64,
) (find.TemplateMatch, error) {
	if err := s.checkAvailable("capture"); err != nil {
		return find.TemplateMatch{}, err
	}
	s.traceAction("find-template rect=%s min-confidence=%.4f", rect, minConfidence)
	return findScreenValue(ctx, s, "find-template", func(operationCtx context.Context) (find.TemplateMatch, error) {
		return find.MatchTemplate(operationCtx, s.backend, rect, reference, minConfidence)
	})
}

// FindTemplates returns up to maxResults non-overlapping matches in descending
// similarity order. It uses one verified capture and the same cancellation and
// Session authority checks as FindTemplate. An empty slice means no matches.
func (s *ScreenBundle) FindTemplates(ctx context.Context, rect image.Rectangle, reference image.Image, minConfidence float64, maxResults int) ([]find.TemplateMatch, error) {
	if err := s.checkAvailable("capture"); err != nil {
		return nil, err
	}
	s.traceAction("find-templates rect=%s min-confidence=%.4f max=%d", rect, minConfidence, maxResults)
	return findScreenValue(ctx, s, "find-templates", func(operationCtx context.Context) ([]find.TemplateMatch, error) {
		return find.MatchTemplates(operationCtx, s.backend, rect, reference, minConfidence, maxResults)
	})
}

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
	return findScreenValue(ctx, s, "find-text", func(operationCtx context.Context) (find.TextResult, error) {
		return find.RecognizeText(operationCtx, verified, rect, query, options)
	})
}
