package v1

import (
	"errors"
	"time"

	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/gofiber/fiber/v2"
)

func (r *V1) checkEditorialDraftIfMatch(
	ctx *fiber.Ctx,
	operation string,
	loadUpdatedAt func() (time.Time, error),
) (bool, error) {
	if !hasIfMatch(ctx) {
		return true, nil
	}

	updatedAt, err := loadUpdatedAt()
	if err != nil {
		if errors.Is(err, entity.ErrDraftNotFound) {
			return checkUpdatedAtIfMatch(ctx, time.Time{}), nil
		}

		r.logEditorialError(err, operation)
		return false, r.editorialError(ctx, err)
	}

	return checkUpdatedAtIfMatch(ctx, updatedAt), nil
}

func (r *V1) checkEditorialResourceIfMatch(
	ctx *fiber.Ctx,
	operation string,
	loadUpdatedAt func() (time.Time, error),
) (bool, error) {
	if !hasIfMatch(ctx) {
		return true, nil
	}

	updatedAt, err := loadUpdatedAt()
	if err != nil {
		r.logEditorialError(err, operation)
		return false, r.editorialError(ctx, err)
	}

	return checkUpdatedAtIfMatch(ctx, updatedAt), nil
}

func (r *V1) checkProductionProjectIfMatch(ctx *fiber.Ctx, operation string) (bool, error) {
	return r.checkEditorialResourceIfMatch(ctx, operation, func() (time.Time, error) {
		project, err := r.editorial.ProductionProject(ctx.UserContext(), ctx.Params("id"))
		if err != nil {
			return time.Time{}, err
		}

		return project.UpdatedAt, nil
	})
}
