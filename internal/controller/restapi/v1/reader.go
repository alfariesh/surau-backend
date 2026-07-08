package v1

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/alfariesh/surau-backend/internal/controller/restapi/v1/request"
	"github.com/alfariesh/surau-backend/internal/controller/restapi/v1/response"
	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/gofiber/fiber/v2"
)

const maxEmbeddedQuranReferences = 200

// @Summary     List kitab categories
// @Description List non-deleted kitab categories. Supported lang values are ar, id, and en; empty defaults to id. Catalog metadata falls back to Arabic and exposes localization metadata when the requested translation is missing.
// @ID          list-kitab-categories
// @Tags        kitab
// @Produce     json
// @Param       lang query    string false "Language code: ar, id, or en" default(id)
// @Success     200  {object} response.CategoryList
// @Failure     400  {object} response.Error
// @Failure     500  {object} response.Error
// @Router      /categories [get]
func (r *V1) listCategories(ctx *fiber.Ctx) error {
	categories, err := r.reader.Categories(ctx.UserContext(), ctx.Query("lang"))
	if err != nil {
		r.logReaderError(ctx, err, "restapi - v1 - listCategories")

		if errors.Is(err, entity.ErrUnsupportedLanguage) {
			return errorResponse(ctx, http.StatusBadRequest, "unsupported language")
		}

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	return ctx.Status(http.StatusOK).JSON(response.CategoryList{Items: categories, Total: len(categories)})
}

// @Summary     List kitab authors
// @Description List authors with requested-language display metadata. Search matches Arabic and any catalog translation; display remains requested language or Arabic fallback.
// @ID          list-kitab-authors
// @Tags        kitab
// @Produce     json
// @Param       q      query    string false "Search query"
// @Param       limit  query    int    false "Page size" default(50)
// @Param       offset query    int    false "Page offset" default(0)
// @Param       lang   query    string false "Language code: ar, id, or en" default(id)
// @Success     200    {object} response.AuthorList
// @Failure     400    {object} response.Error
// @Failure     500    {object} response.Error
// @Router      /authors [get]
func (r *V1) listAuthors(ctx *fiber.Ctx) error {
	authors, total, err := r.reader.Authors(
		ctx.UserContext(),
		ctx.Query("q"),
		queryInt(ctx, "limit", 50),
		queryInt(ctx, "offset", 0),
		ctx.Query("lang"),
	)
	if err != nil {
		r.logReaderError(ctx, err, "restapi - v1 - listAuthors")

		if errors.Is(err, entity.ErrUnsupportedLanguage) {
			return errorResponse(ctx, http.StatusBadRequest, "unsupported language")
		}

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	return ctx.Status(http.StatusOK).JSON(response.AuthorList{Items: authors, Total: total})
}

// @Summary     List kitab books
// @Description List published kitab catalog books. Book list responses keep existing fields, add localization metadata, and omit language_coverage; missing requested catalog translation falls back to Arabic metadata.
// @ID          list-kitab-books
// @Tags        kitab
// @Produce     json
// @Param       q           query    string false "Search query"
// @Param       category_id query    int    false "Category ID"
// @Param       author_id   query    int    false "Author ID"
// @Param       has_content query    bool   false "Filter books that have imported content"
// @Param       limit       query    int    false "Page size" default(50)
// @Param       offset      query    int    false "Page offset" default(0)
// @Param       lang        query    string false "Language code: ar, id, or en" default(id)
// @Success     200         {object} response.BookList
// @Failure     400         {object} response.Error
// @Failure     500         {object} response.Error
// @Router      /books [get]
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
		r.logReaderError(ctx, err, "restapi - v1 - listBooks")

		if errors.Is(err, entity.ErrUnsupportedLanguage) {
			return errorResponse(ctx, http.StatusBadRequest, "unsupported language")
		}

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	stats, err := r.reader.BookStats(ctx.UserContext(), ctx.Query("lang"))
	if err != nil {
		r.logReaderError(ctx, err, "restapi - v1 - listBooks - stats")

		if errors.Is(err, entity.ErrUnsupportedLanguage) {
			return errorResponse(ctx, http.StatusBadRequest, "unsupported language")
		}

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	return ctx.Status(http.StatusOK).JSON(response.BookList{Items: books, Total: total, Stats: stats})
}

// @Summary     Get kitab book
// @Description Get one published kitab book. Detail responses include language_coverage with translated, summarized, and audio section counts by language.
// @ID          get-kitab-book
// @Tags        kitab
// @Produce     json
// @Param       book_id path     int    true  "Book ID"
// @Param       lang    query    string false "Language code: ar, id, or en" default(id)
// @Success     200     {object} entity.Book
// @Failure     400     {object} response.Error
// @Failure     404     {object} response.Error
// @Failure     500     {object} response.Error
// @Router      /books/{book_id} [get]
func (r *V1) getBook(ctx *fiber.Ctx) error {
	bookID, err := pathInt(ctx, "book_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid book_id")
	}

	book, err := r.reader.Book(ctx.UserContext(), bookID, ctx.Query("lang"))
	if err != nil {
		r.logReaderError(ctx, err, "restapi - v1 - getBook")

		if errors.Is(err, entity.ErrBookNotFound) {
			return errorResponse(ctx, http.StatusNotFound, "book not found")
		}

		if errors.Is(err, entity.ErrUnsupportedLanguage) {
			return errorResponse(ctx, http.StatusBadRequest, "unsupported language")
		}

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	return ctx.Status(http.StatusOK).JSON(book)
}

// @Summary     Ask kitab RAG
// @Description Ask a question against one kitab. Sources include requested translation text only when exact requested-language translation exists; response includes requested_lang.
// @ID          ask-kitab-rag
// @Tags        kitab
// @Accept      json
// @Produce     json
// @Param       book_id path     int             true  "Book ID"
// @Param       lang    query    string          false "Language code: ar, id, or en" default(id)
// @Param       body    body     request.BookRAG true  "Question payload"
// @Success     200     {object} entity.BookRAGResponse
// @Failure     400     {object} response.Error
// @Failure     404     {object} response.Error
// @Failure     503     {object} response.Error
// @Failure     500     {object} response.Error
// @Router      /books/{book_id}/rag [post]
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
		r.logReaderError(ctx, err, "restapi - v1 - askBookRAG")

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
			r.logReaderError(ctx, err, "restapi - v1 - streamBookRAG")
		}
	})

	return nil
}

func (r *V1) bookRAGErrorResponse(ctx *fiber.Ctx, err error) error {
	if errors.Is(err, entity.ErrInvalidQuestion) {
		return errorResponse(ctx, http.StatusBadRequest, "invalid question")
	}
	if errors.Is(err, entity.ErrUnsupportedLanguage) {
		return errorResponse(ctx, http.StatusBadRequest, "unsupported language")
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

// @Summary     List kitab pages
// @Description List imported source pages for one published kitab.
// @ID          list-kitab-pages
// @Tags        kitab
// @Produce     json
// @Param       book_id path     int true  "Book ID"
// @Param       limit   query    int false "Page size" default(50)
// @Param       offset  query    int false "Page offset" default(0)
// @Success     200     {object} response.PageList
// @Failure     400     {object} response.Error
// @Failure     500     {object} response.Error
// @Router      /books/{book_id}/pages [get]
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
		r.logReaderError(ctx, err, "restapi - v1 - listBookPages")

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	return ctx.Status(http.StatusOK).JSON(response.PageList{Items: pages, Total: total})
}

// @Summary     Get kitab page
// @Description Get one imported source page for a published kitab.
// @ID          get-kitab-page
// @Tags        kitab
// @Produce     json
// @Param       book_id path     int true "Book ID"
// @Param       page_id path     int true "Page ID"
// @Success     200     {object} entity.BookPage
// @Failure     400     {object} response.Error
// @Failure     404     {object} response.Error
// @Failure     500     {object} response.Error
// @Router      /books/{book_id}/pages/{page_id} [get]
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
		r.logReaderError(ctx, err, "restapi - v1 - getBookPage")

		if errors.Is(err, entity.ErrPageNotFound) {
			return errorResponse(ctx, http.StatusNotFound, "page not found")
		}

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	return ctx.Status(http.StatusOK).JSON(page)
}

// @Summary     List kitab headings
// @Description List raw Arabic heading tree rows for one published kitab. Paginated additively: omitting limit returns the first 200 rows; total always carries the full match count.
// @ID          list-kitab-headings
// @Tags        kitab
// @Produce     json
// @Param       book_id path     int    true  "Book ID"
// @Param       q       query    string false "Search heading title"
// @Param       limit   query    int    false "Page size (default 200, max 200)"
// @Param       offset  query    int    false "Offset (clamped to 10000)"
// @Success     200     {object} response.BookHeadingList
// @Failure     400     {object} response.Error
// @Failure     500     {object} response.Error
// @Router      /books/{book_id}/headings [get]
func (r *V1) listBookHeadings(ctx *fiber.Ctx) error {
	bookID, err := pathInt(ctx, "book_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid book_id")
	}

	limit := queryInt(ctx, "limit", 0)
	offset := queryInt(ctx, "offset", 0)

	headings, total, err := r.reader.Headings(ctx.UserContext(), bookID, ctx.Query("q"), limit, offset)
	if err != nil {
		r.logReaderError(ctx, err, "restapi - v1 - listBookHeadings")

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	return ctx.Status(http.StatusOK).JSON(response.BookHeadingList{Items: headings, Total: total})
}

// @Summary     Get kitab section
// @Description Get source section content plus exact requested-language translation. If lang=en is missing while id exists, translation stays null and available_translation_langs lists alternatives.
// @ID          get-kitab-section
// @Tags        kitab
// @Produce     json
// @Param       book_id    path     int    true  "Book ID"
// @Param       heading_id path     int    true  "Heading ID"
// @Param       lang       query    string false "Language code: ar, id, or en" default(id)
// @Success     200        {object} entity.BookSection
// @Failure     400        {object} response.Error
// @Failure     404        {object} response.Error
// @Failure     500        {object} response.Error
// @Router      /books/{book_id}/sections/{heading_id} [get]
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
		r.logReaderError(ctx, err, "restapi - v1 - getBookSection")

		if errors.Is(err, entity.ErrHeadingNotFound) {
			return errorResponse(ctx, http.StatusNotFound, "heading not found")
		}

		if errors.Is(err, entity.ErrUnsupportedLanguage) {
			return errorResponse(ctx, http.StatusBadRequest, "unsupported language")
		}

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	return ctx.Status(http.StatusOK).JSON(section)
}

// @Summary     List kitab TOC
// @Description List nested TOC nodes. Titles use exact requested translation when available, otherwise Arabic; metadata exposes requested_lang, title_lang, is_title_fallback, translation_missing, and available language arrays.
// @ID          list-kitab-toc
// @Tags        kitab
// @Produce     json
// @Param       book_id       path     int    true  "Book ID"
// @Param       lang          query    string false "Language code: ar, id, or en" default(id)
// @Param       include_audio query    bool   false "Include audio metadata" default(false)
// @Success     200           {object} response.BookTOCList
// @Failure     400           {object} response.Error
// @Failure     404           {object} response.Error
// @Failure     500           {object} response.Error
// @Router      /books/{book_id}/toc [get]
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
		r.logReaderError(ctx, err, "restapi - v1 - listBookTOC")

		if errors.Is(err, entity.ErrBookNotFound) {
			return errorResponse(ctx, http.StatusNotFound, "book not found")
		}

		if errors.Is(err, entity.ErrUnsupportedLanguage) {
			return errorResponse(ctx, http.StatusBadRequest, "unsupported language")
		}

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	return ctx.Status(http.StatusOK).JSON(response.BookTOCList{Items: toc, Total: len(toc)})
}

// @Summary     Read kitab TOC section
// @Description Article-style section response with breadcrumb and sibling navigation. Translation content is exact-language only; missing requested translation returns translation=null with translation_missing=true.
// @ID          read-kitab-toc-section
// @Tags        kitab
// @Produce     json
// @Param       book_id    path     int    true  "Book ID"
// @Param       heading_id path     int    true  "Heading ID"
// @Param       lang       query    string false "Language code: ar, id, or en" default(id)
// @Param       include_quran_references query bool false "Include approved Quran references for this heading" default(false)
// @Success     200        {object} entity.BookTOCRead
// @Failure     400        {object} response.Error
// @Failure     404        {object} response.Error
// @Failure     500        {object} response.Error
// @Router      /books/{book_id}/toc/{heading_id}/read [get]
func (r *V1) readBookTOCSection(ctx *fiber.Ctx) error {
	bookID, err := pathInt(ctx, "book_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid book_id")
	}

	headingID, err := pathInt(ctx, "heading_id")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid heading_id")
	}

	includeQuranReferences, err := optionalQueryBool(ctx, "include_quran_references")
	if err != nil {
		return errorResponse(ctx, http.StatusBadRequest, "invalid include_quran_references")
	}

	section, err := r.reader.TOCRead(ctx.UserContext(), bookID, headingID, ctx.Query("lang"))
	if err != nil {
		r.logReaderError(ctx, err, "restapi - v1 - readBookTOCSection")

		if errors.Is(err, entity.ErrBookNotFound) {
			return errorResponse(ctx, http.StatusNotFound, "book not found")
		}

		if errors.Is(err, entity.ErrHeadingNotFound) {
			return errorResponse(ctx, http.StatusNotFound, "heading not found")
		}

		if errors.Is(err, entity.ErrUnsupportedLanguage) {
			return errorResponse(ctx, http.StatusBadRequest, "unsupported language")
		}

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	if includeQuranReferences != nil && *includeQuranReferences {
		if r.quran == nil {
			return errorResponse(ctx, http.StatusServiceUnavailable, "quran is not configured")
		}

		references, _, err := r.quran.BookReferences(
			ctx.UserContext(),
			bookID,
			&headingID,
			ctx.Query("lang"),
			"approved",
			maxEmbeddedQuranReferences,
			0,
		)
		if err != nil {
			r.logQuranError(ctx, err, "restapi - v1 - readBookTOCSection - quranReferences")

			return r.quranErrorResponse(ctx, err)
		}

		section.QuranReferences = references
		if section.QuranReferences == nil {
			section.QuranReferences = []entity.BookQuranReference{}
		}
	}

	return ctx.Status(http.StatusOK).JSON(section)
}

// @Summary     Get kitab TOC audio playlist
// @Description Get a continuous audiobook manifest for one TOC subtree in the exact requested language.
// @ID          get-kitab-toc-playlist
// @Tags        kitab
// @Produce     json
// @Param       book_id    path     int    true  "Book ID"
// @Param       heading_id path     int    true  "Heading ID"
// @Param       lang       query    string false "Language code: ar, id, or en" default(id)
// @Success     200        {object} entity.BookTOCPlaylist
// @Failure     400        {object} response.Error
// @Failure     404        {object} response.Error
// @Failure     500        {object} response.Error
// @Router      /books/{book_id}/toc/{heading_id}/playlist [get]
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
		r.logReaderError(ctx, err, "restapi - v1 - getBookTOCPlaylist")

		if errors.Is(err, entity.ErrBookNotFound) {
			return errorResponse(ctx, http.StatusNotFound, "book not found")
		}

		if errors.Is(err, entity.ErrHeadingNotFound) {
			return errorResponse(ctx, http.StatusNotFound, "heading not found")
		}

		if errors.Is(err, entity.ErrUnsupportedLanguage) {
			return errorResponse(ctx, http.StatusBadRequest, "unsupported language")
		}

		return errorResponse(ctx, http.StatusInternalServerError, "internal server error")
	}

	return ctx.Status(http.StatusOK).JSON(playlist)
}

// @Summary     Create kitab translation feedback
// @Description Store feedback for an exact section translation language. Posting feedback for missing lang=en still returns 404 translation not found.
// @ID          create-kitab-translation-feedback
// @Tags        kitab
// @Accept      json
// @Produce     json
// @Param       book_id    path     int                               true  "Book ID"
// @Param       heading_id path     int                               true  "Heading ID"
// @Param       lang       query    string                            false "Language code: ar, id, or en" default(id)
// @Param       body       body     request.CreateTranslationFeedback true  "Feedback payload"
// @Success     201        {object} entity.TranslationFeedback
// @Failure     400        {object} response.Error
// @Failure     404        {object} response.Error
// @Failure     500        {object} response.Error
// @Router      /books/{book_id}/toc/{heading_id}/translation-feedback [post]
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
		r.logReaderError(ctx, err, "restapi - v1 - createTranslationFeedback")

		if errors.Is(err, entity.ErrBookNotFound) {
			return errorResponse(ctx, http.StatusNotFound, "book not found")
		}

		if errors.Is(err, entity.ErrHeadingNotFound) {
			return errorResponse(ctx, http.StatusNotFound, "heading not found")
		}

		if errors.Is(err, entity.ErrTranslationNotFound) {
			return errorResponse(ctx, http.StatusNotFound, "translation not found")
		}

		if errors.Is(err, entity.ErrUnsupportedLanguage) {
			return errorResponse(ctx, http.StatusBadRequest, "unsupported language")
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

// defaultListLimit is the page size used when a list endpoint gets no limit.
const defaultListLimit = 50

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
