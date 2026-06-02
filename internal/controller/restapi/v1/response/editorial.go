package response

import "github.com/evrone/go-clean-template/internal/entity"

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
