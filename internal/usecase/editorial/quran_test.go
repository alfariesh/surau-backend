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
	surahWorkspace entity.QuranSurahEditorialWorkspace
	ayahWorkspace  entity.QuranAyahEditorialWorkspace
	revisions      []entity.QuranEditorialRevision
	total          int
	repoErr        error
	savedSurah     entity.QuranSurahEditorialEdit
	savedAyah      entity.QuranAyahEditorialEdit
	method         string
	actorID        string
	surahID        int
	ayahKey        string
	lang           string
	revisionID     string
	expected       *time.Time
	origin         string
	filter         repo.QuranEditorialRevisionFilter
}

func (f *fakeQuranEditorialRepo) GetSurahEditorialWorkspace(
	_ context.Context,
	surahID int,
	lang string,
) (entity.QuranSurahEditorialWorkspace, error) {
	f.method = "get-surah"
	f.surahID = surahID
	f.lang = lang

	return f.surahWorkspace, f.repoErr
}

//nolint:gocritic // value parameter is fixed by repo.QuranEditorialRepo
func (f *fakeQuranEditorialRepo) SaveSurahEditorialDraft(
	_ context.Context,
	actorID string,
	edit entity.QuranSurahEditorialEdit,
	expected *time.Time,
	origin string,
) (entity.QuranSurahEditorialWorkspace, error) {
	f.method = "save-surah"
	f.actorID = actorID
	f.savedSurah = edit
	f.expected = expected
	f.origin = origin

	return f.surahWorkspace, f.repoErr
}

func (f *fakeQuranEditorialRepo) PublishSurahEditorialDraft(
	_ context.Context,
	actorID string,
	surahID int,
	lang string,
	expected *time.Time,
	origin string,
) (entity.QuranSurahEditorialWorkspace, error) {
	f.method = "publish-surah"
	f.actorID = actorID
	f.surahID = surahID
	f.lang = lang
	f.expected = expected
	f.origin = origin

	return f.surahWorkspace, f.repoErr
}

func (f *fakeQuranEditorialRepo) RestoreSurahEditorialRevision(
	_ context.Context,
	actorID string,
	surahID int,
	lang string,
	revisionID string,
	expected *time.Time,
) (entity.QuranSurahEditorialWorkspace, error) {
	f.method = "restore-surah"
	f.actorID = actorID
	f.surahID = surahID
	f.lang = lang
	f.revisionID = revisionID
	f.expected = expected

	return f.surahWorkspace, f.repoErr
}

func (f *fakeQuranEditorialRepo) GetAyahEditorialWorkspace(
	_ context.Context,
	ayahKey string,
	lang string,
) (entity.QuranAyahEditorialWorkspace, error) {
	f.method = "get-ayah"
	f.ayahKey = ayahKey
	f.lang = lang

	return f.ayahWorkspace, f.repoErr
}

//nolint:gocritic // value parameter is fixed by repo.QuranEditorialRepo
func (f *fakeQuranEditorialRepo) SaveAyahEditorialDraft(
	_ context.Context,
	actorID string,
	edit entity.QuranAyahEditorialEdit,
	expected *time.Time,
	origin string,
) (entity.QuranAyahEditorialWorkspace, error) {
	f.method = "save-ayah"
	f.actorID = actorID
	f.savedAyah = edit
	f.expected = expected
	f.origin = origin

	return f.ayahWorkspace, f.repoErr
}

func (f *fakeQuranEditorialRepo) PublishAyahEditorialDraft(
	_ context.Context,
	actorID string,
	ayahKey string,
	lang string,
	expected *time.Time,
	origin string,
) (entity.QuranAyahEditorialWorkspace, error) {
	f.method = "publish-ayah"
	f.actorID = actorID
	f.ayahKey = ayahKey
	f.lang = lang
	f.expected = expected
	f.origin = origin

	return f.ayahWorkspace, f.repoErr
}

func (f *fakeQuranEditorialRepo) RestoreAyahEditorialRevision(
	_ context.Context,
	actorID string,
	ayahKey string,
	lang string,
	revisionID string,
	expected *time.Time,
) (entity.QuranAyahEditorialWorkspace, error) {
	f.method = "restore-ayah"
	f.actorID = actorID
	f.ayahKey = ayahKey
	f.lang = lang
	f.revisionID = revisionID
	f.expected = expected

	return f.ayahWorkspace, f.repoErr
}

func (f *fakeQuranEditorialRepo) ListQuranEditorialRevisions(
	_ context.Context,
	filter repo.QuranEditorialRevisionFilter,
) ([]entity.QuranEditorialRevision, int, error) {
	f.method = "list-revisions"
	f.filter = filter

	return f.revisions, f.total, f.repoErr
}

// quranCapableEditorialRepo lets New receive its required kitab interface while
// also advertising the optional Quran workflow. No kitab method is called.
type quranCapableEditorialRepo struct {
	repo.EditorialRepo
	*fakeQuranEditorialRepo
}

func TestNewWiresQuranEditorialCapability(t *testing.T) {
	t.Parallel()

	fake := &fakeQuranEditorialRepo{}
	combined := &quranCapableEditorialRepo{fakeQuranEditorialRepo: fake}
	uc := New(combined, nil, nil)

	require.NotNil(t, uc.quranEditorial)
	_, err := uc.SurahEditorialWorkspace(t.Context(), 1, "ID-id")
	require.NoError(t, err)
	assert.Equal(t, "get-surah", fake.method)
	assert.Equal(t, "id", fake.lang)
}

func TestQuranEditorialOperationsFailClosedWithoutRepository(t *testing.T) {
	t.Parallel()

	uc := &UseCase{}
	ayahNumber := 1
	revisionID := "550e8400-e29b-41d4-a716-446655440000"
	tests := []struct {
		name string
		call func() error
	}{
		{
			name: "surah workspace",
			call: func() error {
				_, err := uc.SurahEditorialWorkspace(t.Context(), 1, "id")

				return err
			},
		},
		{
			name: "save surah",
			call: func() error {
				_, err := uc.SaveSurahEditorialDraft(t.Context(), "actor", entity.QuranSurahEditorialEdit{}, nil)

				return err
			},
		},
		{
			name: "publish surah",
			call: func() error {
				_, err := uc.PublishSurahEditorialDraft(t.Context(), "actor", 1, "id", nil)

				return err
			},
		},
		{
			name: "restore surah",
			call: func() error {
				_, err := uc.RestoreSurahEditorialRevision(t.Context(), "actor", 1, "id", revisionID, nil)

				return err
			},
		},
		{
			name: "ayah workspace",
			call: func() error {
				_, err := uc.AyahEditorialWorkspace(t.Context(), "1:1", "id")

				return err
			},
		},
		{
			name: "save ayah",
			call: func() error {
				_, err := uc.SaveAyahEditorialDraft(t.Context(), "actor", entity.QuranAyahEditorialEdit{}, nil)

				return err
			},
		},
		{
			name: "publish ayah",
			call: func() error {
				_, err := uc.PublishAyahEditorialDraft(t.Context(), "actor", "1:1", "id", nil)

				return err
			},
		},
		{
			name: "restore ayah",
			call: func() error {
				_, err := uc.RestoreAyahEditorialRevision(t.Context(), "actor", "1:1", "id", revisionID, nil)

				return err
			},
		},
		{
			name: "revision history",
			call: func() error {
				_, _, err := uc.QuranEditorialRevisions(
					t.Context(), entity.QuranEditorialAssetAyah, 1, &ayahNumber, "id", 20, 0,
				)

				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			require.ErrorIs(t, test.call(), entity.ErrEditorialUnavailable)
		})
	}
}

func TestSaveSurahEditorialDraftNormalizesWorkflowFields(t *testing.T) {
	t.Parallel()

	draft := entity.QuranSurahEditorialEdit{SurahID: 67}
	fake := &fakeQuranEditorialRepo{
		surahWorkspace: entity.QuranSurahEditorialWorkspace{Draft: &draft},
	}
	uc := &UseCase{quranEditorial: fake}

	title := "  Judul  "
	description := "  Deskripsi  "
	meaning := "  Arti  "
	virtue := "  Keutamaan  "
	revelation := "  Asbabun nuzul  "
	content := "  Pokok kandungan  "
	author := "  Penulis  "
	reviewer := "  Reviewer  "
	workspace, err := uc.SaveSurahEditorialDraft(t.Context(), "actor", entity.QuranSurahEditorialEdit{
		SurahID:         67,
		Lang:            "ID-id",
		Status:          entity.EditStatusPublished,
		MetaTitle:       &title,
		MetaDescription: &description,
		ArtiNama:        &meaning,
		Keutamaan:       &virtue,
		AsbabunNuzul:    &revelation,
		PokokKandungan:  &content,
		AuthorName:      &author,
		ReviewedBy:      &reviewer,
		LicenseStatus:   entity.LicenseStatusPermitted,
	}, nil)
	require.NoError(t, err)
	require.NotNil(t, workspace.Draft)
	assert.Equal(t, entity.EditStatusDraft, fake.savedSurah.Status)
	assert.Equal(t, "id", fake.savedSurah.Lang)
	require.NotNil(t, fake.savedSurah.MetaTitle)
	assert.Equal(t, "Judul", *fake.savedSurah.MetaTitle)
	assert.Equal(t, "Deskripsi", *fake.savedSurah.MetaDescription)
	assert.Equal(t, "Arti", *fake.savedSurah.ArtiNama)
	assert.Equal(t, "Keutamaan", *fake.savedSurah.Keutamaan)
	assert.Equal(t, "Asbabun nuzul", *fake.savedSurah.AsbabunNuzul)
	assert.Equal(t, "Pokok kandungan", *fake.savedSurah.PokokKandungan)
	assert.Equal(t, "Penulis", *fake.savedSurah.AuthorName)
	assert.Equal(t, "Reviewer", *fake.savedSurah.ReviewedBy)
	assert.Equal(t, "actor", fake.actorID)
	assert.Nil(t, fake.expected)
	assert.Equal(t, entity.EditOriginREST, fake.origin)
}

func TestSaveAyahEditorialDraftCanonicalizesScope(t *testing.T) {
	t.Parallel()

	draft := entity.QuranAyahEditorialEdit{AyahKey: "2:255"}
	fake := &fakeQuranEditorialRepo{
		ayahWorkspace: entity.QuranAyahEditorialWorkspace{Draft: &draft},
	}
	uc := &UseCase{quranEditorial: fake}

	title := " Ayat Kursi "
	description := " Ringkasan "
	summary := " Intisari "
	virtue := " Keutamaan "
	rangeValue := " 255-257 "
	author := " Penulis "
	reviewer := " Reviewer "
	workspace, err := uc.SaveAyahEditorialDraft(t.Context(), "actor", entity.QuranAyahEditorialEdit{
		AyahKey:         " 2:255 ",
		Lang:            "en-US",
		MetaTitle:       &title,
		MetaDescription: &description,
		Intisari:        &summary,
		Keutamaan:       &virtue,
		FAQ: []entity.QuranAyahEditorialFAQ{{
			Question:   " Kapan? ",
			AnswerHTML: " <p>Sekarang.</p> ",
		}},
		TafsirRange:   &rangeValue,
		AuthorName:    &author,
		ReviewedBy:    &reviewer,
		LicenseStatus: entity.LicenseStatusNeedsReview,
	}, nil)
	require.NoError(t, err)
	require.NotNil(t, workspace.Draft)
	assert.Equal(t, 2, fake.savedAyah.SurahID)
	assert.Equal(t, 255, fake.savedAyah.AyahNumber)
	assert.Equal(t, "2:255", fake.savedAyah.AyahKey)
	assert.Equal(t, "en", fake.savedAyah.Lang)
	assert.Equal(t, entity.EditStatusDraft, fake.savedAyah.Status)
	assert.Equal(t, "Ayat Kursi", *fake.savedAyah.MetaTitle)
	assert.Equal(t, "Ringkasan", *fake.savedAyah.MetaDescription)
	assert.Equal(t, "Intisari", *fake.savedAyah.Intisari)
	assert.Equal(t, "Keutamaan", *fake.savedAyah.Keutamaan)
	assert.Equal(t, "Kapan?", fake.savedAyah.FAQ[0].Question)
	assert.Equal(t, "<p>Sekarang.</p>", fake.savedAyah.FAQ[0].AnswerHTML)
	assert.Equal(t, "255-257", *fake.savedAyah.TafsirRange)
	assert.Equal(t, "Penulis", *fake.savedAyah.AuthorName)
	assert.Equal(t, "Reviewer", *fake.savedAyah.ReviewedBy)
	assert.Equal(t, entity.EditOriginREST, fake.origin)
}

func TestQuranEditorialWorkspacesCanonicalizeScopeAndPropagateErrors(t *testing.T) {
	t.Parallel()

	t.Run("surah success", func(t *testing.T) {
		t.Parallel()

		published := entity.QuranSurahEditorialEdit{SurahID: 67, Lang: "id"}
		want := entity.QuranSurahEditorialWorkspace{Published: &published}
		fake := &fakeQuranEditorialRepo{surahWorkspace: want}
		uc := &UseCase{quranEditorial: fake}

		got, err := uc.SurahEditorialWorkspace(t.Context(), 67, " ID-id ")
		require.NoError(t, err)
		assert.Equal(t, want, got)
		assert.Equal(t, "get-surah", fake.method)
		assert.Equal(t, 67, fake.surahID)
		assert.Equal(t, "id", fake.lang)
	})

	t.Run("ayah repository error", func(t *testing.T) {
		t.Parallel()

		fake := &fakeQuranEditorialRepo{repoErr: entity.ErrQuranAyahNotFound}
		uc := &UseCase{quranEditorial: fake}

		_, err := uc.AyahEditorialWorkspace(t.Context(), " 2:255 ", "EN-us")
		require.ErrorIs(t, err, entity.ErrQuranAyahNotFound)
		assert.Equal(t, "get-ayah", fake.method)
		assert.Equal(t, "2:255", fake.ayahKey)
		assert.Equal(t, "en", fake.lang)
	})
}

func TestSaveQuranEditorialDraftPassesExactPreconditionAndRepositoryError(t *testing.T) {
	t.Parallel()

	expected := time.Date(2026, time.July, 11, 1, 2, 3, 4, time.UTC)

	t.Run("surah", func(t *testing.T) {
		t.Parallel()

		fake := &fakeQuranEditorialRepo{repoErr: entity.ErrPreconditionFailed}
		uc := &UseCase{quranEditorial: fake}

		_, err := uc.SaveSurahEditorialDraft(t.Context(), "reviewer", entity.QuranSurahEditorialEdit{
			SurahID:       1,
			Lang:          "ar-SA",
			LicenseStatus: entity.LicenseStatusNeedsReview,
		}, &expected)
		require.ErrorIs(t, err, entity.ErrPreconditionFailed)
		assert.Equal(t, "save-surah", fake.method)
		assert.Equal(t, "reviewer", fake.actorID)
		assert.Equal(t, "ar", fake.savedSurah.Lang)
		assert.Same(t, &expected, fake.expected)
		assert.Equal(t, entity.EditOriginREST, fake.origin)
	})

	t.Run("ayah", func(t *testing.T) {
		t.Parallel()

		fake := &fakeQuranEditorialRepo{repoErr: entity.ErrPreconditionFailed}
		uc := &UseCase{quranEditorial: fake}

		_, err := uc.SaveAyahEditorialDraft(t.Context(), "reviewer", entity.QuranAyahEditorialEdit{
			AyahKey:       "1:7",
			Lang:          "id-ID",
			LicenseStatus: entity.LicenseStatusPermitted,
		}, &expected)
		require.ErrorIs(t, err, entity.ErrPreconditionFailed)
		assert.Equal(t, "save-ayah", fake.method)
		assert.Equal(t, 1, fake.savedAyah.SurahID)
		assert.Equal(t, 7, fake.savedAyah.AyahNumber)
		assert.Equal(t, "1:7", fake.savedAyah.AyahKey)
		assert.Same(t, &expected, fake.expected)
		assert.Equal(t, entity.EditOriginREST, fake.origin)
	})
}

func TestPublishQuranEditorialDraftPassesWorkflowArguments(t *testing.T) {
	t.Parallel()

	expected := time.Date(2026, time.July, 11, 2, 3, 4, 5, time.UTC)

	t.Run("surah license rejection", func(t *testing.T) {
		t.Parallel()

		fake := &fakeQuranEditorialRepo{repoErr: entity.ErrLicenseNotPermitted}
		uc := &UseCase{quranEditorial: fake}

		_, err := uc.PublishSurahEditorialDraft(t.Context(), "admin", 2, "EN-gb", &expected)
		require.ErrorIs(t, err, entity.ErrLicenseNotPermitted)
		assert.Equal(t, "publish-surah", fake.method)
		assert.Equal(t, "admin", fake.actorID)
		assert.Equal(t, 2, fake.surahID)
		assert.Equal(t, "en", fake.lang)
		assert.Same(t, &expected, fake.expected)
		assert.Equal(t, entity.EditOriginREST, fake.origin)
	})

	t.Run("ayah wildcard success", func(t *testing.T) {
		t.Parallel()

		published := entity.QuranAyahEditorialEdit{AyahKey: "2:255", Status: entity.EditStatusPublished}
		want := entity.QuranAyahEditorialWorkspace{Published: &published}
		fake := &fakeQuranEditorialRepo{ayahWorkspace: want}
		uc := &UseCase{quranEditorial: fake}

		got, err := uc.PublishAyahEditorialDraft(t.Context(), "admin", " 2:255 ", "ID-id", nil)
		require.NoError(t, err)
		assert.Equal(t, want, got)
		assert.Equal(t, "publish-ayah", fake.method)
		assert.Equal(t, "2:255", fake.ayahKey)
		assert.Equal(t, "id", fake.lang)
		assert.Nil(t, fake.expected)
		assert.Equal(t, entity.EditOriginREST, fake.origin)
	})
}

func TestRestoreQuranEditorialRevisionValidatesUUIDAndPassesPrecondition(t *testing.T) {
	t.Parallel()

	const revisionID = "550e8400-e29b-41d4-a716-446655440000"

	expected := time.Date(2026, time.July, 11, 3, 4, 5, 6, time.UTC)

	t.Run("surah success", func(t *testing.T) {
		t.Parallel()

		draft := entity.QuranSurahEditorialEdit{SurahID: 36, Status: entity.EditStatusDraft}
		want := entity.QuranSurahEditorialWorkspace{Draft: &draft}
		fake := &fakeQuranEditorialRepo{surahWorkspace: want}
		uc := &UseCase{quranEditorial: fake}

		got, err := uc.RestoreSurahEditorialRevision(
			t.Context(), "reviewer", 36, "ID", "  "+revisionID+"  ", &expected,
		)
		require.NoError(t, err)
		assert.Equal(t, want, got)
		assert.Equal(t, "restore-surah", fake.method)
		assert.Equal(t, revisionID, fake.revisionID)
		assert.Equal(t, "id", fake.lang)
		assert.Same(t, &expected, fake.expected)
	})

	t.Run("ayah concurrent edit", func(t *testing.T) {
		t.Parallel()

		fake := &fakeQuranEditorialRepo{repoErr: entity.ErrPreconditionFailed}
		uc := &UseCase{quranEditorial: fake}

		_, err := uc.RestoreAyahEditorialRevision(
			t.Context(), "reviewer", " 36:58 ", "AR-sa", revisionID, &expected,
		)
		require.ErrorIs(t, err, entity.ErrPreconditionFailed)
		assert.Equal(t, "restore-ayah", fake.method)
		assert.Equal(t, "36:58", fake.ayahKey)
		assert.Equal(t, "ar", fake.lang)
		assert.Equal(t, revisionID, fake.revisionID)
		assert.Same(t, &expected, fake.expected)
	})
}

func TestQuranEditorialOperationScopeValidationStopsBeforeRepository(t *testing.T) {
	t.Parallel()

	const revisionID = "550e8400-e29b-41d4-a716-446655440000"

	ayah := 1
	zeroAyah := 0
	tests := []struct {
		name    string
		wantErr error
		call    func(*UseCase) error
	}{
		{
			name:    "surah workspace out of range",
			wantErr: entity.ErrQuranSurahNotFound,
			call: func(uc *UseCase) error {
				_, err := uc.SurahEditorialWorkspace(t.Context(), 0, "id")

				return err
			},
		},
		{
			name:    "save surah unsupported language",
			wantErr: entity.ErrUnsupportedLanguage,
			call: func(uc *UseCase) error {
				_, err := uc.SaveSurahEditorialDraft(t.Context(), "actor", entity.QuranSurahEditorialEdit{
					SurahID:       1,
					Lang:          "fr",
					LicenseStatus: entity.LicenseStatusPermitted,
				}, nil)

				return err
			},
		},
		{
			name:    "save surah invalid license",
			wantErr: entity.ErrInvalidLicenseStatus,
			call: func(uc *UseCase) error {
				_, err := uc.SaveSurahEditorialDraft(t.Context(), "actor", entity.QuranSurahEditorialEdit{
					SurahID:       1,
					Lang:          "id",
					LicenseStatus: "invented",
				}, nil)

				return err
			},
		},
		{
			name:    "publish surah out of range",
			wantErr: entity.ErrQuranSurahNotFound,
			call: func(uc *UseCase) error {
				_, err := uc.PublishSurahEditorialDraft(t.Context(), "actor", 115, "id", nil)

				return err
			},
		},
		{
			name:    "restore surah unsupported language",
			wantErr: entity.ErrUnsupportedLanguage,
			call: func(uc *UseCase) error {
				_, err := uc.RestoreSurahEditorialRevision(
					t.Context(), "actor", 1, "fr", revisionID, nil,
				)

				return err
			},
		},
		{
			name:    "restore surah invalid revision id",
			wantErr: entity.ErrInvalidQuranEditorial,
			call: func(uc *UseCase) error {
				_, err := uc.RestoreSurahEditorialRevision(
					t.Context(), "actor", 1, "id", "not-a-uuid", nil,
				)

				return err
			},
		},
		{
			name:    "ayah workspace malformed key",
			wantErr: entity.ErrInvalidAyahKey,
			call: func(uc *UseCase) error {
				_, err := uc.AyahEditorialWorkspace(t.Context(), "not-an-ayah", "id")

				return err
			},
		},
		{
			name:    "save ayah surah out of range",
			wantErr: entity.ErrInvalidAyahKey,
			call: func(uc *UseCase) error {
				_, err := uc.SaveAyahEditorialDraft(t.Context(), "actor", entity.QuranAyahEditorialEdit{
					AyahKey:       "115:1",
					Lang:          "id",
					LicenseStatus: entity.LicenseStatusPermitted,
				}, nil)

				return err
			},
		},
		{
			name:    "publish ayah unsupported language",
			wantErr: entity.ErrUnsupportedLanguage,
			call: func(uc *UseCase) error {
				_, err := uc.PublishAyahEditorialDraft(t.Context(), "actor", "1:1", "fr", nil)

				return err
			},
		},
		{
			name:    "restore ayah invalid revision id",
			wantErr: entity.ErrInvalidQuranEditorial,
			call: func(uc *UseCase) error {
				_, err := uc.RestoreAyahEditorialRevision(
					t.Context(), "actor", "1:1", "id", "not-a-uuid", nil,
				)

				return err
			},
		},
		{
			name:    "restore ayah unsupported language",
			wantErr: entity.ErrUnsupportedLanguage,
			call: func(uc *UseCase) error {
				_, err := uc.RestoreAyahEditorialRevision(
					t.Context(), "actor", "1:1", "fr", revisionID, nil,
				)

				return err
			},
		},
		{
			name:    "revision history invalid asset",
			wantErr: entity.ErrInvalidAssetType,
			call: func(uc *UseCase) error {
				_, _, err := uc.QuranEditorialRevisions(t.Context(), "page", 1, nil, "id", 20, 0)

				return err
			},
		},
		{
			name:    "revision history invalid surah",
			wantErr: entity.ErrQuranSurahNotFound,
			call: func(uc *UseCase) error {
				_, _, err := uc.QuranEditorialRevisions(
					t.Context(), entity.QuranEditorialAssetSurah, 0, nil, "id", 20, 0,
				)

				return err
			},
		},
		{
			name:    "surah revision rejects ayah number",
			wantErr: entity.ErrInvalidQuranEditorial,
			call: func(uc *UseCase) error {
				_, _, err := uc.QuranEditorialRevisions(
					t.Context(), entity.QuranEditorialAssetSurah, 1, &ayah, "id", 20, 0,
				)

				return err
			},
		},
		{
			name:    "ayah revision requires ayah number",
			wantErr: entity.ErrInvalidQuranEditorial,
			call: func(uc *UseCase) error {
				_, _, err := uc.QuranEditorialRevisions(
					t.Context(), entity.QuranEditorialAssetAyah, 1, nil, "id", 20, 0,
				)

				return err
			},
		},
		{
			name:    "ayah revision requires positive ayah number",
			wantErr: entity.ErrInvalidQuranEditorial,
			call: func(uc *UseCase) error {
				_, _, err := uc.QuranEditorialRevisions(
					t.Context(), entity.QuranEditorialAssetAyah, 1, &zeroAyah, "id", 20, 0,
				)

				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := &fakeQuranEditorialRepo{}
			uc := &UseCase{quranEditorial: fake}

			require.ErrorIs(t, test.call(uc), test.wantErr)
			assert.Empty(t, fake.method, "invalid input must not reach the repository")
		})
	}
}

func TestQuranEditorialContentValidationStopsBeforeRepository(t *testing.T) {
	t.Parallel()

	require.ErrorIs(t, normalizeSurahEditorialEdit(nil), entity.ErrInvalidLicenseStatus)
	require.ErrorIs(t, normalizeAyahEditorialEdit(nil), entity.ErrInvalidLicenseStatus)
	require.ErrorIs(t, normalizeSurahEditorialEdit(&entity.QuranSurahEditorialEdit{
		LicenseStatus: "invented",
	}), entity.ErrInvalidLicenseStatus)
	require.ErrorIs(t, normalizeAyahEditorialEdit(&entity.QuranAyahEditorialEdit{
		LicenseStatus: "invented",
	}), entity.ErrInvalidLicenseStatus)

	invalidRange := "1 to 7"
	tests := []struct {
		name string
		edit entity.QuranAyahEditorialEdit
	}{
		{
			name: "invalid tafsir range",
			edit: entity.QuranAyahEditorialEdit{
				AyahKey:       "1:1",
				Lang:          "id",
				TafsirRange:   &invalidRange,
				LicenseStatus: entity.LicenseStatusPermitted,
			},
		},
		{
			name: "blank FAQ question",
			edit: entity.QuranAyahEditorialEdit{
				AyahKey: "1:1",
				Lang:    "id",
				FAQ: []entity.QuranAyahEditorialFAQ{{
					Question:   "   ",
					AnswerHTML: "<p>answer</p>",
				}},
				LicenseStatus: entity.LicenseStatusPermitted,
			},
		},
		{
			name: "blank FAQ answer",
			edit: entity.QuranAyahEditorialEdit{
				AyahKey: "1:1",
				Lang:    "id",
				FAQ: []entity.QuranAyahEditorialFAQ{{
					Question:   "question",
					AnswerHTML: "   ",
				}},
				LicenseStatus: entity.LicenseStatusPermitted,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			fake := &fakeQuranEditorialRepo{}
			uc := &UseCase{quranEditorial: fake}

			_, err := uc.SaveAyahEditorialDraft(t.Context(), "actor", test.edit, nil)
			require.ErrorIs(t, err, entity.ErrInvalidQuranEditorial)
			assert.Empty(t, fake.method, "invalid content must not reach the repository")
		})
	}
}

func TestQuranEditorialRevisionsClampPaginationAndPropagateResults(t *testing.T) {
	t.Parallel()

	ayah := 255
	want := []entity.QuranEditorialRevision{{
		ID:         "550e8400-e29b-41d4-a716-446655440000",
		AssetType:  entity.QuranEditorialAssetAyah,
		SurahID:    2,
		AyahNumber: &ayah,
		Lang:       "en",
	}}
	fake := &fakeQuranEditorialRepo{revisions: want, total: 321}
	uc := &UseCase{quranEditorial: fake}

	got, total, err := uc.QuranEditorialRevisions(
		t.Context(), entity.QuranEditorialAssetAyah, 2, &ayah, "EN-us", 0, -10,
	)
	require.NoError(t, err)
	assert.Equal(t, want, got)
	assert.Equal(t, 321, total)
	assert.Equal(t, repo.QuranEditorialRevisionFilter{
		AssetType:  entity.QuranEditorialAssetAyah,
		SurahID:    2,
		AyahNumber: &ayah,
		Lang:       "en",
		Limit:      defaultLimit,
		Offset:     0,
	}, fake.filter)

	_, _, err = uc.QuranEditorialRevisions(
		t.Context(), entity.QuranEditorialAssetSurah, 1, nil, "id", 9999, 999999,
	)
	require.NoError(t, err)
	assert.Equal(t, uint64(maxLimit), fake.filter.Limit)
	assert.Equal(t, uint64(quranEditorialMaxOffset), fake.filter.Offset)

	fake.repoErr = entity.ErrPreconditionFailed
	_, _, err = uc.QuranEditorialRevisions(
		t.Context(), entity.QuranEditorialAssetSurah, 1, nil, "id", 12, 34,
	)
	require.ErrorIs(t, err, entity.ErrPreconditionFailed)
	assert.Equal(t, uint64(12), fake.filter.Limit)
	assert.Equal(t, uint64(34), fake.filter.Offset)
}
