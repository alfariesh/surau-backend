package v1

import (
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
	jwtManager *jwt.Manager,
	l logger.Interface,
) {
	r := &V1{
		reader:    reader,
		bookRAG:   bookRAG,
		quran:     quran,
		u:         u,
		personal:  personal,
		editorial: editorial,
		l:         l,
		v:         validator.New(validator.WithRequiredStructEnabled()),
	}

	// Public routes
	authGroup := apiV1Group.Group("/auth")
	{
		authGroup.Post("/register", r.register)
		authGroup.Post("/login", r.login)
		authGroup.Post("/verify-email", r.verifyEmail)
		authGroup.Post("/resend-verification", r.resendVerification)
		authGroup.Post("/forgot-password", r.forgotPassword)
		authGroup.Post("/reset-password", r.resetPassword)
	}

	// Public reader routes
	bookGroup := apiV1Group.Group("/books")
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

	apiV1Group.Get("/categories", r.listCategories)
	apiV1Group.Get("/authors", r.listAuthors)

	quranGroup := apiV1Group.Group("/quran")
	{
		quranGroup.Get("/recitations", r.listQuranRecitations)
		quranGroup.Get("/translation-sources", r.listQuranTranslationSources)
		quranGroup.Get("/juz", r.listQuranJuz)
		quranGroup.Get("/juz/:juz_number/ayahs", r.listQuranJuzAyahs)
		quranGroup.Get("/hizbs", r.listQuranHizbs)
		quranGroup.Get("/hizbs/:hizb_number/ayahs", r.listQuranHizbAyahs)
		quranGroup.Get("/surahs", r.listQuranSurahs)
		quranGroup.Get("/surahs/:surah_id", r.getQuranSurah)
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
	}

	userGroup := protected.Group("/user")
	{
		userGroup.Get("/profile", r.profile)
		userGroup.Patch("/profile", r.updateProfile)
		userGroup.Patch("/onboarding", r.updateOnboarding)
		userGroup.Patch("/preferences", r.updatePreferences)
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
		editorialReviewGroup.Put("/books/:book_id/metadata-draft", r.editorialSaveMetadataDraft)
		editorialReviewGroup.Get("/books/:book_id/pages/:page_id", r.editorialGetPageEdit)
		editorialReviewGroup.Put("/books/:book_id/pages/:page_id/draft", r.editorialSavePageDraft)
		editorialReviewGroup.Put("/books/:book_id/headings/:heading_id/draft", r.editorialSaveHeadingDraft)

		editorialAdminGroup := editorialGroup.Group("", middleware.RequireRoles(u, entity.UserRoleAdmin))
		editorialAdminGroup.Put("/books/:book_id/publication", r.editorialUpdatePublication)
		editorialAdminGroup.Post("/books/:book_id/metadata-draft/publish", r.editorialPublishMetadataDraft)
		editorialAdminGroup.Post("/books/:book_id/pages/:page_id/publish", r.editorialPublishPageDraft)
		editorialAdminGroup.Post("/books/:book_id/headings/:heading_id/publish", r.editorialPublishHeadingDraft)
		editorialAdminGroup.Post("/collections/:slug/items", r.editorialAddCollectionItem)
	}

	adminGroup := protected.Group("/admin", middleware.RequireRoles(u, entity.UserRoleAdmin))
	{
		adminGroup.Patch("/users/role", r.adminSetUserRole)
	}
}
