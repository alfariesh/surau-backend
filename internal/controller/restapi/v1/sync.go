package v1

import (
	"errors"
	"net/http"
	"time"

	"github.com/evrone/go-clean-template/internal/controller/restapi/v1/request"
	"github.com/evrone/go-clean-template/internal/controller/restapi/v1/response"
	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/gofiber/fiber/v2"
)

// @Summary     Sync personal reader data
// @Description Return reading progress (kitab and Quran), saved items, and khatam cycles changed at or after the since cursor; omit since for a full snapshot. Delivery is at-least-once (a server-side overlap window re-sends recent rows), so clients must upsert idempotently by key and store server_time as the next cursor. saved_item_ids always lists every current saved-item ID for delete reconciliation.
// @ID          sync-personal-data
// @Tags        me
// @Produce     json
// @Security    BearerAuth
// @Param       since query    string false "RFC3339 cursor from the previous response's server_time" example(2026-06-11T00:00:00Z)
// @Success     200   {object} entity.PersonalSyncSnapshot
// @Failure     400   {object} response.Error
// @Failure     401   {object} response.Error
// @Failure     500   {object} response.Error
// @Router      /me/sync [get]
func (r *V1) syncPersonalData(ctx *fiber.Ctx) error {
	userID, ok := ctx.Locals("userID").(string)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	var since *time.Time
	if raw := ctx.Query("since"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return errorResponse(ctx, http.StatusBadRequest, "invalid since")
		}
		since = &parsed
	}

	snapshot, err := r.personal.SyncPersonalData(ctx.UserContext(), userID, since)
	if err != nil {
		r.l.Error(err, "restapi - v1 - syncPersonalData")

		if errors.Is(err, entity.ErrInvalidSyncSince) {
			return errorResponse(ctx, http.StatusBadRequest, "invalid since")
		}

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	return ctx.Status(http.StatusOK).JSON(snapshot)
}

// @Summary     Batch save reading progress
// @Description Replay an offline autosave queue in one request (max 100 kitab and 100 Quran entries). Entries are processed in order and reported one-to-one in the response; stale client_observed_at entries are accepted but never roll progress back. Domain failures (e.g. deleted book) mark only that entry as error.
// @ID          batch-save-progress
// @Tags        me
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       request body     request.BatchProgress true "Offline progress queue"
// @Success     200     {object} response.BatchProgressResults
// @Failure     400     {object} response.Error
// @Failure     401     {object} response.Error
// @Failure     500     {object} response.Error
// @Router      /me/progress/batch [post]
func (r *V1) batchSaveProgress(ctx *fiber.Ctx) error {
	userID, ok := ctx.Locals("userID").(string)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	var body request.BatchProgress
	if err := ctx.BodyParser(&body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	if err := r.v.Struct(body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	if len(body.Kitab) == 0 && len(body.Quran) == 0 {
		return errorResponse(ctx, http.StatusBadRequest, "empty batch")
	}

	results := response.BatchProgressResults{
		Kitab: make([]response.BatchKitabProgressResult, 0, len(body.Kitab)),
		Quran: make([]response.BatchQuranProgressResult, 0, len(body.Quran)),
	}

	for _, entry := range body.Kitab {
		progress, err := r.personal.SaveProgress(
			ctx.UserContext(),
			userID,
			entry.BookID,
			entry.PageID,
			entry.HeadingID,
			entry.ProgressPercent,
			entry.ClientObservedAt,
		)
		if err != nil {
			message, expected := kitabProgressEntryError(err)
			if !expected {
				r.l.Error(err, "restapi - v1 - batchSaveProgress - kitab")

				return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
			}

			results.Kitab = append(results.Kitab, response.BatchKitabProgressResult{Status: "error", Error: &message})

			continue
		}

		results.Kitab = append(results.Kitab, response.BatchKitabProgressResult{Status: "ok", Progress: &progress})
	}

	for _, entry := range body.Quran {
		progress, err := r.personal.SaveQuranProgress(ctx.UserContext(), userID, entry.AyahKey, entry.ClientObservedAt)
		if err != nil {
			message, expected := quranProgressEntryError(err)
			if !expected {
				r.l.Error(err, "restapi - v1 - batchSaveProgress - quran")

				return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
			}

			results.Quran = append(results.Quran, response.BatchQuranProgressResult{Status: "error", Error: &message})

			continue
		}

		results.Quran = append(results.Quran, response.BatchQuranProgressResult{Status: "ok", Progress: &progress})
	}

	return ctx.Status(http.StatusOK).JSON(results)
}

// kitabProgressEntryError maps expected kitab progress failures to a
// per-entry message; unexpected errors abort the whole batch.
func kitabProgressEntryError(err error) (string, bool) {
	switch {
	case errors.Is(err, entity.ErrBookNotFound):
		return "book not found", true
	case errors.Is(err, entity.ErrPageNotFound):
		return "page not found", true
	case errors.Is(err, entity.ErrHeadingNotFound):
		return "heading not found", true
	case errors.Is(err, entity.ErrInvalidReaderLocation):
		return "invalid reader location", true
	case errors.Is(err, entity.ErrInvalidReadingProgress):
		return "invalid reading progress", true
	default:
		return "", false
	}
}

// quranProgressEntryError maps expected Quran progress failures to a
// per-entry message; unexpected errors abort the whole batch.
func quranProgressEntryError(err error) (string, bool) {
	switch {
	case errors.Is(err, entity.ErrInvalidAyahKey):
		return "invalid ayah key", true
	case errors.Is(err, entity.ErrQuranSurahNotFound):
		return "quran surah not found", true
	case errors.Is(err, entity.ErrQuranAyahNotFound):
		return "quran ayah not found", true
	case errors.Is(err, entity.ErrInvalidQuranProgress):
		return "invalid quran progress", true
	default:
		return "", false
	}
}
