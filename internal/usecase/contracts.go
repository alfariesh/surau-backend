// Package usecase implements application business logic. Each logic group in own file.
package usecase

import (
	"context"
	"time"

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
		GetUserAccount(ctx context.Context, userID string) (entity.UserAccount, error)
		CompleteOnboarding(
			ctx context.Context,
			userID string,
			onboarding entity.UserOnboarding,
		) (entity.UserAccount, error)
		UpdateUserProfile(
			ctx context.Context,
			userID string,
			patch entity.UserProfilePatch,
		) (entity.UserAccount, error)
		UpdateUserPreferences(
			ctx context.Context,
			userID string,
			patch entity.UserPreferencesPatch,
		) (entity.UserAccount, error)
		SetRoleByEmail(ctx context.Context, email, role string) (entity.User, error)
		VerifyEmail(ctx context.Context, token string) error
		ResendEmailVerification(ctx context.Context, email string) error
		ForgotPassword(ctx context.Context, email string) error
		ResetPassword(ctx context.Context, token, password string) error
		ChangePassword(ctx context.Context, userID, currentPassword, newPassword string) error
		RequestEmailChange(ctx context.Context, userID, currentPassword, newEmail string) error
		VerifyEmailChange(ctx context.Context, userID, token string) error
		DeleteAccount(ctx context.Context, userID, currentPassword string) error
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
		Categories(ctx context.Context, lang string) ([]entity.Category, error)
		Authors(ctx context.Context, query string, limit, offset int, lang string) ([]entity.Author, int, error)
		Books(ctx context.Context, query string, categoryID, authorID *int, hasContent *bool, limit, offset int, lang string) ([]entity.Book, int, error)
		Book(ctx context.Context, bookID int, lang string) (entity.Book, error)
		Pages(ctx context.Context, bookID int, limit, offset int) ([]entity.BookPage, int, error)
		Page(ctx context.Context, bookID, pageID int) (entity.BookPage, error)
		Headings(ctx context.Context, bookID int, query string) ([]entity.BookHeading, error)
		Section(ctx context.Context, bookID, headingID int, lang string) (entity.BookSection, error)
		TOC(ctx context.Context, bookID int, lang string, includeAudio bool) ([]entity.BookTOCNode, error)
		TOCRead(ctx context.Context, bookID, headingID int, lang string) (entity.BookTOCRead, error)
		TOCPlaylist(ctx context.Context, bookID, headingID int, lang string) (entity.BookTOCPlaylist, error)
		CreateTranslationFeedback(
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
		) (entity.TranslationFeedback, error)
	}

	// BookRAG -.
	BookRAG interface {
		AskBook(
			ctx context.Context,
			bookID int,
			question string,
			lang string,
			maxCitations int,
			includeTrace bool,
		) (entity.BookRAGResponse, error)
		AskBookStream(
			ctx context.Context,
			bookID int,
			question string,
			lang string,
			maxCitations int,
			includeTrace bool,
			emit func(event string, payload any) error,
		) error
	}

	// Quran -.
	Quran interface {
		Surahs(ctx context.Context, lang string, includeInfo bool) ([]entity.QuranSurah, error)
		Surah(ctx context.Context, surahID int, lang string) (entity.QuranSurah, error)
		Recitations(ctx context.Context) ([]entity.QuranRecitation, error)
		TranslationSources(ctx context.Context, lang string) ([]entity.QuranTranslationSource, error)
		Juz(ctx context.Context, lang string) ([]entity.QuranNavigationSegment, error)
		JuzAyahs(
			ctx context.Context,
			juzNumber int,
			lang string,
			translationSource string,
			includeTranslation bool,
			includeAudio bool,
			recitationID string,
		) ([]entity.QuranAyah, error)
		Hizbs(ctx context.Context, lang string) ([]entity.QuranNavigationSegment, error)
		HizbAyahs(
			ctx context.Context,
			hizbNumber int,
			lang string,
			translationSource string,
			includeTranslation bool,
			includeAudio bool,
			recitationID string,
		) ([]entity.QuranAyah, error)
		Ayah(
			ctx context.Context,
			ayahKey string,
			lang string,
			translationSource string,
			includeAudio bool,
			recitationID string,
		) (entity.QuranAyah, error)
		SurahAyahs(
			ctx context.Context,
			surahID int,
			fromAyah int,
			toAyah int,
			lang string,
			translationSource string,
			includeTranslation bool,
			includeAudio bool,
			recitationID string,
		) ([]entity.QuranAyah, error)
		Search(ctx context.Context, query, lang string, limit, offset int) ([]entity.QuranSearchResult, int, error)
		BookReferences(ctx context.Context, bookID int, lang, status string, limit, offset int) ([]entity.BookQuranReference, int, error)
		MissingAssets(ctx context.Context, targetLang, assetType string, surahID *int, limit, offset int) (entity.AdminMissingQuranAssets, error)
	}

	// Personal -.
	Personal interface {
		GetProgress(ctx context.Context, userID string, bookID int) (entity.ReadingProgress, error)
		SaveProgress(ctx context.Context, userID string, bookID int, pageID, headingID *int, progressPercent *float64) (entity.ReadingProgress, error)
		GetQuranProgress(ctx context.Context, userID string) (entity.QuranReadingProgress, error)
		GetQuranSurahProgress(ctx context.Context, userID string, surahID int) (entity.QuranReadingProgress, error)
		ListQuranSurahProgress(ctx context.Context, userID string) ([]entity.QuranReadingProgress, error)
		SaveQuranProgress(ctx context.Context, userID, ayahKey string, clientObservedAt *time.Time) (entity.QuranReadingProgress, error)
		ListSavedItems(ctx context.Context, userID, itemType string, bookID, surahID *int, tag string, limit, offset int) ([]entity.SavedItem, int, error)
		UpsertSavedItem(ctx context.Context, userID string, item entity.SavedItem) (entity.SavedItem, error)
		UpdateSavedItem(ctx context.Context, userID, savedItemID string, label, note *string, tags []string) (entity.SavedItem, error)
		DeleteSavedItem(ctx context.Context, userID, savedItemID string) error
		ListSavedItemTags(ctx context.Context, userID string) ([]string, error)
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
		TranslationFeedbacks(ctx context.Context, bookID, headingID *int, lang, vote, status string, limit, offset int) ([]entity.AdminTranslationFeedback, int, error)
		TranslationFeedbackSummary(ctx context.Context, bookID, headingID *int, lang, vote, status string, limit int) (entity.AdminTranslationFeedbackSummary, error)
		MissingReaderAssets(ctx context.Context, targetLang, assetType string, bookID *int, limit, offset int) (entity.AdminMissingReaderAssets, error)
		ResolveTranslationFeedback(ctx context.Context, actorID, feedbackID string, note *string) (entity.AdminTranslationFeedback, error)
		ReopenTranslationFeedback(ctx context.Context, actorID, feedbackID string) (entity.AdminTranslationFeedback, error)
	}
)
