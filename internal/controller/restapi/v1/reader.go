package v1

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/evrone/go-clean-template/internal/controller/restapi/v1/request"
	"github.com/evrone/go-clean-template/internal/controller/restapi/v1/response"
	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/gofiber/fiber/v2"
)

func (r *V1) listCategories(ctx *fiber.Ctx) error {
	categories, err := r.reader.Categories(ctx.UserContext(), ctx.Query("lang"))
	if err != nil {
		r.l.Error(err, "restapi - v1 - listCategories")

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	return ctx.Status(http.StatusOK).JSON(categories)
}

func (r *V1) listAuthors(ctx *fiber.Ctx) error {
	authors, total, err := r.reader.Authors(
		ctx.UserContext(),
		ctx.Query("q"),
		queryInt(ctx, "limit", 50),
		queryInt(ctx, "offset", 0),
		ctx.Query("lang"),
	)
	if err != nil {
		r.l.Error(err, "restapi - v1 - listAuthors")

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	return ctx.Status(http.StatusOK).JSON(response.AuthorList{Authors: authors, Total: total})
}

func (r *V1) listBooks(ctx *fiber.Ctx) error {
	categoryID, err := optionalQueryInt(ctx, "category_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid category_id")
	}

	authorID, err := optionalQueryInt(ctx, "author_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid author_id")
	}

	hasContent, err := optionalQueryBool(ctx, "has_content")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid has_content")
	}

	books, total, err := r.reader.Books(
		ctx.UserContext(),
		ctx.Query("q"),
		categoryID,
		authorID,
		hasContent,
		queryInt(ctx, "limit", 50),
		queryInt(ctx, "offset", 0),
		ctx.Query("lang"),
	)
	if err != nil {
		r.l.Error(err, "restapi - v1 - listBooks")

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	return ctx.Status(http.StatusOK).JSON(response.BookList{Books: books, Total: total})
}

func (r *V1) getBook(ctx *fiber.Ctx) error {
	bookID, err := pathInt(ctx, "book_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid book_id")
	}

	book, err := r.reader.Book(ctx.UserContext(), bookID, ctx.Query("lang"))
	if err != nil {
		r.l.Error(err, "restapi - v1 - getBook")

		if errors.Is(err, entity.ErrBookNotFound) {
			return errorResponse(ctx, http.StatusNotFound, "book not found")
		}

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	return ctx.Status(http.StatusOK).JSON(book)
}

func (r *V1) askBookRAG(ctx *fiber.Ctx) error {
	if r.bookRAG == nil {
		return errorResponse(ctx, http.StatusServiceUnavailable, "rag is not configured")
	}

	bookID, err := pathInt(ctx, "book_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid book_id")
	}

	var body request.BookRAG
	if err = ctx.BodyParser(&body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}
	if err = r.v.Struct(body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	lang := ctx.Query("lang")
	if body.Stream {
		return r.streamBookRAG(ctx, bookID, lang, body)
	}

	answer, err := r.bookRAG.AskBook(
		ctx.UserContext(),
		bookID,
		body.Question,
		lang,
		body.MaxCitations,
		body.IncludeTrace,
	)
	if err != nil {
		r.l.Error(err, "restapi - v1 - askBookRAG")

		return r.bookRAGErrorResponse(ctx, err)
	}

	return ctx.Status(http.StatusOK).JSON(answer)
}

func (r *V1) streamBookRAG(ctx *fiber.Ctx, bookID int, lang string, body request.BookRAG) error {
	userCtx := ctx.UserContext()
	ctx.Set("Content-Type", "text/event-stream")
	ctx.Set("Cache-Control", "no-cache")
	ctx.Set("Connection", "keep-alive")
	ctx.Set("X-Accel-Buffering", "no")

	ctx.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
		emit := func(event string, payload any) error {
			return writeSSEEvent(w, event, payload)
		}

		if err := r.bookRAG.AskBookStream(
			userCtx,
			bookID,
			body.Question,
			lang,
			body.MaxCitations,
			body.IncludeTrace,
			emit,
		); err != nil {
			r.l.Error(err, "restapi - v1 - streamBookRAG")
		}
	})

	return nil
}

func (r *V1) bookRAGErrorResponse(ctx *fiber.Ctx, err error) error {
	if errors.Is(err, entity.ErrInvalidQuestion) {
		return errorResponse(ctx, http.StatusBadRequest, "invalid question")
	}
	if errors.Is(err, entity.ErrBookNotFound) {
		return errorResponse(ctx, http.StatusNotFound, "book not found")
	}
	if errors.Is(err, entity.ErrRAGNotConfigured) {
		return errorResponse(ctx, http.StatusServiceUnavailable, "rag llm is not configured")
	}
	if errors.Is(err, entity.ErrRAGEvidenceNotFound) {
		return errorResponse(ctx, http.StatusNotFound, "rag evidence not found")
	}

	return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
}

func writeSSEEvent(w *bufio.Writer, event string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		data, _ = json.Marshal(map[string]string{"error": "failed to encode event"})
	}
	if _, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data); err != nil {
		return err
	}

	return w.Flush()
}

func (r *V1) listBookPages(ctx *fiber.Ctx) error {
	bookID, err := pathInt(ctx, "book_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid book_id")
	}

	pages, total, err := r.reader.Pages(
		ctx.UserContext(),
		bookID,
		queryInt(ctx, "limit", 50),
		queryInt(ctx, "offset", 0),
	)
	if err != nil {
		r.l.Error(err, "restapi - v1 - listBookPages")

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	return ctx.Status(http.StatusOK).JSON(response.PageList{Pages: pages, Total: total})
}

func (r *V1) getBookPage(ctx *fiber.Ctx) error {
	bookID, err := pathInt(ctx, "book_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid book_id")
	}

	pageID, err := pathInt(ctx, "page_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid page_id")
	}

	page, err := r.reader.Page(ctx.UserContext(), bookID, pageID)
	if err != nil {
		r.l.Error(err, "restapi - v1 - getBookPage")

		if errors.Is(err, entity.ErrPageNotFound) {
			return errorResponse(ctx, http.StatusNotFound, "page not found")
		}

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	return ctx.Status(http.StatusOK).JSON(page)
}

func (r *V1) listBookHeadings(ctx *fiber.Ctx) error {
	bookID, err := pathInt(ctx, "book_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid book_id")
	}

	headings, err := r.reader.Headings(ctx.UserContext(), bookID, ctx.Query("q"))
	if err != nil {
		r.l.Error(err, "restapi - v1 - listBookHeadings")

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	return ctx.Status(http.StatusOK).JSON(headings)
}

func (r *V1) getBookSection(ctx *fiber.Ctx) error {
	bookID, err := pathInt(ctx, "book_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid book_id")
	}

	headingID, err := pathInt(ctx, "heading_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid heading_id")
	}

	section, err := r.reader.Section(ctx.UserContext(), bookID, headingID, ctx.Query("lang"))
	if err != nil {
		r.l.Error(err, "restapi - v1 - getBookSection")

		if errors.Is(err, entity.ErrHeadingNotFound) {
			return errorResponse(ctx, http.StatusNotFound, "heading not found")
		}

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	return ctx.Status(http.StatusOK).JSON(section)
}

func (r *V1) listBookTOC(ctx *fiber.Ctx) error {
	bookID, err := pathInt(ctx, "book_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid book_id")
	}

	includeAudioValue, err := optionalQueryBool(ctx, "include_audio")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid include_audio")
	}

	includeAudio := false
	if includeAudioValue != nil {
		includeAudio = *includeAudioValue
	}

	toc, err := r.reader.TOC(ctx.UserContext(), bookID, ctx.Query("lang"), includeAudio)
	if err != nil {
		r.l.Error(err, "restapi - v1 - listBookTOC")

		if errors.Is(err, entity.ErrBookNotFound) {
			return errorResponse(ctx, http.StatusNotFound, "book not found")
		}

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	return ctx.Status(http.StatusOK).JSON(toc)
}

func (r *V1) readBookTOCSection(ctx *fiber.Ctx) error {
	bookID, err := pathInt(ctx, "book_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid book_id")
	}

	headingID, err := pathInt(ctx, "heading_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid heading_id")
	}

	section, err := r.reader.TOCRead(ctx.UserContext(), bookID, headingID, ctx.Query("lang"))
	if err != nil {
		r.l.Error(err, "restapi - v1 - readBookTOCSection")

		if errors.Is(err, entity.ErrBookNotFound) {
			return errorResponse(ctx, http.StatusNotFound, "book not found")
		}

		if errors.Is(err, entity.ErrHeadingNotFound) {
			return errorResponse(ctx, http.StatusNotFound, "heading not found")
		}

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	return ctx.Status(http.StatusOK).JSON(section)
}

func (r *V1) getBookTOCPlaylist(ctx *fiber.Ctx) error {
	bookID, err := pathInt(ctx, "book_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid book_id")
	}

	headingID, err := pathInt(ctx, "heading_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid heading_id")
	}

	playlist, err := r.reader.TOCPlaylist(ctx.UserContext(), bookID, headingID, ctx.Query("lang"))
	if err != nil {
		r.l.Error(err, "restapi - v1 - getBookTOCPlaylist")

		if errors.Is(err, entity.ErrBookNotFound) {
			return errorResponse(ctx, http.StatusNotFound, "book not found")
		}

		if errors.Is(err, entity.ErrHeadingNotFound) {
			return errorResponse(ctx, http.StatusNotFound, "heading not found")
		}

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	return ctx.Status(http.StatusOK).JSON(playlist)
}

func (r *V1) createTranslationFeedback(ctx *fiber.Ctx) error {
	bookID, err := pathInt(ctx, "book_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid book_id")
	}

	headingID, err := pathInt(ctx, "heading_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid heading_id")
	}

	var body request.CreateTranslationFeedback
	if err = ctx.BodyParser(&body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	if err = r.v.Struct(body); err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid request body")
	}

	userAgent := ctx.Get("User-Agent")
	clientIP := ctx.IP()
	feedback, err := r.reader.CreateTranslationFeedback(
		ctx.UserContext(),
		bookID,
		headingID,
		ctx.Query("lang"),
		body.Vote,
		body.Reason,
		body.Note,
		body.ClientID,
		&userAgent,
		&clientIP,
	)
	if err != nil {
		r.l.Error(err, "restapi - v1 - createTranslationFeedback")

		if errors.Is(err, entity.ErrBookNotFound) {
			return errorResponse(ctx, http.StatusNotFound, "book not found")
		}

		if errors.Is(err, entity.ErrHeadingNotFound) {
			return errorResponse(ctx, http.StatusNotFound, "heading not found")
		}

		if errors.Is(err, entity.ErrTranslationNotFound) {
			return errorResponse(ctx, http.StatusNotFound, "translation not found")
		}

		if errors.Is(err, entity.ErrInvalidFeedback) {
			return errorResponse(ctx, http.StatusBadRequest, "invalid feedback")
		}

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	return ctx.Status(http.StatusCreated).JSON(feedback)
}

func pathInt(ctx *fiber.Ctx, key string) (int, error) {
	value, err := strconv.Atoi(ctx.Params(key))
	if err != nil {
		return 0, err
	}
	if value <= 0 {
		return 0, errors.New("path parameter must be positive")
	}

	return value, nil
}

func queryInt(ctx *fiber.Ctx, key string, defaultValue int) int {
	value := ctx.Query(key)
	if value == "" {
		return defaultValue
	}

	parsed, err := strconv.Atoi(value)
	if err != nil {
		return defaultValue
	}

	return parsed
}

func optionalQueryInt(ctx *fiber.Ctx, key string) (*int, error) {
	value := ctx.Query(key)
	if value == "" {
		return nil, nil
	}

	parsed, err := strconv.Atoi(value)
	if err != nil {
		return nil, err
	}
	if parsed <= 0 {
		return nil, errors.New("query parameter must be positive")
	}

	return &parsed, nil
}

func optionalQueryBool(ctx *fiber.Ctx, key string) (*bool, error) {
	value := ctx.Query(key)
	if value == "" {
		return nil, nil
	}

	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return nil, err
	}

	return &parsed, nil
}
