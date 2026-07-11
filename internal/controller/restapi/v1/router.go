package v1

import (
	"strings"
	"time"

	"github.com/alfariesh/surau-backend/internal/controller/restapi/middleware"
	"github.com/alfariesh/surau-backend/internal/policy"
	"github.com/alfariesh/surau-backend/internal/usecase"
	"github.com/alfariesh/surau-backend/pkg/jwt"
	"github.com/alfariesh/surau-backend/pkg/logger"
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/limiter"
)

// editorialSavesPerMinute caps per-user editorial draft writes (autosave bursts).
const editorialSavesPerMinute = 120

// personalWritesPerMinute caps per-user personal writes (progress autosave,
// saved items, khatam) so reader clients cannot hammer the database.
const personalWritesPerMinute = 240

// sessionRequestsPerMinute caps per-user session listing/revocation, the only
// protected auth endpoints without a DB-backed limit in the use case.
const sessionRequestsPerMinute = 30

// quranSearchesPerMinute caps per-IP Quran search requests. Search is public and
// expensive (trigram + LATERAL joins + COUNT(*) OVER()), so query-variety must not
// churn the cache and load the DB unbounded.
const quranSearchesPerMinute = 60

// bookRAGRequestsPerMinute bounds the expensive per-book RAG ask endpoint.
const bookRAGRequestsPerMinute = 20

// translationFeedbackPerMinute bounds public feedback submissions.
const translationFeedbackPerMinute = 30

// NewRoutes -.
func NewRoutes(
	apiV1Group fiber.Router,
	reader usecase.Reader,
	bookRAG usecase.BookRAG,
	quran usecase.Quran,
	anchor usecase.AnchorResolver,
	crossReference usecase.CrossReference,
	unitRegistry usecase.UnitRegistry,
	u usecase.User,
	personal usecase.Personal,
	editorial usecase.Editorial,
	email usecase.EmailAdmin,
	emailWebhookSecret string,
	jwtManager *jwt.Manager,
	l logger.Interface,
) {
	var quranEditorial usecase.QuranEditorial
	if implementation, ok := editorial.(usecase.QuranEditorial); ok {
		quranEditorial = implementation
	}

	var licenseAudit usecase.LicenseAudit
	if implementation, ok := editorial.(usecase.LicenseAudit); ok {
		licenseAudit = implementation
	}

	r := &V1{
		reader:             reader,
		bookRAG:            bookRAG,
		quran:              quran,
		anchor:             anchor,
		crossReference:     crossReference,
		unitRegistry:       unitRegistry,
		u:                  u,
		personal:           personal,
		editorial:          editorial,
		quranEditorial:     quranEditorial,
		licenseAudit:       licenseAudit,
		email:              email,
		emailWebhookSecret: strings.TrimSpace(emailWebhookSecret),
		l:                  l,
		v:                  validator.New(validator.WithRequiredStructEnabled()),
	}

	// Public routes
	authGroup := apiV1Group.Group("/auth")
	{
		authGroup.Post("/register", r.register)
		authGroup.Post("/login", r.login)
		authGroup.Post("/refresh", r.refreshToken)
		authGroup.Post("/logout", r.logout)
		authGroup.Post("/verify-email", r.verifyEmail)
		authGroup.Post("/resend-verification", r.resendVerification)
		authGroup.Post("/forgot-password", r.forgotPassword)
		authGroup.Post("/reset-password", r.resetPassword)
		// MFA (A-3): challenge-token endpoints — the mfa_token from login is
		// the proof of password, so these stay outside Bearer auth.
		authGroup.Post("/mfa/verify", r.mfaVerify)
		authGroup.Post("/mfa/reset/request", r.mfaResetRequest)
		authGroup.Post("/mfa/reset/confirm", r.mfaResetConfirm)
	}

	emailPublicGroup := apiV1Group.Group("/email")
	{
		emailPublicGroup.Get("/unsubscribe", r.emailUnsubscribe)
		emailPublicGroup.Post("/unsubscribe", r.emailUnsubscribe)
		emailPublicGroup.Post("/webhooks/cloudflare/bounces", r.emailCloudflareBounceWebhook)
	}

	anchorGroup := apiV1Group.Group("/anchors", middleware.PublicRevalidate())
	{
		anchorGroup.Get("/resolve", r.resolveAnchor)
	}

	crossReferenceGroup := apiV1Group.Group("/cross-references", middleware.PublicRevalidate())
	{
		crossReferenceGroup.Get("/", r.listCrossReferences)
	}

	// Public reader routes
	bookGroup := apiV1Group.Group("/books", middleware.PublicRevalidate())
	{
		bookGroup.Get("/", r.listBooks)
		bookGroup.Get("/:book_id", r.getBook)
		bookGroup.Post("/:book_id/rag", limiter.New(limiter.Config{
			Max:          bookRAGRequestsPerMinute,
			Expiration:   time.Minute,
			LimitReached: limiterLimitReached,
		}), r.askBookRAG)
		bookGroup.Get("/:book_id/pages", r.listBookPages)
		bookGroup.Get("/:book_id/pages/:page_id", r.getBookPage)
		bookGroup.Get("/:book_id/headings", r.listBookHeadings)
		bookGroup.Get("/:book_id/sections/:heading_id", r.getBookSection)
		bookGroup.Get("/:book_id/toc", r.listBookTOC)
		bookGroup.Get("/:book_id/toc/:heading_id/read", r.readBookTOCSection)
		bookGroup.Get("/:book_id/toc/:heading_id/playlist", r.getBookTOCPlaylist)
		bookGroup.Get("/:book_id/quran-references", r.listBookQuranReferences)
		bookGroup.Post("/:book_id/toc/:heading_id/translation-feedback", limiter.New(limiter.Config{
			Max:          translationFeedbackPerMinute,
			Expiration:   time.Minute,
			LimitReached: limiterLimitReached,
		}), r.createTranslationFeedback)
	}

	apiV1Group.Get("/categories", middleware.PublicCache(), r.listCategories)
	apiV1Group.Get("/authors", middleware.PublicCache(), r.listAuthors)

	// Per-IP limiter for the expensive public search route (default key = client IP).
	quranSearchLimiter := limiter.New(limiter.Config{
		Max:          quranSearchesPerMinute,
		Expiration:   time.Minute,
		LimitReached: limiterLimitReached,
	})

	// Search is dynamic (per-query) and must not advertise public caching;
	// the edge worker bypasses q-params for the same reason (F1-D).
	quranGroup := apiV1Group.Group("/quran", middleware.PublicCache(
		middleware.ExcludePath("/v1/quran/search"),
	))
	{
		quranGroup.Get("/recitations", r.listQuranRecitations)
		quranGroup.Get("/translation-sources", r.listQuranTranslationSources)
		quranGroup.Get("/juz", r.listQuranJuz)
		quranGroup.Get("/juz/:juz_number/ayahs", r.listQuranJuzAyahs)
		quranGroup.Get("/hizbs", r.listQuranHizbs)
		quranGroup.Get("/hizbs/:hizb_number/ayahs", r.listQuranHizbAyahs)
		quranGroup.Get("/surahs", r.listQuranSurahs)
		quranGroup.Get("/surahs/:surah_id", r.getQuranSurah)
		quranGroup.Get("/surahs/:surah_id/audio", r.getQuranSurahAudio)
		quranGroup.Get("/surahs/:surah_id/ayahs", r.listQuranSurahAyahs)
		quranGroup.Get("/ayahs/:ayah_key", r.getQuranAyah)
		quranGroup.Get("/search", quranSearchLimiter, r.searchQuran)
	}

	// Protected routes. Auth is attached per-subtree, NOT on the bare /v1
	// group: an empty-prefix Use would claim the whole /v1 namespace and
	// answer unknown paths with 401 before the app-level 404 catch-all can
	// emit the frozen error envelope (the F1-D contract that
	// TestUnknownRouteReturnsErrorEnvelope guards).
	authRequired := middleware.Auth(jwtManager, u)
	protected := apiV1Group.Group("")

	// One shared per-user budget for session listing/revocation; in-memory
	// store is fine (single instance, prefork off) like the other limiters.
	sessionLimiter := newSessionLimiter()

	protectedAuthGroup := protected.Group("/auth", authRequired)
	{
		protectedAuthGroup.Get("/introspect", r.introspect)
		protectedAuthGroup.Post("/change-password", r.changePassword)
		protectedAuthGroup.Post("/change-email/request", r.requestEmailChange)
		protectedAuthGroup.Post("/change-email/verify", r.verifyEmailChange)
		protectedAuthGroup.Post("/delete-account", r.deleteAccount)
		protectedAuthGroup.Post("/logout-all", r.logoutAll)
		protectedAuthGroup.Get("/sessions", sessionLimiter, r.listSessions)
		protectedAuthGroup.Delete("/sessions/:id", sessionLimiter, r.revokeSession)
		// MFA (A-3): session-scoped enrollment, step-up, and management.
		protectedAuthGroup.Post("/mfa/enroll", r.mfaEnroll)
		protectedAuthGroup.Post("/mfa/enroll/confirm", r.mfaEnrollConfirm)
		protectedAuthGroup.Post("/mfa/step-up", r.mfaStepUp)
		protectedAuthGroup.Post("/mfa/disable", r.mfaDisable)
		protectedAuthGroup.Post("/mfa/recovery-codes", r.mfaRecoveryCodes)
		protectedAuthGroup.Get("/mfa/status", r.mfaStatus)
	}

	userGroup := protected.Group("/user", authRequired)
	{
		userGroup.Get("/profile", r.profile)
		userGroup.Patch("/profile", r.updateProfile)
		userGroup.Patch("/onboarding", r.updateOnboarding)
		userGroup.Patch("/preferences", r.updatePreferences)
		userGroup.Get("/email-preferences", r.emailPreferences)
		userGroup.Patch("/email-preferences", r.updateEmailPreferences)
	}

	// One shared per-user budget across all personal writes. GETs stay
	// uncounted. In-memory store is fine: single instance, prefork off.
	personalWriteLimiter := limiter.New(limiter.Config{
		Max:          personalWritesPerMinute,
		Expiration:   time.Minute,
		LimitReached: limiterLimitReached,
		KeyGenerator: func(ctx *fiber.Ctx) string {
			if userID, ok := ctx.Locals("userID").(string); ok && userID != "" {
				return userID
			}

			return ctx.IP()
		},
		Next: func(ctx *fiber.Ctx) bool {
			switch ctx.Method() {
			case fiber.MethodPut, fiber.MethodPost, fiber.MethodPatch, fiber.MethodDelete:
				return false
			}

			return true
		},
	})

	meGroup := protected.Group("/me", authRequired, personalWriteLimiter)
	{
		meGroup.Get("/sync", r.syncPersonalData)
		meGroup.Get("/activity", r.getReadingActivity)
		meGroup.Get("/activity/streak", r.getReadingStreak)
		meGroup.Get("/progress", r.listProgress)
		meGroup.Post("/progress/batch", r.batchSaveProgress)
		meGroup.Get("/progress/:book_id", r.getProgress)
		meGroup.Put("/progress/:book_id", r.saveProgress)
		meGroup.Put("/progress/:book_id/toc/:heading_id", r.saveTOCProgress)
		meGroup.Get("/quran/progress", r.getQuranProgress)
		meGroup.Put("/quran/progress", r.saveQuranProgress)
		meGroup.Get("/quran/progress/surahs", r.listQuranSurahProgress)
		meGroup.Get("/quran/progress/surahs/:surah_id", r.getQuranSurahProgress)
		meGroup.Post("/quran/khatam", r.startKhatamCycle)
		meGroup.Get("/quran/khatam", r.getActiveKhatamCycle)
		meGroup.Get("/quran/khatam/history", r.listKhatamHistory)
		meGroup.Post("/quran/khatam/complete", r.completeKhatamCycle)
		meGroup.Put("/quran/khatam/juz/:juz_number", r.markKhatamJuz)
		meGroup.Delete("/quran/khatam/juz/:juz_number", r.unmarkKhatamJuz)
		meGroup.Get("/saved-items", r.listSavedItems)
		meGroup.Post("/saved-items", r.upsertSavedItem)
		meGroup.Get("/saved-items/tags", r.listSavedItemTags)
		meGroup.Patch("/saved-items/:id", r.updateSavedItem)
		meGroup.Delete("/saved-items/:id", r.deleteSavedItem)
	}

	// One shared per-user budget across all editorial draft saves so autosave
	// bursts cannot monopolize the database. In-memory store is fine: single
	// instance, prefork off.
	editorialSaveLimiter := limiter.New(limiter.Config{
		Max:          editorialSavesPerMinute,
		Expiration:   time.Minute,
		LimitReached: limiterLimitReached,
		KeyGenerator: func(ctx *fiber.Ctx) string {
			if userID, ok := ctx.Locals("userID").(string); ok && userID != "" {
				return userID
			}

			return ctx.IP()
		},
		Next: func(ctx *fiber.Ctx) bool {
			return ctx.Method() != fiber.MethodPut
		},
	})

	editorialGroup := protected.Group("/editorial", authRequired, editorialSaveLimiter)
	{
		editorialReviewGroup := editorialGroup.Group(
			"",
			middleware.RequireCapability(u, policy.CapReviewEditorial),
		)
		editorialReviewGroup.Get("/books", r.editorialListBooks)
		editorialReviewGroup.Get("/license-audit", r.editorialLicenseAudit)
		editorialReviewGroup.Get("/books/:book_id/license", r.editorialGetBookLicense)
		editorialReviewGroup.Get("/cross-references", r.editorialListCrossReferences)
		editorialReviewGroup.Get("/citable-units/:id", r.editorialGetCitableUnit)
		editorialReviewGroup.Post("/cross-references", r.editorialCreateCrossReference)
		editorialReviewGroup.Get("/cross-references/:id", r.editorialGetCrossReference)
		editorialReviewGroup.Patch("/cross-references/:id/review", r.editorialReviewCrossReference)
		editorialReviewGroup.Get("/reader/missing-assets", r.editorialMissingReaderAssets)
		editorialReviewGroup.Get("/quran/missing-assets", r.editorialMissingQuranAssets)
		editorialReviewGroup.Get("/quran/surahs/:surah_id", r.editorialQuranSurahWorkspace)
		editorialReviewGroup.Put("/quran/surahs/:surah_id/draft", r.editorialSaveQuranSurahDraft)
		editorialReviewGroup.Get("/quran/surahs/:surah_id/draft-revisions", r.editorialListQuranSurahRevisions)
		editorialReviewGroup.Post("/quran/surahs/:surah_id/draft-revisions/:revision_id/restore", r.editorialRestoreQuranSurahRevision)
		editorialReviewGroup.Get("/quran/ayahs/:ayah_key", r.editorialQuranAyahWorkspace)
		editorialReviewGroup.Put("/quran/ayahs/:ayah_key/draft", r.editorialSaveQuranAyahDraft)
		editorialReviewGroup.Get("/quran/ayahs/:ayah_key/draft-revisions", r.editorialListQuranAyahRevisions)
		editorialReviewGroup.Post("/quran/ayahs/:ayah_key/draft-revisions/:revision_id/restore", r.editorialRestoreQuranAyahRevision)
		editorialReviewGroup.Get("/translation-feedbacks", r.editorialListTranslationFeedbacks)
		editorialReviewGroup.Get("/translation-feedbacks/summary", r.editorialTranslationFeedbackSummary)
		editorialReviewGroup.Post("/translation-feedbacks/:id/resolve", r.editorialResolveTranslationFeedback)
		editorialReviewGroup.Post("/translation-feedbacks/:id/reopen", r.editorialReopenTranslationFeedback)
		editorialReviewGroup.Get("/production-dashboard", r.editorialProductionDashboard)
		editorialReviewGroup.Get("/production-activity", r.editorialGlobalProductionActivity)
		editorialReviewGroup.Get("/production-candidates", r.editorialListProductionCandidates)
		editorialReviewGroup.Post("/production-projects", r.editorialCreateProductionProject)
		editorialReviewGroup.Get("/production-projects", r.editorialListProductionProjects)
		editorialReviewGroup.Get("/production-projects/:id/workspace", r.editorialProductionWorkspace)
		editorialReviewGroup.Get("/production-projects/:id/activity", r.editorialProductionActivity)
		editorialReviewGroup.Get("/production-projects/:id/publish-check", r.editorialProductionPublishCheck)
		editorialReviewGroup.Get("/production-projects/:id/draft-revisions", r.editorialListProductionDraftRevisions)
		editorialReviewGroup.Post("/production-projects/:id/draft-revisions/:revision_id/restore", r.editorialRestoreProductionDraftRevision)
		editorialReviewGroup.Get("/production-projects/:id", r.editorialGetProductionProject)
		editorialReviewGroup.Patch("/production-projects/:id", r.editorialUpdateProductionProject)
		editorialReviewGroup.Get("/production-projects/:id/completeness", r.editorialProductionCompleteness)
		editorialReviewGroup.Get("/production-projects/:id/metadata-draft", r.editorialGetMetadataTranslationDraft)
		editorialReviewGroup.Put("/production-projects/:id/metadata-draft", r.editorialSaveMetadataTranslationDraft)
		editorialReviewGroup.Delete("/production-projects/:id/metadata-draft", r.editorialDeleteMetadataTranslationDraft)
		editorialReviewGroup.Get("/production-projects/:id/author-draft", r.editorialGetAuthorTranslationDraft)
		editorialReviewGroup.Put("/production-projects/:id/author-draft", r.editorialSaveAuthorTranslationDraft)
		editorialReviewGroup.Delete("/production-projects/:id/author-draft", r.editorialDeleteAuthorTranslationDraft)
		editorialReviewGroup.Get("/production-projects/:id/category-draft", r.editorialGetCategoryTranslationDraft)
		editorialReviewGroup.Put("/production-projects/:id/category-draft", r.editorialSaveCategoryTranslationDraft)
		editorialReviewGroup.Delete("/production-projects/:id/category-draft", r.editorialDeleteCategoryTranslationDraft)
		editorialReviewGroup.Get("/production-projects/:id/toc/:heading_id/translation-draft", r.editorialGetSectionTranslationDraft)
		editorialReviewGroup.Put("/production-projects/:id/toc/:heading_id/translation-draft", r.editorialSaveSectionTranslationDraft)
		editorialReviewGroup.Delete("/production-projects/:id/toc/:heading_id/translation-draft", r.editorialDeleteSectionTranslationDraft)
		editorialReviewGroup.Get("/production-projects/:id/toc/:heading_id/summary-draft", r.editorialGetHeadingSummaryDraft)
		editorialReviewGroup.Put("/production-projects/:id/toc/:heading_id/summary-draft", r.editorialSaveHeadingSummaryDraft)
		editorialReviewGroup.Delete("/production-projects/:id/toc/:heading_id/summary-draft", r.editorialDeleteHeadingSummaryDraft)
		editorialReviewGroup.Get("/production-projects/:id/toc/:heading_id/audio-draft", r.editorialGetSectionAudioDraft)
		editorialReviewGroup.Put("/production-projects/:id/toc/:heading_id/audio-draft", r.editorialSaveSectionAudioDraft)
		editorialReviewGroup.Delete("/production-projects/:id/toc/:heading_id/audio-draft", r.editorialDeleteSectionAudioDraft)
		editorialReviewGroup.Post("/production-projects/:id/review", r.editorialReviewProductionAsset)
		editorialReviewGroup.Get("/books/:book_id/metadata-draft", r.editorialGetMetadataDraft)
		editorialReviewGroup.Put("/books/:book_id/metadata-draft", r.editorialSaveMetadataDraft)
		editorialReviewGroup.Get("/books/:book_id/pages/:page_id", r.editorialGetPageEdit)
		editorialReviewGroup.Put("/books/:book_id/pages/:page_id/draft", r.editorialSavePageDraft)
		editorialReviewGroup.Get("/books/:book_id/pages/:page_id/draft-revisions", r.editorialListPageDraftRevisions)
		editorialReviewGroup.Post("/books/:book_id/pages/:page_id/draft-revisions/:revision_id/restore", r.editorialRestorePageDraftRevision)
		editorialReviewGroup.Get("/books/:book_id/headings/:heading_id/draft", r.editorialGetHeadingDraft)
		editorialReviewGroup.Put("/books/:book_id/headings/:heading_id/draft", r.editorialSaveHeadingDraft)

		// High-class destructive routes demand a FRESH second factor on top of
		// the role (A-3 step-up): publish/unpublish, final-asset deletion.
		editorialAdminGroup := editorialGroup.Group("",
			middleware.RequireCapability(u, policy.CapPublishProduction), middleware.RequireFreshMFA(u))
		editorialAdminGroup.Put("/books/:book_id/publication", r.editorialUpdatePublication)
		editorialAdminGroup.Patch("/books/:book_id/license", r.editorialUpdateBookLicense)
		editorialAdminGroup.Post("/books/:book_id/metadata-draft/publish", r.editorialPublishMetadataDraft)
		editorialAdminGroup.Post("/books/:book_id/pages/:page_id/publish", r.editorialPublishPageDraft)
		editorialAdminGroup.Post("/books/:book_id/headings/:heading_id/publish", r.editorialPublishHeadingDraft)
		editorialAdminGroup.Post("/quran/surahs/:surah_id/publish", r.editorialPublishQuranSurahDraft)
		editorialAdminGroup.Post("/quran/ayahs/:ayah_key/publish", r.editorialPublishQuranAyahDraft)
		editorialAdminGroup.Post("/collections/:slug/items", r.editorialAddCollectionItem)
		editorialAdminGroup.Post("/production-projects/:id/publish", r.editorialPublishProductionProject)
		editorialAdminGroup.Post("/production-projects/:id/unpublish", r.editorialUnpublishProductionProject)
		editorialAdminGroup.Delete("/production-projects/:id/final-assets/:asset_type", r.editorialDeleteFinalProductionAsset)
		editorialAdminGroup.Delete("/production-projects/:id/toc/:heading_id/final-assets/:asset_type", r.editorialDeleteFinalHeadingProductionAsset)
	}

	adminGroup := protected.Group("/admin", authRequired, middleware.RequireCapability(u, policy.CapManageUsers))
	{
		adminGroup.Get("/users", r.adminUsers)
		adminGroup.Get("/users/:id/activity", r.adminUserActivity)
		adminGroup.Get("/users/:id", r.adminUserDetail)
		// Role change is step-up gated (A-3): the rest of /admin stays read-only
		// or routine, so the gate is per-route, not group-wide.
		adminGroup.Patch("/users/role", middleware.RequireFreshMFA(u), r.adminSetUserRole)
		emailGroup := adminGroup.Group("/emails")
		{
			emailGroup.Get("/templates", r.adminEmailTemplates)
			emailGroup.Post("/templates", r.adminEmailCreateTemplate)
			emailGroup.Get("/templates/:id", r.adminEmailTemplate)
			emailGroup.Patch("/templates/:id", r.adminEmailUpdateTemplate)
			emailGroup.Delete("/templates/:id", r.adminEmailDeleteTemplate)
			emailGroup.Get("/templates/:id/versions", r.adminEmailTemplateVersions)
			emailGroup.Post("/templates/:id/versions", r.adminEmailCreateTemplateVersion)
			emailGroup.Patch("/versions/:id", r.adminEmailUpdateTemplateVersion)
			emailGroup.Post("/versions/:id/publish", r.adminEmailPublishTemplateVersion)
			emailGroup.Post("/templates/:id/preview", r.adminEmailPreviewTemplate)
			emailGroup.Post("/templates/:id/test-send", r.adminEmailTestSendTemplate)
			emailGroup.Get("/events/:key", r.adminEmailEventSetting)
			emailGroup.Patch("/events/:key", r.adminEmailUpdateEventSetting)
			emailGroup.Get("/messages", r.adminEmailMessages)
			emailGroup.Get("/messages/:id/delivery-events", r.adminEmailMessageDeliveryEvents)
			emailGroup.Post("/messages/:id/resend", r.adminEmailResendMessage)
			emailGroup.Get("/delivery-events", r.adminEmailDeliveryEvents)
			emailGroup.Get("/suppressions", r.adminEmailSuppressions)
			emailGroup.Post("/suppressions", r.adminEmailCreateSuppression)
			emailGroup.Delete("/suppressions/:id", r.adminEmailDeleteSuppression)
			emailGroup.Get("/campaigns", r.adminEmailCampaigns)
			emailGroup.Post("/campaigns", r.adminEmailCreateCampaign)
			emailGroup.Get("/campaigns/:id/delivery-event-summary", r.adminEmailCampaignDeliveryEventSummary)
			emailGroup.Get("/campaigns/:id", r.adminEmailCampaign)
			emailGroup.Patch("/campaigns/:id", r.adminEmailUpdateCampaign)
			emailGroup.Get("/campaign-recipients/:id/delivery-events", r.adminEmailCampaignRecipientDeliveryEvents)
			emailGroup.Post("/campaigns/:id/preview-audience", r.adminEmailPreviewAudience)
			emailGroup.Post("/campaigns/:id/test-send", r.adminEmailTestSendCampaign)
			emailGroup.Post("/campaigns/:id/schedule", r.adminEmailScheduleCampaign)
			emailGroup.Post("/campaigns/:id/send-now", r.adminEmailSendCampaignNow)
			emailGroup.Post("/campaigns/:id/retry-failed", r.adminEmailRetryFailedCampaign)
			emailGroup.Post("/campaigns/:id/cancel", r.adminEmailCancelCampaign)
		}
	}
}

// newSessionLimiter caps per-user session listing/revocation requests; the
// key falls back to the client IP when no authenticated user is present.
func newSessionLimiter() fiber.Handler {
	return limiter.New(limiter.Config{
		Max:          sessionRequestsPerMinute,
		Expiration:   time.Minute,
		LimitReached: limiterLimitReached,
		KeyGenerator: func(ctx *fiber.Ctx) string {
			if userID, ok := ctx.Locals("userID").(string); ok && userID != "" {
				return userID
			}

			return ctx.IP()
		},
	})
}
