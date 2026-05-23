package v1

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/evrone/go-clean-template/internal/controller/restapi/v1/response"
	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/gofiber/fiber/v2"
)

func (r *V1) listCategories(ctx *fiber.Ctx) error {
	categories, err := r.reader.Categories(ctx.UserContext())
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

	book, err := r.reader.Book(ctx.UserContext(), bookID)
	if err != nil {
		r.l.Error(err, "restapi - v1 - getBook")

		if errors.Is(err, entity.ErrBookNotFound) {
			return errorResponse(ctx, http.StatusNotFound, "book not found")
		}

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	return ctx.Status(http.StatusOK).JSON(book)
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

func pathInt(ctx *fiber.Ctx, key string) (int, error) {
	value, err := strconv.Atoi(ctx.Params(key))
	if err != nil || value <= 0 {
		return 0, err
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
