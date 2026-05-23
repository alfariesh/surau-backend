package editorial

import (
	"context"
	"strings"

	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/evrone/go-clean-template/internal/readerutil"
	"github.com/evrone/go-clean-template/internal/repo"
)

const (
	defaultLimit = 50
	maxLimit     = 200
)

// UseCase provides admin editorial operations.
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
func (uc *UseCase) GetPageEdit(ctx context.Context, bookID, pageID int) (entity.AdminPageEdit, error) {
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
