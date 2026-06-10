package v1

import (
	"strings"
	"time"

	"github.com/evrone/go-clean-template/internal/controller/restapi/middleware"
	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/evrone/go-clean-template/internal/usecase"
	"github.com/evrone/go-clean-template/pkg/jwt"
	"github.com/evrone/go-clean-template/pkg/logger"
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/limiter"
)

// NewRoutes -.
func NewRoutes(
	apiV1Group fiber.Router,
	reader usecase.Reader,
	bookRAG usecase.BookRAG,
	quran usecase.Quran,
	u usecase.User,
	personal usecase.Personal,
	editorial usecase.Editorial,
	email usecase.EmailAdmin,
	emailWebhookSecret string,
	jwtManager *jwt.Manager,
	l logger.Interface,
) {
	r := &V1{
		reader:             reader,
		bookRAG:            bookRAG,
		quran:              quran,
		u:                  u,
		personal:           personal,
		editorial:          editorial,
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
	}

	emailPublicGroup := apiV1Group.Group("/email")
	{
		emailPublicGroup.Get("/unsubscribe", r.emailUnsubscribe)
		emailPublicGroup.Post("/unsubscribe", r.emailUnsubscribe)
		emailPublicGroup.Post("/webhooks/cloudflare/bounces", r.emailCloudflareBounceWebhook)
	}

	// Public reader routes
	bookGroup := apiV1Group.Group("/books", middleware.PublicCache())
	{
		bookGroup.Get("/", r.listBooks)
		bookGroup.Get("/:book_id", r.getBook)
		bookGroup.Post("/:book_id/rag", limiter.New(limiter.Config{
			Max:        20,
			Expiration: time.Minute,
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
			Max:        30,
			Expiration: time.Minute,
		}), r.createTranslationFeedback)
	}

	apiV1Group.Get("/categories", middleware.PublicCache(), r.listCategories)
	apiV1Group.Get("/authors", middleware.PublicCache(), r.listAuthors)

	quranGroup := apiV1Group.Group("/quran", middleware.PublicCache())
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
		quranGroup.Get("/search", r.searchQuran)
	}

	// Protected routes
	protected := apiV1Group.Group("", middleware.Auth(jwtManager, u))

	protectedAuthGroup := protected.Group("/auth")
	{
		protectedAuthGroup.Post("/change-password", r.changePassword)
		protectedAuthGroup.Post("/change-email/request", r.requestEmailChange)
		protectedAuthGroup.Post("/change-email/verify", r.verifyEmailChange)
		protectedAuthGroup.Post("/delete-account", r.deleteAccount)
		protectedAuthGroup.Post("/logout-all", r.logoutAll)
	}

	userGroup := protected.Group("/user")
	{
		userGroup.Get("/profile", r.profile)
		userGroup.Patch("/profile", r.updateProfile)
		userGroup.Patch("/onboarding", r.updateOnboarding)
		userGroup.Patch("/preferences", r.updatePreferences)
		userGroup.Get("/email-preferences", r.emailPreferences)
		userGroup.Patch("/email-preferences", r.updateEmailPreferences)
	}

	meGroup := protected.Group("/me")
	{
		meGroup.Get("/progress/:book_id", r.getProgress)
		meGroup.Put("/progress/:book_id", r.saveProgress)
		meGroup.Put("/progress/:book_id/toc/:heading_id", r.saveTOCProgress)
		meGroup.Get("/quran/progress", r.getQuranProgress)
		meGroup.Put("/quran/progress", r.saveQuranProgress)
		meGroup.Get("/quran/progress/surahs", r.listQuranSurahProgress)
		meGroup.Get("/quran/progress/surahs/:surah_id", r.getQuranSurahProgress)
		meGroup.Get("/saved-items", r.listSavedItems)
		meGroup.Post("/saved-items", r.upsertSavedItem)
		meGroup.Get("/saved-items/tags", r.listSavedItemTags)
		meGroup.Patch("/saved-items/:id", r.updateSavedItem)
		meGroup.Delete("/saved-items/:id", r.deleteSavedItem)
	}

	editorialGroup := protected.Group("/editorial")
	{
		editorialReviewGroup := editorialGroup.Group(
			"",
			middleware.RequireRoles(u, entity.UserRoleEditor, entity.UserRoleAdmin),
		)
		editorialReviewGroup.Get("/books", r.editorialListBooks)
		editorialReviewGroup.Get("/reader/missing-assets", r.editorialMissingReaderAssets)
		editorialReviewGroup.Get("/quran/missing-assets", r.editorialMissingQuranAssets)
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
		editorialReviewGroup.Get("/books/:book_id/headings/:heading_id/draft", r.editorialGetHeadingDraft)
		editorialReviewGroup.Put("/books/:book_id/headings/:heading_id/draft", r.editorialSaveHeadingDraft)

		editorialAdminGroup := editorialGroup.Group("", middleware.RequireRoles(u, entity.UserRoleAdmin))
		editorialAdminGroup.Put("/books/:book_id/publication", r.editorialUpdatePublication)
		editorialAdminGroup.Post("/books/:book_id/metadata-draft/publish", r.editorialPublishMetadataDraft)
		editorialAdminGroup.Post("/books/:book_id/pages/:page_id/publish", r.editorialPublishPageDraft)
		editorialAdminGroup.Post("/books/:book_id/headings/:heading_id/publish", r.editorialPublishHeadingDraft)
		editorialAdminGroup.Post("/collections/:slug/items", r.editorialAddCollectionItem)
		editorialAdminGroup.Post("/production-projects/:id/publish", r.editorialPublishProductionProject)
		editorialAdminGroup.Post("/production-projects/:id/unpublish", r.editorialUnpublishProductionProject)
		editorialAdminGroup.Delete("/production-projects/:id/final-assets/:asset_type", r.editorialDeleteFinalProductionAsset)
		editorialAdminGroup.Delete("/production-projects/:id/toc/:heading_id/final-assets/:asset_type", r.editorialDeleteFinalHeadingProductionAsset)
	}

	adminGroup := protected.Group("/admin", middleware.RequireRoles(u, entity.UserRoleAdmin))
	{
		adminGroup.Get("/users", r.adminUsers)
		adminGroup.Get("/users/:id/activity", r.adminUserActivity)
		adminGroup.Get("/users/:id", r.adminUserDetail)
		adminGroup.Patch("/users/role", r.adminSetUserRole)
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
