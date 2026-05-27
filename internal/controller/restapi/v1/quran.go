package v1

import (
	"errors"
	"net/http"

	"github.com/evrone/go-clean-template/internal/controller/restapi/v1/response"
	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/gofiber/fiber/v2"
)

func (r *V1) listQuranSurahs(ctx *fiber.Ctx) error {
	surahs, err := r.quran.Surahs(ctx.UserContext(), ctx.Query("lang"))
	if err != nil {
		r.l.Error(err, "restapi - v1 - listQuranSurahs")

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	return ctx.Status(http.StatusOK).JSON(surahs)
}

func (r *V1) listQuranRecitations(ctx *fiber.Ctx) error {
	recitations, err := r.quran.Recitations(ctx.UserContext())
	if err != nil {
		r.l.Error(err, "restapi - v1 - listQuranRecitations")

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	return ctx.Status(http.StatusOK).JSON(recitations)
}

func (r *V1) getQuranAyah(ctx *fiber.Ctx) error {
	includeAudioValue, err := optionalQueryBool(ctx, "include_audio")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid include_audio")
	}
	includeAudio := includeAudioValue != nil && *includeAudioValue

	ayah, err := r.quran.Ayah(
		ctx.UserContext(),
		ctx.Params("ayah_key"),
		ctx.Query("lang"),
		ctx.Query("translation_source"),
		includeAudio,
		ctx.Query("recitation_id"),
	)
	if err != nil {
		r.l.Error(err, "restapi - v1 - getQuranAyah")

		return r.quranErrorResponse(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(ayah)
}

func (r *V1) listQuranSurahAyahs(ctx *fiber.Ctx) error {
	surahID, err := pathInt(ctx, "surah_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid surah_id")
	}

	fromAyah, err := optionalQueryInt(ctx, "from")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid from")
	}
	toAyah, err := optionalQueryInt(ctx, "to")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid to")
	}
	includeTranslationValue, err := optionalQueryBool(ctx, "include_translation")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid include_translation")
	}
	includeAudioValue, err := optionalQueryBool(ctx, "include_audio")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid include_audio")
	}

	from := 0
	if fromAyah != nil {
		from = *fromAyah
	}
	to := 0
	if toAyah != nil {
		to = *toAyah
	}
	includeTranslation := true
	if includeTranslationValue != nil {
		includeTranslation = *includeTranslationValue
	}
	includeAudio := includeAudioValue != nil && *includeAudioValue

	ayahs, err := r.quran.SurahAyahs(
		ctx.UserContext(),
		surahID,
		from,
		to,
		ctx.Query("lang"),
		ctx.Query("translation_source"),
		includeTranslation,
		includeAudio,
		ctx.Query("recitation_id"),
	)
	if err != nil {
		r.l.Error(err, "restapi - v1 - listQuranSurahAyahs")

		return r.quranErrorResponse(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(ayahs)
}

func (r *V1) searchQuran(ctx *fiber.Ctx) error {
	results, total, err := r.quran.Search(
		ctx.UserContext(),
		ctx.Query("q"),
		ctx.Query("lang"),
		queryInt(ctx, "limit", 50),
		queryInt(ctx, "offset", 0),
	)
	if err != nil {
		r.l.Error(err, "restapi - v1 - searchQuran")

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	return ctx.Status(http.StatusOK).JSON(response.QuranSearchList{Results: results, Total: total})
}

func (r *V1) listBookQuranReferences(ctx *fiber.Ctx) error {
	bookID, err := pathInt(ctx, "book_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid book_id")
	}

	references, total, err := r.quran.BookReferences(
		ctx.UserContext(),
		bookID,
		ctx.Query("lang"),
		ctx.Query("status", "approved"),
		queryInt(ctx, "limit", 50),
		queryInt(ctx, "offset", 0),
	)
	if err != nil {
		r.l.Error(err, "restapi - v1 - listBookQuranReferences")

		return r.quranErrorResponse(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(response.BookQuranReferenceList{References: references, Total: total})
}

func (r *V1) quranErrorResponse(ctx *fiber.Ctx, err error) error {
	if errors.Is(err, entity.ErrInvalidAyahKey) || errors.Is(err, entity.ErrInvalidQuranRange) {
		return errorResponse(ctx, http.StatusBadRequest, err.Error())
	}
	if errors.Is(err, entity.ErrQuranSurahNotFound) {
		return errorResponse(ctx, http.StatusNotFound, "quran surah not found")
	}
	if errors.Is(err, entity.ErrQuranAyahNotFound) {
		return errorResponse(ctx, http.StatusNotFound, "quran ayah not found")
	}
	if errors.Is(err, entity.ErrBookNotFound) {
		return errorResponse(ctx, http.StatusNotFound, "book not found")
	}

	return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
}
