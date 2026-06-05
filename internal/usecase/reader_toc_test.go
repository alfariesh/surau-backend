package usecase_test

import (
	"context"
	"testing"

	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/evrone/go-clean-template/internal/usecase/reader"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func newReaderUseCase(t *testing.T) (*reader.UseCase, *MockReaderRepo) {
	t.Helper()

	ctrl := gomock.NewController(t)

	mockRepo := NewMockReaderRepo(ctrl)
	useCase := reader.New(mockRepo)

	return useCase, mockRepo
}

func TestReaderTOC(t *testing.T) {
	t.Parallel()

	uc, mockRepo := newReaderUseCase(t)
	parentID := 1
	summary := "Root summary"
	summaryLang := "id"
	entries := []entity.BookTOCEntry{
		{
			BookID:      1,
			HeadingID:   1,
			Title:       "Root",
			PageID:      1,
			Depth:       0,
			Ordinal:     0,
			Summary:     &summary,
			SummaryLang: &summaryLang,
			HasSummary:  true,
			HasAudio:    true,
		},
		{BookID: 1, HeadingID: 2, ParentID: &parentID, Title: "Child", PageID: 2, Depth: 1, Ordinal: 1, HasTranslation: true},
	}

	mockRepo.EXPECT().
		ListTOCEntries(context.Background(), 1, "id", true).
		Return(entries, nil)

	toc, err := uc.TOC(context.Background(), 1, "", true)

	require.NoError(t, err)
	require.Len(t, toc, 1)
	assert.Equal(t, "Root", toc[0].Title)
	assert.Equal(t, &summary, toc[0].Summary)
	assert.Equal(t, &summaryLang, toc[0].SummaryLang)
	assert.True(t, toc[0].HasSummary)
	assert.True(t, toc[0].HasAudio)
	require.Len(t, toc[0].Children, 1)
	assert.Equal(t, "Child", toc[0].Children[0].Title)
	assert.True(t, toc[0].Children[0].HasTranslation)
}

func TestReaderSectionDefaultsToIndonesian(t *testing.T) {
	t.Parallel()

	uc, mockRepo := newReaderUseCase(t)
	section := entity.BookSection{BookID: 1, HeadingID: 2, RequestedLang: "id"}

	mockRepo.EXPECT().
		GetSection(context.Background(), 1, 2, "id").
		Return(section, nil)

	got, err := uc.Section(context.Background(), 1, 2, "")

	require.NoError(t, err)
	assert.Equal(t, "id", got.RequestedLang)
}

func TestReaderUnsupportedLanguage(t *testing.T) {
	t.Parallel()

	uc, _ := newReaderUseCase(t)

	_, err := uc.Categories(context.Background(), "fr")

	require.ErrorIs(t, err, entity.ErrUnsupportedLanguage)
}

func TestReaderTOCRead(t *testing.T) {
	t.Parallel()

	uc, mockRepo := newReaderUseCase(t)
	rootID := 1
	currentID := 2
	summary := "Current summary"
	summaryLang := "id"
	entries := []entity.BookTOCEntry{
		{BookID: 1, HeadingID: 1, Title: "Root", PageID: 1, Depth: 0, Ordinal: 0},
		{
			BookID:      1,
			HeadingID:   2,
			ParentID:    &rootID,
			Title:       "Current",
			PageID:      2,
			Depth:       1,
			Ordinal:     1,
			Summary:     &summary,
			SummaryLang: &summaryLang,
			HasSummary:  true,
			HasAudio:    true,
		},
		{BookID: 1, HeadingID: 3, ParentID: &currentID, Title: "Child", PageID: 3, Depth: 2, Ordinal: 2},
	}
	audio := &entity.SectionAudio{BookID: 1, HeadingID: 2, Lang: "id", URL: "https://cdn.test/2.mp3"}
	section := entity.BookSection{
		BookID:         1,
		HeadingID:      2,
		StartPageID:    2,
		EndPageID:      4,
		OriginalHTML:   "<p>Original</p>",
		OriginalText:   "Original",
		OriginalFormat: "html",
		OriginalBlocks: []entity.SourceBlock{
			{Type: "html", Text: "Original", HTML: "<p>Original</p>"},
		},
		OriginalFootnotes: []entity.SourceFootnote{},
		Audio:             audio,
	}

	mockRepo.EXPECT().
		ListTOCEntries(context.Background(), 1, "id", true).
		Return(entries, nil)
	mockRepo.EXPECT().
		GetSection(context.Background(), 1, 2, "id").
		Return(section, nil)

	read, err := uc.TOCRead(context.Background(), 1, 2, "ID")

	require.NoError(t, err)
	assert.Equal(t, "Current", read.Title)
	assert.Equal(t, &summary, read.Summary)
	assert.Equal(t, &summaryLang, read.SummaryLang)
	assert.True(t, read.HasSummary)
	assert.Equal(t, 2, read.StartPageID)
	assert.Equal(t, 4, read.EndPageID)
	assert.Equal(t, "html", read.OriginalFormat)
	assert.Equal(t, section.OriginalBlocks, read.OriginalBlocks)
	assert.Equal(t, section.OriginalFootnotes, read.OriginalFootnotes)
	assert.Equal(t, audio, read.Audio)
	require.Len(t, read.Breadcrumb, 1)
	assert.Equal(t, "Root", read.Breadcrumb[0].Title)
	require.Len(t, read.Children, 1)
	assert.Equal(t, "Child", read.Children[0].Title)
	require.NotNil(t, read.Previous)
	assert.Equal(t, 1, read.Previous.HeadingID)
	require.NotNil(t, read.Next)
	assert.Equal(t, 3, read.Next.HeadingID)
}

func TestReaderTOCReadMissingRequestedTranslation(t *testing.T) {
	t.Parallel()

	uc, mockRepo := newReaderUseCase(t)
	availableTranslations := []string{"id"}
	availableSummaries := []string{"ar"}
	entries := []entity.BookTOCEntry{
		{
			BookID:                    1,
			HeadingID:                 2,
			Title:                     "Arabic title",
			RequestedLang:             "en",
			TitleLang:                 "ar",
			IsTitleFallback:           true,
			TranslationMissing:        true,
			AvailableTranslationLangs: availableTranslations,
			AvailableSummaryLangs:     availableSummaries,
			PageID:                    2,
			Depth:                     0,
			Ordinal:                   1,
		},
	}
	section := entity.BookSection{
		BookID:                    1,
		HeadingID:                 2,
		RequestedLang:             "en",
		TitleLang:                 "ar",
		IsTitleFallback:           true,
		TranslationMissing:        true,
		AvailableTranslationLangs: availableTranslations,
		AvailableSummaryLangs:     availableSummaries,
		StartPageID:               2,
		EndPageID:                 2,
		OriginalHTML:              "<p>Original</p>",
		OriginalText:              "Original",
	}

	mockRepo.EXPECT().
		ListTOCEntries(context.Background(), 1, "en", true).
		Return(entries, nil)
	mockRepo.EXPECT().
		GetSection(context.Background(), 1, 2, "en").
		Return(section, nil)

	read, err := uc.TOCRead(context.Background(), 1, 2, "en-US")

	require.NoError(t, err)
	assert.Equal(t, "en", read.RequestedLang)
	assert.Equal(t, "ar", read.TitleLang)
	assert.True(t, read.IsTitleFallback)
	assert.True(t, read.TranslationMissing)
	assert.Equal(t, []string{"id"}, read.AvailableTranslationLangs)
	assert.Nil(t, read.Translation)
}

func TestReaderTOCReadHeadingNotFound(t *testing.T) {
	t.Parallel()

	uc, mockRepo := newReaderUseCase(t)
	mockRepo.EXPECT().
		ListTOCEntries(context.Background(), 1, "id", true).
		Return([]entity.BookTOCEntry{{BookID: 1, HeadingID: 1, Title: "Root"}}, nil)

	_, err := uc.TOCRead(context.Background(), 1, 999, "id")

	require.ErrorIs(t, err, entity.ErrHeadingNotFound)
}

func TestReaderTOCPlaylist(t *testing.T) {
	t.Parallel()

	t.Run("parent audio skips descendants", func(t *testing.T) {
		t.Parallel()

		uc, mockRepo := newReaderUseCase(t)
		parentID := 1
		rootDuration := 10
		childDuration := 20
		entries := []entity.BookTOCEntry{
			{
				BookID:    1,
				HeadingID: 1,
				Title:     "Root",
				Depth:     0,
				Ordinal:   0,
				HasAudio:  true,
				Audio: &entity.SectionAudio{
					BookID:          1,
					HeadingID:       1,
					Lang:            "id",
					URL:             "https://cdn.test/root.mp3",
					DurationSeconds: &rootDuration,
				},
			},
			{
				BookID:    1,
				HeadingID: 2,
				ParentID:  &parentID,
				Title:     "Child",
				Depth:     1,
				Ordinal:   1,
				HasAudio:  true,
				Audio: &entity.SectionAudio{
					BookID:          1,
					HeadingID:       2,
					Lang:            "id",
					URL:             "https://cdn.test/child.mp3",
					DurationSeconds: &childDuration,
				},
			},
		}

		mockRepo.EXPECT().
			ListTOCEntries(context.Background(), 1, "id", true).
			Return(entries, nil)

		playlist, err := uc.TOCPlaylist(context.Background(), 1, 1, "id")

		require.NoError(t, err)
		require.Len(t, playlist.Items, 1)
		assert.Equal(t, 1, playlist.Items[0].HeadingID)
		assert.Equal(t, 10, playlist.TotalDurationSeconds)
		assert.Equal(t, 0, playlist.MissingCount)
	})

	t.Run("missing parent descends to child audio", func(t *testing.T) {
		t.Parallel()

		uc, mockRepo := newReaderUseCase(t)
		parentID := 1
		duration := 20
		entries := []entity.BookTOCEntry{
			{BookID: 1, HeadingID: 1, Title: "Root", Depth: 0, Ordinal: 0},
			{
				BookID:    1,
				HeadingID: 2,
				ParentID:  &parentID,
				Title:     "Child With Audio",
				Depth:     1,
				Ordinal:   1,
				HasAudio:  true,
				Audio: &entity.SectionAudio{
					BookID:          1,
					HeadingID:       2,
					Lang:            "id",
					URL:             "https://cdn.test/child.mp3",
					DurationSeconds: &duration,
				},
			},
			{BookID: 1, HeadingID: 3, ParentID: &parentID, Title: "Child Missing", Depth: 1, Ordinal: 2},
		}

		mockRepo.EXPECT().
			ListTOCEntries(context.Background(), 1, "id", true).
			Return(entries, nil)

		playlist, err := uc.TOCPlaylist(context.Background(), 1, 1, "id")

		require.NoError(t, err)
		require.Len(t, playlist.Items, 1)
		assert.Equal(t, 2, playlist.Items[0].HeadingID)
		assert.Equal(t, 20, playlist.TotalDurationSeconds)
		assert.Equal(t, 2, playlist.MissingCount)
	})
}
