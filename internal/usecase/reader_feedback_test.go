package usecase_test

import (
	"context"
	"testing"

	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
)

func TestReaderCreateTranslationFeedbackDislike(t *testing.T) {
	t.Parallel()

	uc, mockRepo := newReaderUseCase(t)
	reason := " Style "
	note := "  Terasa seperti terjemahan literal.  "
	clientID := " browser-1 "
	userAgent := " Test Browser "
	clientIP := " 127.0.0.1 "

	mockRepo.EXPECT().
		CreateTranslationFeedback(context.Background(), gomock.Any()).
		DoAndReturn(func(_ context.Context, feedback entity.TranslationFeedback) (entity.TranslationFeedback, error) {
			assert.NotEmpty(t, feedback.ID)
			assert.Equal(t, 1, feedback.BookID)
			assert.Equal(t, 5, feedback.HeadingID)
			assert.Equal(t, "id", feedback.Lang)
			assert.Equal(t, "dislike", feedback.Vote)
			require.NotNil(t, feedback.Reason)
			assert.Equal(t, "style", *feedback.Reason)
			require.NotNil(t, feedback.Note)
			assert.Equal(t, "Terasa seperti terjemahan literal.", *feedback.Note)
			require.NotNil(t, feedback.ClientID)
			assert.Equal(t, "browser-1", *feedback.ClientID)
			require.NotNil(t, feedback.UserAgent)
			assert.Equal(t, "Test Browser", *feedback.UserAgent)
			require.NotNil(t, feedback.ClientIP)
			assert.Equal(t, "127.0.0.1", *feedback.ClientIP)

			return feedback, nil
		})

	feedback, err := uc.CreateTranslationFeedback(
		context.Background(),
		1,
		5,
		"ID",
		" DISLIKE ",
		&reason,
		&note,
		&clientID,
		&userAgent,
		&clientIP,
	)

	require.NoError(t, err)
	assert.Equal(t, "dislike", feedback.Vote)
}

func TestReaderCreateTranslationFeedbackLikeClearsNote(t *testing.T) {
	t.Parallel()

	uc, mockRepo := newReaderUseCase(t)
	reason := "style"
	note := "ignored"

	mockRepo.EXPECT().
		CreateTranslationFeedback(context.Background(), gomock.Any()).
		DoAndReturn(func(_ context.Context, feedback entity.TranslationFeedback) (entity.TranslationFeedback, error) {
			assert.Equal(t, "like", feedback.Vote)
			assert.Nil(t, feedback.Reason)
			assert.Nil(t, feedback.Note)

			return feedback, nil
		})

	_, err := uc.CreateTranslationFeedback(context.Background(), 1, 5, "id", "like", &reason, &note, nil, nil, nil)

	require.NoError(t, err)
}

func TestReaderCreateTranslationFeedbackInvalid(t *testing.T) {
	t.Parallel()

	uc, _ := newReaderUseCase(t)

	_, err := uc.CreateTranslationFeedback(context.Background(), 1, 5, "id", "meh", nil, nil, nil, nil, nil)

	require.ErrorIs(t, err, entity.ErrInvalidFeedback)
}
