package quran

import (
	"context"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/contentlang"
	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/repo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUseCase_SurahsNormalizesLangAndForwardsIncludeInfo(t *testing.T) {
	t.Parallel()

	repository := &quranRepoStub{}
	uc := New(repository)

	_, err := uc.Surahs(context.Background(), "", true)

	require.NoError(t, err)
	assert.Equal(t, contentlang.Default, repository.listSurahsLang)
	assert.True(t, repository.listSurahsIncludeInfo)
}

func TestUseCase_SurahValidatesIDAndNormalizesLang(t *testing.T) {
	t.Parallel()

	repository := &quranRepoStub{}
	uc := New(repository)

	_, err := uc.Surah(context.Background(), 0, "id")
	require.ErrorIs(t, err, entity.ErrQuranSurahNotFound)
	assert.Zero(t, repository.getSurahCalls)

	_, err = uc.Surah(context.Background(), 73, "")

	require.NoError(t, err)
	assert.Equal(t, 1, repository.getSurahCalls)
	assert.Equal(t, 73, repository.getSurahID)
	assert.Equal(t, contentlang.Default, repository.getSurahLang)
}

func TestUseCase_AyahNormalizesDefaults(t *testing.T) {
	t.Parallel()

	repository := &quranRepoStub{}
	uc := New(repository)

	_, err := uc.Ayah(context.Background(), "73:4", "", "", true, " rec-1 ")

	require.NoError(t, err)
	assert.Equal(t, "73:4", repository.getAyahKey)
	assert.Equal(t, contentlang.Default, repository.getAyahLang)
	assert.Empty(t, repository.getAyahTranslationSource)
	assert.True(t, repository.getAyahIncludeAudio)
	assert.Equal(t, "rec-1", repository.getAyahRecitationID)
}

func TestUseCase_SurahAudioValidatesIDAndNormalizesRecitation(t *testing.T) {
	t.Parallel()

	repository := &quranRepoStub{}
	uc := New(repository)

	_, err := uc.SurahAudio(context.Background(), 0, "rec-1")
	require.ErrorIs(t, err, entity.ErrQuranSurahNotFound)

	_, err = uc.SurahAudio(context.Background(), 73, " rec-1 ")

	require.NoError(t, err)
	assert.Equal(t, 73, repository.getSurahAudioID)
	assert.Equal(t, "rec-1", repository.getSurahAudioRecitationID)
}

func TestUseCase_NavigationValidatesRangesAndNormalizes(t *testing.T) {
	t.Parallel()

	repository := &quranRepoStub{}
	uc := New(repository)

	_, err := uc.Juz(context.Background(), "en-US")
	require.NoError(t, err)
	assert.Equal(t, "juz", repository.navigationKind)
	assert.Equal(t, contentlang.English, repository.navigationLang)

	_, err = uc.JuzAyahs(context.Background(), 0, "id", "", true, false, false, "")
	require.ErrorIs(t, err, entity.ErrInvalidQuranRange)

	_, err = uc.JuzAyahs(context.Background(), 31, "id", "", true, false, false, "")
	require.ErrorIs(t, err, entity.ErrInvalidQuranRange)

	_, err = uc.JuzAyahs(context.Background(), 29, "", " source ", false, true, true, " rec ")
	require.NoError(t, err)
	assert.Equal(t, "juz", repository.navigationAyahsKind)
	assert.Equal(t, 29, repository.navigationAyahsNumber)
	assert.Equal(t, contentlang.Default, repository.navigationAyahsLang)
	assert.Equal(t, "source", repository.navigationAyahsTranslationSource)
	assert.False(t, repository.navigationAyahsIncludeTranslation)
	assert.True(t, repository.navigationAyahsIncludeAudio)
	assert.Equal(t, "rec", repository.navigationAyahsRecitationID)

	_, err = uc.HizbAyahs(context.Background(), 61, "id", "", true, false, false, "")
	require.ErrorIs(t, err, entity.ErrInvalidQuranRange)
}

func TestUseCase_RejectsUnsupportedLang(t *testing.T) {
	t.Parallel()

	uc := New(&quranRepoStub{})

	_, err := uc.Surahs(context.Background(), "fr", false)
	require.ErrorIs(t, err, entity.ErrUnsupportedLanguage)

	_, err = uc.Ayah(context.Background(), "73:4", "fr", "", false, "")
	require.ErrorIs(t, err, entity.ErrUnsupportedLanguage)

	_, _, err = uc.Search(context.Background(), "rahman", "fr", 10, 0)
	require.ErrorIs(t, err, entity.ErrUnsupportedLanguage)
}

func TestUseCase_MissingAssetsFilter(t *testing.T) {
	t.Parallel()

	repository := &quranRepoStub{}
	uc := New(repository)
	surahID := 73

	_, err := uc.MissingAssets(context.Background(), "en-US", "ayah_translation", &surahID, 25, 5)

	require.NoError(t, err)
	assert.Equal(t, []string{contentlang.English}, repository.missingAssetsFilter.TargetLangs)
	assert.Equal(t, entity.MissingQuranAssetAyahTranslation, repository.missingAssetsFilter.AssetType)
	assert.Equal(t, &surahID, repository.missingAssetsFilter.SurahID)
	assert.Equal(t, uint64(25), repository.missingAssetsFilter.Limit)
	assert.Equal(t, uint64(5), repository.missingAssetsFilter.Offset)

	_, err = uc.MissingAssets(context.Background(), "ar", "", nil, 50, 0)
	require.ErrorIs(t, err, entity.ErrUnsupportedLanguage)

	_, err = uc.MissingAssets(context.Background(), "id", "metadata", nil, 50, 0)
	require.ErrorIs(t, err, entity.ErrInvalidAssetType)
}

func TestUseCase_AyahRejectsInvalidKey(t *testing.T) {
	t.Parallel()

	uc := New(&quranRepoStub{})

	_, err := uc.Ayah(context.Background(), "73", "id", "", false, "")

	require.ErrorIs(t, err, entity.ErrInvalidAyahKey)
}

func TestUseCase_SitemapDecoratesLocalizedPathsAndHreflangs(t *testing.T) {
	t.Parallel()

	ayahNumber := 1
	lastmod := time.Date(2026, 7, 15, 9, 10, 11, 0, time.UTC)
	repository := &quranRepoStub{sitemapItems: []entity.QuranSitemapItem{
		{
			PageType:       "surah",
			SurahID:        1,
			Slug:           "al-fatihah",
			Lang:           "id",
			Lastmod:        lastmod,
			AvailableLangs: []string{"en", "id"},
		},
		{
			PageType:       "ayah",
			SurahID:        1,
			AyahNumber:     &ayahNumber,
			Slug:           "al-fatihah",
			Lang:           "en",
			Lastmod:        lastmod,
			AvailableLangs: []string{"en"},
		},
	}}

	items, err := New(repository).Sitemap(context.Background())
	require.NoError(t, err)
	require.Len(t, items, 2)
	assert.Equal(t, "/surah/al-fatihah", items[0].Path)
	assert.Equal(t, []entity.QuranSitemapHreflang{
		{Lang: "en", Path: "/en/surah/al-fatihah"},
		{Lang: "id", Path: "/surah/al-fatihah"},
	}, items[0].Hreflangs)
	assert.Equal(t, "/en/surah/al-fatihah/1", items[1].Path)
	assert.Equal(t, []entity.QuranSitemapHreflang{{Lang: "en", Path: "/en/surah/al-fatihah/1"}}, items[1].Hreflangs)
	assert.Equal(t, lastmod, items[1].Lastmod)
}

func TestUseCase_FeedValidatesAndClampsFilters(t *testing.T) {
	t.Parallel()

	repository := &quranRepoStub{feedTotal: 12}
	uc := New(repository)

	_, total, err := uc.Feed(context.Background(), "2026-07-15T08:00:00Z", "en", "ayah", 999, 99999)
	require.NoError(t, err)
	assert.Equal(t, 12, total)
	require.NotNil(t, repository.feedFilter.Since)
	assert.Equal(t, "en", repository.feedFilter.Lang)
	assert.Equal(t, "ayah", repository.feedFilter.PageType)
	assert.Equal(t, uint64(200), repository.feedFilter.Limit)
	assert.Equal(t, uint64(10000), repository.feedFilter.Offset)

	_, _, err = uc.Feed(context.Background(), "not-a-time", "", "", 0, 0)
	require.ErrorIs(t, err, entity.ErrInvalidSyncSince)
	_, _, err = uc.Feed(context.Background(), "", "ar", "", 0, 0)
	require.ErrorIs(t, err, entity.ErrUnsupportedLanguage)
	_, _, err = uc.Feed(context.Background(), "", "", "juz", 0, 0)
	require.ErrorIs(t, err, entity.ErrInvalidQuranPageType)
}

func TestUseCase_ResolveSlugValidatesContract(t *testing.T) {
	t.Parallel()

	repository := &quranRepoStub{slugResolution: entity.QuranSlugResolution{
		SurahID: 1, RequestedSlug: "old-fatihah", CanonicalSlug: "al-fatihah", IsAlias: true,
	}}
	uc := New(repository)

	resolution, err := uc.ResolveSlug(context.Background(), "old-fatihah")
	require.NoError(t, err)
	assert.True(t, resolution.IsAlias)

	_, err = uc.ResolveSlug(context.Background(), "Al Fatihah")
	require.ErrorIs(t, err, entity.ErrInvalidQuranSlug)
}

type quranRepoStub struct {
	listSurahsLang                    string
	listSurahsIncludeInfo             bool
	getSurahCalls                     int
	getSurahID                        int
	getSurahLang                      string
	getAyahKey                        string
	getAyahLang                       string
	getAyahTranslationSource          string
	getAyahIncludeAudio               bool
	getAyahRecitationID               string
	getSurahAudioID                   int
	getSurahAudioRecitationID         string
	navigationKind                    string
	navigationLang                    string
	navigationAyahsKind               string
	navigationAyahsNumber             int
	navigationAyahsLang               string
	navigationAyahsTranslationSource  string
	navigationAyahsIncludeTranslation bool
	navigationAyahsIncludeAudio       bool
	navigationAyahsRecitationID       string
	missingAssetsFilter               repo.MissingQuranAssetFilter
	sitemapItems                      []entity.QuranSitemapItem
	feedFilter                        repo.QuranFeedFilter
	feedItems                         []entity.QuranSitemapItem
	feedTotal                         int
	slugResolution                    entity.QuranSlugResolution
	coverage                          []entity.QuranEditorialCoverage
}

func (r *quranRepoStub) ListSurahs(_ context.Context, lang string, includeInfo bool) ([]entity.QuranSurah, error) {
	r.listSurahsLang = lang
	r.listSurahsIncludeInfo = includeInfo

	return []entity.QuranSurah{}, nil
}

func (r *quranRepoStub) GetSurah(_ context.Context, surahID int, lang string) (entity.QuranSurah, error) {
	r.getSurahCalls++
	r.getSurahID = surahID
	r.getSurahLang = lang

	return entity.QuranSurah{SurahID: surahID}, nil
}

func (r *quranRepoStub) ListRecitations(context.Context) ([]entity.QuranRecitation, error) {
	return []entity.QuranRecitation{}, nil
}

func (r *quranRepoStub) GetSurahAudioManifest(_ context.Context, surahID int, recitationID string) (entity.QuranSurahAudioManifest, error) {
	r.getSurahAudioID = surahID
	r.getSurahAudioRecitationID = recitationID

	return entity.QuranSurahAudioManifest{}, nil
}

func (r *quranRepoStub) ListTranslationSources(context.Context, string) ([]entity.QuranTranslationSource, error) {
	return []entity.QuranTranslationSource{}, nil
}

func (r *quranRepoStub) ListNavigationSegments(_ context.Context, kind, lang string) ([]entity.QuranNavigationSegment, error) {
	r.navigationKind = kind
	r.navigationLang = lang

	return []entity.QuranNavigationSegment{}, nil
}

func (r *quranRepoStub) GetAyah(
	_ context.Context,
	ayahKey string,
	lang string,
	translationSource string,
	includeAudio bool,
	recitationID string,
) (entity.QuranAyah, error) {
	r.getAyahKey = ayahKey
	r.getAyahLang = lang
	r.getAyahTranslationSource = translationSource
	r.getAyahIncludeAudio = includeAudio
	r.getAyahRecitationID = recitationID

	return entity.QuranAyah{AyahKey: ayahKey}, nil
}

func (r *quranRepoStub) ListSurahAyahs(
	context.Context,
	int,
	int,
	int,
	string,
	string,
	bool,
	bool,
	bool,
	string,
) ([]entity.QuranAyah, error) {
	return []entity.QuranAyah{}, nil
}

func (r *quranRepoStub) ListNavigationAyahs(
	_ context.Context,
	kind string,
	number int,
	lang string,
	translationSource string,
	includeTranslation bool,
	includeAudio bool,
	_ bool,
	recitationID string,
) ([]entity.QuranAyah, error) {
	r.navigationAyahsKind = kind
	r.navigationAyahsNumber = number
	r.navigationAyahsLang = lang
	r.navigationAyahsTranslationSource = translationSource
	r.navigationAyahsIncludeTranslation = includeTranslation
	r.navigationAyahsIncludeAudio = includeAudio
	r.navigationAyahsRecitationID = recitationID

	return []entity.QuranAyah{}, nil
}

func (r *quranRepoStub) SearchAyahs(context.Context, repo.QuranSearchFilter) ([]entity.QuranSearchResult, int, error) {
	return []entity.QuranSearchResult{}, 0, nil
}

func (r *quranRepoStub) ListBookQuranReferences(context.Context, repo.QuranBookReferenceFilter) ([]entity.BookQuranReference, int, error) {
	return []entity.BookQuranReference{}, 0, nil
}

func (r *quranRepoStub) ListMissingQuranAssets(_ context.Context, filter repo.MissingQuranAssetFilter) (entity.EditorialMissingQuranAssets, error) {
	r.missingAssetsFilter = filter
	return entity.EditorialMissingQuranAssets{}, nil
}

func (r *quranRepoStub) ListQuranSitemap(context.Context) ([]entity.QuranSitemapItem, error) {
	return r.sitemapItems, nil
}

func (r *quranRepoStub) ListQuranFeed(_ context.Context, filter repo.QuranFeedFilter) ([]entity.QuranSitemapItem, int, error) {
	r.feedFilter = filter

	return r.feedItems, r.feedTotal, nil
}

func (r *quranRepoStub) ResolveQuranSurahSlug(context.Context, string) (entity.QuranSlugResolution, error) {
	return r.slugResolution, nil
}

func (r *quranRepoStub) ListQuranEditorialCoverage(context.Context) ([]entity.QuranEditorialCoverage, error) {
	return r.coverage, nil
}

var _ repo.QuranRepo = (*quranRepoStub)(nil)
