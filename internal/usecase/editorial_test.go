package usecase_test

import (
	"context"
	"testing"

	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/evrone/go-clean-template/internal/repo"
	"github.com/evrone/go-clean-template/internal/usecase/editorial"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func newEditorialUseCase(t *testing.T) (*editorial.UseCase, *MockEditorialRepo) {
	t.Helper()

	ctrl := gomock.NewController(t)

	mockRepo := NewMockEditorialRepo(ctrl)
	useCase := editorial.New(mockRepo)

	return useCase, mockRepo
}

func TestEditorialBooks(t *testing.T) {
	t.Parallel()

	t.Run("valid filters are normalized", func(t *testing.T) {
		t.Parallel()

		uc, mockRepo := newEditorialUseCase(t)
		status := " published "
		expectedStatus := entity.PublicationStatusPublished
		categoryID := 10
		hasContent := true

		mockRepo.EXPECT().ListBooks(context.Background(), repo.EditorialBookFilter{
			Query:      "kitab",
			Status:     &expectedStatus,
			CategoryID: &categoryID,
			HasContent: &hasContent,
			Limit:      200,
			Offset:     0,
		}).Return([]entity.Book{{ID: 797}}, 1, nil)

		books, total, err := uc.Books(context.Background(), " kitab ", &status, &categoryID, &hasContent, 999, -3)

		require.NoError(t, err)
		assert.Equal(t, 1, total)
		assert.Equal(t, []entity.Book{{ID: 797}}, books)
	})

	t.Run("invalid status is rejected", func(t *testing.T) {
		t.Parallel()

		uc, _ := newEditorialUseCase(t)
		status := "live"

		_, _, err := uc.Books(context.Background(), "", &status, nil, nil, 10, 0)

		require.ErrorIs(t, err, entity.ErrInvalidStatus)
	})
}

func TestEditorialUpdatePublication(t *testing.T) {
	t.Parallel()

	t.Run("valid status is delegated", func(t *testing.T) {
		t.Parallel()

		uc, mockRepo := newEditorialUseCase(t)
		sortOrder := 10
		expected := entity.BookPublication{
			BookID:    797,
			Status:    entity.PublicationStatusPublished,
			Featured:  true,
			SortOrder: &sortOrder,
		}

		mockRepo.EXPECT().
			UpdatePublication(context.Background(), "actor-id", expected).
			Return(expected, nil)

		got, err := uc.UpdatePublication(
			context.Background(),
			"actor-id",
			797,
			"published",
			true,
			&sortOrder,
		)

		require.NoError(t, err)
		assert.Equal(t, expected, got)
	})

	t.Run("invalid status is rejected", func(t *testing.T) {
		t.Parallel()

		uc, _ := newEditorialUseCase(t)

		_, err := uc.UpdatePublication(context.Background(), "actor-id", 797, "live", false, nil)

		require.ErrorIs(t, err, entity.ErrInvalidStatus)
	})
}

func TestEditorialSavePageDraftNormalizesContent(t *testing.T) {
	t.Parallel()

	uc, mockRepo := newEditorialUseCase(t)

	mockRepo.EXPECT().
		SavePageDraft(context.Background(), "actor-id", entity.BookPageEdit{
			BookID:      797,
			PageID:      1,
			Status:      entity.EditStatusDraft,
			ContentHTML: "<p>السلام</p>\n<div>نص</div>",
			ContentText: "السلام\nنص",
		}).
		DoAndReturn(func(_ context.Context, _ string, edit entity.BookPageEdit) (entity.BookPageEdit, error) {
			return edit, nil
		})

	edit, err := uc.SavePageDraft(context.Background(), "actor-id", entity.BookPageEdit{
		BookID:      797,
		PageID:      1,
		ContentHTML: "\ufeff舄<p>السلام</p>\r\n<div>نص</div>",
	})

	require.NoError(t, err)
	assert.Equal(t, "<p>السلام</p>\n<div>نص</div>", edit.ContentHTML)
	assert.Equal(t, "السلام\nنص", edit.ContentText)
}

func TestEditorialSaveMetadataDraftTrimsEmptyFields(t *testing.T) {
	t.Parallel()

	uc, mockRepo := newEditorialUseCase(t)
	displayTitle := "  Title  "
	description := "   "
	categoryID := -1
	expectedTitle := "Title"

	mockRepo.EXPECT().
		SaveMetadataDraft(context.Background(), "actor-id", entity.BookMetadataEdit{
			BookID:       797,
			Status:       entity.EditStatusDraft,
			DisplayTitle: &expectedTitle,
		}).
		DoAndReturn(func(_ context.Context, _ string, edit entity.BookMetadataEdit) (entity.BookMetadataEdit, error) {
			return edit, nil
		})

	edit, err := uc.SaveMetadataDraft(context.Background(), "actor-id", entity.BookMetadataEdit{
		BookID:       797,
		DisplayTitle: &displayTitle,
		Description:  &description,
		CategoryID:   &categoryID,
	})

	require.NoError(t, err)
	assert.Equal(t, &expectedTitle, edit.DisplayTitle)
	assert.Nil(t, edit.Description)
	assert.Nil(t, edit.CategoryID)
}

func TestEditorialTranslationFeedbacksStatusFilter(t *testing.T) {
	t.Parallel()

	t.Run("defaults to open feedback", func(t *testing.T) {
		t.Parallel()

		uc, mockRepo := newEditorialUseCase(t)
		bookID := 1

		mockRepo.EXPECT().
			ListTranslationFeedbacks(context.Background(), repo.TranslationFeedbackFilter{
				BookID: &bookID,
				Lang:   "id",
				Status: entity.FeedbackStatusOpen,
				Limit:  50,
				Offset: 0,
			}).
			Return([]entity.AdminTranslationFeedback{{BookID: 1, Status: entity.FeedbackStatusOpen}}, 1, nil)

		feedbacks, total, err := uc.TranslationFeedbacks(context.Background(), &bookID, nil, " ID ", "", "", 0, -1)

		require.NoError(t, err)
		assert.Equal(t, 1, total)
		assert.Equal(t, entity.FeedbackStatusOpen, feedbacks[0].Status)
	})

	t.Run("all status removes status filter", func(t *testing.T) {
		t.Parallel()

		uc, mockRepo := newEditorialUseCase(t)

		mockRepo.EXPECT().
			TranslationFeedbackSummary(context.Background(), repo.TranslationFeedbackFilter{
				Vote:   "dislike",
				Limit:  25,
				Offset: 0,
			}).
			Return(entity.AdminTranslationFeedbackSummary{Dislikes: 2}, nil)

		summary, err := uc.TranslationFeedbackSummary(context.Background(), nil, nil, "", " DISLIKE ", " all ", 25)

		require.NoError(t, err)
		assert.Equal(t, 2, summary.Dislikes)
	})

	t.Run("invalid status is rejected", func(t *testing.T) {
		t.Parallel()

		uc, _ := newEditorialUseCase(t)

		_, _, err := uc.TranslationFeedbacks(context.Background(), nil, nil, "", "", "done", 10, 0)

		require.ErrorIs(t, err, entity.ErrInvalidFeedback)
	})
}

func TestEditorialMissingReaderAssetsFilter(t *testing.T) {
	t.Parallel()

	t.Run("defaults target languages and normalizes filters", func(t *testing.T) {
		t.Parallel()

		uc, mockRepo := newEditorialUseCase(t)
		bookID := 797

		mockRepo.EXPECT().
			ListMissingReaderAssets(context.Background(), repo.MissingReaderAssetFilter{
				TargetLangs: []string{"id", "en"},
				AssetType:   entity.MissingAssetSectionTranslation,
				BookID:      &bookID,
				Limit:       200,
				Offset:      0,
			}).
			Return(entity.AdminMissingReaderAssets{Total: 1}, nil)

		got, err := uc.MissingReaderAssets(
			context.Background(),
			"",
			" SECTION_TRANSLATION ",
			&bookID,
			999,
			-1,
		)

		require.NoError(t, err)
		assert.Equal(t, 1, got.Total)
	})

	t.Run("normalizes target language region", func(t *testing.T) {
		t.Parallel()

		uc, mockRepo := newEditorialUseCase(t)

		mockRepo.EXPECT().
			ListMissingReaderAssets(context.Background(), repo.MissingReaderAssetFilter{
				TargetLangs: []string{"en"},
				Limit:       50,
				Offset:      0,
			}).
			Return(entity.AdminMissingReaderAssets{}, nil)

		_, err := uc.MissingReaderAssets(context.Background(), "en-US", "", nil, 0, 0)

		require.NoError(t, err)
	})

	t.Run("rejects arabic target", func(t *testing.T) {
		t.Parallel()

		uc, _ := newEditorialUseCase(t)

		_, err := uc.MissingReaderAssets(context.Background(), "ar", "", nil, 50, 0)

		require.ErrorIs(t, err, entity.ErrUnsupportedLanguage)
	})

	t.Run("rejects invalid asset type", func(t *testing.T) {
		t.Parallel()

		uc, _ := newEditorialUseCase(t)

		_, err := uc.MissingReaderAssets(context.Background(), "id", "metadata", nil, 50, 0)

		require.ErrorIs(t, err, entity.ErrInvalidAssetType)
	})
}

func TestEditorialResolveTranslationFeedback(t *testing.T) {
	t.Parallel()

	t.Run("trims resolution note", func(t *testing.T) {
		t.Parallel()

		uc, mockRepo := newEditorialUseCase(t)
		note := "  Sudah diperbaiki.  "
		expectedNote := "Sudah diperbaiki."

		mockRepo.EXPECT().
			ResolveTranslationFeedback(context.Background(), "actor-id", "feedback-id", &expectedNote).
			Return(entity.AdminTranslationFeedback{
				ID:             "feedback-id",
				Status:         entity.FeedbackStatusResolved,
				ResolutionNote: &expectedNote,
			}, nil)

		feedback, err := uc.ResolveTranslationFeedback(context.Background(), "actor-id", " feedback-id ", &note)

		require.NoError(t, err)
		assert.Equal(t, entity.FeedbackStatusResolved, feedback.Status)
		assert.Equal(t, &expectedNote, feedback.ResolutionNote)
	})

	t.Run("empty id is rejected", func(t *testing.T) {
		t.Parallel()

		uc, _ := newEditorialUseCase(t)

		_, err := uc.ReopenTranslationFeedback(context.Background(), "actor-id", " ")

		require.ErrorIs(t, err, entity.ErrInvalidFeedback)
	})
}
