package v1

import (
	"bytes"
	"context"
	"io"
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

func TestAskBookRAGBadRequest(t *testing.T) {
	t.Parallel()

	app := newBookRAGTestApp(&fakeBookRAG{})
	req := httptest.NewRequest(http.MethodPost, "/v1/books/797/rag", bytes.NewBufferString(`{"question":""}`))
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req)

	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestAskBookRAG(t *testing.T) {
	t.Parallel()

	app := newBookRAGTestApp(&fakeBookRAG{
		response: entity.BookRAGResponse{
			BookID:   797,
			Question: "Apa definisi hadis sahih?",
			Answer:   "Jawaban [1].",
			Citations: []entity.BookRAGCitation{
				{Ref: "1", BookID: 797, HeadingID: 11, PageID: 12, Quote: "نص"},
			},
		},
	})
	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/books/797/rag?lang=id",
		bytes.NewBufferString(`{"question":"Apa definisi hadis sahih?","max_citations":5}`),
	)
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req)

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), `"answer":"Jawaban [1]."`)
}

func TestAskBookRAGStream(t *testing.T) {
	t.Parallel()

	app := newBookRAGTestApp(&fakeBookRAG{stream: true})
	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/books/797/rag",
		bytes.NewBufferString(`{"question":"Apa definisi hadis sahih?","stream":true}`),
	)
	req.Header.Set("Content-Type", "application/json")

	resp, err := app.Test(req, -1)

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), "event: delta")
	assert.Contains(t, string(body), "event: citations")
	assert.Contains(t, string(body), "event: done")
}

func newBookRAGTestApp(bookRAG *fakeBookRAG) *fiber.App {
	app := fiber.New()
	controller := &V1{
		bookRAG: bookRAG,
		l:       logger.New("error"),
		v:       validator.New(validator.WithRequiredStructEnabled()),
	}
	app.Post("/v1/books/:book_id/rag", controller.askBookRAG)

	return app
}

type fakeBookRAG struct {
	response entity.BookRAGResponse
	stream   bool
}

func (f *fakeBookRAG) AskBook(
	_ context.Context,
	bookID int,
	question string,
	_ string,
	_ int,
	_ bool,
) (entity.BookRAGResponse, error) {
	if f.response.BookID == 0 {
		f.response.BookID = bookID
	}
	if f.response.Question == "" {
		f.response.Question = question
	}

	return f.response, nil
}

func (f *fakeBookRAG) AskBookStream(
	_ context.Context,
	_ int,
	_ string,
	_ string,
	_ int,
	_ bool,
	emit func(event string, payload any) error,
) error {
	if !f.stream {
		return nil
	}
	if err := emit("delta", map[string]string{"text": "Jawaban"}); err != nil {
		return err
	}
	if err := emit("citations", []entity.BookRAGCitation{{Ref: "1"}}); err != nil {
		return err
	}

	return emit("done", entity.BookRAGResponse{BookID: 797, Answer: "Jawaban"})
}
