package usecase_test

import (
	"context"
	"testing"
	"time"

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
		}, nil, entity.EditOriginREST).
		DoAndReturn(func(_ context.Context, _ string, edit entity.BookPageEdit, _ *time.Time, _ string) (entity.BookPageEdit, error) {
			return edit, nil
		})

	edit, err := uc.SavePageDraft(context.Background(), "actor-id", entity.BookPageEdit{
		BookID:      797,
		PageID:      1,
		ContentHTML: "\ufeff舄<p>السلام</p>\r\n<div>نص</div>",
	}, nil, entity.EditOriginREST)

	require.NoError(t, err)
	assert.Equal(t, "<p>السلام</p>\n<div>نص</div>", edit.ContentHTML)
	assert.Equal(t, "السلام\nنص", edit.ContentText)
}

func TestEditorialSaveMetadataDraftTrimsEmptyFields(t *testing.T) {
	t.Parallel()

	uc, mockRepo := newEditorialUseCase(t)
	displayTitle := "  Title  "
	bibliography := "  Bibliography  "
	hint := " \t "
	description := "   "
	categoryID := -1
	expectedTitle := "Title"
	expectedBibliography := "Bibliography"

	mockRepo.EXPECT().
		SaveMetadataDraft(context.Background(), "actor-id", entity.BookMetadataEdit{
			BookID:       797,
			Status:       entity.EditStatusDraft,
			DisplayTitle: &expectedTitle,
			Bibliography: &expectedBibliography,
		}, nil, entity.EditOriginREST).
		DoAndReturn(func(_ context.Context, _ string, edit entity.BookMetadataEdit, _ *time.Time, _ string) (entity.BookMetadataEdit, error) {
			return edit, nil
		})

	edit, err := uc.SaveMetadataDraft(context.Background(), "actor-id", entity.BookMetadataEdit{
		BookID:       797,
		DisplayTitle: &displayTitle,
		Bibliography: &bibliography,
		Hint:         &hint,
		Description:  &description,
		CategoryID:   &categoryID,
	}, nil, entity.EditOriginREST)

	require.NoError(t, err)
	assert.Equal(t, &expectedTitle, edit.DisplayTitle)
	assert.Equal(t, &expectedBibliography, edit.Bibliography)
	assert.Nil(t, edit.Hint)
	assert.Nil(t, edit.Description)
	assert.Nil(t, edit.CategoryID)
}

func TestEditorialRestorePageDraftRevision(t *testing.T) {
	t.Parallel()

	t.Run("replays snapshot as new draft with restore origin", func(t *testing.T) {
		t.Parallel()

		uc, mockRepo := newEditorialUseCase(t)
		pageID := 1

		mockRepo.EXPECT().
			GetSourceEditRevision(context.Background(), "revision-id").
			Return(entity.BookSourceEditRevision{
				ID:        "revision-id",
				BookID:    797,
				AssetType: entity.SourceEditAssetPage,
				PageID:    &pageID,
				Version:   2,
				Snapshot:  []byte(`{"content_html":"<p>lama</p>","content_text":"lama"}`),
			}, nil)
		mockRepo.EXPECT().
			SavePageDraft(context.Background(), "actor-id", entity.BookPageEdit{
				BookID:      797,
				PageID:      1,
				Status:      entity.EditStatusDraft,
				ContentHTML: "<p>lama</p>",
				ContentText: "lama",
			}, nil, entity.EditOriginRestore).
			DoAndReturn(func(_ context.Context, _ string, edit entity.BookPageEdit, _ *time.Time, _ string) (entity.BookPageEdit, error) {
				return edit, nil
			})

		edit, err := uc.RestorePageDraftRevision(context.Background(), "actor-id", 797, 1, "revision-id")

		require.NoError(t, err)
		assert.Equal(t, "<p>lama</p>", edit.ContentHTML)
	})

	t.Run("revision scoped to another page is rejected", func(t *testing.T) {
		t.Parallel()

		uc, mockRepo := newEditorialUseCase(t)
		otherPageID := 9

		mockRepo.EXPECT().
			GetSourceEditRevision(context.Background(), "revision-id").
			Return(entity.BookSourceEditRevision{
				ID:        "revision-id",
				BookID:    797,
				AssetType: entity.SourceEditAssetPage,
				PageID:    &otherPageID,
				Snapshot:  []byte(`{"content_html":"<p>x</p>"}`),
			}, nil)

		_, err := uc.RestorePageDraftRevision(context.Background(), "actor-id", 797, 1, "revision-id")

		require.ErrorIs(t, err, entity.ErrDraftNotFound)
	})
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
			Return([]entity.EditorialTranslationFeedback{{BookID: 1, Status: entity.FeedbackStatusOpen}}, 1, nil)

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
			Return(entity.EditorialTranslationFeedbackSummary{Dislikes: 2}, nil)

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
			Return(entity.EditorialMissingReaderAssets{Total: 1}, nil)

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
			Return(entity.EditorialMissingReaderAssets{}, nil)

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
			Return(entity.EditorialTranslationFeedback{
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

func TestEditorialCreateProductionProject(t *testing.T) {
	t.Parallel()

	uc, mockRepo := newEditorialUseCase(t)
	notes := "  Initial batch  "
	expectedNotes := "Initial batch"

	mockRepo.EXPECT().
		CreateProductionProject(context.Background(), "actor-id", entity.BookProductionProject{
			BookID:            797,
			Lang:              "en",
			WorkflowStatus:    entity.ProductionWorkflowCandidate,
			PublicationStatus: entity.ProductionPublicationHidden,
			RequiresReview:    true,
			RequiresAudio:     true,
			Priority:          3,
			Notes:             &expectedNotes,
		}).
		Return(entity.BookProductionProject{ID: "project-id", BookID: 797, Lang: "en"}, nil)

	project, err := uc.CreateProductionProject(context.Background(), "actor-id", entity.BookProductionProject{
		BookID:         797,
		Lang:           "en-US",
		RequiresReview: true,
		RequiresAudio:  true,
		Priority:       3,
		Notes:          &notes,
	})

	require.NoError(t, err)
	assert.Equal(t, "project-id", project.ID)
}

func TestEditorialProductionCandidates(t *testing.T) {
	t.Parallel()

	uc, mockRepo := newEditorialUseCase(t)
	categoryID := 10
	authorID := 20
	hasContent := true
	expected := []entity.BookProductionCandidate{{BookID: 797, Name: "book"}}

	mockRepo.EXPECT().
		ListProductionCandidates(context.Background(), repo.ProductionCandidateFilter{
			Lang:       "en",
			Query:      "kitab",
			CategoryID: &categoryID,
			AuthorID:   &authorID,
			HasContent: &hasContent,
			Unstarted:  true,
			Limit:      200,
			Offset:     0,
		}).
		Return(expected, 1, nil)

	got, total, err := uc.ProductionCandidates(
		context.Background(),
		"en-US",
		" kitab ",
		&categoryID,
		&authorID,
		&hasContent,
		true,
		999,
		-1,
	)

	require.NoError(t, err)
	assert.Equal(t, 1, total)
	assert.Equal(t, expected, got)
}

func TestEditorialProductionDashboard(t *testing.T) {
	t.Parallel()

	t.Run("normalizes lang and returns operational counts", func(t *testing.T) {
		t.Parallel()

		uc, mockRepo := newEditorialUseCase(t)
		hasContent := true
		expectedEvents := []entity.BookProductionEvent{{
			ID:        "event-id",
			ProjectID: "project-id",
			EventType: entity.ProductionEventDraftSave,
		}}

		gomock.InOrder(
			mockRepo.EXPECT().
				ListProductionCandidates(context.Background(), repo.ProductionCandidateFilter{
					Lang:       "en",
					HasContent: &hasContent,
					Unstarted:  true,
					Limit:      1,
					Offset:     0,
				}).
				Return(nil, 10, nil),
			mockRepo.EXPECT().
				ListProductionProjects(context.Background(), repo.ProductionProjectFilter{
					Lang:              "en",
					PublicationStatus: entity.ProductionPublicationHidden,
					Limit:             1,
					Offset:            0,
				}).
				Return(nil, 4, nil),
			mockRepo.EXPECT().
				ListProductionProjects(context.Background(), repo.ProductionProjectFilter{
					Lang:      "en",
					NeedsWork: true,
					Limit:     1,
					Offset:    0,
				}).
				Return(nil, 3, nil),
			mockRepo.EXPECT().
				ListProductionProjects(context.Background(), repo.ProductionProjectFilter{
					Lang:           "en",
					ReadyToPublish: true,
					Limit:          1,
					Offset:         0,
				}).
				Return(nil, 1, nil),
			mockRepo.EXPECT().
				ListProductionProjects(context.Background(), repo.ProductionProjectFilter{
					Lang:              "en",
					PublicationStatus: entity.ProductionPublicationPublished,
					Limit:             1,
					Offset:            0,
				}).
				Return(nil, 12, nil),
			mockRepo.EXPECT().
				ListProductionEventsGlobal(context.Background(), "en", uint64(100), uint64(0)).
				Return(expectedEvents, 8, nil),
		)

		got, err := uc.ProductionDashboard(context.Background(), "en-US", 999)

		require.NoError(t, err)
		assert.Equal(t, "en", got.Lang)
		assert.Equal(t, 10, got.CandidateCount)
		assert.Equal(t, 4, got.ActiveProjectCount)
		assert.Equal(t, 3, got.NeedsWorkCount)
		assert.Equal(t, 1, got.ReadyToPublishCount)
		assert.Equal(t, 12, got.PublishedCount)
		assert.Equal(t, 8, got.RecentEventsTotal)
		assert.Equal(t, expectedEvents, got.RecentEvents)
	})

	t.Run("rejects bad lang", func(t *testing.T) {
		t.Parallel()

		uc, _ := newEditorialUseCase(t)

		_, err := uc.ProductionDashboard(context.Background(), "ar", 20)

		require.ErrorIs(t, err, entity.ErrUnsupportedLanguage)
	})
}

func TestEditorialProductionProjects(t *testing.T) {
	t.Parallel()

	t.Run("passes ready filter", func(t *testing.T) {
		t.Parallel()

		uc, mockRepo := newEditorialUseCase(t)
		expected := []entity.BookProductionProject{{ID: "project-id", Lang: "id"}}

		mockRepo.EXPECT().
			ListProductionProjects(context.Background(), repo.ProductionProjectFilter{
				Lang:           "id",
				ReadyToPublish: true,
				Limit:          50,
				Offset:         0,
			}).
			Return(expected, 1, nil)

		got, total, err := uc.ProductionProjects(context.Background(), nil, "id", "", "", true, false, 0, 0)

		require.NoError(t, err)
		assert.Equal(t, 1, total)
		assert.Equal(t, expected, got)
	})

	t.Run("rejects mutually exclusive queue filters", func(t *testing.T) {
		t.Parallel()

		uc, _ := newEditorialUseCase(t)

		_, _, err := uc.ProductionProjects(context.Background(), nil, "id", "", "", true, true, 50, 0)

		require.ErrorIs(t, err, entity.ErrInvalidStatus)
	})
}

func TestEditorialProductionWorkspace(t *testing.T) {
	t.Parallel()

	uc, mockRepo := newEditorialUseCase(t)
	expected := entity.BookProductionWorkspace{
		Project: entity.BookProductionProject{ID: "project-id", BookID: 797, Lang: "id"},
		Book:    entity.BookProductionWorkspaceBook{ID: 797, Name: "book", HasContent: true},
	}

	mockRepo.EXPECT().
		ProductionWorkspace(context.Background(), "project-id").
		Return(expected, nil)

	got, err := uc.ProductionWorkspace(context.Background(), " project-id ")

	require.NoError(t, err)
	assert.Equal(t, expected, got)
}

func TestEditorialProductionActivity(t *testing.T) {
	t.Parallel()

	t.Run("normalizes pagination", func(t *testing.T) {
		t.Parallel()

		uc, mockRepo := newEditorialUseCase(t)
		expected := []entity.BookProductionEvent{{
			ID:        "event-id",
			ProjectID: "project-id",
			EventType: entity.ProductionEventDraftSave,
		}}

		mockRepo.EXPECT().
			ListProductionEvents(context.Background(), "project-id", uint64(100), uint64(0)).
			Return(expected, 1, nil)

		got, total, err := uc.ProductionActivity(context.Background(), " project-id ", 999, -1)

		require.NoError(t, err)
		assert.Equal(t, 1, total)
		assert.Equal(t, expected, got)
	})

	t.Run("rejects empty project", func(t *testing.T) {
		t.Parallel()

		uc, _ := newEditorialUseCase(t)

		_, _, err := uc.ProductionActivity(context.Background(), " ", 50, 0)

		require.ErrorIs(t, err, entity.ErrProductionProjectNotFound)
	})
}

func TestEditorialGlobalProductionActivity(t *testing.T) {
	t.Parallel()

	uc, mockRepo := newEditorialUseCase(t)
	expected := []entity.BookProductionEvent{{
		ID:        "event-id",
		ProjectID: "project-id",
		EventType: entity.ProductionEventProjectUpdate,
	}}

	mockRepo.EXPECT().
		ListProductionEventsGlobal(context.Background(), "id", uint64(100), uint64(0)).
		Return(expected, 1, nil)

	got, total, err := uc.GlobalProductionActivity(context.Background(), " id ", 999, -1)

	require.NoError(t, err)
	assert.Equal(t, 1, total)
	assert.Equal(t, expected, got)
}

func TestEditorialProductionDraftRevisions(t *testing.T) {
	t.Parallel()

	t.Run("normalizes heading asset target", func(t *testing.T) {
		t.Parallel()

		uc, mockRepo := newEditorialUseCase(t)
		headingID := 10
		expected := []entity.BookProductionDraftRevision{{
			ID:        "revision-id",
			ProjectID: "project-id",
			AssetType: entity.ProductionAssetSectionTranslation,
			HeadingID: &headingID,
			Version:   1,
		}}

		mockRepo.EXPECT().
			ListProductionDraftRevisions(context.Background(), repo.ProductionDraftRevisionFilter{
				ProjectID: "project-id",
				AssetType: entity.ProductionAssetSectionTranslation,
				HeadingID: &headingID,
				Limit:     200,
				Offset:    0,
			}).
			Return(expected, 1, nil)

		got, total, err := uc.ProductionDraftRevisions(
			context.Background(),
			" project-id ",
			" SECTION_TRANSLATION ",
			&headingID,
			999,
			-1,
		)

		require.NoError(t, err)
		assert.Equal(t, 1, total)
		assert.Equal(t, expected, got)
	})

	t.Run("requires heading for heading asset", func(t *testing.T) {
		t.Parallel()

		uc, _ := newEditorialUseCase(t)

		_, _, err := uc.ProductionDraftRevisions(context.Background(), "project-id", entity.ProductionAssetSectionAudio, nil, 50, 0)

		require.ErrorIs(t, err, entity.ErrHeadingNotFound)
	})

	t.Run("rejects heading for scalar asset", func(t *testing.T) {
		t.Parallel()

		uc, _ := newEditorialUseCase(t)
		headingID := 10

		_, _, err := uc.ProductionDraftRevisions(context.Background(), "project-id", entity.ProductionAssetBookMetadata, &headingID, 50, 0)

		require.ErrorIs(t, err, entity.ErrInvalidProductionDraft)
	})
}

func TestEditorialRestoreProductionDraftRevision(t *testing.T) {
	t.Parallel()

	uc, mockRepo := newEditorialUseCase(t)
	expected := entity.BookProductionDraftRevision{
		ID:        "revision-id-2",
		ProjectID: "project-id",
		AssetType: entity.ProductionAssetBookMetadata,
		Version:   2,
	}

	mockRepo.EXPECT().
		RestoreProductionDraftRevision(context.Background(), "actor-id", "project-id", "revision-id").
		Return(expected, nil)

	got, err := uc.RestoreProductionDraftRevision(context.Background(), "actor-id", " project-id ", " revision-id ")

	require.NoError(t, err)
	assert.Equal(t, expected, got)
}

func TestEditorialProductionPublishCheck(t *testing.T) {
	t.Parallel()

	t.Run("returns not ready payload", func(t *testing.T) {
		t.Parallel()

		uc, mockRepo := newEditorialUseCase(t)
		expected := entity.BookProductionPublishCheck{
			Project:    entity.BookProductionProject{ID: "project-id"},
			Ready:      false,
			CanPublish: false,
			BlockingErrors: []entity.BookProductionBlocking{{
				Code:      "missing_required_asset",
				AssetType: entity.ProductionAssetBookMetadata,
				Message:   "metadata translation draft is missing",
			}},
		}

		mockRepo.EXPECT().
			ProductionPublishCheck(context.Background(), "project-id").
			Return(expected, nil)

		got, err := uc.ProductionPublishCheck(context.Background(), " project-id ")

		require.NoError(t, err)
		assert.Equal(t, expected, got)
	})

	t.Run("returns ready payload", func(t *testing.T) {
		t.Parallel()

		uc, mockRepo := newEditorialUseCase(t)
		expected := entity.BookProductionPublishCheck{
			Project:    entity.BookProductionProject{ID: "project-id"},
			Ready:      true,
			CanPublish: true,
		}

		mockRepo.EXPECT().
			ProductionPublishCheck(context.Background(), "project-id").
			Return(expected, nil)

		got, err := uc.ProductionPublishCheck(context.Background(), "project-id")

		require.NoError(t, err)
		assert.True(t, got.CanPublish)
		assert.Equal(t, expected, got)
	})
}

func TestEditorialSaveSectionTranslationDraft(t *testing.T) {
	t.Parallel()

	t.Run("normalizes draft", func(t *testing.T) {
		t.Parallel()

		uc, mockRepo := newEditorialUseCase(t)
		title := "  Title  "
		source := " manual "
		expectedTitle := "Title"
		expectedSource := "manual"

		mockRepo.EXPECT().
			SaveSectionTranslationDraft(context.Background(), "actor-id", "project-id", entity.SectionTranslationEdit{
				ProjectID:    "project-id",
				HeadingID:    10,
				Title:        &expectedTitle,
				Content:      "Translated content",
				Source:       &expectedSource,
				ReviewStatus: entity.ProductionReviewDraft,
			}).
			DoAndReturn(func(_ context.Context, _ string, _ string, edit entity.SectionTranslationEdit) (entity.SectionTranslationEdit, error) {
				return edit, nil
			})

		edit, err := uc.SaveSectionTranslationDraft(context.Background(), "actor-id", " project-id ", entity.SectionTranslationEdit{
			HeadingID: 10,
			Title:     &title,
			Content:   "  Translated content  ",
			Source:    &source,
		})

		require.NoError(t, err)
		assert.Equal(t, "Translated content", edit.Content)
		assert.Equal(t, entity.ProductionReviewDraft, edit.ReviewStatus)
	})

	t.Run("rejects empty content", func(t *testing.T) {
		t.Parallel()

		uc, _ := newEditorialUseCase(t)

		_, err := uc.SaveSectionTranslationDraft(context.Background(), "actor-id", "project-id", entity.SectionTranslationEdit{
			HeadingID: 10,
			Content:   " ",
		})

		require.ErrorIs(t, err, entity.ErrInvalidProductionDraft)
	})
}

func TestEditorialReviewProductionAsset(t *testing.T) {
	t.Parallel()

	t.Run("normalizes review request", func(t *testing.T) {
		t.Parallel()

		uc, mockRepo := newEditorialUseCase(t)
		headingID := 10
		note := " approved "
		expectedNote := "approved"

		mockRepo.EXPECT().
			ReviewProductionAsset(
				context.Background(),
				"actor-id",
				"project-id",
				entity.ProductionAssetSectionTranslation,
				&headingID,
				entity.ProductionReviewDecisionApprove,
				&expectedNote,
			).
			Return(nil)

		err := uc.ReviewProductionAsset(
			context.Background(),
			"actor-id",
			" project-id ",
			" SECTION_TRANSLATION ",
			&headingID,
			" APPROVE ",
			&note,
		)

		require.NoError(t, err)
	})

	t.Run("requires heading for heading asset", func(t *testing.T) {
		t.Parallel()

		uc, _ := newEditorialUseCase(t)

		err := uc.ReviewProductionAsset(
			context.Background(),
			"actor-id",
			"project-id",
			entity.ProductionAssetSectionAudio,
			nil,
			entity.ProductionReviewDecisionSubmit,
			nil,
		)

		require.ErrorIs(t, err, entity.ErrHeadingNotFound)
	})
}

func TestEditorialPublishProductionProject(t *testing.T) {
	t.Parallel()

	uc, mockRepo := newEditorialUseCase(t)
	expected := entity.BookProductionProject{
		ID:                "project-id",
		PublicationStatus: entity.ProductionPublicationPublished,
		WorkflowStatus:    entity.ProductionWorkflowPublished,
	}

	mockRepo.EXPECT().
		PublishProductionProject(context.Background(), "actor-id", "project-id").
		Return(expected, nil)

	got, err := uc.PublishProductionProject(context.Background(), "actor-id", " project-id ")

	require.NoError(t, err)
	assert.Equal(t, expected, got)
}
