// Package repo implements application outer layer logic. Each logic group in own file.
package repo

import (
	"context"

	"github.com/evrone/go-clean-template/internal/entity"
)

//go:generate mockgen -source=contracts.go -destination=../usecase/mocks_repo_test.go -package=usecase_test

type (
	// TranslationRepo -.
	TranslationRepo interface {
		Store(ctx context.Context, userID string, t entity.Translation) error
		GetHistory(ctx context.Context, userID string) ([]entity.Translation, error)
	}

	// TranslationWebAPI -.
	TranslationWebAPI interface {
		Translate(ctx context.Context, t entity.Translation) (entity.Translation, error)
	}

	// UserRepo -.
	UserRepo interface {
		Store(ctx context.Context, user *entity.User) error
		GetByID(ctx context.Context, id string) (entity.User, error)
		GetByEmail(ctx context.Context, email string) (entity.User, error)
		SetRoleByEmail(ctx context.Context, email, role string) (entity.User, error)
	}

	// TaskRepo -.
	TaskRepo interface {
		Store(ctx context.Context, task *entity.Task) error
		GetByID(ctx context.Context, userID, taskID string) (entity.Task, error)
		List(ctx context.Context, userID string, filter TaskFilter) ([]entity.Task, int, error)
		Update(ctx context.Context, task *entity.Task) error
		Delete(ctx context.Context, userID, taskID string) error
	}

	// ReaderRepo -.
	ReaderRepo interface {
		ListCategories(ctx context.Context) ([]entity.Category, error)
		ListAuthors(ctx context.Context, filter AuthorFilter) ([]entity.Author, int, error)
		ListBooks(ctx context.Context, filter BookFilter) ([]entity.Book, int, error)
		GetBook(ctx context.Context, bookID int) (entity.Book, error)
		ListBookPages(ctx context.Context, bookID int, filter PageFilter) ([]entity.BookPage, int, error)
		GetBookPage(ctx context.Context, bookID, pageID int) (entity.BookPage, error)
		ListBookHeadings(ctx context.Context, bookID int, query string) ([]entity.BookHeading, error)
		ListTOCEntries(ctx context.Context, bookID int, lang string, includeAudio bool) ([]entity.BookTOCEntry, error)
		GetSection(ctx context.Context, bookID, headingID int, lang string) (entity.BookSection, error)
	}

	// PersonalRepo -.
	PersonalRepo interface {
		GetProgress(ctx context.Context, userID string, bookID int) (entity.ReadingProgress, error)
		SaveProgress(ctx context.Context, progress entity.ReadingProgress) (entity.ReadingProgress, error)
		ListBookmarks(ctx context.Context, userID string, filter BookmarkFilter) ([]entity.Bookmark, int, error)
		CreateBookmark(ctx context.Context, bookmark entity.Bookmark) (entity.Bookmark, error)
		DeleteBookmark(ctx context.Context, userID, bookmarkID string) error
	}

	// EditorialRepo -.
	EditorialRepo interface {
		ListBooks(ctx context.Context, filter EditorialBookFilter) ([]entity.Book, int, error)
		UpdatePublication(ctx context.Context, actorID string, publication entity.BookPublication) (entity.BookPublication, error)
		SaveMetadataDraft(ctx context.Context, actorID string, edit entity.BookMetadataEdit) (entity.BookMetadataEdit, error)
		PublishMetadataDraft(ctx context.Context, actorID string, bookID int) (entity.BookMetadataEdit, error)
		GetPageEdit(ctx context.Context, bookID, pageID int) (entity.AdminPageEdit, error)
		SavePageDraft(ctx context.Context, actorID string, edit entity.BookPageEdit) (entity.BookPageEdit, error)
		PublishPageDraft(ctx context.Context, actorID string, bookID, pageID int) (entity.BookPageEdit, error)
		SaveHeadingDraft(ctx context.Context, actorID string, edit entity.BookHeadingEdit) (entity.BookHeadingEdit, error)
		PublishHeadingDraft(ctx context.Context, actorID string, bookID, headingID int) (entity.BookHeadingEdit, error)
		AddCollectionItem(ctx context.Context, actorID, slug string, bookID int, sortOrder *int) (entity.BookCollectionItem, error)
	}

	// TaskFilter -.
	TaskFilter struct {
		Status *entity.TaskStatus
		Limit  uint64
		Offset uint64
	}

	// BookFilter -.
	BookFilter struct {
		Query      string
		CategoryID *int
		AuthorID   *int
		HasContent *bool
		Limit      uint64
		Offset     uint64
	}

	// AuthorFilter -.
	AuthorFilter struct {
		Query  string
		Limit  uint64
		Offset uint64
	}

	// PageFilter -.
	PageFilter struct {
		Limit  uint64
		Offset uint64
	}

	// BookmarkFilter -.
	BookmarkFilter struct {
		BookID *int
		Limit  uint64
		Offset uint64
	}

	// EditorialBookFilter -.
	EditorialBookFilter struct {
		Query      string
		Status     *string
		CategoryID *int
		HasContent *bool
		Limit      uint64
		Offset     uint64
	}
)
