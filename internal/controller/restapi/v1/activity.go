package v1

import (
	"errors"
	"net/http"

	// Imported for swagger type resolution of response.Error annotations.
	_ "github.com/alfariesh/surau-backend/internal/controller/restapi/v1/response"
	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/gofiber/fiber/v2"
)

// @Summary     Get reading activity
// @Description Return daily reading-activity buckets and an aggregate for [from, to]. Defaults to the last 30 days ending at to (or the server's UTC date). Activity days follow the local calendar date of each save's client_observed_at offset. Max range 366 days.
// @ID          get-reading-activity
// @Tags        me
// @Produce     json
// @Security    BearerAuth
// @Param       from query    string false "Start date (YYYY-MM-DD)" example(2026-05-14)
// @Param       to   query    string false "End date (YYYY-MM-DD), defaults to today" example(2026-06-12)
// @Success     200  {object} entity.ReadingActivitySummary
// @Failure     400  {object} response.Error
// @Failure     401  {object} response.Error
// @Failure     500  {object} response.Error
// @Router      /me/activity [get]
func (r *V1) getReadingActivity(ctx *fiber.Ctx) error {
	userID, ok := ctx.Locals("userID").(string)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	summary, err := r.personal.GetReadingActivity(ctx.UserContext(), userID, ctx.Query("from"), ctx.Query("to"))
	if err != nil {
		r.l.Error(err, "restapi - v1 - getReadingActivity")

		return activityErrorResponse(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(summary)
}

// @Summary     Get reading streak
// @Description Return the consecutive-day reading streak. Pass today as the client's local date so the day boundary matches the device; the current streak counts runs ending today or yesterday.
// @ID          get-reading-streak
// @Tags        me
// @Produce     json
// @Security    BearerAuth
// @Param       today query    string false "Client local date (YYYY-MM-DD), defaults to server UTC date" example(2026-06-12)
// @Success     200   {object} entity.ReadingStreak
// @Failure     400   {object} response.Error
// @Failure     401   {object} response.Error
// @Failure     500   {object} response.Error
// @Router      /me/activity/streak [get]
func (r *V1) getReadingStreak(ctx *fiber.Ctx) error {
	userID, ok := ctx.Locals("userID").(string)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	streak, err := r.personal.GetReadingStreak(ctx.UserContext(), userID, ctx.Query("today"))
	if err != nil {
		r.l.Error(err, "restapi - v1 - getReadingStreak")

		return activityErrorResponse(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(streak)
}

func activityErrorResponse(ctx *fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, entity.ErrInvalidActivityDate):
		return errorResponse(ctx, http.StatusBadRequest, "invalid activity date")
	case errors.Is(err, entity.ErrInvalidActivityRange):
		return errorResponse(ctx, http.StatusBadRequest, "invalid activity range")
	default:
		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}
}
