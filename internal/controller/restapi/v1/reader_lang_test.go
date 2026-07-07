package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/evrone/go-clean-template/pkg/logger"
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReaderUnsupportedLanguageRoutes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "categories", method: http.MethodGet, path: "/v1/categories?lang=fr"},
		{name: "authors", method: http.MethodGet, path: "/v1/authors?lang=fr"},
		{name: "book list", method: http.MethodGet, path: "/v1/books?lang=fr"},
		{name: "book detail", method: http.MethodGet, path: "/v1/books/1?lang=fr"},
		{name: "section", method: http.MethodGet, path: "/v1/books/1/sections/2?lang=fr"},
		{name: "toc", method: http.MethodGet, path: "/v1/books/1/toc?lang=fr"},
		{name: "toc read", method: http.MethodGet, path: "/v1/books/1/toc/2/read?lang=fr"},
		{name: "playlist", method: http.MethodGet, path: "/v1/books/1/toc/2/playlist?lang=fr"},
		{
			name:   "feedback",
			method: http.MethodPost,
			path:   "/v1/books/1/toc/2/translation-feedback?lang=fr",
			body:   `{"vote":"like","client_id":"client-1"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			app := newReaderLanguageTestApp(&fakeReader{err: entity.ErrUnsupportedLanguage})
			req := httptest.NewRequestWithContext(t.Context(), tt.method, tt.path, bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")

			resp, err := app.Test(req)

			require.NoError(t, err)
			assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		})
	}
}

func TestReaderBookListIncludesCatalogStats(t *testing.T) {
	t.Parallel()

	reader := &fakeReader{stats: entity.BookCatalogStats{
		TotalBooks:     12,
		PublishedCount: 10,
		ByCategory: []entity.BookCategoryStat{{
			Total:          5,
			PublishedCount: 4,
		}},
	}}
	app := newReaderLanguageTestApp(reader)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/books?lang=id", nil)

	resp, err := app.Test(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	var body struct {
		Stats entity.BookCatalogStats `json:"stats"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, 12, body.Stats.TotalBooks)
	assert.Equal(t, 10, body.Stats.PublishedCount)
	require.Len(t, body.Stats.ByCategory, 1)
	assert.Equal(t, 5, body.Stats.ByCategory[0].Total)
}

func newReaderLanguageTestApp(reader *fakeReader) *fiber.App {
	return newReaderLanguageTestAppWithLogger(reader, logger.New("error"))
}

func newReaderLanguageTestAppWithLogger(reader *fakeReader, l logger.Interface) *fiber.App {
	app := fiber.New()
	controller := &V1{
		reader: reader,
		l:      l,
		v:      validator.New(validator.WithRequiredStructEnabled()),
	}
	app.Get("/v1/categories", controller.listCategories)
	app.Get("/v1/authors", controller.listAuthors)
	app.Get("/v1/books", controller.listBooks)
	app.Get("/v1/books/:book_id", controller.getBook)
	app.Get("/v1/books/:book_id/sections/:heading_id", controller.getBookSection)
	app.Get("/v1/books/:book_id/toc", controller.listBookTOC)
	app.Get("/v1/books/:book_id/toc/:heading_id/read", controller.readBookTOCSection)
	app.Get("/v1/books/:book_id/toc/:heading_id/playlist", controller.getBookTOCPlaylist)
	app.Post("/v1/books/:book_id/toc/:heading_id/translation-feedback", controller.createTranslationFeedback)

	return app
}

type fakeReader struct {
	err   error
	stats entity.BookCatalogStats
}

func (f *fakeReader) Categories(context.Context, string) ([]entity.Category, error) {
	return nil, f.err
}

func (f *fakeReader) Authors(context.Context, string, int, int, string) ([]entity.Author, int, error) {
	return nil, 0, f.err
}

func (f *fakeReader) Books(
	context.Context,
	string,
	*int,
	*int,
	*bool,
	int,
	int,
	string,
) ([]entity.Book, int, error) {
	return nil, 0, f.err
}

func (f *fakeReader) Book(context.Context, int, string) (entity.Book, error) {
	return entity.Book{}, f.err
}

func (f *fakeReader) BookStats(context.Context, string) (entity.BookCatalogStats, error) {
	if f.stats.TotalBooks != 0 || f.stats.ByCategory != nil {
		return f.stats, f.err
	}

	return entity.BookCatalogStats{ByCategory: []entity.BookCategoryStat{}}, f.err
}

func (f *fakeReader) Pages(context.Context, int, int, int) ([]entity.BookPage, int, error) {
	return nil, 0, f.err
}

func (f *fakeReader) Page(context.Context, int, int) (entity.BookPage, error) {
	return entity.BookPage{}, f.err
}

func (f *fakeReader) Headings(context.Context, int, string, int, int) ([]entity.BookHeading, int, error) {
	return nil, 0, f.err
}

func (f *fakeReader) Section(context.Context, int, int, string) (entity.BookSection, error) {
	return entity.BookSection{}, f.err
}

func (f *fakeReader) TOC(context.Context, int, string, bool) ([]entity.BookTOCNode, error) {
	return nil, f.err
}

func (f *fakeReader) TOCRead(context.Context, int, int, string) (entity.BookTOCRead, error) {
	return entity.BookTOCRead{}, f.err
}

func (f *fakeReader) TOCPlaylist(context.Context, int, int, string) (entity.BookTOCPlaylist, error) {
	return entity.BookTOCPlaylist{}, f.err
}

func (f *fakeReader) CreateTranslationFeedback(
	context.Context,
	int,
	int,
	string,
	string,
	*string,
	*string,
	*string,
	*string,
	*string,
) (entity.TranslationFeedback, error) {
	return entity.TranslationFeedback{}, f.err
}
