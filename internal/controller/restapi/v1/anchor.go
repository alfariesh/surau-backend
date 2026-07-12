package v1

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	_ "github.com/alfariesh/surau-backend/internal/controller/restapi/v1/response" // for swaggo
	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/gofiber/fiber/v2"
)

// @Summary     Resolve an Anchor
// @Description Resolve one canonical Anchor, a legacy ayah_key/toc anchor, or a legacy kitab page tuple to current active targets. Known tombstones return 200; ranges resolve their two boundaries without expanding all content.
// @ID          resolve-anchor
// @Tags        anchors
// @Produce     json
// @Param       anchor  query    string false "Canonical Anchor, legacy ayah_key, or toc-{heading_id}"
// @Param       book_id query    int    false "Required scope for legacy toc/page; forbidden for canonical and ayah_key inputs"
// @Param       page_id query    int    false "Legacy physical page locator; requires book_id and no anchor"
// @Param       surah_id query int false "Legacy Quran surah/range locator"
// @Param       from_ayah_number query int false "Inclusive range start; requires surah_id and to_ayah_number"
// @Param       to_ayah_number query int false "Inclusive range end; requires surah_id and from_ayah_number"
// @Param       juz_number query int false "Legacy Quran juz locator"
// @Param       hizb_number query int false "Legacy Quran hizb locator"
// @Param       page_number query int false "Legacy Quran mushaf-page locator"
// @Success     200     {object} entity.AnchorResolution
// @Failure     400     {object} response.Error
// @Failure     404     {object} response.Error
// @Failure     500     {object} response.Error
// @Router      /anchors/resolve [get]
func (r *V1) resolveAnchor(ctx *fiber.Ctx) error {
	if hasInvalidAnchorQueryShape(ctx) {
		return errorResponse(ctx, http.StatusBadRequest, "invalid anchor")
	}

	rawAnchor := ctx.Query("anchor")
	if ctx.Context().QueryArgs().Has("anchor") && rawAnchor == "" {
		return errorResponse(ctx, http.StatusBadRequest, "invalid anchor")
	}

	bookID, err := strictAnchorQueryInt(ctx, "book_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid anchor")
	}

	pageID, err := strictAnchorQueryInt(ctx, "page_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid anchor")
	}

	input := entity.AnchorResolveInput{Anchor: rawAnchor, BookID: bookID, PageID: pageID}
	for key, destination := range map[string]**int{
		"surah_id":         &input.SurahID,
		"from_ayah_number": &input.FromAyahNumber,
		"to_ayah_number":   &input.ToAyahNumber,
		"juz_number":       &input.JuzNumber,
		"hizb_number":      &input.HizbNumber,
		"page_number":      &input.PageNumber,
	} {
		value, parseErr := strictAnchorQueryInt(ctx, key)
		if parseErr != nil {
			return errorResponse(ctx, http.StatusBadRequest, "invalid anchor")
		}

		*destination = value
	}

	var resolved entity.AnchorResolution
	if typed, ok := r.anchor.(interface {
		ResolveInput(context.Context, entity.AnchorResolveInput) (entity.AnchorResolution, error)
	}); ok {
		resolved, err = typed.ResolveInput(ctx.UserContext(), input)
	} else {
		resolved, err = r.anchor.Resolve(ctx.UserContext(), rawAnchor, bookID, pageID)
	}
	if err != nil {
		r.logAnchorError(ctx, err, "restapi - v1 - resolveAnchor")

		switch {
		case errors.Is(err, entity.ErrInvalidAnchor):
			return errorResponse(ctx, http.StatusBadRequest, "invalid anchor")
		case errors.Is(err, entity.ErrAnchorNotFound), errors.Is(err, entity.ErrUnitNotFound):
			return errorResponse(ctx, http.StatusNotFound, "anchor not found")
		default:
			return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
		}
	}

	return ctx.Status(http.StatusOK).JSON(resolved)
}

func strictAnchorQueryInt(ctx *fiber.Ctx, key string) (*int, error) {
	args := ctx.Context().QueryArgs()
	if !args.Has(key) {
		return nil, nil
	}

	raw := ctx.Query(key)
	if raw == "" || (len(raw) > 1 && raw[0] == '0') {
		return nil, entity.ErrInvalidAnchor
	}

	for index := range len(raw) {
		if raw[index] < '0' || raw[index] > '9' {
			return nil, entity.ErrInvalidAnchor
		}
	}

	parsed, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || parsed <= 0 {
		return nil, entity.ErrInvalidAnchor
	}

	value := int(parsed)

	return &value, nil
}

func hasInvalidAnchorQueryShape(ctx *fiber.Ctx) bool {
	counts := map[string]int{}
	invalid := false

	for key := range ctx.Context().QueryArgs().All() {
		name := string(key)
		switch name {
		case "anchor", "book_id", "page_id", "surah_id", "from_ayah_number",
			"to_ayah_number", "juz_number", "hizb_number", "page_number":
			counts[name]++
			invalid = invalid || counts[name] > 1
		default:
			invalid = true
		}
	}

	return invalid
}
