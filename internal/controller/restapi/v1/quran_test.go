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
		name        string
		path        string
		wantStatus  int
		wantBody    string
		wantNotBody string
	}{
		{name: "surahs are light by default", path: "/v1/quran/surahs?lang=id", wantStatus: http.StatusOK, wantBody: `"surah_id":73`, wantNotBody: `"info"`},
		{name: "surahs include info when requested", path: "/v1/quran/surahs?lang=id&include_info=true", wantStatus: http.StatusOK, wantBody: `"info"`},
		{name: "surah detail includes info", path: "/v1/quran/surahs/73?lang=id", wantStatus: http.StatusOK, wantBody: `"text_html"`},
		{name: "recitations include default flag", path: "/v1/quran/recitations", wantStatus: http.StatusOK, wantBody: `"is_default":true`},
		{name: "translation sources", path: "/v1/quran/translation-sources?lang=id", wantStatus: http.StatusOK, wantBody: `"coverage"`},
		{name: "juz navigation", path: "/v1/quran/juz?lang=id", wantStatus: http.StatusOK, wantBody: `"kind":"juz"`},
		{name: "juz ayahs", path: "/v1/quran/juz/29/ayahs?include_translation=false&include_audio=true&recitation_id=rec-1", wantStatus: http.StatusOK, wantBody: `"recitation_id":"rec-1"`},
		{name: "juz invalid include audio", path: "/v1/quran/juz/29/ayahs?include_audio=wat", wantStatus: http.StatusBadRequest, wantBody: `"invalid include_audio"`},
		{name: "juz invalid view", path: "/v1/quran/juz/29/ayahs?view=compact", wantStatus: http.StatusBadRequest, wantBody: `"invalid view"`},
		{name: "juz invalid number", path: "/v1/quran/juz/bad/ayahs", wantStatus: http.StatusBadRequest, wantBody: `"invalid juz_number"`},
		{name: "hizb navigation", path: "/v1/quran/hizbs?lang=id", wantStatus: http.StatusOK, wantBody: `"kind":"hizb"`},
		{name: "hizb ayahs", path: "/v1/quran/hizbs/57/ayahs", wantStatus: http.StatusOK, wantBody: `"hizb_number":57`},
		{name: "hizb missing", path: "/v1/quran/hizbs/58/ayahs", wantStatus: http.StatusNotFound, wantBody: `"quran navigation not found"`},
		{name: "ayah uses default audio without recitation id", path: "/v1/quran/ayahs/73:1?include_audio=true", wantStatus: http.StatusOK, wantBody: `"recitation_id":"rec-default"`},
		{name: "ayah invalid recitation returns not found", path: "/v1/quran/ayahs/73:1?include_audio=true&recitation_id=bad-id", wantStatus: http.StatusNotFound, wantBody: `"quran recitation not found"`},
		{name: "ayah invalid include audio", path: "/v1/quran/ayahs/73:1?include_audio=wat", wantStatus: http.StatusBadRequest, wantBody: `"invalid include_audio"`},
		{name: "surahs invalid include info", path: "/v1/quran/surahs?include_info=wat", wantStatus: http.StatusBadRequest, wantBody: `"invalid include_info"`},
		{name: "surah ayahs", path: "/v1/quran/surahs/73/ayahs?from=1&to=1&recitation_id=rec-1", wantStatus: http.StatusOK, wantBody: `"text_qpc_hafs"`},
		{name: "surah ayahs invalid view", path: "/v1/quran/surahs/73/ayahs?view=compact", wantStatus: http.StatusBadRequest, wantBody: `"invalid view"`},
		{name: "search", path: "/v1/quran/search?q=muzammil", wantStatus: http.StatusOK, wantBody: `"results"`},
		{name: "book refs", path: "/v1/books/797/quran-references", wantStatus: http.StatusOK, wantBody: `"references"`},
		{name: "unsupported lang", path: "/v1/quran/ayahs/73:1?lang=fr", wantStatus: http.StatusBadRequest, wantBody: `"unsupported language"`},
		{name: "unknown translation source", path: "/v1/quran/ayahs/73:1?translation_source=bad-source", wantStatus: http.StatusNotFound, wantBody: `"quran translation source not found"`},
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
			if tt.wantNotBody != "" {
				assert.NotContains(t, string(body), tt.wantNotBody)
			}
		})
	}
}

func TestQuranReaderMinimalView(t *testing.T) {
	t.Parallel()

	app := newQuranTestApp(&fakeQuran{})

	tests := []struct {
		name         string
		path         string
		wantBody     []string
		wantNotBody  []string
		wantHTTPCode int
	}{
		{
			name: "surah ayahs include compact reader fields",
			path: "/v1/quran/surahs/73/ayahs?view=reader_minimal&include_audio=true&recitation_id=rec-1",
			wantBody: []string{
				`"surah_id":73`,
				`"ayah_number":1`,
				`"ayah_key":"73:1"`,
				`"text_qpc_hafs"`,
				`"juz_number":29`,
				`"page_number":574`,
				`"translation":{"text":"Wahai orang yang berselimut!"}`,
				`"recitation_id":"rec-1"`,
				`"url":"https://cdn.example/73-1.mp3"`,
				`"segment_index":1`,
				`"timestamp_from_ms":1000`,
				`"duration_ms":3000`,
			},
			wantNotBody: []string{
				`"text_imlaei_simple"`,
				`"search_text"`,
				`"script_type"`,
				`"font_family"`,
				`"hizb_number"`,
				`"source_id"`,
				`"available_translation_langs"`,
				`"translation_missing"`,
				`"availability"`,
				`"metadata"`,
				`"updated_at"`,
				`"audio_url"`,
				`"public_url"`,
				`"r2_key"`,
			},
			wantHTTPCode: http.StatusOK,
		},
		{
			name: "juz ayahs support compact view",
			path: "/v1/quran/juz/29/ayahs?view=reader_minimal&include_audio=true&recitation_id=rec-1",
			wantBody: []string{
				`"juz_number":29`,
				`"url":"https://cdn.example/73-1.mp3"`,
			},
			wantNotBody: []string{
				`"hizb_number"`,
				`"metadata"`,
				`"updated_at"`,
			},
			wantHTTPCode: http.StatusOK,
		},
		{
			name: "hizb ayahs support compact view",
			path: "/v1/quran/hizbs/57/ayahs?view=reader_minimal",
			wantBody: []string{
				`"surah_id":73`,
				`"translation":{"text":"Wahai orang yang berselimut!"}`,
			},
			wantNotBody: []string{
				`"hizb_number"`,
				`"audio"`,
				`"metadata"`,
				`"updated_at"`,
			},
			wantHTTPCode: http.StatusOK,
		},
		{
			name: "translation follows include translation",
			path: "/v1/quran/surahs/73/ayahs?view=reader_minimal&include_translation=false",
			wantBody: []string{
				`"text_qpc_hafs"`,
			},
			wantNotBody: []string{
				`"translation"`,
			},
			wantHTTPCode: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			resp, err := app.Test(httptest.NewRequest(http.MethodGet, tt.path, nil))
			require.NoError(t, err)
			assert.Equal(t, tt.wantHTTPCode, resp.StatusCode)

			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)
			bodyText := string(body)
			for _, want := range tt.wantBody {
				assert.Contains(t, bodyText, want)
			}
			for _, notWant := range tt.wantNotBody {
				assert.NotContains(t, bodyText, notWant)
			}
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
	app.Get("/v1/quran/translation-sources", controller.listQuranTranslationSources)
	app.Get("/v1/quran/juz", controller.listQuranJuz)
	app.Get("/v1/quran/juz/:juz_number/ayahs", controller.listQuranJuzAyahs)
	app.Get("/v1/quran/hizbs", controller.listQuranHizbs)
	app.Get("/v1/quran/hizbs/:hizb_number/ayahs", controller.listQuranHizbAyahs)
	app.Get("/v1/quran/ayahs/:ayah_key", controller.getQuranAyah)
	app.Get("/v1/quran/surahs/:surah_id", controller.getQuranSurah)
	app.Get("/v1/quran/surahs/:surah_id/ayahs", controller.listQuranSurahAyahs)
	app.Get("/v1/quran/search", controller.searchQuran)
	app.Get("/v1/books/:book_id/quran-references", controller.listBookQuranReferences)

	return app
}

type fakeQuran struct{}

func (f *fakeQuran) Surahs(_ context.Context, _ string, includeInfo bool) ([]entity.QuranSurah, error) {
	name := "المزمل"
	surah := entity.QuranSurah{SurahID: 73, NameArabic: &name, AyahCount: 20}
	if includeInfo {
		surah.Info = fakeSurahInfo()
	}

	return []entity.QuranSurah{surah}, nil
}

func (f *fakeQuran) Surah(_ context.Context, surahID int, _ string) (entity.QuranSurah, error) {
	name := "المزمل"

	return entity.QuranSurah{SurahID: surahID, NameArabic: &name, AyahCount: 20, Info: fakeSurahInfo()}, nil
}

func (f *fakeQuran) Recitations(_ context.Context) ([]entity.QuranRecitation, error) {
	return []entity.QuranRecitation{
		{
			ID:                 "rec-default",
			Name:               "Reciter",
			Mode:               "ayah",
			TrackCount:         6236,
			PublicTrackCount:   6236,
			PlayableTrackCount: 6236,
			HasPublicAudio:     true,
			HasPlayableAudio:   true,
			IsDefault:          true,
		},
	}, nil
}

func (f *fakeQuran) TranslationSources(_ context.Context, _ string) ([]entity.QuranTranslationSource, error) {
	return []entity.QuranTranslationSource{{
		ID:            "qul-kfgqpc-id-simple",
		Lang:          "id",
		Name:          "King Fahad Quran Complex",
		Format:        "simple.json",
		LicenseStatus: "needs_review",
		Coverage:      entity.QuranTranslationCoverage{TranslatedAyahs: 6236, TotalAyahs: 6236, Percent: 100},
		IsDefault:     true,
	}}, nil
}

func (f *fakeQuran) Juz(_ context.Context, _ string) ([]entity.QuranNavigationSegment, error) {
	return []entity.QuranNavigationSegment{fakeNavigationSegment("juz", 29)}, nil
}

func (f *fakeQuran) JuzAyahs(
	_ context.Context,
	juzNumber int,
	_ string,
	_ string,
	_ bool,
	includeAudio bool,
	recitationID string,
) ([]entity.QuranAyah, error) {
	return fakeNavigationAyahs("juz", juzNumber, includeAudio, recitationID)
}

func (f *fakeQuran) Hizbs(_ context.Context, _ string) ([]entity.QuranNavigationSegment, error) {
	return []entity.QuranNavigationSegment{fakeNavigationSegment("hizb", 57)}, nil
}

func (f *fakeQuran) HizbAyahs(
	_ context.Context,
	hizbNumber int,
	_ string,
	_ string,
	_ bool,
	includeAudio bool,
	recitationID string,
) ([]entity.QuranAyah, error) {
	if hizbNumber == 58 {
		return nil, entity.ErrQuranNavigationNotFound
	}

	return fakeNavigationAyahs("hizb", hizbNumber, includeAudio, recitationID)
}

func (f *fakeQuran) Ayah(
	_ context.Context,
	ayahKey string,
	lang string,
	translationSource string,
	includeAudio bool,
	recitationID string,
) (entity.QuranAyah, error) {
	if lang == "fr" {
		return entity.QuranAyah{}, entity.ErrUnsupportedLanguage
	}
	if translationSource == "bad-source" {
		return entity.QuranAyah{}, entity.ErrQuranTranslationSourceNotFound
	}
	if recitationID == "bad-id" {
		return entity.QuranAyah{}, entity.ErrQuranRecitationNotFound
	}

	ayah := fakeQuranAyah(73, true, includeAudio, recitationID)
	ayah.AyahKey = ayahKey

	return ayah, nil
}

func (f *fakeQuran) SurahAyahs(
	_ context.Context,
	surahID int,
	_ int,
	_ int,
	_ string,
	_ string,
	includeTranslation bool,
	includeAudio bool,
	recitationID string,
) ([]entity.QuranAyah, error) {
	return []entity.QuranAyah{fakeQuranAyah(surahID, includeTranslation, includeAudio, recitationID)}, nil
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

func (f *fakeQuran) MissingAssets(
	context.Context,
	string,
	string,
	*int,
	int,
	int,
) (entity.EditorialMissingQuranAssets, error) {
	return entity.EditorialMissingQuranAssets{}, nil
}

func fakeSurahInfo() *entity.QuranSurahInfo {
	return &entity.QuranSurahInfo{
		Lang:          "id",
		TextHTML:      "<p>Info</p>",
		SourceName:    "QUL Surah information",
		Format:        "json",
		LicenseStatus: "needs_review",
	}
}

func fakeNavigationSegment(kind string, number int) entity.QuranNavigationSegment {
	name := "Al-Muzzammil"

	return entity.QuranNavigationSegment{
		Kind:      kind,
		Number:    number,
		AyahCount: 20,
		Start:     entity.QuranNavigationBoundary{SurahID: 73, AyahNumber: 1, AyahKey: "73:1", SurahName: &name},
		End:       entity.QuranNavigationBoundary{SurahID: 73, AyahNumber: 20, AyahKey: "73:20", SurahName: &name},
	}
}

func fakeNavigationAyahs(kind string, number int, includeAudio bool, recitationID string) ([]entity.QuranAyah, error) {
	ayah := fakeQuranAyah(73, true, includeAudio, recitationID)
	if kind == "juz" {
		ayah.JuzNumber = &number
	} else {
		ayah.HizbNumber = &number
	}

	return []entity.QuranAyah{ayah}, nil
}

func fakeQuranAyah(surahID int, includeTranslation bool, includeAudio bool, recitationID string) entity.QuranAyah {
	text := "يَـٰٓأَيُّهَا ٱلْمُزَّمِّلُ"
	imlaei := "يا أيها المزمل"
	searchText := "يا ايها المزمل"
	scriptType := "qpc"
	fontFamily := "qpc-hafs"
	ayahNumber := 1
	pageNumber := 574
	juzNumber := 29
	hizbNumber := 57
	ayah := entity.QuranAyah{
		SurahID:          surahID,
		AyahNumber:       ayahNumber,
		AyahKey:          "73:1",
		TextQPCHafs:      &text,
		TextImlaeiSimple: &imlaei,
		SearchText:       &searchText,
		ScriptType:       &scriptType,
		FontFamily:       &fontFamily,
		PageNumber:       &pageNumber,
		JuzNumber:        &juzNumber,
		HizbNumber:       &hizbNumber,
		Metadata:         entity.RawJSON(`{"debug":true}`),
	}
	if includeTranslation {
		ayah.Translation = &entity.QuranTranslation{
			SourceID:  "qul-kfgqpc-id-simple",
			Lang:      "id",
			Text:      "Wahai orang yang berselimut!",
			Footnotes: entity.RawJSON(`[]`),
			Metadata:  entity.RawJSON(`{"debug":true}`),
		}
	}
	if includeAudio {
		ayah.Audio = []entity.QuranAudioTrack{fakeQuranAudioTrack(surahID, ayahNumber, recitationID)}
	}

	return ayah
}

func fakeQuranAudioTrack(surahID int, ayahNumber int, recitationID string) entity.QuranAudioTrack {
	if recitationID == "" {
		recitationID = "rec-default"
	}

	audioURL := "https://source.example/73-1.mp3"
	publicURL := "https://cdn.example/73-1.mp3"
	r2Key := "quran/73-1.mp3"
	durationMS := 3000
	mimeType := "audio/mpeg"

	return entity.QuranAudioTrack{
		RecitationID: recitationID,
		TrackType:    "ayah",
		TrackKey:     "73:1",
		SurahID:      surahID,
		AyahNumber:   &ayahNumber,
		AudioURL:     &audioURL,
		R2Key:        &r2Key,
		PublicURL:    &publicURL,
		DurationMS:   &durationMS,
		MIMEType:     &mimeType,
		Segments: []entity.QuranAudioSegment{
			{
				SegmentIndex:    1,
				AyahKey:         "73:1",
				TimestampFromMS: 1000,
				TimestampToMS:   4000,
				DurationMS:      &durationMS,
				Metadata:        entity.RawJSON(`{"debug":true}`),
			},
		},
		Metadata: entity.RawJSON(`{"debug":true}`),
	}
}
