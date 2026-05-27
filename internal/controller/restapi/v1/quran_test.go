package v1

import (
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

func TestQuranRoutes(t *testing.T) {
	t.Parallel()

	app := newQuranTestApp(&fakeQuran{})

	tests := []struct {
		name       string
		path       string
		wantStatus int
		wantBody   string
	}{
		{name: "surahs", path: "/v1/quran/surahs?lang=id", wantStatus: http.StatusOK, wantBody: `"surah_id":73`},
		{name: "recitations", path: "/v1/quran/recitations", wantStatus: http.StatusOK, wantBody: `"mode":"ayah"`},
		{name: "ayah", path: "/v1/quran/ayahs/73:1?include_audio=true&recitation_id=rec-1", wantStatus: http.StatusOK, wantBody: `"ayah_key":"73:1"`},
		{name: "surah ayahs", path: "/v1/quran/surahs/73/ayahs?from=1&to=1&recitation_id=rec-1", wantStatus: http.StatusOK, wantBody: `"text_qpc_hafs"`},
		{name: "search", path: "/v1/quran/search?q=muzammil", wantStatus: http.StatusOK, wantBody: `"results"`},
		{name: "book refs", path: "/v1/books/797/quran-references", wantStatus: http.StatusOK, wantBody: `"references"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			resp, err := app.Test(httptest.NewRequest(http.MethodGet, tt.path, nil))

			require.NoError(t, err)
			assert.Equal(t, tt.wantStatus, resp.StatusCode)
			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			assert.Contains(t, string(body), tt.wantBody)
		})
	}
}

func newQuranTestApp(quran *fakeQuran) *fiber.App {
	app := fiber.New()
	controller := &V1{
		quran: quran,
		l:     logger.New("error"),
		v:     validator.New(validator.WithRequiredStructEnabled()),
	}
	app.Get("/v1/quran/surahs", controller.listQuranSurahs)
	app.Get("/v1/quran/recitations", controller.listQuranRecitations)
	app.Get("/v1/quran/ayahs/:ayah_key", controller.getQuranAyah)
	app.Get("/v1/quran/surahs/:surah_id/ayahs", controller.listQuranSurahAyahs)
	app.Get("/v1/quran/search", controller.searchQuran)
	app.Get("/v1/books/:book_id/quran-references", controller.listBookQuranReferences)

	return app
}

type fakeQuran struct{}

func (f *fakeQuran) Surahs(_ context.Context, _ string) ([]entity.QuranSurah, error) {
	name := "المزمل"

	return []entity.QuranSurah{{SurahID: 73, NameArabic: &name, AyahCount: 20}}, nil
}

func (f *fakeQuran) Recitations(_ context.Context) ([]entity.QuranRecitation, error) {
	return []entity.QuranRecitation{{ID: "rec-1", Name: "Reciter", Mode: "ayah", TrackCount: 6236}}, nil
}

func (f *fakeQuran) Ayah(
	_ context.Context,
	ayahKey string,
	_ string,
	_ string,
	_ bool,
	_ string,
) (entity.QuranAyah, error) {
	text := "يَـٰٓأَيُّهَا ٱلْمُزَّمِّلُ"

	return entity.QuranAyah{SurahID: 73, AyahNumber: 1, AyahKey: ayahKey, TextQPCHafs: &text}, nil
}

func (f *fakeQuran) SurahAyahs(
	_ context.Context,
	surahID int,
	_ int,
	_ int,
	_ string,
	_ string,
	_ bool,
	_ bool,
	_ string,
) ([]entity.QuranAyah, error) {
	text := "يَـٰٓأَيُّهَا ٱلْمُزَّمِّلُ"

	return []entity.QuranAyah{{SurahID: surahID, AyahNumber: 1, AyahKey: "73:1", TextQPCHafs: &text}}, nil
}

func (f *fakeQuran) Search(
	_ context.Context,
	_ string,
	_ string,
	_ int,
	_ int,
) ([]entity.QuranSearchResult, int, error) {
	return []entity.QuranSearchResult{{Ayah: entity.QuranAyah{SurahID: 73, AyahNumber: 1, AyahKey: "73:1"}, Score: 1}}, 1, nil
}

func (f *fakeQuran) BookReferences(
	_ context.Context,
	bookID int,
	_ string,
	_ string,
	_ int,
	_ int,
) ([]entity.BookQuranReference, int, error) {
	return []entity.BookQuranReference{{ID: "ref-1", BookID: bookID, PageID: 1, SourceText: "سورة المزمل: 1", ReferenceKind: "surah_ayah", MatchStrategy: "explicit_surah_ayah", ReviewStatus: "approved"}}, 1, nil
}
