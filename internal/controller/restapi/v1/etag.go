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

// parseIfMatch extracts the expected updated_at from an If-Match header so the
// repo layer can enforce it atomically. Returns:
//   - (nil, false, true) when the header is absent,
//   - (nil, true, true) for "*" (precondition present but unconditional),
//   - (&t, true, true) for a parseable updated_at ETag,
//   - (nil, true, false) when present but no candidate can ever match (412).
func parseIfMatch(ctx *fiber.Ctx) (expected *time.Time, present, ok bool) {
	header := requestHeader(ctx, fiber.HeaderIfMatch)
	if header == "" {
		return nil, false, true
	}

	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" {
			return nil, true, true
		}

		candidate = strings.TrimSpace(strings.TrimPrefix(candidate, "W/"))

		unquoted, err := strconv.Unquote(candidate)
		if err != nil {
			continue
		}

		nanos, err := strconv.ParseInt(unquoted, 10, 64)
		if err != nil {
			continue
		}

		parsed := time.Unix(0, nanos).UTC()

		return &parsed, true, true
	}

	return nil, true, false
}
