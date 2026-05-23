// Package usecase implements application business logic. Each logic group in own file.
package usecase

import (
	"context"

	"github.com/evrone/go-clean-template/internal/entity"
)

//go:generate mockgen -source=contracts.go -destination=./mocks_usecase_test.go -package=usecase_test

type (
	// Translation -.
	Translation interface {
		Translate(ctx context.Context, userID string, t entity.Translation) (entity.Translation, error)
		History(ctx context.Context, userID string) (entity.TranslationHistory, error)
	}

	// User -.
	User interface {
		Register(ctx context.Context, username, email, password string) (entity.User, error)
		Login(ctx context.Context, email, password string) (string, error)
		GetUser(ctx context.Context, userID string) (entity.User, error)
		SetRoleByEmail(ctx context.Context, email, role string) (entity.User, error)
	}

	// Task -.
	Task interface {
		Create(ctx context.Context, userID, title, description string) (entity.Task, error)
		Get(ctx context.Context, userID, taskID string) (entity.Task, error)
		List(ctx context.Context, userID string, status *entity.TaskStatus, limit, offset int) ([]entity.Task, int, error)
		Update(ctx context.Context, userID, taskID, title, description string) (entity.Task, error)
		Transition(ctx context.Context, userID, taskID string, newStatus entity.TaskStatus) (entity.Task, error)
		Delete(ctx context.Context, userID, taskID string) error
	}

	// Reader -.
	Reader interface {
		Categories(ctx context.Context) ([]entity.Category, error)
		Authors(ctx context.Context, query string, limit, offset int) ([]entity.Author, int, error)
		Books(ctx context.Context, query string, categoryID, authorID *int, hasContent *bool, limit, offset int) ([]entity.Book, int, error)
		Book(ctx context.Context, bookID int) (entity.Book, error)
		Pages(ctx context.Context, bookID int, limit, offset int) ([]entity.BookPage, int, error)
		Page(ctx context.Context, bookID, pageID int) (entity.BookPage, error)
		Headings(ctx context.Context, bookID int, query string) ([]entity.BookHeading, error)
		Section(ctx context.Context, bookID, headingID int, lang string) (entity.BookSection, error)
		TOC(ctx context.Context, bookID int, lang string, includeAudio bool) ([]entity.BookTOCNode, error)
		TOCRead(ctx context.Context, bookID, headingID int, lang string) (entity.BookTOCRead, error)
		TOCPlaylist(ctx context.Context, bookID, headingID int, lang string) (entity.BookTOCPlaylist, error)
	}

	// Personal -.
	Personal interface {
		GetProgress(ctx context.Context, userID string, bookID int) (entity.ReadingProgress, error)
		SaveProgress(ctx context.Context, userID string, bookID int, pageID, headingID *int, progressPercent *float64) (entity.ReadingProgress, error)
		ListBookmarks(ctx context.Context, userID string, bookID *int, limit, offset int) ([]entity.Bookmark, int, error)
		CreateBookmark(ctx context.Context, userID string, bookID int, pageID, headingID *int, label, note *string) (entity.Bookmark, error)
		DeleteBookmark(ctx context.Context, userID, bookmarkID string) error
	}

	// Editorial -.
	Editorial interface {
		Books(ctx context.Context, query string, status *string, categoryID *int, hasContent *bool, limit, offset int) ([]entity.Book, int, error)
		UpdatePublication(ctx context.Context, actorID string, bookID int, status string, featured bool, sortOrder *int) (entity.BookPublication, error)
		SaveMetadataDraft(ctx context.Context, actorID string, edit entity.BookMetadataEdit) (entity.BookMetadataEdit, error)
		PublishMetadataDraft(ctx context.Context, actorID string, bookID int) (entity.BookMetadataEdit, error)
		GetPageEdit(ctx context.Context, bookID, pageID int) (entity.AdminPageEdit, error)
		SavePageDraft(ctx context.Context, actorID string, edit entity.BookPageEdit) (entity.BookPageEdit, error)
		PublishPageDraft(ctx context.Context, actorID string, bookID, pageID int) (entity.BookPageEdit, error)
		SaveHeadingDraft(ctx context.Context, actorID string, edit entity.BookHeadingEdit) (entity.BookHeadingEdit, error)
		PublishHeadingDraft(ctx context.Context, actorID string, bookID, headingID int) (entity.BookHeadingEdit, error)
		AddCollectionItem(ctx context.Context, actorID, slug string, bookID int, sortOrder *int) (entity.BookCollectionItem, error)
	}
)
