package v1

import (
	"errors"

	"github.com/alfariesh/surau-backend/internal/controller/restapi/middleware"
	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/pkg/logger"
	"github.com/gofiber/fiber/v2"
)

// reqLog returns the request-scoped logger (request_id + trace_id stamped by
// middleware.TraceContext), falling back to the base logger outside a request.
func (r *V1) reqLog(ctx *fiber.Ctx) logger.Interface {
	return middleware.RequestLogger(ctx, r.l)
}

func (r *V1) logReaderError(ctx *fiber.Ctx, err error, operation string) {
	if isExpectedReaderError(err) {
		r.reqLog(ctx).Warn("%s: %s", operation, err)
		return
	}

	r.reqLog(ctx).Error(err, operation)
}

func (r *V1) logEditorialError(ctx *fiber.Ctx, err error, operation string) {
	if isExpectedEditorialError(err) {
		r.reqLog(ctx).Warn("%s: %s", operation, err)
		return
	}

	r.reqLog(ctx).Error(err, operation)
}

func (r *V1) logQuranError(ctx *fiber.Ctx, err error, operation string) {
	if isExpectedQuranError(err) {
		r.reqLog(ctx).Warn("%s: %s", operation, err)
		return
	}

	r.reqLog(ctx).Error(err, operation)
}

func (r *V1) logAnchorError(ctx *fiber.Ctx, err error, operation string) {
	if errors.Is(err, entity.ErrInvalidAnchor) ||
		errors.Is(err, entity.ErrAnchorNotFound) ||
		errors.Is(err, entity.ErrUnitNotFound) {
		r.reqLog(ctx).Warn("%s: %s", operation, err)

		return
	}

	r.reqLog(ctx).Error(err, operation)
}

func (r *V1) logCrossReferenceError(ctx *fiber.Ctx, err error, operation string) {
	if errors.Is(err, entity.ErrInvalidCrossReference) ||
		errors.Is(err, entity.ErrCrossReferenceNotFound) ||
		errors.Is(err, entity.ErrCrossReferenceConflict) ||
		errors.Is(err, entity.ErrAnchorNotFound) ||
		errors.Is(err, entity.ErrPreconditionFailed) {
		r.reqLog(ctx).Warn("%s: %s", operation, err)

		return
	}

	r.reqLog(ctx).Error(err, operation)
}

func isExpectedReaderError(err error) bool {
	return errors.Is(err, entity.ErrUnsupportedLanguage) ||
		errors.Is(err, entity.ErrBookNotFound) ||
		errors.Is(err, entity.ErrPageNotFound) ||
		errors.Is(err, entity.ErrHeadingNotFound) ||
		errors.Is(err, entity.ErrTranslationNotFound) ||
		errors.Is(err, entity.ErrInvalidFeedback) ||
		errors.Is(err, entity.ErrInvalidQuestion) ||
		errors.Is(err, entity.ErrRAGEvidenceNotFound)
}

func isExpectedEditorialError(err error) bool {
	return isExpectedReaderError(err) ||
		errors.Is(err, entity.ErrInvalidStatus) ||
		errors.Is(err, entity.ErrInvalidRole) ||
		errors.Is(err, entity.ErrInvalidAssetType) ||
		errors.Is(err, entity.ErrDraftNotFound) ||
		errors.Is(err, entity.ErrFeedbackNotFound) ||
		errors.Is(err, entity.ErrForbidden)
}

func isExpectedQuranError(err error) bool {
	return errors.Is(err, entity.ErrUnsupportedLanguage) ||
		errors.Is(err, entity.ErrQuranSurahNotFound) ||
		errors.Is(err, entity.ErrQuranAyahNotFound) ||
		errors.Is(err, entity.ErrQuranNavigationNotFound) ||
		errors.Is(err, entity.ErrQuranRecitationNotFound) ||
		errors.Is(err, entity.ErrQuranTranslationSourceNotFound) ||
		errors.Is(err, entity.ErrInvalidAyahKey) ||
		errors.Is(err, entity.ErrInvalidQuranRange) ||
		errors.Is(err, entity.ErrInvalidAssetType) ||
		errors.Is(err, entity.ErrBookNotFound)
}
