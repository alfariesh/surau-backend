// Package repo implements application outer layer logic. Each logic group in own file.
package repo

import (
	"context"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
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
		StoreWithVerificationToken(ctx context.Context, user *entity.User, token *entity.EmailVerificationToken) error
		GetByID(ctx context.Context, id string) (entity.User, error)
		GetByEmail(ctx context.Context, email string) (entity.User, error)
		GetAccount(ctx context.Context, userID string) (entity.UserAccount, error)
		ListAccounts(ctx context.Context, filter UserFilter) ([]entity.UserAccount, int, error)
		ListUserActivity(ctx context.Context, filter UserActivityFilter) ([]entity.UserActivity, int, error)
		UpsertProfile(ctx context.Context, profile entity.UserProfile) error
		UpsertPreferences(ctx context.Context, preferences entity.UserPreferences) error
		SetRoleByEmail(ctx context.Context, email, role string) (entity.UserRoleChange, error)
		ChangePassword(ctx context.Context, userID, passwordHash string) (entity.User, error)
		ReplaceVerificationToken(ctx context.Context, token *entity.EmailVerificationToken) error
		RevokeUnusedVerificationTokens(ctx context.Context, userID string) error
		GetVerificationTokenByHash(ctx context.Context, tokenHash string) (entity.EmailVerificationToken, error)
		GetLatestUnusedVerificationToken(ctx context.Context, userID string) (entity.EmailVerificationToken, error)
		VerifyEmailWithToken(ctx context.Context, tokenID, userID string) (entity.User, error)
		ReplacePasswordResetToken(ctx context.Context, token *entity.PasswordResetToken) error
		RevokeUnusedPasswordResetTokens(ctx context.Context, userID string) error
		GetPasswordResetTokenByHash(ctx context.Context, tokenHash string) (entity.PasswordResetToken, error)
		GetLatestUnusedPasswordResetToken(ctx context.Context, userID string) (entity.PasswordResetToken, error)
		ResetPasswordWithToken(ctx context.Context, tokenID, userID, passwordHash string) (entity.User, error)
		ReplaceEmailChangeToken(ctx context.Context, token *entity.EmailChangeToken) error
		RevokeUnusedEmailChangeTokens(ctx context.Context, userID string) error
		GetEmailChangeTokenByHash(ctx context.Context, tokenHash string) (entity.EmailChangeToken, error)
		GetLatestUnusedEmailChangeToken(ctx context.Context, userID string) (entity.EmailChangeToken, error)
		ChangeEmailWithToken(ctx context.Context, tokenID, userID, newEmail string) (entity.EmailChangeResult, error)
		DeleteAccount(ctx context.Context, userID string) error
		RecordAuthLoginFingerprint(ctx context.Context, fingerprint entity.AuthLoginFingerprint) (bool, error)
		AcquireAuthNotificationCooldown(ctx context.Context, cooldown entity.AuthNotificationCooldown) (bool, error)
	}

	// AuthRateLimitRepo -.
	AuthRateLimitRepo interface {
		IncrementAuthRateLimit(ctx context.Context, limit entity.AuthRateLimit) (entity.AuthRateLimitResult, error)
	}

	// AuthSessionRepo stores refresh-token sessions.
	AuthSessionRepo interface {
		CreateAuthSession(ctx context.Context, session entity.AuthSession) error
		GetAuthSessionByTokenHash(ctx context.Context, tokenHash string) (entity.AuthSession, error)
		// RotateAuthSession atomically revokes the old session row and inserts
		// its replacement. Returns entity.ErrInvalidRefreshToken when the old
		// row was already revoked or replaced (concurrent rotation = reuse).
		RotateAuthSession(ctx context.Context, oldID string, next entity.AuthSession) error
		RevokeAuthSessionFamily(ctx context.Context, familyID string) (int64, error)
		// RevokeAllAuthSessions revokes every active session for the user and
		// bumps users.token_version in one transaction (logout everywhere).
		RevokeAllAuthSessions(ctx context.Context, userID string) (int64, error)
		// ListActiveAuthSessions returns the user's unrevoked, unexpired sessions
		// (one row per active device), newest activity first.
		ListActiveAuthSessions(ctx context.Context, userID string) ([]entity.AuthSession, error)
		// RevokeAuthSessionByID revokes the family of one active session, scoped
		// to the owning user. Returns entity.ErrAuthSessionNotFound when no
		// active session matches the id for that user.
		RevokeAuthSessionByID(ctx context.Context, userID, sessionID string) error
	}

	// AuthLockoutRepo stores progressive login lockout counters.
	AuthLockoutRepo interface {
		// GetAuthLoginLockout returns the zero value when no row exists.
		GetAuthLoginLockout(ctx context.Context, keyHash string) (entity.AuthLoginLockout, error)
		// IncrementAuthLoginFailure upserts the counter and applies lockedUntil
		// when non-nil, returning the new consecutive failure count.
		IncrementAuthLoginFailure(ctx context.Context, keyHash string, lockedUntil *time.Time) (int, error)
		ResetAuthLoginLockout(ctx context.Context, keyHash string) error
	}

	// AuthMaintenanceRepo deletes expired auth rows.
	AuthMaintenanceRepo interface {
		CleanupAuthData(ctx context.Context, policy AuthCleanupPolicy) (entity.AuthCleanupResult, error)
	}

	// MFARepo stores TOTP enrollment state, one-time recovery codes,
	// short-lived second-factor challenges, and the session step-up stamp
	// (A-3). Wired separately from UserRepo so the user usecase treats a nil
	// MFARepo as "feature absent" and keeps pre-MFA behavior.
	MFARepo interface {
		// UpsertPendingMFA writes a NOT-yet-confirmed enrollment, overwriting
		// a previous pending one. Returns entity.ErrMFAAlreadyEnabled when a
		// confirmed enrollment exists.
		UpsertPendingMFA(ctx context.Context, userID, secretEnc string) error
		// GetMFA returns entity.ErrMFANotEnabled when no row exists.
		GetMFA(ctx context.Context, userID string) (entity.UserMFA, error)
		// ConfirmMFA activates a pending enrollment. Returns
		// entity.ErrMFAEnrollmentNotStarted when no pending row exists.
		ConfirmMFA(ctx context.Context, userID string) error
		// AdvanceMFATOTPStep persists the last accepted TOTP step, guarded
		// monotonically: returns entity.ErrInvalidMFACode when step is not
		// strictly greater than the stored one (replay/concurrent use).
		AdvanceMFATOTPStep(ctx context.Context, userID string, step int64) error
		DeleteMFA(ctx context.Context, userID string) error

		// ReplaceRecoveryCodes swaps the full recovery-code set atomically.
		ReplaceRecoveryCodes(ctx context.Context, userID string, codeHashes []string) error
		// ConsumeRecoveryCode marks one unused code as used, atomically.
		// Returns entity.ErrInvalidMFACode when the code is unknown, foreign,
		// or already used (one-time semantics, AC-3).
		ConsumeRecoveryCode(ctx context.Context, userID, codeHash string) error
		CountUnusedRecoveryCodes(ctx context.Context, userID string) (int, error)

		CreateMFAChallenge(ctx context.Context, challenge entity.MFAChallenge) error
		// GetMFAChallengeByTokenHash returns entity.ErrInvalidMFAChallenge
		// when no live (unconsumed, unexpired) challenge matches.
		GetMFAChallengeByTokenHash(ctx context.Context, tokenHash, purpose string) (entity.MFAChallenge, error)
		// ConsumeMFAChallenge flips consumed_at exactly once; returns
		// entity.ErrInvalidMFAChallenge when already consumed or expired.
		ConsumeMFAChallenge(ctx context.Context, id string) error

		// SetSessionMFAVerified stamps the active session of one family.
		SetSessionMFAVerified(ctx context.Context, userID, familyID string, at time.Time) error
		// GetMFAGateData is the single read behind the step-up gate: user's
		// grace anchor + enrollment state + the active session's stamp.
		GetMFAGateData(ctx context.Context, userID, familyID string) (entity.MFAGateData, error)
	}

	// AuthAuditRepo -.
	AuthAuditRepo interface {
		StoreAuthAuditLog(ctx context.Context, log entity.AuthAuditLog) error
		// ListAuthAuditEventsSince returns audit rows for one event type created
		// strictly after since, oldest first, capped at limit.
		ListAuthAuditEventsSince(
			ctx context.Context,
			event string,
			since time.Time,
			limit int,
		) ([]entity.AuthAuditLog, error)
	}

	// EmailSender -.
	EmailSender interface {
		Send(ctx context.Context, message entity.EmailMessage) (entity.EmailSendResult, error)
	}

	// EmailEventPoller fetches provider-side asynchronous email delivery events.
	EmailEventPoller interface {
		PollCloudflareEmailEvents(
			ctx context.Context,
			query entity.CloudflareEmailEventPollQuery,
		) ([]entity.CloudflareEmailEvent, error)
	}

	// EmailRepo stores admin-managed templates, logs, subscriptions, suppressions, and campaigns.
	EmailRepo interface {
		CreateEmailTemplate(ctx context.Context, template entity.EmailTemplate) (entity.EmailTemplate, error)
		ListEmailTemplates(ctx context.Context, filter EmailTemplateFilter) ([]entity.EmailTemplate, int, error)
		GetEmailTemplateByID(ctx context.Context, id string) (entity.EmailTemplate, error)
		GetEmailTemplateByKey(ctx context.Context, key string) (entity.EmailTemplate, error)
		UpdateEmailTemplate(ctx context.Context, id string, patch entity.EmailTemplatePatch) (entity.EmailTemplate, error)
		DeleteEmailTemplate(ctx context.Context, id string) error
		CreateEmailTemplateVersion(
			ctx context.Context,
			version entity.EmailTemplateVersion,
		) (entity.EmailTemplateVersion, error)
		ListEmailTemplateVersions(ctx context.Context, templateID string) ([]entity.EmailTemplateVersion, error)
		GetEmailTemplateVersionByID(ctx context.Context, id string) (entity.EmailTemplateVersion, error)
		GetLatestEmailTemplateVersion(ctx context.Context, templateID, lang string) (entity.EmailTemplateVersion, error)
		GetPublishedEmailTemplateVersion(
			ctx context.Context,
			templateKey,
			lang string,
		) (entity.EmailTemplateVersion, entity.EmailTemplate, error)
		UpdateEmailTemplateVersion(
			ctx context.Context,
			id string,
			patch entity.EmailTemplateVersionPatch,
		) (entity.EmailTemplateVersion, error)
		PublishEmailTemplateVersion(ctx context.Context, id, actorID string) (entity.EmailTemplateVersion, error)
		GetEmailEventSetting(ctx context.Context, key string) (entity.EmailEventSetting, error)
		UpdateEmailEventSetting(
			ctx context.Context,
			key string,
			patch entity.EmailEventSettingPatch,
		) (entity.EmailEventSetting, error)
		CreateEmailMessage(ctx context.Context, message entity.EmailMessageLog) (entity.EmailMessageLog, error)
		UpdateEmailMessageStatus(
			ctx context.Context,
			id string,
			status string,
			attempts int,
			providerResponse string,
			deliveryError string,
			sentAt *time.Time,
		) (entity.EmailMessageLog, error)
		ScheduleEmailMessageRetry(
			ctx context.Context,
			id string,
			attempts int,
			providerResponse string,
			deliveryError string,
			scheduledAt time.Time,
		) (entity.EmailMessageLog, error)
		ClaimDueTransactionalEmailMessages(
			ctx context.Context,
			now time.Time,
			limit int,
			visibilityTimeout time.Duration,
		) ([]entity.EmailMessageLog, error)
		ListEmailMessages(ctx context.Context, filter EmailMessageFilter) ([]entity.EmailMessageLog, int, error)
		GetEmailMessageByID(ctx context.Context, id string) (entity.EmailMessageLog, error)
		GetEmailSubscription(ctx context.Context, userID string) (entity.EmailSubscription, error)
		UpsertEmailSubscription(ctx context.Context, subscription entity.EmailSubscription) (entity.EmailSubscription, error)
		UnsubscribeEmail(ctx context.Context, userID, email, source string) (entity.EmailSubscription, error)
		ListEmailSuppressions(ctx context.Context, filter EmailSuppressionFilter) ([]entity.EmailSuppression, int, error)
		CreateEmailSuppression(ctx context.Context, suppression entity.EmailSuppression) (entity.EmailSuppression, error)
		UpsertAutomaticEmailSuppression(
			ctx context.Context,
			suppression entity.EmailSuppression,
		) (entity.EmailSuppression, error)
		DeleteEmailSuppression(ctx context.Context, id string) error
		IsEmailSuppressed(ctx context.Context, email, category string) (bool, error)
		UpsertEmailDeliveryEvent(ctx context.Context, event entity.EmailDeliveryEvent) (entity.EmailDeliveryEvent, bool, error)
		ListEmailDeliveryEvents(ctx context.Context, filter EmailDeliveryEventFilter) ([]entity.EmailDeliveryEvent, int, error)
		GetEmailCampaignDeliveryEventSummary(
			ctx context.Context,
			campaignID string,
		) (entity.EmailCampaignDeliveryEventSummary, error)
		GetEmailProviderPollCursor(
			ctx context.Context,
			provider string,
			cursorKey string,
		) (entity.EmailProviderPollCursor, error)
		UpsertEmailProviderPollCursor(
			ctx context.Context,
			cursor entity.EmailProviderPollCursor,
		) (entity.EmailProviderPollCursor, error)
		CreateEmailCampaign(ctx context.Context, campaign entity.EmailCampaign) (entity.EmailCampaign, error)
		ListEmailCampaigns(ctx context.Context, filter EmailCampaignFilter) ([]entity.EmailCampaign, int, error)
		GetEmailCampaign(ctx context.Context, id string) (entity.EmailCampaign, error)
		UpdateEmailCampaign(ctx context.Context, campaign entity.EmailCampaign) (entity.EmailCampaign, error)
		ClaimEmailCampaignForRetry(ctx context.Context, id, actorID string) (entity.EmailCampaign, error)
		ListMarketingAudience(
			ctx context.Context,
			filter entity.EmailAudienceFilter,
		) ([]entity.EmailAudienceRecipient, int, error)
		ReplaceEmailCampaignRecipients(
			ctx context.Context,
			campaignID string,
			recipients []entity.EmailCampaignRecipient,
		) error
		ListEmailCampaignRecipients(
			ctx context.Context,
			campaignID string,
			status string,
			limit int,
		) ([]entity.EmailCampaignRecipient, error)
		ListEmailCampaignRecipientsForRetry(
			ctx context.Context,
			campaignID string,
			cutoff time.Time,
			limit int,
		) ([]entity.EmailCampaignRecipient, error)
		CountEmailCampaignRecipientsByStatus(ctx context.Context, campaignID string) (map[string]int, error)
		UpdateEmailCampaignRecipientStatus(
			ctx context.Context,
			id,
			status,
			messageID,
			deliveryError string,
			sentAt *time.Time,
		) (entity.EmailCampaignRecipient, error)
		ListDueEmailCampaigns(ctx context.Context, now time.Time, limit int) ([]entity.EmailCampaign, error)
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
		ListCategories(ctx context.Context, lang string) ([]entity.Category, error)
		ListAuthors(ctx context.Context, filter AuthorFilter) ([]entity.Author, int, error)
		ListBooks(ctx context.Context, filter BookFilter) ([]entity.Book, int, error)
		GetBookCatalogStats(ctx context.Context, lang string) (entity.BookCatalogStats, error)
		GetBook(ctx context.Context, bookID int, lang string) (entity.Book, error)
		ListBookPages(ctx context.Context, bookID int, filter PageFilter) ([]entity.BookPage, int, error)
		GetBookPage(ctx context.Context, bookID, pageID int) (entity.BookPage, error)
		ListBookHeadings(ctx context.Context, bookID int, filter HeadingFilter) ([]entity.BookHeading, int, error)
		ListTOCEntries(ctx context.Context, bookID int, lang string, includeAudio bool) ([]entity.BookTOCEntry, error)
		GetSection(ctx context.Context, bookID, headingID int, lang string) (entity.BookSection, error)
		CreateTranslationFeedback(ctx context.Context, feedback entity.TranslationFeedback) (entity.TranslationFeedback, error)
	}

	// BookRAGRepo provides PageIndex-like retrieval data for book RAG.
	BookRAGRepo interface {
		GetRAGBookDocument(ctx context.Context, bookID int, lang string) (entity.RAGBookDocument, error)
		ListRAGStructure(ctx context.Context, bookID int, lang string) ([]entity.RAGStructureNode, error)
		CheckRAGUnitMaterialization(ctx context.Context, bookID int) error
		GetRAGPageSources(
			ctx context.Context,
			bookID int,
			headingIDs []int,
			focusPageIDs []int,
			lang string,
			maxPages int,
		) ([]entity.RAGPageSource, error)
		GetRAGUnitSources(
			ctx context.Context,
			bookID int,
			headingIDs []int,
			focusPageIDs []int,
			lang string,
			maxPages int,
		) ([]entity.RAGPageSource, error)
		SearchRAGPages(ctx context.Context, bookID int, query, lang string, limit int) ([]entity.RAGSearchResult, error)
		SearchRAGUnits(ctx context.Context, bookID int, query string, limit int) ([]entity.RAGSearchResult, error)
		ResolveRAGUnitCitation(
			ctx context.Context,
			bookID int,
			headingID int,
			pageID int,
			quote string,
		) (entity.RAGUnitLocator, error)
	}

	// QuranRepo provides public Quran browse/search and kitab reference lookups.
	QuranRepo interface {
		ListSurahs(ctx context.Context, lang string, includeInfo bool) ([]entity.QuranSurah, error)
		GetSurah(ctx context.Context, surahID int, lang string) (entity.QuranSurah, error)
		ListRecitations(ctx context.Context) ([]entity.QuranRecitation, error)
		GetSurahAudioManifest(ctx context.Context, surahID int, recitationID string) (entity.QuranSurahAudioManifest, error)
		ListTranslationSources(ctx context.Context, lang string) ([]entity.QuranTranslationSource, error)
		ListNavigationSegments(ctx context.Context, kind, lang string) ([]entity.QuranNavigationSegment, error)
		GetAyah(
			ctx context.Context,
			ayahKey string,
			lang string,
			translationSource string,
			includeAudio bool,
			recitationID string,
		) (entity.QuranAyah, error)
		ListSurahAyahs(
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
		ListNavigationAyahs(
			ctx context.Context,
			kind string,
			number int,
			lang string,
			translationSource string,
			includeTranslation bool,
			includeAudio bool,
			includeEditorial bool,
			recitationID string,
		) ([]entity.QuranAyah, error)
		SearchAyahs(ctx context.Context, filter QuranSearchFilter) ([]entity.QuranSearchResult, int, error)
		ListBookQuranReferences(ctx context.Context, filter QuranBookReferenceFilter) ([]entity.BookQuranReference, int, error)
		ListMissingQuranAssets(ctx context.Context, filter MissingQuranAssetFilter) (entity.EditorialMissingQuranAssets, error)
		ListQuranSitemap(ctx context.Context) ([]entity.QuranSitemapItem, error)
		ListQuranFeed(ctx context.Context, filter QuranFeedFilter) ([]entity.QuranSitemapItem, int, error)
		ResolveQuranSurahSlug(ctx context.Context, slug string) (entity.QuranSlugResolution, error)
		ListQuranEditorialCoverage(ctx context.Context) ([]entity.QuranEditorialCoverage, error)
	}

	// PersonalRepo -.
	PersonalRepo interface {
		GetProgress(ctx context.Context, userID string, bookID int) (entity.ReadingProgress, error)
		SaveProgress(ctx context.Context, progress entity.ReadingProgress) (entity.ReadingProgress, error)
		ListProgress(ctx context.Context, userID, lang string, limit, offset uint64) ([]entity.ContinueReadingEntry, int, error)
		GetQuranProgress(ctx context.Context, userID string) (entity.QuranReadingProgress, error)
		GetQuranSurahProgress(ctx context.Context, userID string, surahID int) (entity.QuranReadingProgress, error)
		ListQuranSurahProgress(ctx context.Context, userID string) ([]entity.QuranReadingProgress, error)
		SaveQuranProgress(ctx context.Context, progress entity.QuranReadingProgress) (entity.QuranReadingProgress, error)
		ListSavedItems(ctx context.Context, userID string, filter SavedItemFilter) ([]entity.SavedItem, int, error)
		UpsertSavedItem(ctx context.Context, item entity.SavedItem) (entity.SavedItem, bool, error)
		UpdateSavedItem(ctx context.Context, userID, savedItemID string, patch entity.SavedItemPatch) (entity.SavedItem, error)
		DeleteSavedItem(ctx context.Context, userID, savedItemID string) error
		ListSavedItemTags(ctx context.Context, userID string) ([]string, error)
		StartKhatamCycle(ctx context.Context, cycle entity.QuranKhatamCycle) (entity.QuranKhatamCycle, error)
		GetActiveKhatamCycle(ctx context.Context, userID string) (entity.QuranKhatamCycle, error)
		MarkKhatamJuz(ctx context.Context, userID string, juzNumber int) (entity.QuranKhatamCycle, bool, error)
		UnmarkKhatamJuz(ctx context.Context, userID string, juzNumber int) (entity.QuranKhatamCycle, bool, error)
		CompleteKhatamCycle(ctx context.Context, userID string) (entity.QuranKhatamCycle, error)
		ListKhatamHistory(ctx context.Context, userID string, limit, offset uint64) ([]entity.QuranKhatamCycle, int, error)
		SyncSnapshot(ctx context.Context, userID string, since *time.Time) (entity.PersonalSyncSnapshot, error)
		GetReadingStreak(ctx context.Context, userID, today string) (entity.ReadingStreak, error)
		GetReadingActivity(ctx context.Context, userID, from, to string) (entity.ReadingActivitySummary, error)
	}

	// EditorialRepo -.
	EditorialRepo interface {
		ListBooks(ctx context.Context, filter EditorialBookFilter) ([]entity.Book, int, error)
		ListProductionCandidates(ctx context.Context, filter ProductionCandidateFilter) ([]entity.BookProductionCandidate, int, error)
		UpdatePublication(ctx context.Context, actorID string, publication entity.BookPublication) (entity.BookPublication, error)
		GetMetadataDraft(ctx context.Context, bookID int) (entity.BookMetadataEdit, error)
		SaveMetadataDraft(ctx context.Context, actorID string, edit entity.BookMetadataEdit, expected *time.Time, origin string) (entity.BookMetadataEdit, error)
		PublishMetadataDraft(ctx context.Context, actorID string, bookID int, expected *time.Time) (entity.BookMetadataEdit, error)
		GetPageEdit(ctx context.Context, bookID, pageID int) (entity.EditorialPageEdit, error)
		SavePageDraft(ctx context.Context, actorID string, edit entity.BookPageEdit, expected *time.Time, origin string) (entity.BookPageEdit, error)
		PublishPageDraft(ctx context.Context, actorID string, bookID, pageID int, expected *time.Time) (entity.BookPageEdit, error)
		GetHeadingDraft(ctx context.Context, bookID, headingID int) (entity.BookHeadingEdit, error)
		SaveHeadingDraft(ctx context.Context, actorID string, edit entity.BookHeadingEdit, expected *time.Time, origin string) (entity.BookHeadingEdit, error)
		PublishHeadingDraft(ctx context.Context, actorID string, bookID, headingID int, expected *time.Time) (entity.BookHeadingEdit, error)
		ListSourceEditRevisions(ctx context.Context, filter SourceEditRevisionFilter) ([]entity.BookSourceEditRevision, int, error)
		GetSourceEditRevision(ctx context.Context, revisionID string) (entity.BookSourceEditRevision, error)
		AddCollectionItem(ctx context.Context, actorID, slug string, bookID int, sortOrder *int) (entity.BookCollectionItem, error)
		ListTranslationFeedbacks(ctx context.Context, filter TranslationFeedbackFilter) ([]entity.EditorialTranslationFeedback, int, error)
		TranslationFeedbackSummary(ctx context.Context, filter TranslationFeedbackFilter) (entity.EditorialTranslationFeedbackSummary, error)
		ListMissingReaderAssets(ctx context.Context, filter MissingReaderAssetFilter) (entity.EditorialMissingReaderAssets, error)
		ResolveTranslationFeedback(ctx context.Context, actorID, feedbackID string, note *string) (entity.EditorialTranslationFeedback, error)
		ReopenTranslationFeedback(ctx context.Context, actorID, feedbackID string) (entity.EditorialTranslationFeedback, error)
		CreateProductionProject(ctx context.Context, actorID string, project entity.BookProductionProject) (entity.BookProductionProject, error)
		ListProductionProjects(ctx context.Context, filter ProductionProjectFilter) ([]entity.BookProductionProject, int, error)
		GetProductionProject(ctx context.Context, projectID string) (entity.BookProductionProject, error)
		ProductionWorkspace(ctx context.Context, projectID string) (entity.BookProductionWorkspace, error)
		ListProductionEvents(ctx context.Context, projectID string, limit, offset uint64) ([]entity.BookProductionEvent, int, error)
		ListProductionEventsGlobal(ctx context.Context, lang string, limit, offset uint64) ([]entity.BookProductionEvent, int, error)
		ListProductionDraftRevisions(ctx context.Context, filter ProductionDraftRevisionFilter) ([]entity.BookProductionDraftRevision, int, error)
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

	// QuranEditorialRepo is the Q-1 companion to EditorialRepo. Keeping the
	// workflow narrow prevents every kitab-only integration from becoming a
	// Quran writer while the concrete EditorialRepo remains the single
	// persistent implementation for both domains.
	QuranEditorialRepo interface {
		GetSurahEditorialWorkspace(ctx context.Context, surahID int, lang string) (entity.QuranSurahEditorialWorkspace, error)
		SaveSurahEditorialDraft(ctx context.Context, actorID string, edit entity.QuranSurahEditorialEdit, expected *time.Time, origin string) (entity.QuranSurahEditorialWorkspace, error)
		PublishSurahEditorialDraft(ctx context.Context, actorID string, surahID int, lang string, expected *time.Time, origin string) (entity.QuranSurahEditorialWorkspace, error)
		RestoreSurahEditorialRevision(ctx context.Context, actorID string, surahID int, lang, revisionID string, expected *time.Time) (entity.QuranSurahEditorialWorkspace, error)
		GetAyahEditorialWorkspace(ctx context.Context, ayahKey, lang string) (entity.QuranAyahEditorialWorkspace, error)
		SaveAyahEditorialDraft(ctx context.Context, actorID string, edit entity.QuranAyahEditorialEdit, expected *time.Time, origin string) (entity.QuranAyahEditorialWorkspace, error)
		PublishAyahEditorialDraft(ctx context.Context, actorID, ayahKey, lang string, expected *time.Time, origin string) (entity.QuranAyahEditorialWorkspace, error)
		RestoreAyahEditorialRevision(ctx context.Context, actorID, ayahKey, lang, revisionID string, expected *time.Time) (entity.QuranAyahEditorialWorkspace, error)
		ListQuranEditorialRevisions(ctx context.Context, filter QuranEditorialRevisionFilter) ([]entity.QuranEditorialRevision, int, error)
	}

	// LicenseRepo owns the B-4 kitab license audit queue and the atomic,
	// actor-attributed status transition. It stays separate from EditorialRepo
	// so existing editorial integrations do not accidentally become license
	// writers merely by implementing the broader editing contract.
	LicenseRepo interface {
		ListBookLicenseAudit(
			ctx context.Context,
			filter LicenseAuditFilter,
		) ([]entity.BookLicenseAuditItem, int, entity.BookLicenseAuditCounts, error)
		GetBookLicense(ctx context.Context, bookID int) (entity.BookLicense, error)
		UpdateBookLicense(
			ctx context.Context,
			actorID string,
			update entity.BookLicenseUpdate,
			expectedUpdatedAt *time.Time,
		) (entity.BookLicense, error)
	}

	QuranSourceLicenseRepo interface {
		ListQuranSourceLicenses(
			ctx context.Context,
			sourceKind, status string,
			limit, offset uint64,
		) ([]entity.QuranSourceLicense, int, error)
		GetQuranSourceLicense(
			ctx context.Context,
			sourceKind, sourceID string,
		) (entity.QuranSourceLicense, error)
		UpdateQuranSourceLicense(
			ctx context.Context,
			actorID string,
			update entity.QuranSourceLicenseUpdate,
			expectedUpdatedAt *time.Time,
		) (entity.QuranSourceLicense, error)
	}

	// TaskFilter -.
	UserFilter struct {
		Query         string
		Role          string
		EmailVerified *bool
		Limit         uint64
		Offset        uint64
	}

	UserActivityFilter struct {
		UserID string
		Limit  uint64
		Offset uint64
	}

	// LicenseAuditFilter selects one status or the special unresolved/all
	// queue views. Pagination is always clamped by the usecase.
	LicenseAuditFilter struct {
		Status string
		Limit  uint64
		Offset uint64
	}

	// AuthCleanupPolicy bounds one auth-data cleanup run. AuditRetention 0
	// keeps audit logs forever.
	AuthCleanupPolicy struct {
		Now              time.Time
		TokenRetention   time.Duration
		SessionRetention time.Duration
		AuditRetention   time.Duration
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
		Lang       string
		CategoryID *int
		AuthorID   *int
		HasContent *bool
		Limit      uint64
		Offset     uint64
	}

	// AuthorFilter -.
	AuthorFilter struct {
		Query  string
		Lang   string
		Limit  uint64
		Offset uint64
	}

	// PageFilter -.
	PageFilter struct {
		Limit  uint64
		Offset uint64
	}

	// HeadingFilter -.
	HeadingFilter struct {
		Query  string
		Limit  uint64
		Offset uint64
	}

	// SavedItemFilter -.
	SavedItemFilter struct {
		ItemType string
		BookID   *int
		SurahID  *int
		Tag      string
		Limit    uint64
		Offset   uint64
	}

	// EmailTemplateFilter -.
	EmailTemplateFilter struct {
		Query           string
		Category        string
		IncludeArchived bool
		Limit           uint64
		Offset          uint64
	}

	// EmailMessageFilter -.
	EmailMessageFilter struct {
		Category string
		Status   string
		Email    string
		Limit    uint64
		Offset   uint64
	}

	// EmailSuppressionFilter -.
	EmailSuppressionFilter struct {
		Email  string
		Scope  string
		Limit  uint64
		Offset uint64
	}

	// EmailDeliveryEventFilter -.
	EmailDeliveryEventFilter struct {
		Provider            string
		EventType           string
		Email               string
		MessageID           string
		CampaignID          string
		CampaignRecipientID string
		Limit               uint64
		Offset              uint64
	}

	// EmailCampaignFilter -.
	EmailCampaignFilter struct {
		Status string
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

	// ProductionCandidateFilter -.
	ProductionCandidateFilter struct {
		Lang       string
		Query      string
		CategoryID *int
		AuthorID   *int
		HasContent *bool
		Unstarted  bool
		Limit      uint64
		Offset     uint64
	}

	// ProductionProjectFilter -.
	ProductionProjectFilter struct {
		BookID            *int
		Lang              string
		WorkflowStatus    string
		PublicationStatus string
		ReadyToPublish    bool
		NeedsWork         bool
		Limit             uint64
		Offset            uint64
	}

	// ProductionDraftRevisionFilter -.
	ProductionDraftRevisionFilter struct {
		ProjectID string
		AssetType string
		HeadingID *int
		Limit     uint64
		Offset    uint64
	}

	// SourceEditRevisionFilter -.
	SourceEditRevisionFilter struct {
		BookID    int
		AssetType string
		PageID    *int
		HeadingID *int
		Limit     uint64
		Offset    uint64
	}

	// QuranEditorialRevisionFilter identifies one surah/ayah editorial
	// resource. Versioning is per resource and language, across both statuses.
	QuranEditorialRevisionFilter struct {
		AssetType  string
		SurahID    int
		AyahNumber *int
		Lang       string
		Limit      uint64
		Offset     uint64
	}

	// TranslationFeedbackFilter -.
	TranslationFeedbackFilter struct {
		BookID    *int
		HeadingID *int
		Lang      string
		Vote      string
		Status    string
		Limit     uint64
		Offset    uint64
	}

	// MissingReaderAssetFilter -.
	MissingReaderAssetFilter struct {
		TargetLangs []string
		AssetType   string
		BookID      *int
		Limit       uint64
		Offset      uint64
	}

	// MissingQuranAssetFilter -.
	MissingQuranAssetFilter struct {
		TargetLangs []string
		AssetType   string
		SurahID     *int
		Limit       uint64
		Offset      uint64
	}

	// QuranSearchFilter -.
	QuranSearchFilter struct {
		Query             string
		Lang              string
		TranslationSource string
		Limit             uint64
		Offset            uint64
	}

	// QuranFeedFilter selects current indexable sitemap records for incremental
	// consumers. Since is inclusive to prevent missed equal-timestamp records.
	QuranFeedFilter struct {
		Since    *time.Time
		Lang     string
		PageType string
		Limit    uint64
		Offset   uint64
	}

	// QuranBookReferenceFilter -.
	QuranBookReferenceFilter struct {
		BookID            int
		HeadingID         *int
		Lang              string
		TranslationSource string
		Status            string
		Limit             uint64
		Offset            uint64
	}

	// CitableUnitRepo is the persistence seam of the shared Citable Unit
	// registry (phase-1b B-1). ApplyReconcile is the ONLY write path into
	// citable_units / citable_unit_lineage: it runs inside the guarded
	// transaction (SET LOCAL surau.registry_writer) with a per-book advisory
	// lock, and rejects plans built on a stale fingerprint with
	// entity.ErrUnitReconcileConflict.
	CitableUnitRepo interface {
		LoadBookSource(ctx context.Context, bookID int) (entity.BookUnitSource, error)
		Snapshot(ctx context.Context, bookID int) (entity.UnitRegistrySnapshot, error)
		ApplyReconcile(ctx context.Context, plan *entity.UnitReconcilePlan) error
		BookDerivedAt(ctx context.Context, bookID int) (*time.Time, error)
		ResolveUnit(ctx context.Context, unitID string) (entity.UnitResolution, error)
		AuditCounts(ctx context.Context) (entity.CitableAuditReport, error)
		ListActiveUnitsForHashCheck(ctx context.Context) ([]entity.CitableUnit, error)
	}

	// CitableUnitCatalogTx is the one-book transactional surface used only by
	// K-1's catalog runner. Implementations keep all methods on one
	// repeatable-read transaction and one per-book advisory lock.
	CitableUnitCatalogTx interface {
		LoadBookSource(ctx context.Context, bookID int) (entity.BookUnitSource, error)
		Snapshot(ctx context.Context, bookID int) (entity.UnitRegistrySnapshot, error)
		ApplyReconcile(ctx context.Context, plan *entity.UnitReconcilePlan) error
		BindKnowledgeMentions(ctx context.Context, bookID int) error
		SourceFingerprint(ctx context.Context, bookID int) ([32]byte, error)
		RegistryChecksum(ctx context.Context, bookID int) ([32]byte, error)
		CompleteQueueItem(ctx context.Context, jobName string, bookID int, source, checksum [32]byte) error
	}

	CitableUnitCatalogRepo interface {
		WithCatalogTransaction(
			ctx context.Context,
			bookID int,
			fn func(CitableUnitCatalogTx) error,
		) error
	}

	// QuranCitableUnitRepo extends the same registry persistence service with a
	// Quran-shaped adapter. It does not own a second registry: all writes still
	// land in citable_units/citable_unit_lineage under the B-1 guard.
	QuranCitableUnitRepo interface {
		LoadQuranSource(ctx context.Context, surahID int) (entity.QuranUnitSource, error)
		QuranSnapshot(ctx context.Context, surahID int) (entity.QuranUnitRegistrySnapshot, error)
		ApplyQuranReconcile(ctx context.Context, plan *entity.QuranUnitReconcilePlan) error
		ListQuranSurahsForReconcile(ctx context.Context, staleOnly bool, limit int) ([]int, error)
	}

	// AnchorRepo is the indexed, visibility-filtered read seam for the public
	// B-2 Anchor resolver. Legacy locators intentionally remain first-class
	// lookups: they are permanent aliases, not a deprecation path.
	AnchorRepo interface {
		ResolveQuran(ctx context.Context, ayahKey string) (entity.AnchorLookupResult, error)
		ResolveQuranSurah(ctx context.Context, surahID int) (entity.AnchorLookupResult, error)
		ResolveWork(ctx context.Context, bookID int) (entity.AnchorLookupResult, error)
		ResolveHeading(ctx context.Context, bookID, headingID int) (entity.AnchorLookupResult, error)
		ResolvePage(ctx context.Context, bookID, pageID int) (entity.AnchorLookupResult, error)
		ResolveCanonicalUnit(ctx context.Context, canonicalAnchor string) (entity.AnchorLookupResult, error)
	}

	// QuranLocatorAnchorRepo is the additive Q-2 adapter used by the existing
	// B-2 resolver for juz/hizb/page aliases. Results are two bounded ayah
	// lookups; aliases never create a parallel canonical registry.
	QuranLocatorAnchorRepo interface {
		ResolveQuranLocator(
			ctx context.Context,
			kind string,
			number int,
		) (entity.AnchorLookupResult, entity.AnchorLookupResult, error)
	}

	// CrossReferenceRepo is the guarded persistence seam for B-3. All writes
	// run inside transactions which set the cross-reference service GUC; direct
	// table DML is rejected by the database guard.
	CrossReferenceRepo interface {
		Create(ctx context.Context, ref entity.CrossReference) (entity.CrossReference, error)
		UpsertDerived(
			ctx context.Context,
			ref entity.CrossReference,
			bridge *entity.QuranCrossReferenceBridge,
		) (entity.CrossReference, error)
		Get(ctx context.Context, id string) (entity.CrossReference, error)
		Review(
			ctx context.Context,
			id, status, reviewerID string,
			expectedUpdatedAt *time.Time,
		) (entity.CrossReference, error)
		List(ctx context.Context, filter CrossReferenceFilter) (entity.CrossReferenceList, error)
		FreezeLegacyQuranWrites(ctx context.Context) error
		UnfreezeLegacyQuranWrites(ctx context.Context) error
	}

	// CrossReferenceFilter is shared by public and editorial list surfaces.
	// PublicOnly forces approved visibility in persistence; callers cannot
	// broaden it by also setting ReviewStatus.
	CrossReferenceFilter struct {
		Anchor       string
		Direction    string
		Kind         string
		Method       string
		ReviewStatus string
		PublicOnly   bool
		Limit        uint64
		Offset       uint64
	}
)
