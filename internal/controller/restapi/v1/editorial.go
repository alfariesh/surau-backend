package v1

import (
	"errors"
	"net/http"

	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/gofiber/fiber/v2"
)

func (r *V1) editorialError(ctx *fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, entity.ErrUnsupportedLanguage):
		return errorResponse(ctx, http.StatusBadRequest, "unsupported language")
	case errors.Is(err, entity.ErrInvalidAssetType):
		return errorResponse(ctx, http.StatusBadRequest, "invalid asset_type")
	case errors.Is(err, entity.ErrInvalidStatus):
		return errorResponse(ctx, http.StatusBadRequest, "invalid status")
	case errors.Is(err, entity.ErrInvalidFeedback):
		return errorResponse(ctx, http.StatusBadRequest, "invalid feedback")
	case errors.Is(err, entity.ErrInvalidRole):
		return errorResponse(ctx, http.StatusBadRequest, "invalid role")
	case errors.Is(err, entity.ErrDraftNotFound):
		return errorResponse(ctx, http.StatusNotFound, "draft not found")
	case errors.Is(err, entity.ErrProductionProjectNotFound):
		return errorResponse(ctx, http.StatusNotFound, "production project not found")
	case errors.Is(err, entity.ErrProductionProjectExists):
		return errorResponse(ctx, http.StatusConflict, "production project already exists")
	case errors.Is(err, entity.ErrProductionNotReady):
		return errorResponse(ctx, http.StatusConflict, "production project is not ready")
	case errors.Is(err, entity.ErrInvalidReviewDecision):
		return errorResponse(ctx, http.StatusBadRequest, "invalid review decision")
	case errors.Is(err, entity.ErrInvalidProductionDraft):
		return errorResponse(ctx, http.StatusBadRequest, "invalid production draft")
	case errors.Is(err, entity.ErrFeedbackNotFound):
		return errorResponse(ctx, http.StatusNotFound, "feedback not found")
	case errors.Is(err, entity.ErrTranslationNotFound):
		return errorResponse(ctx, http.StatusNotFound, "translation not found")
	case errors.Is(err, entity.ErrBookNotFound):
		return errorResponse(ctx, http.StatusNotFound, "book not found")
	case errors.Is(err, entity.ErrPageNotFound):
		return errorResponse(ctx, http.StatusNotFound, "page not found")
	case errors.Is(err, entity.ErrHeadingNotFound):
		return errorResponse(ctx, http.StatusNotFound, "heading not found")
	case errors.Is(err, entity.ErrForbidden):
		return errorResponse(ctx, http.StatusForbidden, "forbidden")
	default:
		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}
}

func pagePath(ctx *fiber.Ctx) (int, int, error) {
	bookID, err := pathInt(ctx, "book_id")
	if err != nil {
		return 0, 0, errors.New("invalid book_id")
	}

	pageID, err := pathInt(ctx, "page_id")
	if err != nil {
		return 0, 0, errors.New("invalid page_id")
	}

	return bookID, pageID, nil
}

func headingPath(ctx *fiber.Ctx) (int, int, error) {
	bookID, err := pathInt(ctx, "book_id")
	if err != nil {
		return 0, 0, errors.New("invalid book_id")
	}

	headingID, err := pathInt(ctx, "heading_id")
	if err != nil {
		return 0, 0, errors.New("invalid heading_id")
	}

	return bookID, headingID, nil
}

func optionalQueryString(ctx *fiber.Ctx, key string) *string {
	value := ctx.Query(key)
	if value == "" {
		return nil
	}

	return &value
}
