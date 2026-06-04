package v1

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/evrone/go-clean-template/internal/controller/restapi/v1/response"
	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/gofiber/fiber/v2"
)

const (
	quranAyahViewFull          = "full"
	quranAyahViewReaderMinimal = "reader_minimal"
)

// @Summary     List Quran surahs
// @Description List Quran surahs in mushaf order. Surah info is omitted by default; pass include_info=true for language-specific info HTML.
// @ID          list-quran-surahs
// @Tags        quran
// @Produce     json
// @Param       lang         query    string false "Language code" default(id)
// @Param       include_info query    bool   false "Include surah info HTML" default(false)
// @Success     200          {array}  entity.QuranSurah
// @Failure     400          {object} response.Error
// @Failure     500          {object} response.Error
// @Router      /quran/surahs [get]
func (r *V1) listQuranSurahs(ctx *fiber.Ctx) error {
	includeInfoValue, err := optionalQueryBool(ctx, "include_info")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid include_info")
	}
	includeInfo := includeInfoValue != nil && *includeInfoValue

	surahs, err := r.quran.Surahs(ctx.UserContext(), ctx.Query("lang"), includeInfo)
	if err != nil {
		r.logQuranError(err, "restapi - v1 - listQuranSurahs")

		return r.quranErrorResponse(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(surahs)
}

// @Summary     Get Quran surah
// @Description Get one Quran surah with language-specific info.
// @ID          get-quran-surah
// @Tags        quran
// @Produce     json
// @Param       surah_id path     int    true  "Surah ID" minimum(1) maximum(114)
// @Param       lang     query    string false "Language code" default(id)
// @Success     200      {object} entity.QuranSurah
// @Failure     400      {object} response.Error
// @Failure     404      {object} response.Error
// @Failure     500      {object} response.Error
// @Router      /quran/surahs/{surah_id} [get]
func (r *V1) getQuranSurah(ctx *fiber.Ctx) error {
	surahID, err := pathInt(ctx, "surah_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid surah_id")
	}

	surah, err := r.quran.Surah(ctx.UserContext(), surahID, ctx.Query("lang"))
	if err != nil {
		r.logQuranError(err, "restapi - v1 - getQuranSurah")

		return r.quranErrorResponse(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(surah)
}

// @Summary     List Quran recitations
// @Description List imported recitation resources and audio coverage. Exactly one fully playable recitation may be marked is_default. A track is playable when public_url or source audio_url exists.
// @ID          list-quran-recitations
// @Tags        quran
// @Produce     json
// @Success     200 {array}  entity.QuranRecitation
// @Failure     500 {object} response.Error
// @Router      /quran/recitations [get]
func (r *V1) listQuranRecitations(ctx *fiber.Ctx) error {
	recitations, err := r.quran.Recitations(ctx.UserContext())
	if err != nil {
		r.l.Error(err, "restapi - v1 - listQuranRecitations")

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	return ctx.Status(http.StatusOK).JSON(recitations)
}

// @Summary     Get Quran surah audio manifest
// @Description Get a compact playable audio manifest for one surah. If recitation_id is omitted, the backend uses the default playable visible recitation.
// @ID          get-quran-surah-audio
// @Tags        quran
// @Produce     json
// @Param       surah_id      path     int    true  "Surah ID" minimum(1) maximum(114)
// @Param       recitation_id query    string false "Visible recitation ID. Empty uses the default recitation."
// @Success     200           {object} response.QuranSurahAudioManifest
// @Failure     400           {object} response.Error
// @Failure     404           {object} response.Error
// @Failure     500           {object} response.Error
// @Router      /quran/surahs/{surah_id}/audio [get]
func (r *V1) getQuranSurahAudio(ctx *fiber.Ctx) error {
	surahID, err := pathInt(ctx, "surah_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid surah_id")
	}

	manifest, err := r.quran.SurahAudio(ctx.UserContext(), surahID, ctx.Query("recitation_id"))
	if err != nil {
		r.logQuranError(err, "restapi - v1 - getQuranSurahAudio")

		return r.quranErrorResponse(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(response.QuranSurahAudioManifestFromEntity(manifest))
}

// @Summary     List Quran translation sources
// @Description List imported Quran translation sources for the requested language, with coverage and default marker.
// @ID          list-quran-translation-sources
// @Tags        quran
// @Produce     json
// @Param       lang query string false "Language code: ar, id, or en" default(id)
// @Success     200  {array} entity.QuranTranslationSource
// @Failure     400  {object} response.Error
// @Failure     500  {object} response.Error
// @Router      /quran/translation-sources [get]
func (r *V1) listQuranTranslationSources(ctx *fiber.Ctx) error {
	sources, err := r.quran.TranslationSources(ctx.UserContext(), ctx.Query("lang"))
	if err != nil {
		r.logQuranError(err, "restapi - v1 - listQuranTranslationSources")

		return r.quranErrorResponse(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(sources)
}

// @Summary     List Quran juz
// @Description List imported Quran juz segments with lightweight start/end boundaries.
// @ID          list-quran-juz
// @Tags        quran
// @Produce     json
// @Param       lang query string false "Language code" default(id)
// @Success     200  {array} entity.QuranNavigationSegment
// @Failure     400  {object} response.Error
// @Failure     500  {object} response.Error
// @Router      /quran/juz [get]
func (r *V1) listQuranJuz(ctx *fiber.Ctx) error {
	segments, err := r.quran.Juz(ctx.UserContext(), ctx.Query("lang"))
	if err != nil {
		r.logQuranError(err, "restapi - v1 - listQuranJuz")

		return r.quranErrorResponse(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(segments)
}

// @Summary     List Quran ayahs in a juz
// @Description List all ayahs in one imported juz segment. When include_audio=true and recitation_id is omitted, the backend uses the default playable recitation.
// @ID          list-quran-juz-ayahs
// @Tags        quran
// @Produce     json
// @Param       juz_number          path     int    true  "Juz number" minimum(1) maximum(30)
// @Param       lang                query    string false "Language code" default(id)
// @Param       translation_source  query    string false "Translation source ID. Empty uses language default."
// @Param       include_translation query    bool   false "Include selected translation" default(true)
// @Param       include_audio       query    bool   false "Include audio track and timestamp segments" default(false)
// @Param       recitation_id       query    string false "Recitation ID. Defaults to the playable default recitation when include_audio=true."
// @Param       view                query    string false "Response view. Empty/full returns the existing QuranAyah shape; reader_minimal returns v1.QuranReaderAyah." Enums(full,reader_minimal)
// @Success     200                 {array}  response.QuranReaderAyah
// @Success     200                 {array}  entity.QuranAyah
// @Failure     400                 {object} response.Error
// @Failure     404                 {object} response.Error
// @Failure     500                 {object} response.Error
// @Router      /quran/juz/{juz_number}/ayahs [get]
func (r *V1) listQuranJuzAyahs(ctx *fiber.Ctx) error {
	juzNumber, err := pathInt(ctx, "juz_number")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid juz_number")
	}

	ayahs, view, ok, err := r.quranNavigationAyahs(ctx, juzNumber, "restapi - v1 - listQuranJuzAyahs", r.quran.JuzAyahs)
	if err != nil || !ok {
		return err
	}

	return quranAyahListResponse(ctx, ayahs, view)
}

// @Summary     List Quran hizbs
// @Description List imported Quran hizb segments with lightweight start/end boundaries.
// @ID          list-quran-hizbs
// @Tags        quran
// @Produce     json
// @Param       lang query string false "Language code" default(id)
// @Success     200  {array} entity.QuranNavigationSegment
// @Failure     400  {object} response.Error
// @Failure     500  {object} response.Error
// @Router      /quran/hizbs [get]
func (r *V1) listQuranHizbs(ctx *fiber.Ctx) error {
	segments, err := r.quran.Hizbs(ctx.UserContext(), ctx.Query("lang"))
	if err != nil {
		r.logQuranError(err, "restapi - v1 - listQuranHizbs")

		return r.quranErrorResponse(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(segments)
}

// @Summary     List Quran ayahs in a hizb
// @Description List all ayahs in one imported hizb segment. When include_audio=true and recitation_id is omitted, the backend uses the default playable recitation.
// @ID          list-quran-hizb-ayahs
// @Tags        quran
// @Produce     json
// @Param       hizb_number         path     int    true  "Hizb number" minimum(1) maximum(60)
// @Param       lang                query    string false "Language code" default(id)
// @Param       translation_source  query    string false "Translation source ID. Empty uses language default."
// @Param       include_translation query    bool   false "Include selected translation" default(true)
// @Param       include_audio       query    bool   false "Include audio track and timestamp segments" default(false)
// @Param       recitation_id       query    string false "Recitation ID. Defaults to the playable default recitation when include_audio=true."
// @Param       view                query    string false "Response view. Empty/full returns the existing QuranAyah shape; reader_minimal returns v1.QuranReaderAyah." Enums(full,reader_minimal)
// @Success     200                 {array}  response.QuranReaderAyah
// @Success     200                 {array}  entity.QuranAyah
// @Failure     400                 {object} response.Error
// @Failure     404                 {object} response.Error
// @Failure     500                 {object} response.Error
// @Router      /quran/hizbs/{hizb_number}/ayahs [get]
func (r *V1) listQuranHizbAyahs(ctx *fiber.Ctx) error {
	hizbNumber, err := pathInt(ctx, "hizb_number")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid hizb_number")
	}

	ayahs, view, ok, err := r.quranNavigationAyahs(ctx, hizbNumber, "restapi - v1 - listQuranHizbAyahs", r.quran.HizbAyahs)
	if err != nil || !ok {
		return err
	}

	return quranAyahListResponse(ctx, ayahs, view)
}

// @Summary     Get Quran ayah
// @Description Get one ayah by canonical ayah key. When include_audio=true and recitation_id is omitted, the backend uses the default playable recitation.
// @ID          get-quran-ayah
// @Tags        quran
// @Produce     json
// @Param       ayah_key           path     string true  "Canonical ayah key, for example 73:4"
// @Param       lang               query    string false "Language code" default(id)
// @Param       translation_source query    string false "Translation source ID. Empty uses language default."
// @Param       include_audio      query    bool   false "Include audio track and timestamp segments" default(false)
// @Param       recitation_id      query    string false "Recitation ID. Defaults to the playable default recitation when include_audio=true."
// @Success     200                {object} entity.QuranAyah
// @Failure     400                {object} response.Error
// @Failure     404                {object} response.Error
// @Failure     500                {object} response.Error
// @Router      /quran/ayahs/{ayah_key} [get]
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
		r.logQuranError(err, "restapi - v1 - getQuranAyah")

		return r.quranErrorResponse(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(ayah)
}

// @Summary     List Quran ayahs for a surah
// @Description List all ayahs or an ayah range for one surah. When include_audio=true and recitation_id is omitted, the backend uses the default playable recitation.
// @ID          list-quran-surah-ayahs
// @Tags        quran
// @Produce     json
// @Param       surah_id           path     int    true  "Surah ID" minimum(1) maximum(114)
// @Param       from               query    int    false "Starting ayah number"
// @Param       to                 query    int    false "Ending ayah number"
// @Param       lang               query    string false "Language code" default(id)
// @Param       translation_source query    string false "Translation source ID. Empty uses language default."
// @Param       include_translation query   bool   false "Include selected translation" default(true)
// @Param       include_audio      query    bool   false "Include audio track and timestamp segments" default(false)
// @Param       recitation_id      query    string false "Recitation ID. Defaults to the playable default recitation when include_audio=true."
// @Param       view               query    string false "Response view. Empty/full returns the existing QuranAyah shape; reader_minimal returns v1.QuranReaderAyah." Enums(full,reader_minimal)
// @Success     200                {array}  response.QuranReaderAyah
// @Success     200                {array}  entity.QuranAyah
// @Failure     400                {object} response.Error
// @Failure     404                {object} response.Error
// @Failure     500                {object} response.Error
// @Router      /quran/surahs/{surah_id}/ayahs [get]
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
	view, err := quranAyahListView(ctx)
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid view")
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
		r.logQuranError(err, "restapi - v1 - listQuranSurahAyahs")

		return r.quranErrorResponse(ctx, err)
	}

	return quranAyahListResponse(ctx, ayahs, view)
}

// @Summary     Search Quran
// @Description Search Arabic Quran text, the requested translation, and other imported translations for discoverability. Display translation remains exact requested language only.
// @ID          search-quran
// @Tags        quran
// @Produce     json
// @Param       q      query    string true  "Search query"
// @Param       lang   query    string false "Language code" default(id)
// @Param       limit  query    int    false "Limit" default(50)
// @Param       offset query    int    false "Offset" default(0)
// @Success     200    {object} response.QuranSearchList
// @Failure     400    {object} response.Error
// @Failure     500    {object} response.Error
// @Router      /quran/search [get]
func (r *V1) searchQuran(ctx *fiber.Ctx) error {
	results, total, err := r.quran.Search(
		ctx.UserContext(),
		ctx.Query("q"),
		ctx.Query("lang"),
		queryInt(ctx, "limit", 50),
		queryInt(ctx, "offset", 0),
	)
	if err != nil {
		r.logQuranError(err, "restapi - v1 - searchQuran")

		return r.quranErrorResponse(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(response.QuranSearchList{Results: results, Total: total})
}

// @Summary     List book Quran references
// @Description List Quran references linked to a public kitab. Defaults to approved references.
// @ID          list-book-quran-references
// @Tags        quran
// @Produce     json
// @Param       book_id path     int    true  "Book ID"
// @Param       heading_id query int    false "Heading ID"
// @Param       lang    query    string false "Language code" default(id)
// @Param       status  query    string false "Review status" Enums(approved,pending,rejected,ambiguous,needs_review,all) default(approved)
// @Param       limit   query    int    false "Limit" default(50)
// @Param       offset  query    int    false "Offset" default(0)
// @Success     200     {object} response.BookQuranReferenceList
// @Failure     400     {object} response.Error
// @Failure     404     {object} response.Error
// @Failure     500     {object} response.Error
// @Router      /books/{book_id}/quran-references [get]
func (r *V1) listBookQuranReferences(ctx *fiber.Ctx) error {
	bookID, err := pathInt(ctx, "book_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid book_id")
	}
	headingID, err := optionalQueryInt(ctx, "heading_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid heading_id")
	}

	references, total, err := r.quran.BookReferences(
		ctx.UserContext(),
		bookID,
		headingID,
		ctx.Query("lang"),
		ctx.Query("status", "approved"),
		queryInt(ctx, "limit", 50),
		queryInt(ctx, "offset", 0),
	)
	if err != nil {
		r.logQuranError(err, "restapi - v1 - listBookQuranReferences")

		return r.quranErrorResponse(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(response.BookQuranReferenceList{References: references, Total: total})
}

type quranNavigationAyahLoader func(
	ctxUser context.Context,
	number int,
	lang string,
	translationSource string,
	includeTranslation bool,
	includeAudio bool,
	recitationID string,
) ([]entity.QuranAyah, error)

func (r *V1) quranNavigationAyahs(
	ctx *fiber.Ctx,
	number int,
	operation string,
	load quranNavigationAyahLoader,
) ([]entity.QuranAyah, string, bool, error) {
	includeTranslationValue, err := optionalQueryBool(ctx, "include_translation")
	if err != nil {
		return nil, "", false, errorResponse(ctx, http.StatusBadRequest, "invalid include_translation")
	}
	includeAudioValue, err := optionalQueryBool(ctx, "include_audio")
	if err != nil {
		return nil, "", false, errorResponse(ctx, http.StatusBadRequest, "invalid include_audio")
	}
	view, err := quranAyahListView(ctx)
	if err != nil {
		return nil, "", false, errorResponse(ctx, http.StatusBadRequest, "invalid view")
	}

	includeTranslation := true
	if includeTranslationValue != nil {
		includeTranslation = *includeTranslationValue
	}
	includeAudio := includeAudioValue != nil && *includeAudioValue

	ayahs, err := load(
		ctx.UserContext(),
		number,
		ctx.Query("lang"),
		ctx.Query("translation_source"),
		includeTranslation,
		includeAudio,
		ctx.Query("recitation_id"),
	)
	if err != nil {
		r.logQuranError(err, operation)

		return nil, "", false, r.quranErrorResponse(ctx, err)
	}

	return ayahs, view, true, nil
}

func quranAyahListView(ctx *fiber.Ctx) (string, error) {
	view := strings.TrimSpace(ctx.Query("view"))
	switch view {
	case "", quranAyahViewFull:
		return quranAyahViewFull, nil
	case quranAyahViewReaderMinimal:
		return quranAyahViewReaderMinimal, nil
	default:
		return "", errors.New("invalid view")
	}
}

func quranAyahListResponse(ctx *fiber.Ctx, ayahs []entity.QuranAyah, view string) error {
	if view == quranAyahViewReaderMinimal {
		return ctx.Status(http.StatusOK).JSON(response.QuranReaderAyahs(ayahs))
	}

	return ctx.Status(http.StatusOK).JSON(ayahs)
}

func (r *V1) quranErrorResponse(ctx *fiber.Ctx, err error) error {
	if errors.Is(err, entity.ErrUnsupportedLanguage) {
		return errorResponse(ctx, http.StatusBadRequest, "unsupported language")
	}
	if errors.Is(err, entity.ErrInvalidAyahKey) || errors.Is(err, entity.ErrInvalidQuranRange) {
		return errorResponse(ctx, http.StatusBadRequest, err.Error())
	}
	if errors.Is(err, entity.ErrInvalidAssetType) {
		return errorResponse(ctx, http.StatusBadRequest, "invalid asset type")
	}
	if errors.Is(err, entity.ErrQuranSurahNotFound) {
		return errorResponse(ctx, http.StatusNotFound, "quran surah not found")
	}
	if errors.Is(err, entity.ErrQuranAyahNotFound) {
		return errorResponse(ctx, http.StatusNotFound, "quran ayah not found")
	}
	if errors.Is(err, entity.ErrQuranNavigationNotFound) {
		return errorResponse(ctx, http.StatusNotFound, "quran navigation not found")
	}
	if errors.Is(err, entity.ErrQuranRecitationNotFound) {
		return errorResponse(ctx, http.StatusNotFound, "quran recitation not found")
	}
	if errors.Is(err, entity.ErrQuranTranslationSourceNotFound) {
		return errorResponse(ctx, http.StatusNotFound, "quran translation source not found")
	}
	if errors.Is(err, entity.ErrBookNotFound) {
		return errorResponse(ctx, http.StatusNotFound, "book not found")
	}

	return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
}
