package perfuncted

import (
	"context"
	"errors"
	"image"

	"github.com/nskaggs/perfuncted/find"
)

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

	operationCtx, timeoutCancel := s.backendContext(ctx, s.session.Timeouts().Medium)
	defer timeoutCancel()
	operationCtx, cancel := context.WithCancel(operationCtx)
	defer cancel()
	stopSessionCancel := context.AfterFunc(s.session.ctx, cancel) //nolint:contextcheck // Session closure revokes CPU-bound matching even when the caller context remains live.
	defer stopSessionCancel()

	match, err := find.MatchTemplate(operationCtx, s.backend, rect, reference, minConfidence)
	if ctxErr := operationCtx.Err(); ctxErr != nil {
		return find.TemplateMatch{}, errors.Join(ctxErr, s.operationError("find-template", err))
	}
	if openErr := s.session.ensureOpen(); openErr != nil {
		return find.TemplateMatch{}, errors.Join(openErr, s.operationError("find-template", err))
	}
	if err != nil {
		return find.TemplateMatch{}, s.operationError("find-template", err)
	}
	return match, nil
}
