package editorial

import (
	"context"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/repo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeQuranEditorialRepo struct {
	savedSurah entity.QuranSurahEditorialEdit
	savedAyah  entity.QuranAyahEditorialEdit
	origin     string
	filter     repo.QuranEditorialRevisionFilter
}

func (f *fakeQuranEditorialRepo) GetSurahEditorialWorkspace(
	context.Context,
	int,
	string,
) (entity.QuranSurahEditorialWorkspace, error) {
	return entity.QuranSurahEditorialWorkspace{}, nil
}

//nolint:gocritic // value parameter is fixed by repo.QuranEditorialRepo
func (f *fakeQuranEditorialRepo) SaveSurahEditorialDraft(
	_ context.Context,
	_ string,
	edit entity.QuranSurahEditorialEdit,
	_ *time.Time,
	origin string,
) (entity.QuranSurahEditorialWorkspace, error) {
	f.savedSurah = edit
	f.origin = origin

	return entity.QuranSurahEditorialWorkspace{Draft: &edit}, nil
}

func (f *fakeQuranEditorialRepo) PublishSurahEditorialDraft(
	context.Context,
	string,
	int,
	string,
	*time.Time,
	string,
) (entity.QuranSurahEditorialWorkspace, error) {
	return entity.QuranSurahEditorialWorkspace{}, nil
}

func (f *fakeQuranEditorialRepo) RestoreSurahEditorialRevision(
	context.Context,
	string,
	int,
	string,
	string,
	*time.Time,
) (entity.QuranSurahEditorialWorkspace, error) {
	return entity.QuranSurahEditorialWorkspace{}, nil
}

func (f *fakeQuranEditorialRepo) GetAyahEditorialWorkspace(
	context.Context,
	string,
	string,
) (entity.QuranAyahEditorialWorkspace, error) {
	return entity.QuranAyahEditorialWorkspace{}, nil
}

//nolint:gocritic // value parameter is fixed by repo.QuranEditorialRepo
func (f *fakeQuranEditorialRepo) SaveAyahEditorialDraft(
	_ context.Context,
	_ string,
	edit entity.QuranAyahEditorialEdit,
	_ *time.Time,
	origin string,
) (entity.QuranAyahEditorialWorkspace, error) {
	f.savedAyah = edit
	f.origin = origin

	return entity.QuranAyahEditorialWorkspace{Draft: &edit}, nil
}

func (f *fakeQuranEditorialRepo) PublishAyahEditorialDraft(
	context.Context,
	string,
	string,
	string,
	*time.Time,
	string,
) (entity.QuranAyahEditorialWorkspace, error) {
	return entity.QuranAyahEditorialWorkspace{}, nil
}

func (f *fakeQuranEditorialRepo) RestoreAyahEditorialRevision(
	context.Context,
	string,
	string,
	string,
	string,
	*time.Time,
) (entity.QuranAyahEditorialWorkspace, error) {
	return entity.QuranAyahEditorialWorkspace{}, nil
}

func (f *fakeQuranEditorialRepo) ListQuranEditorialRevisions(
	_ context.Context,
	filter repo.QuranEditorialRevisionFilter,
) ([]entity.QuranEditorialRevision, int, error) {
	f.filter = filter

	return []entity.QuranEditorialRevision{}, 0, nil
}

func TestSaveSurahEditorialDraftNormalizesWorkflowFields(t *testing.T) {
	t.Parallel()

	fake := &fakeQuranEditorialRepo{}
	uc := &UseCase{quranEditorial: fake}

	title := "  Judul  "
	workspace, err := uc.SaveSurahEditorialDraft(t.Context(), "actor", entity.QuranSurahEditorialEdit{
		SurahID:       67,
		Lang:          "ID-id",
		Status:        entity.EditStatusPublished,
		MetaTitle:     &title,
		LicenseStatus: entity.LicenseStatusPermitted,
	}, nil)
	require.NoError(t, err)
	require.NotNil(t, workspace.Draft)
	assert.Equal(t, entity.EditStatusDraft, fake.savedSurah.Status)
	assert.Equal(t, "id", fake.savedSurah.Lang)
	require.NotNil(t, fake.savedSurah.MetaTitle)
	assert.Equal(t, "Judul", *fake.savedSurah.MetaTitle)
	assert.Equal(t, entity.EditOriginREST, fake.origin)
}

func TestSaveAyahEditorialDraftCanonicalizesScope(t *testing.T) {
	t.Parallel()

	fake := &fakeQuranEditorialRepo{}
	uc := &UseCase{quranEditorial: fake}

	workspace, err := uc.SaveAyahEditorialDraft(t.Context(), "actor", entity.QuranAyahEditorialEdit{
		AyahKey:       " 2:255 ",
		Lang:          "en-US",
		LicenseStatus: entity.LicenseStatusNeedsReview,
		FAQ: []entity.QuranAyahEditorialFAQ{{
			Question:   " Kapan? ",
			AnswerHTML: " <p>Sekarang.</p> ",
		}},
	}, nil)
	require.NoError(t, err)
	require.NotNil(t, workspace.Draft)
	assert.Equal(t, 2, fake.savedAyah.SurahID)
	assert.Equal(t, 255, fake.savedAyah.AyahNumber)
	assert.Equal(t, "2:255", fake.savedAyah.AyahKey)
	assert.Equal(t, "en", fake.savedAyah.Lang)
	assert.Equal(t, "Kapan?", fake.savedAyah.FAQ[0].Question)
	assert.Equal(t, entity.EditOriginREST, fake.origin)
}

func TestQuranEditorialValidationAndRevisionClamp(t *testing.T) {
	t.Parallel()

	fake := &fakeQuranEditorialRepo{}
	uc := &UseCase{quranEditorial: fake}

	_, err := uc.SaveSurahEditorialDraft(t.Context(), "actor", entity.QuranSurahEditorialEdit{
		SurahID:       1,
		Lang:          "id",
		LicenseStatus: "invented",
	}, nil)
	require.ErrorIs(t, err, entity.ErrInvalidLicenseStatus)

	_, err = uc.SaveAyahEditorialDraft(t.Context(), "actor", entity.QuranAyahEditorialEdit{
		AyahKey:       "not-an-ayah",
		Lang:          "id",
		LicenseStatus: entity.LicenseStatusPermitted,
	}, nil)
	require.ErrorIs(t, err, entity.ErrInvalidAyahKey)

	_, err = uc.RestoreSurahEditorialRevision(
		t.Context(), "actor", 1, "id", "not-a-uuid", nil,
	)
	require.ErrorIs(t, err, entity.ErrInvalidQuranEditorial)

	_, err = uc.RestoreAyahEditorialRevision(
		t.Context(), "actor", "1:1", "id", "not-a-uuid", nil,
	)
	require.ErrorIs(t, err, entity.ErrInvalidQuranEditorial)

	ayah := 1
	_, _, err = uc.QuranEditorialRevisions(
		t.Context(), entity.QuranEditorialAssetAyah, 1, &ayah, "id", 9999, 999999,
	)
	require.NoError(t, err)
	assert.Equal(t, uint64(maxLimit), fake.filter.Limit)
	assert.Equal(t, uint64(quranEditorialMaxOffset), fake.filter.Offset)
}
