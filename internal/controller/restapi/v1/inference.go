package v1

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/alfariesh/surau-backend/internal/controller/restapi/middleware"
	_ "github.com/alfariesh/surau-backend/internal/controller/restapi/v1/response" // for swaggo
	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/usecase"
	"github.com/alfariesh/surau-backend/pkg/logger"
	"github.com/gofiber/fiber/v2"
)

const inferenceUsageMaxDays = 366

// InferenceInternalRouteManifest is the audited A-2 route registry for U-0.
var InferenceInternalRouteManifest = []struct { //nolint:gochecknoglobals // immutable security contract
	Method string
	Path   string
	Scope  string
}{
	{Method: fiber.MethodPost, Path: "/inference/invoke", Scope: entity.ServiceScopeInferenceInvoke},
	{Method: fiber.MethodPost, Path: "/inference/sessions", Scope: entity.ServiceScopeInferenceInvoke},
	{Method: fiber.MethodGet, Path: "/inference/prompts", Scope: entity.ServiceScopePromptRegistryManage},
	{Method: fiber.MethodPost, Path: "/inference/prompts", Scope: entity.ServiceScopePromptRegistryManage},
	{Method: fiber.MethodGet, Path: "/inference/budget", Scope: entity.ServiceScopeInferenceBudgetManage},
	{Method: fiber.MethodPatch, Path: "/inference/budget", Scope: entity.ServiceScopeInferenceBudgetManage},
}

type inferenceInternal struct {
	inference usecase.Inference
}

// NewInferenceInternalRoutes mounts every U-0 route through live A-2 scope
// authentication and durable request audit.
func NewInferenceInternalRoutes(
	group fiber.Router,
	inference usecase.Inference,
	identities usecase.ServiceIdentity,
	l logger.Interface,
) {
	controller := &inferenceInternal{inference: inference}
	register := func(method, path, scope string, handler fiber.Handler) {
		group.Add(method, path, middleware.RequireServicePrincipal(identities, scope, l), handler)
	}
	register(
		InferenceInternalRouteManifest[0].Method,
		InferenceInternalRouteManifest[0].Path,
		InferenceInternalRouteManifest[0].Scope,
		controller.invoke,
	)
	register(
		InferenceInternalRouteManifest[1].Method,
		InferenceInternalRouteManifest[1].Path,
		InferenceInternalRouteManifest[1].Scope,
		controller.createSession,
	)
	register(
		InferenceInternalRouteManifest[2].Method,
		InferenceInternalRouteManifest[2].Path,
		InferenceInternalRouteManifest[2].Scope,
		controller.prompts,
	)
	register(
		InferenceInternalRouteManifest[3].Method,
		InferenceInternalRouteManifest[3].Path,
		InferenceInternalRouteManifest[3].Scope,
		controller.registerPrompt,
	)
	register(
		InferenceInternalRouteManifest[4].Method,
		InferenceInternalRouteManifest[4].Path,
		InferenceInternalRouteManifest[4].Scope,
		controller.budget,
	)
	register(
		InferenceInternalRouteManifest[5].Method,
		InferenceInternalRouteManifest[5].Path,
		InferenceInternalRouteManifest[5].Scope,
		controller.updateBudget,
	)
}

func (c *inferenceInternal) invoke(ctx *fiber.Ctx) error {
	if c.inference == nil {
		return errorResponse(ctx, http.StatusServiceUnavailable, "inference is not configured")
	}

	var input entity.InferenceInvoke
	if err := ctx.BodyParser(&input); err != nil ||
		strings.TrimSpace(input.TaskKey) == "" || input.Variables == nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid inference request")
	}

	result, err := c.inference.Invoke(ctx.UserContext(), input)
	if err != nil {
		return inferenceErrorResponse(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(result)
}

func (c *inferenceInternal) createSession(ctx *fiber.Ctx) error {
	var input struct {
		TaskKey string `json:"task_key"`
	}
	if err := ctx.BodyParser(&input); err != nil || strings.TrimSpace(input.TaskKey) == "" {
		return errorResponse(ctx, http.StatusBadRequest, "invalid inference request")
	}

	session, err := c.inference.CreateSession(ctx.UserContext(), input.TaskKey)
	if err != nil {
		return inferenceErrorResponse(ctx, err)
	}

	return ctx.Status(http.StatusCreated).JSON(session)
}

func (c *inferenceInternal) prompts(ctx *fiber.Ctx) error {
	items, err := c.inference.Registry(ctx.UserContext())
	if err != nil {
		return inferenceErrorResponse(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(fiber.Map{"items": items, "total": len(items)})
}

func (c *inferenceInternal) registerPrompt(ctx *fiber.Ctx) error {
	var manifest entity.InferencePromptManifest
	if err := ctx.BodyParser(&manifest); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid inference prompt")
	}

	if err := c.inference.RegisterPrompt(ctx.UserContext(), manifest); err != nil {
		return inferenceErrorResponse(ctx, err)
	}

	return ctx.SendStatus(http.StatusCreated)
}

func (c *inferenceInternal) budget(ctx *fiber.Ctx) error {
	budget, err := c.inference.Budget(ctx.UserContext())
	if err != nil {
		return inferenceErrorResponse(ctx, err)
	}

	setInferenceBudgetETag(ctx, budget.Revision)

	return ctx.Status(http.StatusOK).JSON(budget)
}

func (c *inferenceInternal) updateBudget(ctx *fiber.Ctx) error {
	var patch entity.InferenceBudgetPatch
	if err := ctx.BodyParser(&patch); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid inference budget")
	}

	expected, err := inferenceBudgetIfMatch(ctx, c.inference)
	if err != nil {
		return inferenceErrorResponse(ctx, err)
	}

	budget, err := c.inference.UpdateBudget(ctx.UserContext(), "", expected, patch)
	if err != nil {
		return inferenceErrorResponse(ctx, err)
	}

	setInferenceBudgetETag(ctx, budget.Revision)

	return ctx.Status(http.StatusOK).JSON(budget)
}

// @Summary List daily LLM usage and cost
// @Description Shows metered token/cost totals. Requires manage-service-tokens.
// @ID admin-inference-usage
// @Tags admin,inference
// @Produce json
// @Param from query string false "RFC3339 start; default 30 days ago"
// @Param to query string false "RFC3339 end; default now"
// @Param group_by query string false "day, task, provider, or model" default(day)
// @Success 200 {object} entity.InferenceUsageList
// @Failure 400 {object} response.Error
// @Security BearerAuth
// @Router /admin/inference/usage [get]
func (r *V1) adminInferenceUsage(ctx *fiber.Ctx) error {
	to := time.Now().UTC()
	from := to.AddDate(0, 0, -30)

	var err error
	if raw := strings.TrimSpace(ctx.Query("from")); raw != "" {
		from, err = time.Parse(time.RFC3339, raw)
		if err != nil {
			return errorResponse(ctx, http.StatusBadRequest, "invalid inference usage range")
		}
	}

	if raw := strings.TrimSpace(ctx.Query("to")); raw != "" {
		to, err = time.Parse(time.RFC3339, raw)
		if err != nil {
			return errorResponse(ctx, http.StatusBadRequest, "invalid inference usage range")
		}
	}

	if !from.Before(to) || to.Sub(from) > inferenceUsageMaxDays*24*time.Hour {
		return errorResponse(ctx, http.StatusBadRequest, "invalid inference usage range")
	}

	items, err := r.inference.Usage(ctx.UserContext(), from, to, ctx.Query("group_by", "day"))
	if err != nil {
		return inferenceErrorResponse(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(entity.InferenceUsageList{Items: items, Total: len(items)})
}

// @Summary List active LLM registry
// @ID admin-inference-registry
// @Tags admin,inference
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Security BearerAuth
// @Router /admin/inference/registry [get]
func (r *V1) adminInferenceRegistry(ctx *fiber.Ctx) error {
	items, err := r.inference.Registry(ctx.UserContext())
	if err != nil {
		return inferenceErrorResponse(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(fiber.Map{"items": items, "total": len(items)})
}

// @Summary Read LLM budget guard
// @ID admin-inference-budget
// @Tags admin,inference
// @Produce json
// @Success 200 {object} entity.InferenceBudgetStatus
// @Security BearerAuth
// @Router /admin/inference/budget [get]
func (r *V1) adminInferenceBudget(ctx *fiber.Ctx) error {
	budget, err := r.inference.Budget(ctx.UserContext())
	if err != nil {
		return inferenceErrorResponse(ctx, err)
	}

	setInferenceBudgetETag(ctx, budget.Revision)

	return ctx.Status(http.StatusOK).JSON(budget)
}

// @Summary Replace LLM budget policy
// @Description Creates an append-only policy revision. Requires fresh MFA, If-Match and reason.
// @ID admin-update-inference-budget
// @Tags admin,inference
// @Accept json
// @Produce json
// @Param If-Match header string true "Current budget revision ETag or *"
// @Param request body entity.InferenceBudgetPatch true "Budget revision"
// @Success 200 {object} entity.InferenceBudgetStatus
// @Failure 412 {object} response.Error
// @Failure 428 {object} response.Error
// @Security BearerAuth
// @Router /admin/inference/budget [patch]
func (r *V1) adminUpdateInferenceBudget(ctx *fiber.Ctx) error {
	actorID, ok := ctx.Locals("userID").(string)
	if !ok || actorID == "" {
		return errorResponse(ctx, http.StatusUnauthorized, "unauthorized")
	}

	var patch entity.InferenceBudgetPatch
	if err := ctx.BodyParser(&patch); err != nil ||
		strings.TrimSpace(patch.Reason) == "" {
		return errorResponse(ctx, http.StatusBadRequest, "invalid inference budget")
	}

	expected, err := inferenceBudgetIfMatch(ctx, r.inference)
	if err != nil {
		return inferenceErrorResponse(ctx, err)
	}

	budget, err := r.inference.UpdateBudget(ctx.UserContext(), actorID, expected, patch)
	if err != nil {
		return inferenceErrorResponse(ctx, err)
	}

	setInferenceBudgetETag(ctx, budget.Revision)

	return ctx.Status(http.StatusOK).JSON(budget)
}

func inferenceBudgetIfMatch(
	ctx *fiber.Ctx,
	inference usecase.Inference,
) (int64, error) {
	value := strings.TrimSpace(ctx.Get(fiber.HeaderIfMatch))
	if value == "" {
		return 0, entity.ErrPreconditionRequired
	}

	if value == "*" {
		status, err := inference.Budget(ctx.UserContext())
		if err != nil {
			return 0, err
		}

		return status.Revision, nil
	}

	value = strings.Trim(value, `"`)
	value = strings.TrimPrefix(value, "inference-budget-")

	revision, err := strconv.ParseInt(value, 10, 64)
	if err != nil || revision <= 0 {
		return 0, entity.ErrPreconditionFailed
	}

	return revision, nil
}

func setInferenceBudgetETag(ctx *fiber.Ctx, revision int64) {
	ctx.Set(fiber.HeaderETag, fmt.Sprintf(`"inference-budget-%d"`, revision))
}

func inferenceErrorResponse(ctx *fiber.Ctx, err error) error {
	var exceeded *entity.InferenceBudgetExceededError
	switch {
	case errors.As(err, &exceeded):
		retryAfter := int64(math.Ceil(max(exceeded.RetryAfter.Seconds(), 1)))
		ctx.Set(fiber.HeaderRetryAfter, strconv.FormatInt(retryAfter, 10))

		return errorResponseWithRetryDetails(
			ctx, http.StatusServiceUnavailable, "inference budget exceeded",
			retryAfter, fiber.Map{
				"reset_at": exceeded.ResetAt,
				"window":   exceeded.Window,
			},
		)
	case errors.Is(err, entity.ErrInferenceProviderFailure),
		errors.Is(err, entity.ErrInferenceSessionPinned):
		return errorResponse(ctx, http.StatusServiceUnavailable, "inference provider unavailable")
	case errors.Is(err, entity.ErrInferenceRouteMissing),
		errors.Is(err, entity.ErrRAGNotConfigured):
		return errorResponse(ctx, http.StatusServiceUnavailable, "inference is not configured")
	case errors.Is(err, entity.ErrInferenceRegistryConflict):
		return errorResponse(ctx, http.StatusConflict, "inference registry conflict")
	case errors.Is(err, entity.ErrPreconditionRequired):
		return errorResponse(ctx, http.StatusPreconditionRequired, "if-match header required")
	case errors.Is(err, entity.ErrPreconditionFailed):
		return errorResponse(ctx, http.StatusPreconditionFailed, "precondition failed")
	default:
		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}
}

func errorResponseWithRetryDetails(
	ctx *fiber.Ctx,
	status int,
	message string,
	retryAfter int64,
	details any,
) error {
	response := fiber.Map{
		"error": message, "code": "inference_budget_exceeded",
		"message": message, "retry_after": retryAfter,
		"request_id": requestID(ctx), "details": details,
	}

	return ctx.Status(status).JSON(response)
}
