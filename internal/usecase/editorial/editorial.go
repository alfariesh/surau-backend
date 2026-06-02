package editorial

import (
	"context"
	"strings"

	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/evrone/go-clean-template/internal/readerlang"
	"github.com/evrone/go-clean-template/internal/readerutil"
	"github.com/evrone/go-clean-template/internal/repo"
)

const (
	defaultLimit = 50
	maxLimit     = 200
)

// UseCase provides editorial operations.
type UseCase struct {
	repo repo.EditorialRepo
}

// New creates an editorial usecase.
func New(r repo.EditorialRepo) *UseCase {
	return &UseCase{repo: r}
}

// Books returns paginated books for admin review.
func (uc *UseCase) Books(
	ctx context.Context,
	query string,
	status *string,
	categoryID *int,
	hasContent *bool,
	limit, offset int,
) ([]entity.Book, int, error) {
	if status != nil {
		trimmed := strings.TrimSpace(*status)
		if trimmed == "" {
			status = nil
		} else if !isPublicationStatus(trimmed) {
			return nil, 0, entity.ErrInvalidStatus
		} else {
			status = &trimmed
		}
	}

	return uc.repo.ListBooks(ctx, repo.EditorialBookFilter{
		Query:      strings.TrimSpace(query),
		Status:     status,
		CategoryID: categoryID,
		HasContent: hasContent,
		Limit:      clampLimit(limit),
		Offset:     clampOffset(offset),
	})
}

// ProductionCandidates returns source books with current production state for one target language.
func (uc *UseCase) ProductionCandidates(
	ctx context.Context,
	lang,
	query string,
	categoryID,
	authorID *int,
	hasContent *bool,
	unstarted bool,
	limit,
	offset int,
) ([]entity.BookProductionCandidate, int, error) {
	normalizedLang, err := entity.NormalizeProductionLang(lang)
	if err != nil {
		return nil, 0, err
	}

	return uc.repo.ListProductionCandidates(ctx, repo.ProductionCandidateFilter{
		Lang:       normalizedLang,
		Query:      strings.TrimSpace(query),
		CategoryID: categoryID,
		AuthorID:   authorID,
		HasContent: hasContent,
		Unstarted:  unstarted,
		Limit:      clampLimit(limit),
		Offset:     clampOffset(offset),
	})
}

// UpdatePublication changes visibility and ordering.
func (uc *UseCase) UpdatePublication(
	ctx context.Context,
	actorID string,
	bookID int,
	status string,
	featured bool,
	sortOrder *int,
) (entity.BookPublication, error) {
	status = strings.TrimSpace(status)
	if !isPublicationStatus(status) {
		return entity.BookPublication{}, entity.ErrInvalidStatus
	}

	return uc.repo.UpdatePublication(ctx, actorID, entity.BookPublication{
		BookID:    bookID,
		Status:    status,
		Featured:  featured,
		SortOrder: sortOrder,
	})
}

// SaveMetadataDraft stores metadata override as draft.
func (uc *UseCase) SaveMetadataDraft(
	ctx context.Context,
	actorID string,
	edit entity.BookMetadataEdit,
) (entity.BookMetadataEdit, error) {
	edit.Status = entity.EditStatusDraft
	edit.DisplayTitle = trimStringPtr(edit.DisplayTitle)
	edit.Description = trimStringPtr(edit.Description)
	edit.CoverURL = trimStringPtr(edit.CoverURL)
	edit.Notes = trimStringPtr(edit.Notes)
	if edit.CategoryID != nil && *edit.CategoryID <= 0 {
		edit.CategoryID = nil
	}

	return uc.repo.SaveMetadataDraft(ctx, actorID, edit)
}

// PublishMetadataDraft promotes metadata draft to published.
func (uc *UseCase) PublishMetadataDraft(ctx context.Context, actorID string, bookID int) (entity.BookMetadataEdit, error) {
	return uc.repo.PublishMetadataDraft(ctx, actorID, bookID)
}

// GetPageEdit returns raw page plus draft and published overrides.
func (uc *UseCase) GetPageEdit(ctx context.Context, bookID, pageID int) (entity.EditorialPageEdit, error) {
	return uc.repo.GetPageEdit(ctx, bookID, pageID)
}

// SavePageDraft stores page override as draft.
func (uc *UseCase) SavePageDraft(ctx context.Context, actorID string, edit entity.BookPageEdit) (entity.BookPageEdit, error) {
	contentHTML, contentText := readerutil.NormalizeContent(edit.ContentHTML)
	edit.Status = entity.EditStatusDraft
	edit.ContentHTML = contentHTML
	edit.ContentText = contentText

	return uc.repo.SavePageDraft(ctx, actorID, edit)
}

// PublishPageDraft promotes page draft to published.
func (uc *UseCase) PublishPageDraft(ctx context.Context, actorID string, bookID, pageID int) (entity.BookPageEdit, error) {
	return uc.repo.PublishPageDraft(ctx, actorID, bookID, pageID)
}

// SaveHeadingDraft stores heading title override as draft.
func (uc *UseCase) SaveHeadingDraft(
	ctx context.Context,
	actorID string,
	edit entity.BookHeadingEdit,
) (entity.BookHeadingEdit, error) {
	edit.Status = entity.EditStatusDraft
	edit.Content = strings.TrimSpace(edit.Content)

	return uc.repo.SaveHeadingDraft(ctx, actorID, edit)
}

// PublishHeadingDraft promotes heading draft to published.
func (uc *UseCase) PublishHeadingDraft(ctx context.Context, actorID string, bookID, headingID int) (entity.BookHeadingEdit, error) {
	return uc.repo.PublishHeadingDraft(ctx, actorID, bookID, headingID)
}

// AddCollectionItem adds or reorders a book in a collection.
func (uc *UseCase) AddCollectionItem(
	ctx context.Context,
	actorID, slug string,
	bookID int,
	sortOrder *int,
) (entity.BookCollectionItem, error) {
	return uc.repo.AddCollectionItem(ctx, actorID, strings.TrimSpace(slug), bookID, sortOrder)
}

// TranslationFeedbacks returns paginated reader feedback for admin review.
func (uc *UseCase) TranslationFeedbacks(
	ctx context.Context,
	bookID, headingID *int,
	lang, vote, status string,
	limit, offset int,
) ([]entity.EditorialTranslationFeedback, int, error) {
	filter, err := translationFeedbackFilter(bookID, headingID, lang, vote, status, limit, offset)
	if err != nil {
		return nil, 0, err
	}

	return uc.repo.ListTranslationFeedbacks(ctx, filter)
}

// TranslationFeedbackSummary aggregates reader feedback for admin review.
func (uc *UseCase) TranslationFeedbackSummary(
	ctx context.Context,
	bookID, headingID *int,
	lang, vote, status string,
	limit int,
) (entity.EditorialTranslationFeedbackSummary, error) {
	filter, err := translationFeedbackFilter(bookID, headingID, lang, vote, status, limit, 0)
	if err != nil {
		return entity.EditorialTranslationFeedbackSummary{}, err
	}

	return uc.repo.TranslationFeedbackSummary(ctx, filter)
}

// MissingReaderAssets returns admin queue items for missing localized reader assets.
func (uc *UseCase) MissingReaderAssets(
	ctx context.Context,
	targetLang string,
	assetType string,
	bookID *int,
	limit,
	offset int,
) (entity.EditorialMissingReaderAssets, error) {
	filter, err := missingReaderAssetFilter(targetLang, assetType, bookID, limit, offset)
	if err != nil {
		return entity.EditorialMissingReaderAssets{}, err
	}

	return uc.repo.ListMissingReaderAssets(ctx, filter)
}

// ResolveTranslationFeedback marks reader feedback as handled by an admin.
func (uc *UseCase) ResolveTranslationFeedback(
	ctx context.Context,
	actorID, feedbackID string,
	note *string,
) (entity.EditorialTranslationFeedback, error) {
	feedbackID = strings.TrimSpace(feedbackID)
	if feedbackID == "" {
		return entity.EditorialTranslationFeedback{}, entity.ErrInvalidFeedback
	}

	return uc.repo.ResolveTranslationFeedback(ctx, actorID, feedbackID, trimStringPtr(note))
}

// ReopenTranslationFeedback moves a handled feedback row back to the active queue.
func (uc *UseCase) ReopenTranslationFeedback(
	ctx context.Context,
	actorID, feedbackID string,
) (entity.EditorialTranslationFeedback, error) {
	feedbackID = strings.TrimSpace(feedbackID)
	if feedbackID == "" {
		return entity.EditorialTranslationFeedback{}, entity.ErrInvalidFeedback
	}

	return uc.repo.ReopenTranslationFeedback(ctx, actorID, feedbackID)
}

func isPublicationStatus(status string) bool {
	switch status {
	case entity.PublicationStatusHidden,
		entity.PublicationStatusDraft,
		entity.PublicationStatusPublished,
		entity.PublicationStatusArchived:
		return true
	default:
		return false
	}
}

func translationFeedbackFilter(
	bookID, headingID *int,
	lang, vote, status string,
	limit, offset int,
) (repo.TranslationFeedbackFilter, error) {
	lang = strings.ToLower(strings.TrimSpace(lang))
	vote = strings.ToLower(strings.TrimSpace(vote))
	if vote != "" && vote != "like" && vote != "dislike" {
		return repo.TranslationFeedbackFilter{}, entity.ErrInvalidFeedback
	}

	status = strings.ToLower(strings.TrimSpace(status))
	if status == "" {
		status = entity.FeedbackStatusOpen
	}
	if status == "all" {
		status = ""
	} else if status != entity.FeedbackStatusOpen && status != entity.FeedbackStatusResolved {
		return repo.TranslationFeedbackFilter{}, entity.ErrInvalidFeedback
	}

	return repo.TranslationFeedbackFilter{
		BookID:    bookID,
		HeadingID: headingID,
		Lang:      lang,
		Vote:      vote,
		Status:    status,
		Limit:     clampLimit(limit),
		Offset:    clampOffset(offset),
	}, nil
}

func missingReaderAssetFilter(
	targetLang string,
	assetType string,
	bookID *int,
	limit,
	offset int,
) (repo.MissingReaderAssetFilter, error) {
	targetLang = strings.TrimSpace(targetLang)
	targetLangs := []string{readerlang.Default, readerlang.English}
	if targetLang != "" {
		normalized, err := readerlang.Normalize(targetLang)
		if err != nil || normalized == readerlang.Arabic {
			return repo.MissingReaderAssetFilter{}, entity.ErrUnsupportedLanguage
		}

		targetLangs = []string{normalized}
	}

	assetType = strings.ToLower(strings.TrimSpace(assetType))
	if assetType != "" && !isMissingReaderAssetType(assetType) {
		return repo.MissingReaderAssetFilter{}, entity.ErrInvalidAssetType
	}

	return repo.MissingReaderAssetFilter{
		TargetLangs: targetLangs,
		AssetType:   assetType,
		BookID:      bookID,
		Limit:       clampLimit(limit),
		Offset:      clampOffset(offset),
	}, nil
}

func isMissingReaderAssetType(assetType string) bool {
	switch assetType {
	case entity.MissingAssetBookMetadata,
		entity.MissingAssetCategoryMetadata,
		entity.MissingAssetAuthorMetadata,
		entity.MissingAssetSectionTranslation,
		entity.MissingAssetHeadingSummary,
		entity.MissingAssetSectionAudio:
		return true
	default:
		return false
	}
}

func trimStringPtr(value *string) *string {
	if value == nil {
		return nil
	}

	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}

	return &trimmed
}

func clampLimit(limit int) uint64 {
	if limit <= 0 {
		return defaultLimit
	}

	if limit > maxLimit {
		return maxLimit
	}

	return uint64(limit)
}

func clampOffset(offset int) uint64 {
	if offset < 0 {
		return 0
	}

	return uint64(offset)
}
