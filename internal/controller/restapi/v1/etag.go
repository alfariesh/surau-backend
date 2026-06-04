package v1

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
)

func jsonWithUpdatedAtETag(ctx *fiber.Ctx, status int, body any, updatedAt time.Time) error {
	setUpdatedAtETag(ctx, updatedAt)

	return ctx.Status(status).JSON(body)
}

func setUpdatedAtETag(ctx *fiber.Ctx, updatedAt time.Time) {
	if updatedAt.IsZero() {
		return
	}

	ctx.Set(fiber.HeaderETag, updatedAtETag(updatedAt))
}

func checkUpdatedAtIfMatch(ctx *fiber.Ctx, updatedAt time.Time) bool {
	if !hasIfMatch(ctx) {
		return true
	}

	if updatedAtETagMatches(requestHeader(ctx, fiber.HeaderIfMatch), updatedAt) {
		return true
	}

	if err := errorResponse(ctx, http.StatusPreconditionFailed, "precondition failed"); err != nil {
		return false
	}

	return false
}

func hasIfMatch(ctx *fiber.Ctx) bool {
	return requestHeader(ctx, fiber.HeaderIfMatch) != ""
}

func requestHeader(ctx *fiber.Ctx, key string) string {
	if value := strings.TrimSpace(ctx.Get(key)); value != "" {
		return value
	}

	for headerKey, values := range ctx.GetReqHeaders() {
		if !strings.EqualFold(headerKey, key) {
			continue
		}

		if len(values) == 0 {
			return ""
		}

		return strings.TrimSpace(values[0])
	}

	return ""
}

func updatedAtETagMatches(header string, updatedAt time.Time) bool {
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" {
			return true
		}

		candidate = strings.TrimSpace(strings.TrimPrefix(candidate, "W/"))
		if !updatedAt.IsZero() && candidate == updatedAtETag(updatedAt) {
			return true
		}
	}

	return false
}

func updatedAtETag(updatedAt time.Time) string {
	return strconv.Quote(strconv.FormatInt(updatedAt.UTC().UnixNano(), 10))
}
