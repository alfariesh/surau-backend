package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/pkg/logger"
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeQuranEditorial struct {
	surahWorkspaceFn func(context.Context, int, string) (entity.QuranSurahEditorialWorkspace, error)
	saveSurahFn      func(
		context.Context,
		string,
		entity.QuranSurahEditorialEdit,
		*time.Time,
	) (entity.QuranSurahEditorialWorkspace, error)
	publishSurahFn func(
		context.Context,
		string,
		int,
		string,
		*time.Time,
	) (entity.QuranSurahEditorialWorkspace, error)
	restoreSurahFn func(
		context.Context,
		string,
		int,
		string,
		string,
		*time.Time,
	) (entity.QuranSurahEditorialWorkspace, error)
	ayahWorkspaceFn func(context.Context, string, string) (entity.QuranAyahEditorialWorkspace, error)
	saveAyahFn      func(
		context.Context,
		string,
		entity.QuranAyahEditorialEdit,
		*time.Time,
	) (entity.QuranAyahEditorialWorkspace, error)
	publishAyahFn func(
		context.Context,
		string,
		string,
		string,
		*time.Time,
	) (entity.QuranAyahEditorialWorkspace, error)
	restoreAyahFn func(
		context.Context,
		string,
		string,
		string,
		string,
		*time.Time,
	) (entity.QuranAyahEditorialWorkspace, error)
	revisionsFn func(
		context.Context,
		string,
		int,
		*int,
		string,
		int,
		int,
	) ([]entity.QuranEditorialRevision, int, error)
}

func (f *fakeQuranEditorial) SurahEditorialWorkspace(
	ctx context.Context,
	surahID int,
	lang string,
) (entity.QuranSurahEditorialWorkspace, error) {
	return f.surahWorkspaceFn(ctx, surahID, lang)
}

//nolint:gocritic // value parameter mirrors usecase.QuranEditorial exactly
func (f *fakeQuranEditorial) SaveSurahEditorialDraft(
	ctx context.Context,
	actorID string,
	edit entity.QuranSurahEditorialEdit,
	expected *time.Time,
) (entity.QuranSurahEditorialWorkspace, error) {
	return f.saveSurahFn(ctx, actorID, edit, expected)
}

func (f *fakeQuranEditorial) PublishSurahEditorialDraft(
	ctx context.Context,
	actorID string,
	surahID int,
	lang string,
	expected *time.Time,
) (entity.QuranSurahEditorialWorkspace, error) {
	return f.publishSurahFn(ctx, actorID, surahID, lang, expected)
}

func (f *fakeQuranEditorial) RestoreSurahEditorialRevision(
	ctx context.Context,
	actorID string,
	surahID int,
	lang,
	revisionID string,
	expected *time.Time,
) (entity.QuranSurahEditorialWorkspace, error) {
	return f.restoreSurahFn(ctx, actorID, surahID, lang, revisionID, expected)
}

func (f *fakeQuranEditorial) AyahEditorialWorkspace(
	ctx context.Context,
	ayahKey,
	lang string,
) (entity.QuranAyahEditorialWorkspace, error) {
	return f.ayahWorkspaceFn(ctx, ayahKey, lang)
}

//nolint:gocritic // value parameter mirrors usecase.QuranEditorial exactly
func (f *fakeQuranEditorial) SaveAyahEditorialDraft(
	ctx context.Context,
	actorID string,
	edit entity.QuranAyahEditorialEdit,
	expected *time.Time,
) (entity.QuranAyahEditorialWorkspace, error) {
	return f.saveAyahFn(ctx, actorID, edit, expected)
}

func (f *fakeQuranEditorial) PublishAyahEditorialDraft(
	ctx context.Context,
	actorID,
	ayahKey,
	lang string,
	expected *time.Time,
) (entity.QuranAyahEditorialWorkspace, error) {
	return f.publishAyahFn(ctx, actorID, ayahKey, lang, expected)
}

func (f *fakeQuranEditorial) RestoreAyahEditorialRevision(
	ctx context.Context,
	actorID,
	ayahKey,
	lang,
	revisionID string,
	expected *time.Time,
) (entity.QuranAyahEditorialWorkspace, error) {
	return f.restoreAyahFn(ctx, actorID, ayahKey, lang, revisionID, expected)
}

func (f *fakeQuranEditorial) QuranEditorialRevisions(
	ctx context.Context,
	assetType string,
	surahID int,
	ayahNumber *int,
	lang string,
	limit,
	offset int,
) ([]entity.QuranEditorialRevision, int, error) {
	return f.revisionsFn(ctx, assetType, surahID, ayahNumber, lang, limit, offset)
}

func newQuranEditorialTestApp(editorial *fakeQuranEditorial) *fiber.App {
	app := fiber.New()
	controller := &V1{
		quranEditorial: editorial,
		l:              logger.New("error"),
		v:              validator.New(validator.WithRequiredStructEnabled()),
	}
	injectActor := func(ctx *fiber.Ctx) error {
		ctx.Locals("userID", "editor-id")

		return ctx.Next()
	}

	group := app.Group("/v1/editorial", injectActor)
	group.Get("/quran/surahs/:surah_id", controller.editorialQuranSurahWorkspace)
	group.Put("/quran/surahs/:surah_id/draft", controller.editorialSaveQuranSurahDraft)
	group.Post("/quran/surahs/:surah_id/publish", controller.editorialPublishQuranSurahDraft)
	group.Get("/quran/surahs/:surah_id/draft-revisions", controller.editorialListQuranSurahRevisions)
	group.Post(
		"/quran/surahs/:surah_id/draft-revisions/:revision_id/restore",
		controller.editorialRestoreQuranSurahRevision,
	)
	group.Get("/quran/ayahs/:ayah_key", controller.editorialQuranAyahWorkspace)
	group.Put("/quran/ayahs/:ayah_key/draft", controller.editorialSaveQuranAyahDraft)
	group.Post("/quran/ayahs/:ayah_key/publish", controller.editorialPublishQuranAyahDraft)
	group.Get("/quran/ayahs/:ayah_key/draft-revisions", controller.editorialListQuranAyahRevisions)
	group.Post(
		"/quran/ayahs/:ayah_key/draft-revisions/:revision_id/restore",
		controller.editorialRestoreQuranAyahRevision,
	)

	return app
}

func TestEditorialQuranSurahWorkspaceDefaultsLanguageAndUsesDraftETag(t *testing.T) {
	t.Parallel()

	publishedAt := time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)
	draftAt := publishedAt.Add(time.Hour)
	editorial := &fakeQuranEditorial{
		surahWorkspaceFn: func(
			_ context.Context,
			surahID int,
			lang string,
		) (entity.QuranSurahEditorialWorkspace, error) {
			assert.Equal(t, 73, surahID)
			assert.Equal(t, "id", lang)

			return entity.QuranSurahEditorialWorkspace{
				Draft:     &entity.QuranSurahEditorialEdit{UpdatedAt: draftAt},
				Published: &entity.QuranSurahEditorialEdit{UpdatedAt: publishedAt},
			}, nil
		},
	}
	app := newQuranEditorialTestApp(editorial)

	resp, err := app.Test(httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "/v1/editorial/quran/surahs/73", nil,
	))
	require.NoError(t, err)

	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, updatedAtETag(draftAt), resp.Header.Get(fiber.HeaderETag))
}

func TestEditorialQuranMutationsRequireParseableIfMatch(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{
			name:   "surah save",
			method: http.MethodPut,
			path:   "/v1/editorial/quran/surahs/73/draft",
			body:   `{"license_status":"needs_review"}`,
		},
		{
			name:   "surah publish",
			method: http.MethodPost,
			path:   "/v1/editorial/quran/surahs/73/publish",
		},
		{
			name:   "surah restore",
			method: http.MethodPost,
			path:   "/v1/editorial/quran/surahs/73/draft-revisions/rev-1/restore",
		},
		{
			name:   "ayah save",
			method: http.MethodPut,
			path:   "/v1/editorial/quran/ayahs/2:255/draft",
			body:   `{"license_status":"needs_review"}`,
		},
		{
			name:   "ayah publish",
			method: http.MethodPost,
			path:   "/v1/editorial/quran/ayahs/2:255/publish",
		},
		{
			name:   "ayah restore",
			method: http.MethodPost,
			path:   "/v1/editorial/quran/ayahs/2:255/draft-revisions/rev-1/restore",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			app := newQuranEditorialTestApp(&fakeQuranEditorial{})

			for _, test := range []struct {
				name       string
				ifMatch    string
				wantStatus int
			}{
				{name: "missing", wantStatus: http.StatusPreconditionRequired},
				{name: "malformed", ifMatch: "garbage", wantStatus: http.StatusPreconditionFailed},
			} {
				t.Run(test.name, func(t *testing.T) {
					request := httptest.NewRequestWithContext(
						t.Context(), tc.method, tc.path, strings.NewReader(tc.body),
					)
					request.Header.Set("Content-Type", "application/json")

					if test.ifMatch != "" {
						request.Header.Set(fiber.HeaderIfMatch, test.ifMatch)
					}

					response, err := app.Test(request)
					require.NoError(t, err)

					defer response.Body.Close()

					assert.Equal(t, test.wantStatus, response.StatusCode)
				})
			}
		})
	}
}

func TestEditorialSaveQuranAyahDraftAcceptsWildcardAndMapsBody(t *testing.T) {
	t.Parallel()

	updatedAt := time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	editorial := &fakeQuranEditorial{
		saveAyahFn: func(
			_ context.Context,
			actorID string,
			edit entity.QuranAyahEditorialEdit,
			expected *time.Time,
		) (entity.QuranAyahEditorialWorkspace, error) {
			assert.Equal(t, "editor-id", actorID)
			assert.Equal(t, "2:255", edit.AyahKey)
			assert.Equal(t, "en", edit.Lang)
			assert.Equal(t, "permitted", edit.LicenseStatus)
			assert.JSONEq(t, `{"source":"editor"}`, string(edit.Metadata))
			require.Len(t, edit.FAQ, 1)
			assert.Equal(t, "What?", edit.FAQ[0].Question)
			assert.Nil(t, expected)

			return entity.QuranAyahEditorialWorkspace{
				Draft: &entity.QuranAyahEditorialEdit{UpdatedAt: updatedAt},
			}, nil
		},
	}
	app := newQuranEditorialTestApp(editorial)
	body := `{
		"meta_title":"Ayat Kursi",
		"faq":[{"question":"What?","answer_html":"<p>Answer</p>"}],
		"license_status":"permitted",
		"metadata":{"source":"editor"}
	}`
	request := httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPut,
		"/v1/editorial/quran/ayahs/2:255/draft?lang=en",
		strings.NewReader(body),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(fiber.HeaderIfMatch, "*")

	resp, err := app.Test(request)
	require.NoError(t, err)

	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, updatedAtETag(updatedAt), resp.Header.Get(fiber.HeaderETag))
}

func TestEditorialPublishQuranSurahStaleETagMapsTo412(t *testing.T) {
	t.Parallel()

	expectedAt := time.Date(2026, 4, 5, 6, 7, 8, 0, time.UTC)
	editorial := &fakeQuranEditorial{
		publishSurahFn: func(
			_ context.Context,
			actorID string,
			surahID int,
			lang string,
			expected *time.Time,
		) (entity.QuranSurahEditorialWorkspace, error) {
			assert.Equal(t, "editor-id", actorID)
			assert.Equal(t, 73, surahID)
			assert.Equal(t, "id", lang)
			require.NotNil(t, expected)
			assert.True(t, expected.Equal(expectedAt))

			return entity.QuranSurahEditorialWorkspace{}, entity.ErrPreconditionFailed
		},
	}
	app := newQuranEditorialTestApp(editorial)
	request := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/v1/editorial/quran/surahs/73/publish", nil,
	)
	request.Header.Set(fiber.HeaderIfMatch, updatedAtETag(expectedAt))

	resp, err := app.Test(request)
	require.NoError(t, err)

	defer resp.Body.Close()

	assert.Equal(t, http.StatusPreconditionFailed, resp.StatusCode)
}

func TestEditorialQuranAyahRevisionsUseStandardEnvelopeAndScope(t *testing.T) {
	t.Parallel()

	editorial := &fakeQuranEditorial{
		revisionsFn: func(
			_ context.Context,
			assetType string,
			surahID int,
			ayahNumber *int,
			lang string,
			limit,
			offset int,
		) ([]entity.QuranEditorialRevision, int, error) {
			assert.Equal(t, entity.QuranEditorialAssetAyah, assetType)
			assert.Equal(t, 2, surahID)
			require.NotNil(t, ayahNumber)
			assert.Equal(t, 255, *ayahNumber)
			assert.Equal(t, "en", lang)
			assert.Equal(t, 25, limit)
			assert.Equal(t, 5, offset)

			return []entity.QuranEditorialRevision{{ID: "revision-id"}}, 9, nil
		},
	}
	app := newQuranEditorialTestApp(editorial)

	resp, err := app.Test(httptest.NewRequestWithContext(
		t.Context(),
		http.MethodGet,
		"/v1/editorial/quran/ayahs/2:255/draft-revisions?lang=en&limit=25&offset=5",
		nil,
	))
	require.NoError(t, err)

	defer resp.Body.Close()

	var body map[string]json.RawMessage
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, body, "items")
	assert.Contains(t, body, "total")
	assert.NotContains(t, body, "revisions")
}

func TestEditorialQuranErrorMappings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{name: "bad ayah key", err: entity.ErrInvalidAyahKey, wantStatus: 400, wantCode: "invalid_ayah_key"},
		{name: "bad draft", err: entity.ErrInvalidQuranEditorial, wantStatus: 400, wantCode: "invalid_request_body"},
		{name: "surah missing", err: entity.ErrQuranSurahNotFound, wantStatus: 404, wantCode: "quran_surah_not_found"},
		{name: "ayah missing", err: entity.ErrQuranAyahNotFound, wantStatus: 404, wantCode: "quran_ayah_not_found"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			app := fiber.New()
			controller := &V1{}

			app.Get("/", func(ctx *fiber.Ctx) error { return controller.editorialError(ctx, test.err) })

			resp, err := app.Test(httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))
			require.NoError(t, err)

			defer resp.Body.Close()

			var body struct {
				Code string `json:"code"`
			}
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
			assert.Equal(t, test.wantStatus, resp.StatusCode)
			assert.Equal(t, test.wantCode, body.Code)
		})
	}
}
