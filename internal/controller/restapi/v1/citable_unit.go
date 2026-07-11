package v1

import (
	"errors"
	"net/http"

	"github.com/alfariesh/surau-backend/internal/controller/restapi/v1/response"
	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/gofiber/fiber/v2"
)

// @Summary     Get one Citable Unit for editorial review
// @Description Return normalized text, lifecycle, provenance, generation identity, and active lineage successors. Requires CapReviewEditorial.
// @ID          editorial-get-citable-unit
// @Tags        editorial,citable-units
// @Produce     json
// @Param       id  path     string true "Citable Unit UUID"
// @Success     200 {object} response.EditorialCitableUnitResolution
// @Failure     401 {object} response.Error
// @Failure     403 {object} response.Error
// @Failure     404 {object} response.Error
// @Failure     500 {object} response.Error
// @Security    BearerAuth
// @Router      /editorial/citable-units/{id} [get]
func (r *V1) editorialGetCitableUnit(ctx *fiber.Ctx) error {
	resolved, err := r.unitRegistry.ResolveUnit(restAuthContext(ctx), ctx.Params("id"))
	if err != nil {
		if r.l != nil {
			r.l.Error(err, "restapi - v1 - editorialGetCitableUnit")
		}

		if errors.Is(err, entity.ErrUnitNotFound) {
			return errorResponse(ctx, http.StatusNotFound, "citable unit not found")
		}

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	return ctx.Status(http.StatusOK).JSON(response.NewEditorialCitableUnitResolution(&resolved))
}
