package quran

import (
	"context"
	"testing"

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
	assert.Equal(t, defaultLang, repository.listSurahsLang)
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
	assert.Equal(t, defaultLang, repository.getSurahLang)
}

func TestUseCase_AyahNormalizesDefaults(t *testing.T) {
	t.Parallel()

	repository := &quranRepoStub{}
	uc := New(repository)

	_, err := uc.Ayah(context.Background(), "73:4", "", "", true, " rec-1 ")

	require.NoError(t, err)
	assert.Equal(t, "73:4", repository.getAyahKey)
	assert.Equal(t, defaultLang, repository.getAyahLang)
	assert.Equal(t, defaultTranslationSourceID, repository.getAyahTranslationSource)
	assert.True(t, repository.getAyahIncludeAudio)
	assert.Equal(t, "rec-1", repository.getAyahRecitationID)
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

var _ repo.QuranRepo = (*quranRepoStub)(nil)
