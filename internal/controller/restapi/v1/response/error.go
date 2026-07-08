package response

import (
	"github.com/alfariesh/surau-backend/internal/controller/restapi/apierror"
	"github.com/alfariesh/surau-backend/internal/entity"
)

// Error -.
type Error struct {
	Error      string `json:"error" example:"message"`
	Code       string `json:"code" example:"invalid_request_body"`
	Message    string `json:"message" example:"message"`
	Details    any    `json:"details,omitempty"`
	RetryAfter int64  `json:"retry_after,omitempty" example:"60"`
	RequestID  string `json:"request_id,omitempty" example:"550e8400-e29b-41d4-a716-446655440000"`
} // @name v1.Error

type ProductionProjectConflict struct {
	Error             string `json:"error" example:"production project already exists"`
	Code              string `json:"code,omitempty" example:"production_project_already_exists"`
	RequestID         string `json:"request_id,omitempty" example:"550e8400-e29b-41d4-a716-446655440000"`
	ExistingProjectID string `json:"existing_project_id,omitempty" example:"550e8400-e29b-41d4-a716-446655440000"`
} // @name v1.ProductionProjectConflict

type ProductionPublishBlocked struct {
	Error          string                              `json:"error" example:"production project is not ready"`
	Code           string                              `json:"code,omitempty" example:"production_project_is_not_ready"`
	RequestID      string                              `json:"request_id,omitempty" example:"550e8400-e29b-41d4-a716-446655440000"`
	Project        entity.BookProductionProject        `json:"project"`
	Ready          bool                                `json:"ready" example:"false"`
	CanPublish     bool                                `json:"can_publish" example:"false"`
	RequiredCount  int                                 `json:"required_count" example:"42"`
	CompleteCount  int                                 `json:"complete_count" example:"40"`
	MissingCount   int                                 `json:"missing_count" example:"2"`
	Missing        []entity.BookProductionMissingAsset `json:"missing"`
	BlockingErrors []entity.BookProductionBlocking     `json:"blocking_errors"`
} // @name v1.ProductionPublishBlocked

func ProductionPublishBlockedFromCheck(message string, check entity.BookProductionPublishCheck) ProductionPublishBlocked {
	return ProductionPublishBlocked{
		Error:          message,
		Code:           apierror.Code(message),
		Project:        check.Project,
		Ready:          check.Ready,
		CanPublish:     check.CanPublish,
		RequiredCount:  check.RequiredCount,
		CompleteCount:  check.CompleteCount,
		MissingCount:   check.MissingCount,
		Missing:        check.Missing,
		BlockingErrors: check.BlockingErrors,
	}
}
