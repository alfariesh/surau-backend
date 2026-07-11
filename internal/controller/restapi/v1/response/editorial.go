package response

import (
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
)

// ProductionProjectList -.
type ProductionProjectList struct {
	Projects []entity.BookProductionProject `json:"projects"`
	Total    int                            `json:"total" example:"42"`
} // @name v1.ProductionProjectList

// ProductionCandidateList -.
type ProductionCandidateList struct {
	Candidates []entity.BookProductionCandidate `json:"candidates"`
	Total      int                              `json:"total" example:"42"`
} // @name v1.ProductionCandidateList

// ProductionEventList -.
type ProductionEventList struct {
	Events []entity.BookProductionEvent `json:"events"`
	Total  int                          `json:"total" example:"42"`
} // @name v1.ProductionEventList

// ProductionDraftRevisionList -.
type ProductionDraftRevisionList struct {
	Revisions []entity.BookProductionDraftRevision `json:"revisions"`
	Total     int                                  `json:"total" example:"42"`
} // @name v1.ProductionDraftRevisionList

// SourceEditRevisionList -.
type SourceEditRevisionList struct {
	Revisions []entity.BookSourceEditRevision `json:"revisions"`
	Total     int                             `json:"total" example:"42"`
} // @name v1.SourceEditRevisionList

// QuranEditorialRevisionList uses the standard list envelope for the new Q-1
// API rather than inheriting the legacy kitab revision key.
type QuranEditorialRevisionList struct {
	Items []entity.QuranEditorialRevision `json:"items"`
	Total int                             `json:"total" example:"42"`
} // @name v1.QuranEditorialRevisionList

// CollabPageDraft is the seeding payload for a fresh collaborative document:
// the current draft when one exists, otherwise the raw page content.
type CollabPageDraft struct {
	BookID      int       `json:"book_id"      example:"797"`
	PageID      int       `json:"page_id"      example:"1"`
	Source      string    `json:"source"       example:"draft"`
	ContentHTML string    `json:"content_html"`
	UpdatedAt   time.Time `json:"updated_at"   example:"2026-01-01T00:00:00Z"`
} // @name v1.CollabPageDraft
