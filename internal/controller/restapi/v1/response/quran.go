package response

import "github.com/evrone/go-clean-template/internal/entity"

// QuranSearchList -.
type QuranSearchList struct {
	Results []entity.QuranSearchResult `json:"results"`
	Total   int                        `json:"total" example:"42"`
} // @name v1.QuranSearchList

// BookQuranReferenceList -.
type BookQuranReferenceList struct {
	References []entity.BookQuranReference `json:"references"`
	Total      int                         `json:"total" example:"42"`
} // @name v1.BookQuranReferenceList
