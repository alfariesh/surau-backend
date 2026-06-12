package v1

import (
	"errors"
	"net/http"

	"github.com/evrone/go-clean-template/internal/controller/restapi/v1/request"
	"github.com/evrone/go-clean-template/internal/controller/restapi/v1/response"
	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/gofiber/fiber/v2"
)

// @Summary     List reading progress
// @Description Return the authenticated user's in-progress books ordered by recent activity (continue-reading shelf), enriched with light book metadata.
// @ID          list-progress
// @Tags        me
// @Produce     json
// @Security    BearerAuth
// @Param       lang   query    string false "Language code: ar, id, or en" default(id)
// @Param       limit  query    int    false "Limit" default(50)
// @Param       offset query    int    false "Offset" default(0)
// @Success     200    {object} response.ContinueReadingList
// @Failure     400    {object} response.Error
// @Failure     401    {object} response.Error
// @Failure     500    {object} response.Error
// @Router      /me/progress [get]
func (r *V1) listProgress(ctx *fiber.Ctx) error {
	userID, ok := ctx.Locals("userID").(string)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	entries, total, err := r.personal.ListProgress(
		ctx.UserContext(),
		userID,
		ctx.Query("lang"),
		queryInt(ctx, "limit", defaultListLimit),
		queryInt(ctx, "offset", 0),
	)
	if err != nil {
		r.l.Error(err, "restapi - v1 - listProgress")

		if errors.Is(err, entity.ErrUnsupportedLanguage) {
			return errorResponse(ctx, http.StatusBadRequest, "unsupported language")
		}

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	return ctx.Status(http.StatusOK).JSON(response.ContinueReadingList{Items: entries, Total: total})
}

// @Summary     Get reading progress
// @Description Return the authenticated user's reading position for one book.
// @ID          get-progress
// @Tags        me
// @Produce     json
// @Security    BearerAuth
// @Param       book_id path     int true "Book ID"
// @Success     200     {object} entity.ReadingProgress
// @Failure     400     {object} response.Error
// @Failure     401     {object} response.Error
// @Failure     404     {object} response.Error
// @Failure     500     {object} response.Error
// @Router      /me/progress/{book_id} [get]
func (r *V1) getProgress(ctx *fiber.Ctx) error {
	userID, ok := ctx.Locals("userID").(string)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	bookID, err := pathInt(ctx, "book_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid book_id")
	}

	progress, err := r.personal.GetProgress(ctx.UserContext(), userID, bookID)
	if err != nil {
		r.l.Error(err, "restapi - v1 - getProgress")

		if errors.Is(err, entity.ErrProgressNotFound) {
			return errorResponse(ctx, http.StatusNotFound, "progress not found")
		}

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	return ctx.Status(http.StatusOK).JSON(progress)
}

// @Summary     Save reading progress
// @Description Upsert the authenticated user's reading position for one book. Older client_observed_at events do not roll back progress.
// @ID          save-progress
// @Tags        me
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       book_id path     int                  true "Book ID"
// @Param       request body     request.SaveProgress true "Reading position"
// @Success     200     {object} entity.ReadingProgress
// @Failure     400     {object} response.Error
// @Failure     401     {object} response.Error
// @Failure     404     {object} response.Error
// @Failure     500     {object} response.Error
// @Router      /me/progress/{book_id} [put]
func (r *V1) saveProgress(ctx *fiber.Ctx) error {
	userID, ok := ctx.Locals("userID").(string)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	bookID, err := pathInt(ctx, "book_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid book_id")
	}

	var body request.SaveProgress
	if err = ctx.BodyParser(&body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	if err = r.v.Struct(body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	progress, err := r.personal.SaveProgress(
		ctx.UserContext(),
		userID,
		bookID,
		body.PageID,
		body.HeadingID,
		body.ProgressPercent,
		body.ClientObservedAt,
	)
	if err != nil {
		r.l.Error(err, "restapi - v1 - saveProgress")

		return readerLocationErrorResponse(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(progress)
}

// @Summary     Save TOC reading progress
// @Description Upsert the authenticated user's reading position at a TOC heading. Older client_observed_at events do not roll back progress.
// @ID          save-toc-progress
// @Tags        me
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       book_id    path     int                     true "Book ID"
// @Param       heading_id path     int                     true "Heading ID"
// @Param       request    body     request.SaveTOCProgress true "Reading position"
// @Success     200        {object} entity.ReadingProgress
// @Failure     400        {object} response.Error
// @Failure     401        {object} response.Error
// @Failure     404        {object} response.Error
// @Failure     500        {object} response.Error
// @Router      /me/progress/{book_id}/toc/{heading_id} [put]
func (r *V1) saveTOCProgress(ctx *fiber.Ctx) error {
	userID, ok := ctx.Locals("userID").(string)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	bookID, err := pathInt(ctx, "book_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid book_id")
	}

	headingID, err := pathInt(ctx, "heading_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid heading_id")
	}

	var body request.SaveTOCProgress
	if err = ctx.BodyParser(&body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	if err = r.v.Struct(body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	progress, err := r.personal.SaveProgress(
		ctx.UserContext(),
		userID,
		bookID,
		nil,
		&headingID,
		body.ProgressPercent,
		body.ClientObservedAt,
	)
	if err != nil {
		r.l.Error(err, "restapi - v1 - saveTOCProgress")

		return readerLocationErrorResponse(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(progress)
}

// @Summary     Get latest Quran progress
// @Description Return the authenticated user's latest Quran resume position across surahs.
// @ID          get-quran-progress
// @Tags        me
// @Produce     json
// @Security    BearerAuth
// @Success     200 {object} entity.QuranReadingProgress
// @Failure     401 {object} response.Error
// @Failure     404 {object} response.Error
// @Failure     500 {object} response.Error
// @Router      /me/quran/progress [get]
func (r *V1) getQuranProgress(ctx *fiber.Ctx) error {
	userID, ok := ctx.Locals("userID").(string)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	progress, err := r.personal.GetQuranProgress(ctx.UserContext(), userID)
	if err != nil {
		r.l.Error(err, "restapi - v1 - getQuranProgress")

		return quranProgressErrorResponse(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(progress)
}

// @Summary     Save Quran progress
// @Description Upsert one Quran resume position. Older client_observed_at events do not roll back progress.
// @ID          save-quran-progress
// @Tags        me
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       request body     request.SaveQuranProgress true "Quran progress position"
// @Success     200     {object} entity.QuranReadingProgress
// @Failure     400     {object} response.Error
// @Failure     401     {object} response.Error
// @Failure     404     {object} response.Error
// @Failure     500     {object} response.Error
// @Router      /me/quran/progress [put]
func (r *V1) saveQuranProgress(ctx *fiber.Ctx) error {
	userID, ok := ctx.Locals("userID").(string)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	var body request.SaveQuranProgress
	if err := ctx.BodyParser(&body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	if err := r.v.Struct(body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	progress, err := r.personal.SaveQuranProgress(ctx.UserContext(), userID, body.AyahKey, body.ClientObservedAt)
	if err != nil {
		r.l.Error(err, "restapi - v1 - saveQuranProgress")

		return quranProgressErrorResponse(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(progress)
}

// @Summary     List Quran surah progress
// @Description Return all authenticated user's per-surah Quran resume positions ordered by observed_at descending.
// @ID          list-quran-surah-progress
// @Tags        me
// @Produce     json
// @Security    BearerAuth
// @Success     200 {object} response.QuranProgressList
// @Failure     401 {object} response.Error
// @Failure     500 {object} response.Error
// @Router      /me/quran/progress/surahs [get]
func (r *V1) listQuranSurahProgress(ctx *fiber.Ctx) error {
	userID, ok := ctx.Locals("userID").(string)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	progress, err := r.personal.ListQuranSurahProgress(ctx.UserContext(), userID)
	if err != nil {
		r.l.Error(err, "restapi - v1 - listQuranSurahProgress")

		return quranProgressErrorResponse(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(response.QuranProgressList{Items: progress, Total: len(progress)})
}

// @Summary     Get Quran surah progress
// @Description Return the authenticated user's Quran resume position for one surah.
// @ID          get-quran-surah-progress
// @Tags        me
// @Produce     json
// @Security    BearerAuth
// @Param       surah_id path int true "Surah ID"
// @Success     200      {object} entity.QuranReadingProgress
// @Failure     400      {object} response.Error
// @Failure     401      {object} response.Error
// @Failure     404      {object} response.Error
// @Failure     500      {object} response.Error
// @Router      /me/quran/progress/surahs/{surah_id} [get]
func (r *V1) getQuranSurahProgress(ctx *fiber.Ctx) error {
	userID, ok := ctx.Locals("userID").(string)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	surahID, err := pathInt(ctx, "surah_id")
	if err != nil || surahID <= 0 || surahID > 114 {
		return errorResponse(ctx, http.StatusBadRequest, "invalid surah_id")
	}

	progress, err := r.personal.GetQuranSurahProgress(ctx.UserContext(), userID, surahID)
	if err != nil {
		r.l.Error(err, "restapi - v1 - getQuranSurahProgress")

		return quranProgressErrorResponse(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(progress)
}

// @Summary     List saved items
// @Description List private saved Quran and kitab targets. Responses are reference-only and do not hydrate target content.
// @ID          list-saved-items
// @Tags        me
// @Produce     json
// @Security    BearerAuth
// @Param       item_type query    string false "Filter by item type" Enums(book_page,book_heading,quran_ayah,quran_range)
// @Param       book_id   query    int    false "Filter by kitab book ID"
// @Param       surah_id  query    int    false "Filter by Quran surah ID"
// @Param       tag       query    string false "Filter by normalized tag"
// @Param       limit     query    int    false "Limit" default(50)
// @Param       offset    query    int    false "Offset" default(0)
// @Success     200       {object} response.SavedItemList
// @Failure     400       {object} response.Error
// @Failure     401       {object} response.Error
// @Failure     500       {object} response.Error
// @Router      /me/saved-items [get]
func (r *V1) listSavedItems(ctx *fiber.Ctx) error {
	userID, ok := ctx.Locals("userID").(string)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	bookID, err := optionalQueryInt(ctx, "book_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid book_id")
	}
	surahID, err := optionalQueryInt(ctx, "surah_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid surah_id")
	}

	items, total, err := r.personal.ListSavedItems(
		ctx.UserContext(),
		userID,
		ctx.Query("item_type"),
		bookID,
		surahID,
		ctx.Query("tag"),
		queryInt(ctx, "limit", defaultListLimit),
		queryInt(ctx, "offset", 0),
	)
	if err != nil {
		r.l.Error(err, "restapi - v1 - listSavedItems")

		return savedItemErrorResponse(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(response.SavedItemList{Items: items, Total: total})
}

// @Summary     Save or update an item
// @Description Save a Quran ayah/range or kitab page/heading. Posting the same target updates provided metadata only; absent label/note/tags never clear stored values (clearing is PATCH's job). Returns 201 when a new item is created, 200 when an existing one is updated.
// @ID          upsert-saved-item
// @Tags        me
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       request body     request.UpsertSavedItem true "Saved item target and metadata"
// @Success     200     {object} entity.SavedItem
// @Success     201     {object} entity.SavedItem
// @Failure     400     {object} response.Error
// @Failure     401     {object} response.Error
// @Failure     500     {object} response.Error
// @Router      /me/saved-items [post]
func (r *V1) upsertSavedItem(ctx *fiber.Ctx) error {
	userID, ok := ctx.Locals("userID").(string)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	var body request.UpsertSavedItem
	if err := ctx.BodyParser(&body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	if err := r.v.Struct(body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	item, created, err := r.personal.UpsertSavedItem(ctx.UserContext(), userID, entity.SavedItem{
		ItemType:       body.ItemType,
		BookID:         body.BookID,
		PageID:         body.PageID,
		HeadingID:      body.HeadingID,
		SurahID:        body.SurahID,
		AyahKey:        body.AyahKey,
		FromAyahNumber: body.FromAyahNumber,
		ToAyahNumber:   body.ToAyahNumber,
		Label:          body.Label,
		Note:           body.Note,
		Tags:           body.Tags,
	})
	if err != nil {
		r.l.Error(err, "restapi - v1 - upsertSavedItem")

		return savedItemErrorResponse(ctx, err)
	}

	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}

	return ctx.Status(status).JSON(item)
}

// @Summary     Update saved item metadata
// @Description Partially update label, note, and tags for one private saved item. Absent fields stay unchanged; explicit null clears a field. A body without any known field is rejected.
// @ID          update-saved-item
// @Tags        me
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       id      path     string                  true "Saved item ID"
// @Param       request body     request.UpdateSavedItem true "Saved item metadata"
// @Success     200     {object} entity.SavedItem
// @Failure     400     {object} response.Error
// @Failure     401     {object} response.Error
// @Failure     404     {object} response.Error
// @Failure     500     {object} response.Error
// @Router      /me/saved-items/{id} [patch]
func (r *V1) updateSavedItem(ctx *fiber.Ctx) error {
	userID, ok := ctx.Locals("userID").(string)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	savedItemID := ctx.Params("id")

	var body request.UpdateSavedItem
	if err := ctx.BodyParser(&body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	patch := entity.SavedItemPatch{
		Label:    body.Label.Value,
		LabelSet: body.Label.Set,
		Note:     body.Note.Value,
		NoteSet:  body.Note.Set,
		TagsSet:  body.Tags.Set,
	}
	if body.Tags.Set && body.Tags.Value != nil {
		patch.Tags = *body.Tags.Value
	}

	item, err := r.personal.UpdateSavedItem(ctx.UserContext(), userID, savedItemID, patch)
	if err != nil {
		r.l.Error(err, "restapi - v1 - updateSavedItem")

		return savedItemErrorResponse(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(item)
}

// @Summary     Delete saved item
// @Description Delete one private saved item.
// @ID          delete-saved-item
// @Tags        me
// @Produce     json
// @Security    BearerAuth
// @Param       id  path string true "Saved item ID"
// @Success     204
// @Failure     401 {object} response.Error
// @Failure     404 {object} response.Error
// @Failure     500 {object} response.Error
// @Router      /me/saved-items/{id} [delete]
func (r *V1) deleteSavedItem(ctx *fiber.Ctx) error {
	userID, ok := ctx.Locals("userID").(string)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	err := r.personal.DeleteSavedItem(ctx.UserContext(), userID, ctx.Params("id"))
	if err != nil {
		r.l.Error(err, "restapi - v1 - deleteSavedItem")

		return savedItemErrorResponse(ctx, err)
	}

	return ctx.SendStatus(http.StatusNoContent)
}

// @Summary     List saved item tags
// @Description List distinct normalized tags used by the authenticated user's saved items.
// @ID          list-saved-item-tags
// @Tags        me
// @Produce     json
// @Security    BearerAuth
// @Success     200 {object} response.SavedItemTags
// @Failure     401 {object} response.Error
// @Failure     500 {object} response.Error
// @Router      /me/saved-items/tags [get]
func (r *V1) listSavedItemTags(ctx *fiber.Ctx) error {
	userID, ok := ctx.Locals("userID").(string)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	tags, err := r.personal.ListSavedItemTags(ctx.UserContext(), userID)
	if err != nil {
		r.l.Error(err, "restapi - v1 - listSavedItemTags")

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	return ctx.Status(http.StatusOK).JSON(response.SavedItemTags{Items: tags, Total: len(tags)})
}

func readerLocationErrorResponse(ctx *fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, entity.ErrBookNotFound):
		return errorResponse(ctx, http.StatusNotFound, "book not found")
	case errors.Is(err, entity.ErrPageNotFound):
		return errorResponse(ctx, http.StatusBadRequest, "page not found")
	case errors.Is(err, entity.ErrHeadingNotFound):
		return errorResponse(ctx, http.StatusBadRequest, "heading not found")
	case errors.Is(err, entity.ErrInvalidReaderLocation):
		return errorResponse(ctx, http.StatusBadRequest, "invalid reader location")
	case errors.Is(err, entity.ErrInvalidReadingProgress):
		return errorResponse(ctx, http.StatusBadRequest, "invalid reading progress")
	default:
		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}
}

func savedItemErrorResponse(ctx *fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, entity.ErrSavedItemNotFound):
		return errorResponse(ctx, http.StatusNotFound, "saved item not found")
	case errors.Is(err, entity.ErrBookNotFound):
		return errorResponse(ctx, http.StatusNotFound, "book not found")
	case errors.Is(err, entity.ErrPageNotFound):
		return errorResponse(ctx, http.StatusBadRequest, "page not found")
	case errors.Is(err, entity.ErrHeadingNotFound):
		return errorResponse(ctx, http.StatusBadRequest, "heading not found")
	case errors.Is(err, entity.ErrQuranSurahNotFound):
		return errorResponse(ctx, http.StatusBadRequest, "quran surah not found")
	case errors.Is(err, entity.ErrQuranAyahNotFound):
		return errorResponse(ctx, http.StatusBadRequest, "quran ayah not found")
	case errors.Is(err, entity.ErrInvalidAyahKey):
		return errorResponse(ctx, http.StatusBadRequest, "invalid ayah key")
	case errors.Is(err, entity.ErrInvalidQuranRange):
		return errorResponse(ctx, http.StatusBadRequest, "invalid quran range")
	case errors.Is(err, entity.ErrInvalidSavedItem), errors.Is(err, entity.ErrInvalidReaderLocation):
		return errorResponse(ctx, http.StatusBadRequest, "invalid saved item")
	default:
		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}
}

func quranProgressErrorResponse(ctx *fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, entity.ErrProgressNotFound):
		return errorResponse(ctx, http.StatusNotFound, "progress not found")
	case errors.Is(err, entity.ErrQuranSurahNotFound):
		return errorResponse(ctx, http.StatusNotFound, "quran surah not found")
	case errors.Is(err, entity.ErrQuranAyahNotFound):
		return errorResponse(ctx, http.StatusNotFound, "quran ayah not found")
	case errors.Is(err, entity.ErrInvalidAyahKey):
		return errorResponse(ctx, http.StatusBadRequest, "invalid ayah key")
	case errors.Is(err, entity.ErrInvalidQuranProgress):
		return errorResponse(ctx, http.StatusBadRequest, "invalid quran progress")
	default:
		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}
}
