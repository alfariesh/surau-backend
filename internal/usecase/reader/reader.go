package reader

import (
	"context"
	"strings"

	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/evrone/go-clean-template/internal/readerlang"
	"github.com/evrone/go-clean-template/internal/repo"
)

const (
	defaultLimit = 50
	maxLimit     = 200
)

// UseCase provides public reader operations.
type UseCase struct {
	repo repo.ReaderRepo
}

// New creates a reader usecase.
func New(r repo.ReaderRepo) *UseCase {
	return &UseCase{repo: r}
}

// Categories returns catalog categories.
func (uc *UseCase) Categories(ctx context.Context, lang string) ([]entity.Category, error) {
	lang, err := readerlang.Normalize(lang)
	if err != nil {
		return nil, err
	}

	return uc.repo.ListCategories(ctx, lang)
}

// Authors returns paginated authors.
func (uc *UseCase) Authors(ctx context.Context, query string, limit, offset int, lang string) ([]entity.Author, int, error) {
	lang, err := readerlang.Normalize(lang)
	if err != nil {
		return nil, 0, err
	}

	return uc.repo.ListAuthors(ctx, repo.AuthorFilter{
		Query:  strings.TrimSpace(query),
		Lang:   lang,
		Limit:  clampLimit(limit),
		Offset: clampOffset(offset),
	})
}

// Books returns paginated books.
func (uc *UseCase) Books(
	ctx context.Context,
	query string,
	categoryID, authorID *int,
	hasContent *bool,
	limit, offset int,
	lang string,
) ([]entity.Book, int, error) {
	lang, err := readerlang.Normalize(lang)
	if err != nil {
		return nil, 0, err
	}

	return uc.repo.ListBooks(ctx, repo.BookFilter{
		Query:      strings.TrimSpace(query),
		Lang:       lang,
		CategoryID: categoryID,
		AuthorID:   authorID,
		HasContent: hasContent,
		Limit:      clampLimit(limit),
		Offset:     clampOffset(offset),
	})
}

// Book returns one book.
func (uc *UseCase) Book(ctx context.Context, bookID int, lang string) (entity.Book, error) {
	lang, err := readerlang.Normalize(lang)
	if err != nil {
		return entity.Book{}, err
	}

	return uc.repo.GetBook(ctx, bookID, lang)
}

// Pages returns paginated pages for a book.
func (uc *UseCase) Pages(ctx context.Context, bookID int, limit, offset int) ([]entity.BookPage, int, error) {
	return uc.repo.ListBookPages(ctx, bookID, repo.PageFilter{
		Limit:  clampLimit(limit),
		Offset: clampOffset(offset),
	})
}

// Page returns one page.
func (uc *UseCase) Page(ctx context.Context, bookID, pageID int) (entity.BookPage, error) {
	return uc.repo.GetBookPage(ctx, bookID, pageID)
}

// Headings returns a flat heading tree for a book.
func (uc *UseCase) Headings(ctx context.Context, bookID int, query string) ([]entity.BookHeading, error) {
	return uc.repo.ListBookHeadings(ctx, bookID, strings.TrimSpace(query))
}

// Section returns one section in Arabic plus optional requested language assets.
func (uc *UseCase) Section(ctx context.Context, bookID, headingID int, lang string) (entity.BookSection, error) {
	lang, err := readerlang.Normalize(lang)
	if err != nil {
		return entity.BookSection{}, err
	}

	return uc.repo.GetSection(ctx, bookID, headingID, lang)
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
