package quran

import (
	"context"
	"testing"

	"github.com/evrone/go-clean-template/internal/contentlang"
	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/evrone/go-clean-template/internal/repo"
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

type quranRepoStub struct {
	listSurahsLang           string
	listSurahsIncludeInfo    bool
	getSurahCalls            int
	getSurahID               int
	getSurahLang             string
	getAyahKey               string
	getAyahLang              string
	getAyahTranslationSource string
	getAyahIncludeAudio      bool
	getAyahRecitationID      string
	missingAssetsFilter      repo.MissingQuranAssetFilter
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

func (r *quranRepoStub) ListTranslationSources(context.Context, string) ([]entity.QuranTranslationSource, error) {
	return []entity.QuranTranslationSource{}, nil
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
	string,
) ([]entity.QuranAyah, error) {
	return []entity.QuranAyah{}, nil
}

func (r *quranRepoStub) SearchAyahs(context.Context, repo.QuranSearchFilter) ([]entity.QuranSearchResult, int, error) {
	return []entity.QuranSearchResult{}, 0, nil
}

func (r *quranRepoStub) ListBookQuranReferences(context.Context, repo.QuranBookReferenceFilter) ([]entity.BookQuranReference, int, error) {
	return []entity.BookQuranReference{}, 0, nil
}

func (r *quranRepoStub) ListMissingQuranAssets(_ context.Context, filter repo.MissingQuranAssetFilter) (entity.AdminMissingQuranAssets, error) {
	r.missingAssetsFilter = filter
	return entity.AdminMissingQuranAssets{}, nil
}

var _ repo.QuranRepo = (*quranRepoStub)(nil)
