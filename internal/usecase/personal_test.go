package usecase_test

import (
	"context"
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

	mockRepo.EXPECT().
		SaveProgress(context.Background(), entity.ReadingProgress{
			UserID:          "user-id",
			BookID:          1,
			HeadingID:       &headingID,
			ProgressPercent: &progressPercent,
		}).
		DoAndReturn(func(_ context.Context, progress entity.ReadingProgress) (entity.ReadingProgress, error) {
			return progress, nil
		})

	progress, err := uc.SaveProgress(context.Background(), "user-id", 1, nil, &headingID, &progressPercent)

	require.NoError(t, err)
	assert.Nil(t, progress.PageID)
	assert.Equal(t, &headingID, progress.HeadingID)
	assert.Equal(t, &progressPercent, progress.ProgressPercent)
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
			observed: timePtr(time.Now().Add(10 * time.Minute)),
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
		DoAndReturn(func(_ context.Context, item entity.SavedItem) (entity.SavedItem, error) {
			require.NotEmpty(t, item.ID)
			require.NotNil(t, item.SurahID)
			require.NotNil(t, item.AyahKey)
			assert.Equal(t, "user-id", item.UserID)
			assert.Equal(t, entity.SavedItemTypeQuranAyah, item.ItemType)
			assert.Equal(t, 73, *item.SurahID)
			assert.Equal(t, "73:4", *item.AyahKey)
			assert.Equal(t, []string{"tafsir", "fiqh"}, item.Tags)

			return item, nil
		})

	item, err := uc.UpsertSavedItem(context.Background(), "user-id", entity.SavedItem{
		ItemType: entity.SavedItemTypeQuranAyah,
		AyahKey:  &ayahKey,
		Tags:     []string{" Tafsir ", "tafsir", "Fiqh", ""},
	})

	require.NoError(t, err)
	assert.Equal(t, []string{"tafsir", "fiqh"}, item.Tags)
}

func timePtr(value time.Time) *time.Time {
	return &value
}

func TestPersonalUpsertSavedItemNormalizesSingleAyahRange(t *testing.T) {
	t.Parallel()

	uc, mockRepo := newPersonalUseCase(t)
	surahID := 73
	fromAyah := 4
	toAyah := 4

	mockRepo.EXPECT().
		UpsertSavedItem(context.Background(), gomock.Any()).
		DoAndReturn(func(_ context.Context, item entity.SavedItem) (entity.SavedItem, error) {
			require.NotNil(t, item.SurahID)
			require.NotNil(t, item.AyahKey)
			assert.Equal(t, entity.SavedItemTypeQuranAyah, item.ItemType)
			assert.Equal(t, 73, *item.SurahID)
			assert.Equal(t, "73:4", *item.AyahKey)
			assert.Nil(t, item.FromAyahNumber)
			assert.Nil(t, item.ToAyahNumber)

			return item, nil
		})

	item, err := uc.UpsertSavedItem(context.Background(), "user-id", entity.SavedItem{
		ItemType:       entity.SavedItemTypeQuranRange,
		SurahID:        &surahID,
		FromAyahNumber: &fromAyah,
		ToAyahNumber:   &toAyah,
	})

	require.NoError(t, err)
	assert.Equal(t, entity.SavedItemTypeQuranAyah, item.ItemType)
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
