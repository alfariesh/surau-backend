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
	u usecase.User,
	personal usecase.Personal,
	editorial usecase.Editorial,
	jwtManager *jwt.Manager,
	l logger.Interface,
) {
	r := &V1{
		reader:    reader,
		u:         u,
		personal:  personal,
		editorial: editorial,
		l:         l,
		v:         validator.New(validator.WithRequiredStructEnabled()),
	}

	// Public routes
	authGroup := apiV1Group.Group("/auth", limiter.New(limiter.Config{
		Max:        10,
		Expiration: time.Minute,
	}))
	{
		authGroup.Post("/register", r.register)
		authGroup.Post("/login", r.login)
	}

	// Public reader routes
	bookGroup := apiV1Group.Group("/books")
	{
		bookGroup.Get("/", r.listBooks)
		bookGroup.Get("/:book_id", r.getBook)
		bookGroup.Get("/:book_id/pages", r.listBookPages)
		bookGroup.Get("/:book_id/pages/:page_id", r.getBookPage)
		bookGroup.Get("/:book_id/headings", r.listBookHeadings)
		bookGroup.Get("/:book_id/sections/:heading_id", r.getBookSection)
		bookGroup.Get("/:book_id/toc", r.listBookTOC)
		bookGroup.Get("/:book_id/toc/:heading_id/read", r.readBookTOCSection)
		bookGroup.Get("/:book_id/toc/:heading_id/playlist", r.getBookTOCPlaylist)
		bookGroup.Post("/:book_id/toc/:heading_id/translation-feedback", limiter.New(limiter.Config{
			Max:        30,
			Expiration: time.Minute,
		}), r.createTranslationFeedback)
	}

	apiV1Group.Get("/categories", r.listCategories)
	apiV1Group.Get("/authors", r.listAuthors)

	// Protected routes
	protected := apiV1Group.Group("", middleware.Auth(jwtManager))

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
