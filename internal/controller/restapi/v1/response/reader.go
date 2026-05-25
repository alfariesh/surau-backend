package response

import "github.com/evrone/go-clean-template/internal/entity"

// AuthorList -.
type AuthorList struct {
	Authors []entity.Author `json:"authors"`
	Total   int             `json:"total" example:"42"`
} // @name v1.AuthorList

// BookList -.
type BookList struct {
	Books []entity.Book `json:"books"`
	Total int           `json:"total" example:"42"`
} // @name v1.BookList

// PageList -.
type PageList struct {
	Pages []entity.BookPage `json:"pages"`
	Total int               `json:"total" example:"42"`
} // @name v1.PageList

// BookmarkList -.
type BookmarkList struct {
	Bookmarks []entity.Bookmark `json:"bookmarks"`
	Total     int               `json:"total" example:"42"`
} // @name v1.BookmarkList

// TranslationFeedbackList -.
type TranslationFeedbackList struct {
	Feedbacks []entity.AdminTranslationFeedback `json:"feedbacks"`
	Total     int                               `json:"total" example:"42"`
} // @name v1.TranslationFeedbackList
