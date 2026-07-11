package v1

import (
	"errors"
	"net/http"

	"github.com/alfariesh/surau-backend/internal/controller/restapi/apierror"
	"github.com/alfariesh/surau-backend/internal/controller/restapi/v1/response"
	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/gofiber/fiber/v2"
)

func (r *V1) editorialError(ctx *fiber.Ctx, err error) error {
	if existingProjectID := productionProjectExistsWithID(err); existingProjectID != "" {
		const msg = "production project already exists"

		return ctx.Status(http.StatusConflict).JSON(response.ProductionProjectConflict{
			Error:             msg,
			Code:              apierror.Code(msg),
			RequestID:         requestID(ctx),
			ExistingProjectID: existingProjectID,
		})
	}

	switch {
	case errors.Is(err, entity.ErrUnsupportedLanguage):
		return errorResponse(ctx, http.StatusBadRequest, "unsupported language")
	case errors.Is(err, entity.ErrInvalidAssetType):
		return errorResponse(ctx, http.StatusBadRequest, "invalid asset_type")
	case errors.Is(err, entity.ErrInvalidStatus):
		return errorResponse(ctx, http.StatusBadRequest, "invalid status")
	case errors.Is(err, entity.ErrInvalidLicenseStatus):
		return errorResponse(ctx, http.StatusBadRequest, "invalid license status")
	case errors.Is(err, entity.ErrInvalidLicenseReason), errors.Is(err, entity.ErrInvalidLicenseEvidenceURL):
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	case errors.Is(err, entity.ErrLicenseNotPermitted):
		return errorResponse(ctx, http.StatusConflict, "license not permitted")
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
	case errors.Is(err, entity.ErrInvalidQuranEditorial):
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	case errors.Is(err, entity.ErrInvalidAyahKey):
		return errorResponse(ctx, http.StatusBadRequest, "invalid ayah key")
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
	case errors.Is(err, entity.ErrQuranSurahNotFound):
		return errorResponse(ctx, http.StatusNotFound, "quran surah not found")
	case errors.Is(err, entity.ErrQuranAyahNotFound):
		return errorResponse(ctx, http.StatusNotFound, "quran ayah not found")
	case errors.Is(err, entity.ErrForbidden):
		return errorResponse(ctx, http.StatusForbidden, "forbidden")
	case errors.Is(err, entity.ErrPreconditionFailed):
		return errorResponse(ctx, http.StatusPreconditionFailed, "precondition failed")
	case errors.Is(err, entity.ErrPreconditionRequired):
		return errorResponse(ctx, http.StatusPreconditionRequired, "if-match header required")
	default:
		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}
}

func productionProjectExistsWithID(err error) string {
	var exists *entity.ProductionProjectExistsError
	if errors.As(err, &exists) {
		return exists.ExistingProjectID
	}

	return ""
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
