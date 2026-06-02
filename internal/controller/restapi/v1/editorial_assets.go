package v1

import (
	"net/http"

	_ "github.com/evrone/go-clean-template/internal/controller/restapi/v1/response" // for swaggo
	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/gofiber/fiber/v2"
)

// @Summary     List missing reader assets
// @Description Editorial queue of missing kitab metadata, section translations, summaries, and audio for target languages id/en.
// @ID          editorial-list-missing-reader-assets
// @Tags        editorial
// @Produce     json
// @Param       target_lang query    string false "Target language: id or en; empty returns both" Enums(id,en)
// @Param       asset_type  query    string false "Asset type filter" Enums(book_metadata,category_metadata,author_metadata,section_translation,heading_summary,section_audio)
// @Param       book_id     query    int    false "Book ID"
// @Param       limit       query    int    false "Page size" default(50)
// @Param       offset      query    int    false "Page offset" default(0)
// @Success     200         {object} entity.EditorialMissingReaderAssets
// @Failure     400         {object} response.Error
// @Failure     401         {object} response.Error
// @Failure     403         {object} response.Error
// @Failure     500         {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/reader/missing-assets [get]
func (r *V1) editorialMissingReaderAssets(ctx *fiber.Ctx) error {
	bookID, err := optionalQueryInt(ctx, "book_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid book_id")
	}

	assets, err := r.editorial.MissingReaderAssets(
		ctx.UserContext(),
		ctx.Query("target_lang"),
		ctx.Query("asset_type"),
		bookID,
		queryInt(ctx, "limit", 50),
		queryInt(ctx, "offset", 0),
	)
	if err != nil {
		r.logEditorialError(err, "restapi - v1 - editorialMissingReaderAssets")

		return r.editorialError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(assets)
}

// @Summary     List missing Quran assets
// @Description Editorial queue of missing Quran surah info, ayah translations, translation sources, and app-owned public audio URLs. Source audio_url may still be playable.
// @ID          editorial-list-missing-quran-assets
// @Tags        editorial
// @Produce     json
// @Param       target_lang query    string false "Target language: id or en; empty returns both" Enums(id,en)
// @Param       asset_type  query    string false "Asset type filter" Enums(surah_info,ayah_translation,translation_source,audio_public)
// @Param       surah_id    query    int    false "Surah ID"
// @Param       limit       query    int    false "Page size" default(50)
// @Param       offset      query    int    false "Page offset" default(0)
// @Success     200         {object} entity.EditorialMissingQuranAssets
// @Failure     400         {object} response.Error
// @Failure     401         {object} response.Error
// @Failure     403         {object} response.Error
// @Failure     500         {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/quran/missing-assets [get]
func (r *V1) editorialMissingQuranAssets(ctx *fiber.Ctx) error {
	surahID, err := optionalQueryInt(ctx, "surah_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid surah_id")
	}

	assets, err := r.quran.MissingAssets(
		ctx.UserContext(),
		ctx.Query("target_lang"),
		ctx.Query("asset_type"),
		surahID,
		queryInt(ctx, "limit", 50),
		queryInt(ctx, "offset", 0),
	)
	if err != nil {
		r.logQuranError(err, "restapi - v1 - editorialMissingQuranAssets")

		return r.quranErrorResponse(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(assets)
}

var (
	_ = entity.EditorialMissingReaderAssets{}
	_ = entity.EditorialMissingQuranAssets{}
)
