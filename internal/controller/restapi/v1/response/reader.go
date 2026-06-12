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

// ContinueReadingList -.
type ContinueReadingList struct {
	Items []entity.ContinueReadingEntry `json:"items"`
	Total int                           `json:"total" example:"3"`
} // @name v1.ContinueReadingList

// KhatamHistory -.
type KhatamHistory struct {
	Cycles []entity.QuranKhatamCycle `json:"cycles"`
	Total  int                       `json:"total" example:"2"`
} // @name v1.KhatamHistory

// BatchKitabProgressResult is one per-entry outcome of a batch replay.
type BatchKitabProgressResult struct {
	Status   string                  `json:"status"             example:"ok" enums:"ok,error"`
	Error    *string                 `json:"error,omitempty"    example:"book not found"`
	Progress *entity.ReadingProgress `json:"progress,omitempty"`
} // @name v1.BatchKitabProgressResult

// BatchQuranProgressResult is one per-entry outcome of a batch replay.
type BatchQuranProgressResult struct {
	Status   string                       `json:"status"             example:"ok" enums:"ok,error"`
	Error    *string                      `json:"error,omitempty"    example:"quran ayah not found"`
	Progress *entity.QuranReadingProgress `json:"progress,omitempty"`
} // @name v1.BatchQuranProgressResult

// BatchProgressResults mirrors the request entry order one-to-one.
type BatchProgressResults struct {
	Kitab []BatchKitabProgressResult `json:"kitab"`
	Quran []BatchQuranProgressResult `json:"quran"`
} // @name v1.BatchProgressResults

// TranslationFeedbackList -.
type TranslationFeedbackList struct {
	Feedbacks []entity.EditorialTranslationFeedback `json:"feedbacks"`
	Total     int                                   `json:"total" example:"42"`
} // @name v1.TranslationFeedbackList
