package v1

import (
	"net/http"
	"strings"

	"github.com/alfariesh/surau-backend/internal/controller/restapi/v1/request"
	"github.com/alfariesh/surau-backend/internal/controller/restapi/v1/response"
	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/quranutil"
	"github.com/gofiber/fiber/v2"
)

// @Summary     Get Quran surah editorial workspace
// @Description Get draft and published editorial states for one surah and language. Requires editorial review capability.
// @ID          editorial-quran-surah-workspace
// @Tags        editorial
// @Produce     json
// @Param       surah_id path  int    true  "Surah ID" minimum(1) maximum(114)
// @Param       lang     query string false "Language code" default(id)
// @Success     200 {object} entity.QuranSurahEditorialWorkspace
// @Failure     400 {object} response.Error
// @Failure     401 {object} response.Error
// @Failure     403 {object} response.Error
// @Failure     404 {object} response.Error
// @Failure     500 {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/quran/surahs/{surah_id} [get]
func (r *V1) editorialQuranSurahWorkspace(ctx *fiber.Ctx) error {
	surahID, err := pathInt(ctx, "surah_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid surah_id")
	}

	if r.quranEditorial == nil {
		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	workspace, err := r.quranEditorial.SurahEditorialWorkspace(
		ctx.UserContext(), surahID, quranEditorialLang(ctx),
	)
	if err != nil {
		r.logEditorialError(ctx, err, "restapi - v1 - editorialQuranSurahWorkspace")

		return r.editorialError(ctx, err)
	}

	return jsonWithUpdatedAtETag(ctx, http.StatusOK, workspace, workspace.CurrentUpdatedAt())
}

// @Summary     Save Quran surah editorial draft
// @Description Save a complete surah editorial draft. If-Match is required; use * only for an explicit force-write.
// @ID          editorial-save-quran-surah-draft
// @Tags        editorial
// @Accept      json
// @Produce     json
// @Param       surah_id path   int                                   true  "Surah ID" minimum(1) maximum(114)
// @Param       lang     query  string                                false "Language code" default(id)
// @Param       If-Match header string                                true  "Workspace ETag or *"
// @Param       body     body   request.SaveQuranSurahEditorialDraft true  "Complete draft snapshot"
// @Success     200 {object} entity.QuranSurahEditorialWorkspace
// @Failure     400 {object} response.Error
// @Failure     401 {object} response.Error
// @Failure     403 {object} response.Error
// @Failure     404 {object} response.Error
// @Failure     412 {object} response.Error
// @Failure     428 {object} response.Error
// @Failure     500 {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/quran/surahs/{surah_id}/draft [put]
func (r *V1) editorialSaveQuranSurahDraft(ctx *fiber.Ctx) error {
	actorID, ok := editorialActorID(ctx)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	surahID, err := pathInt(ctx, "surah_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid surah_id")
	}

	var body request.SaveQuranSurahEditorialDraft
	if err = ctx.BodyParser(&body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	if err = r.v.Struct(body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	expected, ok, preconditionErr := r.editorialIfMatch(ctx, true)
	if !ok {
		return preconditionErr
	}

	if r.quranEditorial == nil {
		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	workspace, err := r.quranEditorial.SaveSurahEditorialDraft(
		ctx.UserContext(),
		actorID,
		entity.QuranSurahEditorialEdit{
			SurahID:         surahID,
			Lang:            quranEditorialLang(ctx),
			MetaTitle:       body.MetaTitle,
			MetaDescription: body.MetaDescription,
			ArtiNama:        body.ArtiNama,
			Keutamaan:       body.Keutamaan,
			AsbabunNuzul:    body.AsbabunNuzul,
			PokokKandungan:  body.PokokKandungan,
			AuthorName:      body.AuthorName,
			ReviewedBy:      body.ReviewedBy,
			ReviewedAt:      body.ReviewedAt,
			LicenseStatus:   body.LicenseStatus,
			Metadata:        entity.RawJSON(body.Metadata),
		},
		expected,
	)
	if err != nil {
		r.logEditorialError(ctx, err, "restapi - v1 - editorialSaveQuranSurahDraft")

		return r.editorialError(ctx, err)
	}

	return jsonWithUpdatedAtETag(ctx, http.StatusOK, workspace, workspace.CurrentUpdatedAt())
}

// @Summary     Publish Quran surah editorial draft
// @Description Publish the current permitted surah draft. Requires production publish capability and fresh MFA.
// @ID          editorial-publish-quran-surah-draft
// @Tags        editorial
// @Produce     json
// @Param       surah_id path   int    true  "Surah ID" minimum(1) maximum(114)
// @Param       lang     query  string false "Language code" default(id)
// @Param       If-Match header string true  "Workspace ETag or *"
// @Success     200 {object} entity.QuranSurahEditorialWorkspace
// @Failure     400 {object} response.Error
// @Failure     401 {object} response.Error
// @Failure     403 {object} response.Error
// @Failure     404 {object} response.Error
// @Failure     409 {object} response.Error
// @Failure     412 {object} response.Error
// @Failure     428 {object} response.Error
// @Failure     500 {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/quran/surahs/{surah_id}/publish [post]
func (r *V1) editorialPublishQuranSurahDraft(ctx *fiber.Ctx) error {
	actorID, ok := editorialActorID(ctx)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	surahID, err := pathInt(ctx, "surah_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid surah_id")
	}

	expected, ok, preconditionErr := r.editorialIfMatch(ctx, true)
	if !ok {
		return preconditionErr
	}

	if r.quranEditorial == nil {
		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	workspace, err := r.quranEditorial.PublishSurahEditorialDraft(
		ctx.UserContext(), actorID, surahID, quranEditorialLang(ctx), expected,
	)
	if err != nil {
		r.logEditorialError(ctx, err, "restapi - v1 - editorialPublishQuranSurahDraft")

		return r.editorialError(ctx, err)
	}

	return jsonWithUpdatedAtETag(ctx, http.StatusOK, workspace, workspace.CurrentUpdatedAt())
}

// @Summary     List Quran surah editorial revisions
// @Description List newest-first revision snapshots for one surah and language. Requires editorial review capability.
// @ID          editorial-list-quran-surah-revisions
// @Tags        editorial
// @Produce     json
// @Param       surah_id path  int    true  "Surah ID" minimum(1) maximum(114)
// @Param       lang     query string false "Language code" default(id)
// @Param       limit    query int    false "Page size (default 50, max 200)"
// @Param       offset   query int    false "Offset (max 10000)"
// @Success     200 {object} response.QuranEditorialRevisionList
// @Failure     400 {object} response.Error
// @Failure     401 {object} response.Error
// @Failure     403 {object} response.Error
// @Failure     404 {object} response.Error
// @Failure     500 {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/quran/surahs/{surah_id}/draft-revisions [get]
func (r *V1) editorialListQuranSurahRevisions(ctx *fiber.Ctx) error {
	surahID, err := pathInt(ctx, "surah_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid surah_id")
	}

	if r.quranEditorial == nil {
		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	items, total, err := r.quranEditorial.QuranEditorialRevisions(
		ctx.UserContext(),
		entity.QuranEditorialAssetSurah,
		surahID,
		nil,
		quranEditorialLang(ctx),
		queryInt(ctx, "limit", defaultRevisionPageSize),
		queryInt(ctx, "offset", 0),
	)
	if err != nil {
		r.logEditorialError(ctx, err, "restapi - v1 - editorialListQuranSurahRevisions")

		return r.editorialError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(response.QuranEditorialRevisionList{Items: items, Total: total})
}

// @Summary     Restore Quran surah editorial revision
// @Description Restore a historical snapshot into draft only. The restore creates a new revision and never publishes automatically.
// @ID          editorial-restore-quran-surah-revision
// @Tags        editorial
// @Produce     json
// @Param       surah_id     path   int    true  "Surah ID" minimum(1) maximum(114)
// @Param       revision_id  path   string true  "Revision ID"
// @Param       lang         query  string false "Language code" default(id)
// @Param       If-Match     header string true  "Workspace ETag or *"
// @Success     200 {object} entity.QuranSurahEditorialWorkspace
// @Failure     400 {object} response.Error
// @Failure     401 {object} response.Error
// @Failure     403 {object} response.Error
// @Failure     404 {object} response.Error
// @Failure     412 {object} response.Error
// @Failure     428 {object} response.Error
// @Failure     500 {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/quran/surahs/{surah_id}/draft-revisions/{revision_id}/restore [post]
func (r *V1) editorialRestoreQuranSurahRevision(ctx *fiber.Ctx) error {
	actorID, ok := editorialActorID(ctx)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	surahID, err := pathInt(ctx, "surah_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid surah_id")
	}

	expected, ok, preconditionErr := r.editorialIfMatch(ctx, true)
	if !ok {
		return preconditionErr
	}

	if r.quranEditorial == nil {
		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	workspace, err := r.quranEditorial.RestoreSurahEditorialRevision(
		ctx.UserContext(),
		actorID,
		surahID,
		quranEditorialLang(ctx),
		strings.TrimSpace(ctx.Params("revision_id")),
		expected,
	)
	if err != nil {
		r.logEditorialError(ctx, err, "restapi - v1 - editorialRestoreQuranSurahRevision")

		return r.editorialError(ctx, err)
	}

	return jsonWithUpdatedAtETag(ctx, http.StatusOK, workspace, workspace.CurrentUpdatedAt())
}

// @Summary     Get Quran ayah editorial workspace
// @Description Get draft and published editorial states for one ayah and language. Requires editorial review capability.
// @ID          editorial-quran-ayah-workspace
// @Tags        editorial
// @Produce     json
// @Param       ayah_key path  string true  "Canonical ayah key" example(2:255)
// @Param       lang     query string false "Language code" default(id)
// @Success     200 {object} entity.QuranAyahEditorialWorkspace
// @Failure     400 {object} response.Error
// @Failure     401 {object} response.Error
// @Failure     403 {object} response.Error
// @Failure     404 {object} response.Error
// @Failure     500 {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/quran/ayahs/{ayah_key} [get]
func (r *V1) editorialQuranAyahWorkspace(ctx *fiber.Ctx) error {
	if r.quranEditorial == nil {
		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	workspace, err := r.quranEditorial.AyahEditorialWorkspace(
		ctx.UserContext(), ctx.Params("ayah_key"), quranEditorialLang(ctx),
	)
	if err != nil {
		r.logEditorialError(ctx, err, "restapi - v1 - editorialQuranAyahWorkspace")

		return r.editorialError(ctx, err)
	}

	return jsonWithUpdatedAtETag(ctx, http.StatusOK, workspace, workspace.CurrentUpdatedAt())
}

// @Summary     Save Quran ayah editorial draft
// @Description Save a complete ayah editorial draft. If-Match is required; use * only for an explicit force-write.
// @ID          editorial-save-quran-ayah-draft
// @Tags        editorial
// @Accept      json
// @Produce     json
// @Param       ayah_key path   string                               true  "Canonical ayah key" example(2:255)
// @Param       lang     query  string                               false "Language code" default(id)
// @Param       If-Match header string                               true  "Workspace ETag or *"
// @Param       body     body   request.SaveQuranAyahEditorialDraft true  "Complete draft snapshot"
// @Success     200 {object} entity.QuranAyahEditorialWorkspace
// @Failure     400 {object} response.Error
// @Failure     401 {object} response.Error
// @Failure     403 {object} response.Error
// @Failure     404 {object} response.Error
// @Failure     412 {object} response.Error
// @Failure     428 {object} response.Error
// @Failure     500 {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/quran/ayahs/{ayah_key}/draft [put]
func (r *V1) editorialSaveQuranAyahDraft(ctx *fiber.Ctx) error {
	actorID, ok := editorialActorID(ctx)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	var body request.SaveQuranAyahEditorialDraft
	if err := ctx.BodyParser(&body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	if err := r.v.Struct(body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	expected, ok, preconditionErr := r.editorialIfMatch(ctx, true)
	if !ok {
		return preconditionErr
	}

	if r.quranEditorial == nil {
		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	workspace, err := r.quranEditorial.SaveAyahEditorialDraft(
		ctx.UserContext(),
		actorID,
		entity.QuranAyahEditorialEdit{
			AyahKey:         ctx.Params("ayah_key"),
			Lang:            quranEditorialLang(ctx),
			MetaTitle:       body.MetaTitle,
			MetaDescription: body.MetaDescription,
			Intisari:        body.Intisari,
			Keutamaan:       body.Keutamaan,
			FAQ:             quranEditorialFAQs(body.FAQ),
			TafsirRange:     body.TafsirRange,
			AuthorName:      body.AuthorName,
			ReviewedBy:      body.ReviewedBy,
			ReviewedAt:      body.ReviewedAt,
			LicenseStatus:   body.LicenseStatus,
			Metadata:        entity.RawJSON(body.Metadata),
		},
		expected,
	)
	if err != nil {
		r.logEditorialError(ctx, err, "restapi - v1 - editorialSaveQuranAyahDraft")

		return r.editorialError(ctx, err)
	}

	return jsonWithUpdatedAtETag(ctx, http.StatusOK, workspace, workspace.CurrentUpdatedAt())
}

// @Summary     Publish Quran ayah editorial draft
// @Description Publish the current permitted ayah draft. Requires production publish capability and fresh MFA.
// @ID          editorial-publish-quran-ayah-draft
// @Tags        editorial
// @Produce     json
// @Param       ayah_key path   string true  "Canonical ayah key" example(2:255)
// @Param       lang     query  string false "Language code" default(id)
// @Param       If-Match header string true  "Workspace ETag or *"
// @Success     200 {object} entity.QuranAyahEditorialWorkspace
// @Failure     400 {object} response.Error
// @Failure     401 {object} response.Error
// @Failure     403 {object} response.Error
// @Failure     404 {object} response.Error
// @Failure     409 {object} response.Error
// @Failure     412 {object} response.Error
// @Failure     428 {object} response.Error
// @Failure     500 {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/quran/ayahs/{ayah_key}/publish [post]
func (r *V1) editorialPublishQuranAyahDraft(ctx *fiber.Ctx) error {
	actorID, ok := editorialActorID(ctx)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	expected, ok, preconditionErr := r.editorialIfMatch(ctx, true)
	if !ok {
		return preconditionErr
	}

	if r.quranEditorial == nil {
		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	workspace, err := r.quranEditorial.PublishAyahEditorialDraft(
		ctx.UserContext(), actorID, ctx.Params("ayah_key"), quranEditorialLang(ctx), expected,
	)
	if err != nil {
		r.logEditorialError(ctx, err, "restapi - v1 - editorialPublishQuranAyahDraft")

		return r.editorialError(ctx, err)
	}

	return jsonWithUpdatedAtETag(ctx, http.StatusOK, workspace, workspace.CurrentUpdatedAt())
}

// @Summary     List Quran ayah editorial revisions
// @Description List newest-first revision snapshots for one ayah and language. Requires editorial review capability.
// @ID          editorial-list-quran-ayah-revisions
// @Tags        editorial
// @Produce     json
// @Param       ayah_key path  string true  "Canonical ayah key" example(2:255)
// @Param       lang     query string false "Language code" default(id)
// @Param       limit    query int    false "Page size (default 50, max 200)"
// @Param       offset   query int    false "Offset (max 10000)"
// @Success     200 {object} response.QuranEditorialRevisionList
// @Failure     400 {object} response.Error
// @Failure     401 {object} response.Error
// @Failure     403 {object} response.Error
// @Failure     404 {object} response.Error
// @Failure     500 {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/quran/ayahs/{ayah_key}/draft-revisions [get]
func (r *V1) editorialListQuranAyahRevisions(ctx *fiber.Ctx) error {
	surahID, ayahNumber, err := quranutil.ParseAyahKey(ctx.Params("ayah_key"))
	if err != nil || surahID < 1 || surahID > 114 {
		return errorResponse(ctx, http.StatusBadRequest, "invalid ayah key")
	}

	if r.quranEditorial == nil {
		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	items, total, err := r.quranEditorial.QuranEditorialRevisions(
		ctx.UserContext(),
		entity.QuranEditorialAssetAyah,
		surahID,
		&ayahNumber,
		quranEditorialLang(ctx),
		queryInt(ctx, "limit", defaultRevisionPageSize),
		queryInt(ctx, "offset", 0),
	)
	if err != nil {
		r.logEditorialError(ctx, err, "restapi - v1 - editorialListQuranAyahRevisions")

		return r.editorialError(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(response.QuranEditorialRevisionList{Items: items, Total: total})
}

// @Summary     Restore Quran ayah editorial revision
// @Description Restore a historical snapshot into draft only. The restore creates a new revision and never publishes automatically.
// @ID          editorial-restore-quran-ayah-revision
// @Tags        editorial
// @Produce     json
// @Param       ayah_key     path   string true  "Canonical ayah key" example(2:255)
// @Param       revision_id  path   string true  "Revision ID"
// @Param       lang         query  string false "Language code" default(id)
// @Param       If-Match     header string true  "Workspace ETag or *"
// @Success     200 {object} entity.QuranAyahEditorialWorkspace
// @Failure     400 {object} response.Error
// @Failure     401 {object} response.Error
// @Failure     403 {object} response.Error
// @Failure     404 {object} response.Error
// @Failure     412 {object} response.Error
// @Failure     428 {object} response.Error
// @Failure     500 {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/quran/ayahs/{ayah_key}/draft-revisions/{revision_id}/restore [post]
func (r *V1) editorialRestoreQuranAyahRevision(ctx *fiber.Ctx) error {
	actorID, ok := editorialActorID(ctx)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	expected, ok, preconditionErr := r.editorialIfMatch(ctx, true)
	if !ok {
		return preconditionErr
	}

	if r.quranEditorial == nil {
		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	workspace, err := r.quranEditorial.RestoreAyahEditorialRevision(
		ctx.UserContext(),
		actorID,
		ctx.Params("ayah_key"),
		quranEditorialLang(ctx),
		strings.TrimSpace(ctx.Params("revision_id")),
		expected,
	)
	if err != nil {
		r.logEditorialError(ctx, err, "restapi - v1 - editorialRestoreQuranAyahRevision")

		return r.editorialError(ctx, err)
	}

	return jsonWithUpdatedAtETag(ctx, http.StatusOK, workspace, workspace.CurrentUpdatedAt())
}

func editorialActorID(ctx *fiber.Ctx) (string, bool) {
	actorID, ok := ctx.Locals("userID").(string)

	return actorID, ok && actorID != ""
}

func quranEditorialLang(ctx *fiber.Ctx) string {
	return ctx.Query("lang", "id")
}

func quranEditorialFAQs(items []request.QuranAyahEditorialFAQ) []entity.QuranAyahEditorialFAQ {
	result := make([]entity.QuranAyahEditorialFAQ, 0, len(items))
	for _, item := range items {
		result = append(result, entity.QuranAyahEditorialFAQ{
			Question:   item.Question,
			AnswerHTML: item.AnswerHTML,
		})
	}

	return result
}
