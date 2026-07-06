// Package usecase implements application business logic. Each logic group in own file.
package usecase

import (
	"context"
	"time"

	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/evrone/go-clean-template/internal/repo"
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
		Login(ctx context.Context, email, password string) (entity.LoginResult, error)
		RefreshSession(ctx context.Context, refreshToken string) (entity.LoginResult, error)
		Logout(ctx context.Context, refreshToken string) error
		LogoutAll(ctx context.Context, userID string) error
		ListSessions(ctx context.Context, userID string) ([]entity.AuthSession, error)
		RevokeSession(ctx context.Context, userID, sessionID string) error
		GetUser(ctx context.Context, userID string) (entity.User, error)
		GetUserAccount(ctx context.Context, userID string) (entity.UserAccount, error)
		AdminUsers(ctx context.Context, query, role string, emailVerified *bool, limit, offset int) ([]entity.UserAccount, int, error)
		AdminUserActivity(ctx context.Context, userID string, limit, offset int) ([]entity.UserActivity, int, error)
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
		SetRoleByEmail(ctx context.Context, actorID, actorEmail, email, role string) (entity.User, error)
		VerifyEmail(ctx context.Context, token, email, otp string) error
		ResendEmailVerification(ctx context.Context, email string) error
		ForgotPassword(ctx context.Context, email string) error
		ResetPassword(ctx context.Context, token, password string) error
		ChangePassword(ctx context.Context, userID, currentPassword, newPassword string) (entity.LoginResult, error)
		RequestEmailChange(ctx context.Context, userID, currentPassword, newEmail string) error
		VerifyEmailChange(ctx context.Context, userID, token, otp string) (entity.LoginResult, error)
		DeleteAccount(ctx context.Context, userID, currentPassword string) error
	}

	// EmailAdmin provides admin-managed email templates, delivery logs, consent, and campaigns.
	EmailAdmin interface {
		SendTransactional(ctx context.Context, req entity.TransactionalEmailRequest) error
		Templates(ctx context.Context, filter repo.EmailTemplateFilter) ([]entity.EmailTemplate, int, error)
		CreateTemplate(ctx context.Context, template entity.EmailTemplate) (entity.EmailTemplate, error)
		Template(ctx context.Context, id string) (entity.EmailTemplate, error)
		UpdateTemplate(ctx context.Context, id string, patch entity.EmailTemplatePatch) (entity.EmailTemplate, error)
		DeleteTemplate(ctx context.Context, id string) error
		CreateVersion(ctx context.Context, version entity.EmailTemplateVersion) (entity.EmailTemplateVersion, error)
		Versions(ctx context.Context, templateID string) ([]entity.EmailTemplateVersion, error)
		UpdateVersion(
			ctx context.Context,
			id string,
			patch entity.EmailTemplateVersionPatch,
		) (entity.EmailTemplateVersion, error)
		PublishVersion(ctx context.Context, id, actorID string) (entity.EmailTemplateVersion, error)
		PreviewTemplate(
			ctx context.Context,
			templateID,
			lang string,
			variables map[string]string,
		) (entity.EmailPreview, error)
		TestSendTemplate(
			ctx context.Context,
			templateID,
			lang,
			to string,
			variables map[string]string,
		) (entity.EmailMessageLog, error)
		EventSetting(ctx context.Context, key string) (entity.EmailEventSetting, error)
		UpdateEventSetting(
			ctx context.Context,
			key string,
			patch entity.EmailEventSettingPatch,
		) (entity.EmailEventSetting, error)
		Messages(ctx context.Context, filter repo.EmailMessageFilter) ([]entity.EmailMessageLog, int, error)
		Subscription(ctx context.Context, userID string) (entity.EmailSubscription, error)
		UpdateSubscription(
			ctx context.Context,
			userID string,
			marketingOptIn bool,
			source string,
		) (entity.EmailSubscription, error)
		Suppressions(ctx context.Context, filter repo.EmailSuppressionFilter) ([]entity.EmailSuppression, int, error)
		CreateSuppression(ctx context.Context, suppression entity.EmailSuppression) (entity.EmailSuppression, error)
		DeleteSuppression(ctx context.Context, id string) error
		DeliveryEvents(ctx context.Context, filter repo.EmailDeliveryEventFilter) ([]entity.EmailDeliveryEvent, int, error)
		CampaignDeliveryEventSummary(
			ctx context.Context,
			campaignID string,
		) (entity.EmailCampaignDeliveryEventSummary, error)
		Campaigns(ctx context.Context, filter repo.EmailCampaignFilter) ([]entity.EmailCampaign, int, error)
		CreateCampaign(ctx context.Context, campaign entity.EmailCampaign) (entity.EmailCampaign, error)
		Campaign(ctx context.Context, id string) (entity.EmailCampaign, error)
		UpdateCampaign(ctx context.Context, campaign entity.EmailCampaign) (entity.EmailCampaign, error)
		PreviewAudience(
			ctx context.Context,
			filter entity.EmailAudienceFilter,
		) ([]entity.EmailAudienceRecipient, int, error)
		ScheduleCampaign(ctx context.Context, id, actorID string, scheduledAt time.Time) (entity.EmailCampaign, error)
		SendCampaignNow(ctx context.Context, id, actorID string) (entity.EmailCampaign, error)
		RetryFailedCampaign(ctx context.Context, id, actorID string) (entity.EmailCampaign, error)
		CancelCampaign(ctx context.Context, id, actorID string) (entity.EmailCampaign, error)
		IngestCloudflareBounceWebhook(ctx context.Context, payload []byte) (entity.EmailWebhookIngestResult, error)
		TestSendCampaign(
			ctx context.Context,
			id,
			to,
			lang string,
			variables map[string]string,
		) (entity.EmailMessageLog, error)
		DispatchDueCampaigns(ctx context.Context, limit int) error
		DispatchDueTransactionalEmails(ctx context.Context, limit int) error
		PollCloudflareEmailEvents(ctx context.Context) (entity.EmailWebhookIngestResult, error)
		Unsubscribe(ctx context.Context, token string) (entity.EmailSubscription, error)
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
		BookStats(ctx context.Context, lang string) (entity.BookCatalogStats, error)
		Book(ctx context.Context, bookID int, lang string) (entity.Book, error)
		Pages(ctx context.Context, bookID, limit, offset int) ([]entity.BookPage, int, error)
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
		SurahAudio(ctx context.Context, surahID int, recitationID string) (entity.QuranSurahAudioManifest, error)
		TranslationSources(ctx context.Context, lang string) ([]entity.QuranTranslationSource, error)
		Juz(ctx context.Context, lang string) ([]entity.QuranNavigationSegment, error)
		JuzAyahs(
			ctx context.Context,
			juzNumber int,
			lang string,
			translationSource string,
			includeTranslation bool,
			includeAudio bool,
			includeEditorial bool,
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
			includeEditorial bool,
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
			includeEditorial bool,
			recitationID string,
		) ([]entity.QuranAyah, error)
		Search(ctx context.Context, query, lang string, limit, offset int) ([]entity.QuranSearchResult, int, error)
		BookReferences(ctx context.Context, bookID int, headingID *int, lang, status string, limit, offset int) ([]entity.BookQuranReference, int, error)
		MissingAssets(ctx context.Context, targetLang, assetType string, surahID *int, limit, offset int) (entity.EditorialMissingQuranAssets, error)
	}

	// Personal -.
	Personal interface {
		GetProgress(ctx context.Context, userID string, bookID int) (entity.ReadingProgress, error)
		SaveProgress(ctx context.Context, userID string, bookID int, pageID, headingID *int, progressPercent *float64, clientObservedAt *time.Time) (entity.ReadingProgress, error)
		ListProgress(ctx context.Context, userID, lang string, limit, offset int) ([]entity.ContinueReadingEntry, int, error)
		GetQuranProgress(ctx context.Context, userID string) (entity.QuranReadingProgress, error)
		GetQuranSurahProgress(ctx context.Context, userID string, surahID int) (entity.QuranReadingProgress, error)
		ListQuranSurahProgress(ctx context.Context, userID string) ([]entity.QuranReadingProgress, error)
		SaveQuranProgress(ctx context.Context, userID, ayahKey string, clientObservedAt *time.Time) (entity.QuranReadingProgress, error)
		ListSavedItems(ctx context.Context, userID, itemType string, bookID, surahID *int, tag string, limit, offset int) ([]entity.SavedItem, int, error)
		UpsertSavedItem(ctx context.Context, userID string, item entity.SavedItem) (entity.SavedItem, bool, error)
		UpdateSavedItem(ctx context.Context, userID, savedItemID string, patch entity.SavedItemPatch) (entity.SavedItem, error)
		DeleteSavedItem(ctx context.Context, userID, savedItemID string) error
		ListSavedItemTags(ctx context.Context, userID string) ([]string, error)
		StartKhatamCycle(ctx context.Context, userID string, notes *string) (entity.QuranKhatamCycle, error)
		GetActiveKhatamCycle(ctx context.Context, userID string) (entity.QuranKhatamCycle, error)
		MarkKhatamJuz(ctx context.Context, userID string, juzNumber int) (entity.QuranKhatamCycle, error)
		UnmarkKhatamJuz(ctx context.Context, userID string, juzNumber int) (entity.QuranKhatamCycle, error)
		CompleteKhatamCycle(ctx context.Context, userID string) (entity.QuranKhatamCycle, error)
		ListKhatamHistory(ctx context.Context, userID string, limit, offset int) ([]entity.QuranKhatamCycle, int, error)
		SyncPersonalData(ctx context.Context, userID string, since *time.Time) (entity.PersonalSyncSnapshot, error)
		GetReadingStreak(ctx context.Context, userID, today string) (entity.ReadingStreak, error)
		GetReadingActivity(ctx context.Context, userID, from, to string) (entity.ReadingActivitySummary, error)
	}

	// Editorial -.
	Editorial interface {
		Books(ctx context.Context, query string, status *string, categoryID *int, hasContent *bool, limit, offset int) ([]entity.Book, int, error)
		ProductionCandidates(ctx context.Context, lang, query string, categoryID, authorID *int, hasContent *bool, unstarted bool, limit, offset int) ([]entity.BookProductionCandidate, int, error)
		UpdatePublication(ctx context.Context, actorID string, bookID int, status string, featured bool, sortOrder *int) (entity.BookPublication, error)
		GetMetadataDraft(ctx context.Context, bookID int) (entity.BookMetadataEdit, error)
		SaveMetadataDraft(ctx context.Context, actorID string, edit entity.BookMetadataEdit, expected *time.Time, origin string) (entity.BookMetadataEdit, error)
		PublishMetadataDraft(ctx context.Context, actorID string, bookID int, expected *time.Time) (entity.BookMetadataEdit, error)
		GetPageEdit(ctx context.Context, bookID, pageID int) (entity.EditorialPageEdit, error)
		SavePageDraft(ctx context.Context, actorID string, edit entity.BookPageEdit, expected *time.Time, origin string) (entity.BookPageEdit, error)
		PublishPageDraft(ctx context.Context, actorID string, bookID, pageID int, expected *time.Time) (entity.BookPageEdit, error)
		PageDraftRevisions(ctx context.Context, bookID, pageID, limit, offset int) ([]entity.BookSourceEditRevision, int, error)
		RestorePageDraftRevision(ctx context.Context, actorID string, bookID, pageID int, revisionID string) (entity.BookPageEdit, error)
		GetHeadingDraft(ctx context.Context, bookID, headingID int) (entity.BookHeadingEdit, error)
		SaveHeadingDraft(ctx context.Context, actorID string, edit entity.BookHeadingEdit, expected *time.Time, origin string) (entity.BookHeadingEdit, error)
		PublishHeadingDraft(ctx context.Context, actorID string, bookID, headingID int, expected *time.Time) (entity.BookHeadingEdit, error)
		AddCollectionItem(ctx context.Context, actorID, slug string, bookID int, sortOrder *int) (entity.BookCollectionItem, error)
		TranslationFeedbacks(ctx context.Context, bookID, headingID *int, lang, vote, status string, limit, offset int) ([]entity.EditorialTranslationFeedback, int, error)
		TranslationFeedbackSummary(ctx context.Context, bookID, headingID *int, lang, vote, status string, limit int) (entity.EditorialTranslationFeedbackSummary, error)
		MissingReaderAssets(ctx context.Context, targetLang, assetType string, bookID *int, limit, offset int) (entity.EditorialMissingReaderAssets, error)
		ResolveTranslationFeedback(ctx context.Context, actorID, feedbackID string, note *string) (entity.EditorialTranslationFeedback, error)
		ReopenTranslationFeedback(ctx context.Context, actorID, feedbackID string) (entity.EditorialTranslationFeedback, error)
		CreateProductionProject(ctx context.Context, actorID string, project entity.BookProductionProject) (entity.BookProductionProject, error)
		ProductionDashboard(ctx context.Context, lang string, activityLimit int) (entity.BookProductionDashboard, error)
		ProductionProjects(ctx context.Context, bookID *int, lang, workflowStatus, publicationStatus string, readyToPublish, needsWork bool, limit, offset int) ([]entity.BookProductionProject, int, error)
		ProductionProject(ctx context.Context, projectID string) (entity.BookProductionProject, error)
		ProductionWorkspace(ctx context.Context, projectID string) (entity.BookProductionWorkspace, error)
		ProductionActivity(ctx context.Context, projectID string, limit, offset int) ([]entity.BookProductionEvent, int, error)
		GlobalProductionActivity(ctx context.Context, lang string, limit, offset int) ([]entity.BookProductionEvent, int, error)
		ProductionDraftRevisions(ctx context.Context, projectID, assetType string, headingID *int, limit, offset int) ([]entity.BookProductionDraftRevision, int, error)
		RestoreProductionDraftRevision(ctx context.Context, actorID, projectID, revisionID string) (entity.BookProductionDraftRevision, error)
		ProductionPublishCheck(ctx context.Context, projectID string) (entity.BookProductionPublishCheck, error)
		UpdateProductionProject(ctx context.Context, actorID, projectID string, patch entity.BookProductionProjectPatch, expected *time.Time) (entity.BookProductionProject, error)
		ProductionCompleteness(ctx context.Context, projectID string) (entity.BookProductionCompleteness, error)
		GetMetadataTranslationDraft(ctx context.Context, projectID string) (entity.BookMetadataTranslationEdit, error)
		SaveMetadataTranslationDraft(ctx context.Context, actorID, projectID string, edit entity.BookMetadataTranslationEdit, expected *time.Time) (entity.BookMetadataTranslationEdit, error)
		DeleteMetadataTranslationDraft(ctx context.Context, actorID, projectID string, expected *time.Time) error
		GetAuthorTranslationDraft(ctx context.Context, projectID string) (entity.AuthorTranslationEdit, error)
		SaveAuthorTranslationDraft(ctx context.Context, actorID, projectID string, edit entity.AuthorTranslationEdit, expected *time.Time) (entity.AuthorTranslationEdit, error)
		DeleteAuthorTranslationDraft(ctx context.Context, actorID, projectID string, expected *time.Time) error
		GetCategoryTranslationDraft(ctx context.Context, projectID string) (entity.CategoryTranslationEdit, error)
		SaveCategoryTranslationDraft(ctx context.Context, actorID, projectID string, edit entity.CategoryTranslationEdit, expected *time.Time) (entity.CategoryTranslationEdit, error)
		DeleteCategoryTranslationDraft(ctx context.Context, actorID, projectID string, expected *time.Time) error
		GetSectionTranslationDraft(ctx context.Context, projectID string, headingID int) (entity.SectionTranslationEdit, error)
		SaveSectionTranslationDraft(ctx context.Context, actorID, projectID string, edit entity.SectionTranslationEdit, expected *time.Time) (entity.SectionTranslationEdit, error)
		DeleteSectionTranslationDraft(ctx context.Context, actorID, projectID string, headingID int, expected *time.Time) error
		GetHeadingSummaryDraft(ctx context.Context, projectID string, headingID int) (entity.HeadingSummaryEdit, error)
		SaveHeadingSummaryDraft(ctx context.Context, actorID, projectID string, edit entity.HeadingSummaryEdit, expected *time.Time) (entity.HeadingSummaryEdit, error)
		DeleteHeadingSummaryDraft(ctx context.Context, actorID, projectID string, headingID int, expected *time.Time) error
		GetSectionAudioDraft(ctx context.Context, projectID string, headingID int) (entity.SectionAudioEdit, error)
		SaveSectionAudioDraft(ctx context.Context, actorID, projectID string, edit entity.SectionAudioEdit, expected *time.Time) (entity.SectionAudioEdit, error)
		DeleteSectionAudioDraft(ctx context.Context, actorID, projectID string, headingID int, expected *time.Time) error
		ReviewProductionAsset(ctx context.Context, actorID, projectID, assetType string, headingID *int, decision string, note *string) error
		PublishProductionProject(ctx context.Context, actorID, projectID string, expected *time.Time) (entity.BookProductionProject, error)
		UnpublishProductionProject(ctx context.Context, actorID, projectID string, expected *time.Time) (entity.BookProductionProject, error)
		DeleteFinalProductionAsset(ctx context.Context, actorID, projectID, assetType string, headingID *int, reason *string) error
	}
)
