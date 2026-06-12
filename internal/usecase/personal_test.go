package usecase_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/evrone/go-clean-template/internal/repo"
	"github.com/evrone/go-clean-template/internal/usecase/personal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func newPersonalUseCase(t *testing.T) (*personal.UseCase, *MockPersonalRepo) {
	t.Helper()

	ctrl := gomock.NewController(t)

	mockRepo := NewMockPersonalRepo(ctrl)
	useCase := personal.New(mockRepo)

	return useCase, mockRepo
}

func TestPersonalSaveProgressHeadingOnly(t *testing.T) {
	t.Parallel()

	uc, mockRepo := newPersonalUseCase(t)
	headingID := 12
	progressPercent := 33.5
	observedAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.FixedZone("WIB", 7*60*60))

	mockRepo.EXPECT().
		SaveProgress(context.Background(), gomock.Any()).
		DoAndReturn(func(_ context.Context, progress entity.ReadingProgress) (entity.ReadingProgress, error) {
			assert.Equal(t, "user-id", progress.UserID)
			assert.Equal(t, 1, progress.BookID)
			assert.Nil(t, progress.PageID)
			assert.Equal(t, &headingID, progress.HeadingID)
			assert.Equal(t, &progressPercent, progress.ProgressPercent)
			assert.Equal(t, observedAt.UTC(), progress.ObservedAt)

			return progress, nil
		})

	progress, err := uc.SaveProgress(context.Background(), "user-id", 1, nil, &headingID, &progressPercent, &observedAt)

	require.NoError(t, err)
	assert.Nil(t, progress.PageID)
	assert.Equal(t, &headingID, progress.HeadingID)
	assert.Equal(t, &progressPercent, progress.ProgressPercent)
}

func TestPersonalSaveProgressDefaultsObservedAt(t *testing.T) {
	t.Parallel()

	uc, mockRepo := newPersonalUseCase(t)

	mockRepo.EXPECT().
		SaveProgress(context.Background(), gomock.Any()).
		DoAndReturn(func(_ context.Context, progress entity.ReadingProgress) (entity.ReadingProgress, error) {
			assert.WithinDuration(t, time.Now().UTC(), progress.ObservedAt, time.Minute)

			return progress, nil
		})

	_, err := uc.SaveProgress(context.Background(), "user-id", 1, nil, nil, nil, nil)

	require.NoError(t, err)
}

func TestPersonalSaveProgressRejectsFutureObservedAt(t *testing.T) {
	t.Parallel()

	uc, _ := newPersonalUseCase(t)

	_, err := uc.SaveProgress(
		context.Background(),
		"user-id",
		1,
		nil,
		nil,
		nil,
		new(time.Now().Add(10*time.Minute)),
	)

	require.ErrorIs(t, err, entity.ErrInvalidReadingProgress)
}

func TestPersonalListProgressNormalizesLangAndClampsPagination(t *testing.T) {
	t.Parallel()

	uc, mockRepo := newPersonalUseCase(t)

	mockRepo.EXPECT().
		ListProgress(context.Background(), "user-id", "id", uint64(200), uint64(10000)).
		Return([]entity.ContinueReadingEntry{}, 0, nil)

	entries, total, err := uc.ListProgress(context.Background(), "user-id", "", 999, 99999)

	require.NoError(t, err)
	assert.Empty(t, entries)
	assert.Zero(t, total)
}

func TestPersonalListProgressRejectsUnsupportedLang(t *testing.T) {
	t.Parallel()

	uc, _ := newPersonalUseCase(t)

	_, _, err := uc.ListProgress(context.Background(), "user-id", "fr", 10, 0)

	require.ErrorIs(t, err, entity.ErrUnsupportedLanguage)
}

func TestPersonalSaveQuranProgressNormalizesAyahKey(t *testing.T) {
	t.Parallel()

	uc, mockRepo := newPersonalUseCase(t)
	observedAt := time.Date(2026, 1, 1, 1, 2, 3, 0, time.FixedZone("WIB", 7*60*60))

	mockRepo.EXPECT().
		SaveQuranProgress(context.Background(), gomock.Any()).
		DoAndReturn(func(_ context.Context, progress entity.QuranReadingProgress) (entity.QuranReadingProgress, error) {
			assert.Equal(t, "user-id", progress.UserID)
			assert.Equal(t, 73, progress.SurahID)
			assert.Equal(t, 4, progress.AyahNumber)
			assert.Equal(t, "73:4", progress.AyahKey)
			assert.Equal(t, observedAt.UTC(), progress.ObservedAt)

			return progress, nil
		})

	progress, err := uc.SaveQuranProgress(context.Background(), "user-id", " 73:4 ", &observedAt)

	require.NoError(t, err)
	assert.Equal(t, "73:4", progress.AyahKey)
}

func TestPersonalSaveQuranProgressRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		ayahKey  string
		observed *time.Time
		wantErr  error
	}{
		{
			name:    "invalid ayah key",
			ayahKey: "not-an-ayah",
			wantErr: entity.ErrInvalidAyahKey,
		},
		{
			name:     "future observed at",
			ayahKey:  "73:4",
			observed: new(time.Now().Add(10 * time.Minute)),
			wantErr:  entity.ErrInvalidQuranProgress,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			uc, _ := newPersonalUseCase(t)

			_, err := uc.SaveQuranProgress(context.Background(), "user-id", tt.ayahKey, tt.observed)

			require.ErrorIs(t, err, tt.wantErr)
		})
	}
}

func TestPersonalQuranProgressReadMethods(t *testing.T) {
	t.Parallel()

	uc, mockRepo := newPersonalUseCase(t)
	expected := entity.QuranReadingProgress{UserID: "user-id", SurahID: 73, AyahNumber: 4, AyahKey: "73:4"}

	mockRepo.EXPECT().GetQuranProgress(context.Background(), "user-id").Return(expected, nil)
	mockRepo.EXPECT().GetQuranSurahProgress(context.Background(), "user-id", 73).Return(expected, nil)
	mockRepo.EXPECT().ListQuranSurahProgress(context.Background(), "user-id").Return([]entity.QuranReadingProgress{expected}, nil)

	progress, err := uc.GetQuranProgress(context.Background(), "user-id")
	require.NoError(t, err)
	assert.Equal(t, expected, progress)

	progress, err = uc.GetQuranSurahProgress(context.Background(), "user-id", 73)
	require.NoError(t, err)
	assert.Equal(t, expected, progress)

	list, err := uc.ListQuranSurahProgress(context.Background(), "user-id")
	require.NoError(t, err)
	assert.Equal(t, []entity.QuranReadingProgress{expected}, list)
}

func TestPersonalUpsertSavedItemNormalizesQuranTags(t *testing.T) {
	t.Parallel()

	uc, mockRepo := newPersonalUseCase(t)
	ayahKey := "73:4"

	mockRepo.EXPECT().
		UpsertSavedItem(context.Background(), gomock.Any()).
		DoAndReturn(func(_ context.Context, item entity.SavedItem) (entity.SavedItem, bool, error) {
			require.NotEmpty(t, item.ID)
			require.NotNil(t, item.SurahID)
			require.NotNil(t, item.AyahKey)
			assert.Equal(t, "user-id", item.UserID)
			assert.Equal(t, entity.SavedItemTypeQuranAyah, item.ItemType)
			assert.Equal(t, 73, *item.SurahID)
			assert.Equal(t, "73:4", *item.AyahKey)
			assert.Equal(t, []string{"tafsir", "fiqh"}, item.Tags)

			return item, true, nil
		})

	item, created, err := uc.UpsertSavedItem(context.Background(), "user-id", entity.SavedItem{
		ItemType: entity.SavedItemTypeQuranAyah,
		AyahKey:  &ayahKey,
		Tags:     []string{" Tafsir ", "tafsir", "Fiqh", ""},
	})

	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, []string{"tafsir", "fiqh"}, item.Tags)
}

func TestPersonalUpsertSavedItemKeepsAbsentTagsNil(t *testing.T) {
	t.Parallel()

	uc, mockRepo := newPersonalUseCase(t)
	ayahKey := "73:4"

	mockRepo.EXPECT().
		UpsertSavedItem(context.Background(), gomock.Any()).
		DoAndReturn(func(_ context.Context, item entity.SavedItem) (entity.SavedItem, bool, error) {
			assert.Nil(t, item.Tags)

			return item, false, nil
		})

	_, created, err := uc.UpsertSavedItem(context.Background(), "user-id", entity.SavedItem{
		ItemType: entity.SavedItemTypeQuranAyah,
		AyahKey:  &ayahKey,
	})

	require.NoError(t, err)
	assert.False(t, created)
}

func TestPersonalUpdateSavedItemPatchSemantics(t *testing.T) {
	t.Parallel()

	t.Run("empty patch rejected", func(t *testing.T) {
		t.Parallel()

		uc, _ := newPersonalUseCase(t)

		_, err := uc.UpdateSavedItem(context.Background(), "user-id", "saved-id", entity.SavedItemPatch{})

		require.ErrorIs(t, err, entity.ErrInvalidSavedItem)
	})

	t.Run("label too long rejected", func(t *testing.T) {
		t.Parallel()

		uc, _ := newPersonalUseCase(t)
		label := strings.Repeat("a", 256)

		_, err := uc.UpdateSavedItem(context.Background(), "user-id", "saved-id", entity.SavedItemPatch{
			Label:    &label,
			LabelSet: true,
		})

		require.ErrorIs(t, err, entity.ErrInvalidSavedItem)
	})

	t.Run("explicit null tags clear to empty", func(t *testing.T) {
		t.Parallel()

		uc, mockRepo := newPersonalUseCase(t)

		mockRepo.EXPECT().
			UpdateSavedItem(context.Background(), "user-id", "saved-id", gomock.Any()).
			DoAndReturn(func(_ context.Context, _, _ string, patch entity.SavedItemPatch) (entity.SavedItem, error) {
				assert.True(t, patch.TagsSet)
				assert.Equal(t, []string{}, patch.Tags)
				assert.False(t, patch.LabelSet)
				assert.False(t, patch.NoteSet)

				return entity.SavedItem{ID: "saved-id"}, nil
			})

		_, err := uc.UpdateSavedItem(context.Background(), "user-id", "saved-id", entity.SavedItemPatch{
			TagsSet: true,
			Tags:    nil,
		})

		require.NoError(t, err)
	})

	t.Run("tags normalized", func(t *testing.T) {
		t.Parallel()

		uc, mockRepo := newPersonalUseCase(t)

		mockRepo.EXPECT().
			UpdateSavedItem(context.Background(), "user-id", "saved-id", gomock.Any()).
			DoAndReturn(func(_ context.Context, _, _ string, patch entity.SavedItemPatch) (entity.SavedItem, error) {
				assert.Equal(t, []string{"tafsir", "fiqh"}, patch.Tags)

				return entity.SavedItem{ID: "saved-id"}, nil
			})

		_, err := uc.UpdateSavedItem(context.Background(), "user-id", "saved-id", entity.SavedItemPatch{
			TagsSet: true,
			Tags:    []string{" Tafsir ", "tafsir", "Fiqh", ""},
		})

		require.NoError(t, err)
	})
}

func TestPersonalUpsertSavedItemNormalizesSingleAyahRange(t *testing.T) {
	t.Parallel()

	uc, mockRepo := newPersonalUseCase(t)
	surahID := 73
	fromAyah := 4
	toAyah := 4

	mockRepo.EXPECT().
		UpsertSavedItem(context.Background(), gomock.Any()).
		DoAndReturn(func(_ context.Context, item entity.SavedItem) (entity.SavedItem, bool, error) {
			require.NotNil(t, item.SurahID)
			require.NotNil(t, item.AyahKey)
			assert.Equal(t, entity.SavedItemTypeQuranAyah, item.ItemType)
			assert.Equal(t, 73, *item.SurahID)
			assert.Equal(t, "73:4", *item.AyahKey)
			assert.Nil(t, item.FromAyahNumber)
			assert.Nil(t, item.ToAyahNumber)

			return item, true, nil
		})

	item, _, err := uc.UpsertSavedItem(context.Background(), "user-id", entity.SavedItem{
		ItemType:       entity.SavedItemTypeQuranRange,
		SurahID:        &surahID,
		FromAyahNumber: &fromAyah,
		ToAyahNumber:   &toAyah,
	})

	require.NoError(t, err)
	assert.Equal(t, entity.SavedItemTypeQuranAyah, item.ItemType)
}

func TestPersonalSyncPersonalData(t *testing.T) {
	t.Parallel()

	t.Run("nil since passes through", func(t *testing.T) {
		t.Parallel()

		uc, mockRepo := newPersonalUseCase(t)

		mockRepo.EXPECT().
			SyncSnapshot(context.Background(), "user-id", nil).
			Return(entity.PersonalSyncSnapshot{}, nil)

		_, err := uc.SyncPersonalData(context.Background(), "user-id", nil)

		require.NoError(t, err)
	})

	t.Run("since normalized to UTC", func(t *testing.T) {
		t.Parallel()

		uc, mockRepo := newPersonalUseCase(t)
		since := time.Date(2026, 6, 11, 7, 0, 0, 0, time.FixedZone("WIB", 7*60*60))

		mockRepo.EXPECT().
			SyncSnapshot(context.Background(), "user-id", gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, got *time.Time) (entity.PersonalSyncSnapshot, error) {
				require.NotNil(t, got)
				assert.Equal(t, time.Date(2026, 6, 11, 0, 0, 0, 0, time.UTC), *got)

				return entity.PersonalSyncSnapshot{}, nil
			})

		_, err := uc.SyncPersonalData(context.Background(), "user-id", &since)

		require.NoError(t, err)
	})

	t.Run("future since rejected", func(t *testing.T) {
		t.Parallel()

		uc, _ := newPersonalUseCase(t)
		future := time.Now().Add(10 * time.Minute)

		_, err := uc.SyncPersonalData(context.Background(), "user-id", &future)

		require.ErrorIs(t, err, entity.ErrInvalidSyncSince)
	})
}

func TestPersonalKhatamValidations(t *testing.T) {
	t.Parallel()

	t.Run("mark rejects out-of-range juz", func(t *testing.T) {
		t.Parallel()

		uc, _ := newPersonalUseCase(t)

		_, err := uc.MarkKhatamJuz(context.Background(), "user-id", 0)
		require.ErrorIs(t, err, entity.ErrInvalidJuzNumber)

		_, err = uc.UnmarkKhatamJuz(context.Background(), "user-id", 31)
		require.ErrorIs(t, err, entity.ErrInvalidJuzNumber)
	})

	t.Run("start trims notes and generates id", func(t *testing.T) {
		t.Parallel()

		uc, mockRepo := newPersonalUseCase(t)
		notes := "  Khatam Ramadhan  "

		mockRepo.EXPECT().
			StartKhatamCycle(context.Background(), gomock.Any()).
			DoAndReturn(func(_ context.Context, cycle entity.QuranKhatamCycle) (entity.QuranKhatamCycle, error) {
				require.NotEmpty(t, cycle.ID)
				assert.Equal(t, "user-id", cycle.UserID)
				require.NotNil(t, cycle.Notes)
				assert.Equal(t, "Khatam Ramadhan", *cycle.Notes)

				return cycle, nil
			})

		_, err := uc.StartKhatamCycle(context.Background(), "user-id", &notes)

		require.NoError(t, err)
	})

	t.Run("start drops empty notes", func(t *testing.T) {
		t.Parallel()

		uc, mockRepo := newPersonalUseCase(t)
		notes := "   "

		mockRepo.EXPECT().
			StartKhatamCycle(context.Background(), gomock.Any()).
			DoAndReturn(func(_ context.Context, cycle entity.QuranKhatamCycle) (entity.QuranKhatamCycle, error) {
				assert.Nil(t, cycle.Notes)

				return cycle, nil
			})

		_, err := uc.StartKhatamCycle(context.Background(), "user-id", &notes)

		require.NoError(t, err)
	})

	t.Run("history clamps pagination", func(t *testing.T) {
		t.Parallel()

		uc, mockRepo := newPersonalUseCase(t)

		mockRepo.EXPECT().
			ListKhatamHistory(context.Background(), "user-id", uint64(200), uint64(0)).
			Return([]entity.QuranKhatamCycle{}, 0, nil)

		_, _, err := uc.ListKhatamHistory(context.Background(), "user-id", 999, -5)

		require.NoError(t, err)
	})
}

func TestPersonalListSavedItemsClampsPaginationAndNormalizesTag(t *testing.T) {
	t.Parallel()

	uc, mockRepo := newPersonalUseCase(t)
	bookID := 1

	mockRepo.EXPECT().
		ListSavedItems(context.Background(), "user-id", repo.SavedItemFilter{
			ItemType: entity.SavedItemTypeBookHeading,
			BookID:   &bookID,
			Tag:      "tafsir",
			Limit:    200,
			Offset:   0,
		}).
		Return([]entity.SavedItem{}, 0, nil)

	items, total, err := uc.ListSavedItems(
		context.Background(),
		"user-id",
		" book_heading ",
		&bookID,
		nil,
		" Tafsir ",
		500,
		-10,
	)

	require.NoError(t, err)
	assert.Empty(t, items)
	assert.Zero(t, total)
}
