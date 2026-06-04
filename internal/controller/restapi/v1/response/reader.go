package response

import "github.com/evrone/go-clean-template/internal/entity"

// AuthorList -.
type AuthorList struct {
	Authors []entity.Author `json:"authors"`
	Total   int             `json:"total" example:"42"`
} // @name v1.AuthorList

// BookList -.
type BookList struct {
	Books []entity.Book           `json:"books"`
	Total int                     `json:"total" example:"42"`
	Stats entity.BookCatalogStats `json:"stats"`
} // @name v1.BookList

// PageList -.
type PageList struct {
	Pages []entity.BookPage `json:"pages"`
	Total int               `json:"total" example:"42"`
} // @name v1.PageList

// SavedItemList -.
type SavedItemList struct {
	Items []entity.SavedItem `json:"items"`
	Total int                `json:"total" example:"42"`
} // @name v1.SavedItemList

// SavedItemTags -.
type SavedItemTags struct {
	Tags []string `json:"tags"`
} // @name v1.SavedItemTags

// QuranProgressList -.
type QuranProgressList struct {
	Surahs []entity.QuranReadingProgress `json:"surahs"`
} // @name v1.QuranProgressList

// TranslationFeedbackList -.
type TranslationFeedbackList struct {
	Feedbacks []entity.EditorialTranslationFeedback `json:"feedbacks"`
	Total     int                                   `json:"total" example:"42"`
} // @name v1.TranslationFeedbackList
