package reader

import (
	"context"
	"strings"

	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/evrone/go-clean-template/internal/readerlang"
	"github.com/google/uuid"
)

const (
	feedbackLike    = "like"
	feedbackDislike = "dislike"
)

var allowedFeedbackReasons = map[string]struct{}{
	"inaccurate": {},
	"unclear":    {},
	"style":      {},
	"typo":       {},
	"formatting": {},
	"other":      {},
}

// CreateTranslationFeedback stores a reader quality signal for one translated TOC section.
func (uc *UseCase) CreateTranslationFeedback(
	ctx context.Context,
	bookID int,
	headingID int,
	lang string,
	vote string,
	reason *string,
	note *string,
	clientID *string,
	userAgent *string,
	clientIP *string,
) (entity.TranslationFeedback, error) {
	lang, err := readerlang.Normalize(lang)
	if err != nil {
		return entity.TranslationFeedback{}, err
	}

	feedback := entity.TranslationFeedback{
		ID:        uuid.New().String(),
		BookID:    bookID,
		HeadingID: headingID,
		Lang:      lang,
		Vote:      strings.ToLower(strings.TrimSpace(vote)),
		ClientID:  trimOptional(clientID),
		UserAgent: trimOptional(userAgent),
		ClientIP:  trimOptional(clientIP),
	}

	switch feedback.Vote {
	case feedbackLike:
		feedback.Reason = nil
		feedback.Note = nil
	case feedbackDislike:
		feedback.Reason = trimOptional(reason)
		feedback.Note = trimOptional(note)
		if feedback.Reason != nil {
			*feedback.Reason = strings.ToLower(*feedback.Reason)
			if _, ok := allowedFeedbackReasons[*feedback.Reason]; !ok {
				return entity.TranslationFeedback{}, entity.ErrInvalidFeedback
			}
		}
	default:
		return entity.TranslationFeedback{}, entity.ErrInvalidFeedback
	}

	return uc.repo.CreateTranslationFeedback(ctx, feedback)
}

func trimOptional(value *string) *string {
	if value == nil {
		return nil
	}

	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}

	return &trimmed
}
