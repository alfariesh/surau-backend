package v1

import (
	"time"

	"github.com/evrone/go-clean-template/internal/controller/restapi/middleware"
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
	}

	userGroup := protected.Group("/user")
	{
		userGroup.Get("/profile", r.profile)
	}

	meGroup := protected.Group("/me")
	{
		meGroup.Get("/progress/:book_id", r.getProgress)
		meGroup.Put("/progress/:book_id", r.saveProgress)
		meGroup.Put("/progress/:book_id/toc/:heading_id", r.saveTOCProgress)
		meGroup.Get("/bookmarks", r.listBookmarks)
		meGroup.Post("/bookmarks", r.createBookmark)
		meGroup.Post("/bookmarks/toc/:book_id/:heading_id", r.createTOCBookmark)
		meGroup.Delete("/bookmarks/:id", r.deleteBookmark)
	}

	adminGroup := protected.Group("/admin", middleware.Admin(u))
	{
		adminGroup.Get("/books", r.adminListBooks)
		adminGroup.Get("/reader/missing-assets", r.adminMissingReaderAssets)
		adminGroup.Get("/quran/missing-assets", r.adminMissingQuranAssets)
		adminGroup.Get("/translation-feedbacks", r.adminListTranslationFeedbacks)
		adminGroup.Get("/translation-feedbacks/summary", r.adminTranslationFeedbackSummary)
		adminGroup.Post("/translation-feedbacks/:id/resolve", r.adminResolveTranslationFeedback)
		adminGroup.Post("/translation-feedbacks/:id/reopen", r.adminReopenTranslationFeedback)
		adminGroup.Put("/books/:book_id/publication", r.adminUpdatePublication)
		adminGroup.Put("/books/:book_id/metadata-draft", r.adminSaveMetadataDraft)
		adminGroup.Post("/books/:book_id/metadata-draft/publish", r.adminPublishMetadataDraft)
		adminGroup.Get("/books/:book_id/pages/:page_id", r.adminGetPageEdit)
		adminGroup.Put("/books/:book_id/pages/:page_id/draft", r.adminSavePageDraft)
		adminGroup.Post("/books/:book_id/pages/:page_id/publish", r.adminPublishPageDraft)
		adminGroup.Put("/books/:book_id/headings/:heading_id/draft", r.adminSaveHeadingDraft)
		adminGroup.Post("/books/:book_id/headings/:heading_id/publish", r.adminPublishHeadingDraft)
		adminGroup.Post("/collections/:slug/items", r.adminAddCollectionItem)
	}
}
