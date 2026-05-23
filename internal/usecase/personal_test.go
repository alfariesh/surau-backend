package usecase_test

import (
	"context"
	"testing"

	"github.com/evrone/go-clean-template/internal/entity"
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

func TestPersonalCreateBookmarkHeadingOnly(t *testing.T) {
	t.Parallel()

	uc, mockRepo := newPersonalUseCase(t)
	headingID := 12
	label := "Bab niat"

	mockRepo.EXPECT().
		CreateBookmark(context.Background(), gomock.Any()).
		DoAndReturn(func(_ context.Context, bookmark entity.Bookmark) (entity.Bookmark, error) {
			assert.NotEmpty(t, bookmark.ID)
			assert.Equal(t, "user-id", bookmark.UserID)
			assert.Equal(t, 1, bookmark.BookID)
			assert.Nil(t, bookmark.PageID)
			assert.Equal(t, &headingID, bookmark.HeadingID)
			assert.Equal(t, &label, bookmark.Label)

			return bookmark, nil
		})

	bookmark, err := uc.CreateBookmark(context.Background(), "user-id", 1, nil, &headingID, &label, nil)

	require.NoError(t, err)
	assert.Nil(t, bookmark.PageID)
	assert.Equal(t, &headingID, bookmark.HeadingID)
	assert.Equal(t, &label, bookmark.Label)
}
