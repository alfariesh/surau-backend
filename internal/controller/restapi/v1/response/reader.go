package response

import "github.com/evrone/go-clean-template/internal/entity"

// Every user-facing list endpoint shares the {items, total} envelope. For
// paginated lists total is the unbounded match count; for full (non-paginated)
// lists it is len(items).

// CategoryList -.
type CategoryList struct {
	Items []entity.Category `json:"items"`
	Total int               `json:"total" example:"12"`
} // @name v1.CategoryList

// AuthorList -.
type AuthorList struct {
	Items []entity.Author `json:"items"`
	Total int             `json:"total" example:"42"`
} // @name v1.AuthorList

// BookList -.
type BookList struct {
	Items []entity.Book           `json:"items"`
	Total int                     `json:"total" example:"42"`
	Stats entity.BookCatalogStats `json:"stats"`
} // @name v1.BookList

// PageList -.
type PageList struct {
	Items []entity.BookPage `json:"items"`
	Total int               `json:"total" example:"42"`
} // @name v1.PageList

// BookHeadingList -.
type BookHeadingList struct {
	Items []entity.BookHeading `json:"items"`
	Total int                  `json:"total" example:"42"`
} // @name v1.BookHeadingList

// BookTOCList holds the top-level table-of-contents nodes; child nodes stay
// nested inside each item.
type BookTOCList struct {
	Items []entity.BookTOCNode `json:"items"`
	Total int                  `json:"total" example:"12"`
} // @name v1.BookTOCList

// SavedItemList -.
type SavedItemList struct {
	Items []entity.SavedItem `json:"items"`
	Total int                `json:"total" example:"42"`
} // @name v1.SavedItemList

// SavedItemTags -.
type SavedItemTags struct {
	Items []string `json:"items" example:"tafsir,favorit"`
	Total int      `json:"total" example:"2"`
} // @name v1.SavedItemTags

// QuranProgressList -.
type QuranProgressList struct {
	Items []entity.QuranReadingProgress `json:"items"`
	Total int                           `json:"total" example:"3"`
} // @name v1.QuranProgressList

// ContinueReadingList -.
type ContinueReadingList struct {
	Items []entity.ContinueReadingEntry `json:"items"`
	Total int                           `json:"total" example:"3"`
} // @name v1.ContinueReadingList

// KhatamHistory -.
type KhatamHistory struct {
	Items []entity.QuranKhatamCycle `json:"items"`
	Total int                       `json:"total" example:"2"`
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
