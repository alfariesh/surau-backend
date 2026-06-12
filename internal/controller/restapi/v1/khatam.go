package v1

import (
	"context"
	"errors"
	"net/http"

	"github.com/evrone/go-clean-template/internal/controller/restapi/v1/request"
	"github.com/evrone/go-clean-template/internal/controller/restapi/v1/response"
	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/gofiber/fiber/v2"
)

// @Summary     Start khatam cycle
// @Description Start a new Quran khatam cycle. Only one active cycle is allowed per user.
// @ID          start-khatam-cycle
// @Tags        me
// @Accept      json
// @Produce     json
// @Security    BearerAuth
// @Param       request body     request.StartKhatamCycle false "Optional cycle notes"
// @Success     201     {object} entity.QuranKhatamCycle
// @Failure     400     {object} response.Error
// @Failure     401     {object} response.Error
// @Failure     409     {object} response.Error
// @Failure     500     {object} response.Error
// @Router      /me/quran/khatam [post]
func (r *V1) startKhatamCycle(ctx *fiber.Ctx) error {
	userID, ok := ctx.Locals("userID").(string)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	var body request.StartKhatamCycle
	if len(ctx.Body()) > 0 {
		if err := ctx.BodyParser(&body); err != nil {
			return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
		}

		if err := r.v.Struct(body); err != nil {
			return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
		}
	}

	cycle, err := r.personal.StartKhatamCycle(ctx.UserContext(), userID, body.Notes)
	if err != nil {
		r.l.Error(err, "restapi - v1 - startKhatamCycle")

		return khatamErrorResponse(ctx, err)
	}

	return ctx.Status(http.StatusCreated).JSON(cycle)
}

// @Summary     Get active khatam cycle
// @Description Return the authenticated user's active khatam cycle with completed juz marks.
// @ID          get-active-khatam-cycle
// @Tags        me
// @Produce     json
// @Security    BearerAuth
// @Success     200 {object} entity.QuranKhatamCycle
// @Failure     401 {object} response.Error
// @Failure     404 {object} response.Error
// @Failure     500 {object} response.Error
// @Router      /me/quran/khatam [get]
func (r *V1) getActiveKhatamCycle(ctx *fiber.Ctx) error {
	userID, ok := ctx.Locals("userID").(string)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	cycle, err := r.personal.GetActiveKhatamCycle(ctx.UserContext(), userID)
	if err != nil {
		r.l.Error(err, "restapi - v1 - getActiveKhatamCycle")

		return khatamErrorResponse(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(cycle)
}

// @Summary     Mark khatam juz
// @Description Mark one juz (1-30) as completed on the active khatam cycle. Idempotent.
// @ID          mark-khatam-juz
// @Tags        me
// @Produce     json
// @Security    BearerAuth
// @Param       juz_number path     int true "Juz number (1-30)"
// @Success     200        {object} entity.QuranKhatamCycle
// @Failure     400        {object} response.Error
// @Failure     401        {object} response.Error
// @Failure     404        {object} response.Error
// @Failure     500        {object} response.Error
// @Router      /me/quran/khatam/juz/{juz_number} [put]
func (r *V1) markKhatamJuz(ctx *fiber.Ctx) error {
	return r.applyKhatamJuz(ctx, r.personal.MarkKhatamJuz, "restapi - v1 - markKhatamJuz")
}

// @Summary     Unmark khatam juz
// @Description Remove one juz mark (1-30) from the active khatam cycle. Idempotent.
// @ID          unmark-khatam-juz
// @Tags        me
// @Produce     json
// @Security    BearerAuth
// @Param       juz_number path     int true "Juz number (1-30)"
// @Success     200        {object} entity.QuranKhatamCycle
// @Failure     400        {object} response.Error
// @Failure     401        {object} response.Error
// @Failure     404        {object} response.Error
// @Failure     500        {object} response.Error
// @Router      /me/quran/khatam/juz/{juz_number} [delete]
func (r *V1) unmarkKhatamJuz(ctx *fiber.Ctx) error {
	return r.applyKhatamJuz(ctx, r.personal.UnmarkKhatamJuz, "restapi - v1 - unmarkKhatamJuz")
}

// @Summary     Complete khatam cycle
// @Description Complete the active khatam cycle. Requires all 30 juz to be marked; marking the 30th juz does not auto-complete so accidental marks stay reversible.
// @ID          complete-khatam-cycle
// @Tags        me
// @Produce     json
// @Security    BearerAuth
// @Success     200 {object} entity.QuranKhatamCycle
// @Failure     401 {object} response.Error
// @Failure     404 {object} response.Error
// @Failure     409 {object} response.Error
// @Failure     500 {object} response.Error
// @Router      /me/quran/khatam/complete [post]
func (r *V1) completeKhatamCycle(ctx *fiber.Ctx) error {
	userID, ok := ctx.Locals("userID").(string)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	cycle, err := r.personal.CompleteKhatamCycle(ctx.UserContext(), userID)
	if err != nil {
		r.l.Error(err, "restapi - v1 - completeKhatamCycle")

		return khatamErrorResponse(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(cycle)
}

// @Summary     List khatam history
// @Description Return the authenticated user's completed khatam cycles ordered by completion recency.
// @ID          list-khatam-history
// @Tags        me
// @Produce     json
// @Security    BearerAuth
// @Param       limit  query    int false "Limit" default(50)
// @Param       offset query    int false "Offset" default(0)
// @Success     200    {object} response.KhatamHistory
// @Failure     401    {object} response.Error
// @Failure     500    {object} response.Error
// @Router      /me/quran/khatam/history [get]
func (r *V1) listKhatamHistory(ctx *fiber.Ctx) error {
	userID, ok := ctx.Locals("userID").(string)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	cycles, total, err := r.personal.ListKhatamHistory(
		ctx.UserContext(),
		userID,
		queryInt(ctx, "limit", 50),
		queryInt(ctx, "offset", 0),
	)
	if err != nil {
		r.l.Error(err, "restapi - v1 - listKhatamHistory")

		return khatamErrorResponse(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(response.KhatamHistory{Cycles: cycles, Total: total})
}

func (r *V1) applyKhatamJuz(
	ctx *fiber.Ctx,
	apply func(c context.Context, userID string, juzNumber int) (entity.QuranKhatamCycle, error),
	operation string,
) error {
	userID, ok := ctx.Locals("userID").(string)
	if !ok {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	juzNumber, err := pathInt(ctx, "juz_number")
	if err != nil || juzNumber < 1 || juzNumber > entity.KhatamJuzTotal {
		return errorResponse(ctx, http.StatusBadRequest, "invalid juz_number")
	}

	cycle, err := apply(ctx.UserContext(), userID, juzNumber)
	if err != nil {
		r.l.Error(err, operation)

		return khatamErrorResponse(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(cycle)
}

func khatamErrorResponse(ctx *fiber.Ctx, err error) error {
	switch {
	case errors.Is(err, entity.ErrKhatamCycleNotFound):
		return errorResponse(ctx, http.StatusNotFound, "khatam cycle not found")
	case errors.Is(err, entity.ErrKhatamCycleActiveExists):
		return errorResponse(ctx, http.StatusConflict, "active khatam cycle already exists")
	case errors.Is(err, entity.ErrKhatamCycleIncomplete):
		return errorResponse(ctx, http.StatusConflict, "khatam cycle incomplete")
	case errors.Is(err, entity.ErrInvalidJuzNumber):
		return errorResponse(ctx, http.StatusBadRequest, "invalid juz_number")
	default:
		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}
}
