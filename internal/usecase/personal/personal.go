package personal

import (
	"context"

	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/evrone/go-clean-template/internal/repo"
	"github.com/google/uuid"
)

const (
	defaultLimit = 50
	maxLimit     = 200
)

// UseCase provides authenticated reader operations.
type UseCase struct {
	repo repo.PersonalRepo
}

// New creates a personal usecase.
func New(r repo.PersonalRepo) *UseCase {
	return &UseCase{repo: r}
}

// GetProgress returns one user's progress for a book.
func (uc *UseCase) GetProgress(ctx context.Context, userID string, bookID int) (entity.ReadingProgress, error) {
	return uc.repo.GetProgress(ctx, userID, bookID)
}

// SaveProgress upserts one user's progress for a book.
func (uc *UseCase) SaveProgress(
	ctx context.Context,
	userID string,
	bookID int,
	pageID, headingID *int,
	progressPercent *float64,
) (entity.ReadingProgress, error) {
	return uc.repo.SaveProgress(ctx, entity.ReadingProgress{
		UserID:          userID,
		BookID:          bookID,
		PageID:          pageID,
		HeadingID:       headingID,
		ProgressPercent: progressPercent,
	})
}

// ListBookmarks returns paginated bookmarks.
func (uc *UseCase) ListBookmarks(ctx context.Context, userID string, bookID *int, limit, offset int) ([]entity.Bookmark, int, error) {
	return uc.repo.ListBookmarks(ctx, userID, repo.BookmarkFilter{
		BookID: bookID,
		Limit:  clampLimit(limit),
		Offset: clampOffset(offset),
	})
}

// CreateBookmark creates a bookmark.
func (uc *UseCase) CreateBookmark(
	ctx context.Context,
	userID string,
	bookID int,
	pageID, headingID *int,
	label, note *string,
) (entity.Bookmark, error) {
	return uc.repo.CreateBookmark(ctx, entity.Bookmark{
		ID:        uuid.New().String(),
		UserID:    userID,
		BookID:    bookID,
		PageID:    pageID,
		HeadingID: headingID,
		Label:     label,
		Note:      note,
	})
}

// DeleteBookmark removes one bookmark.
func (uc *UseCase) DeleteBookmark(ctx context.Context, userID, bookmarkID string) error {
	return uc.repo.DeleteBookmark(ctx, userID, bookmarkID)
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
