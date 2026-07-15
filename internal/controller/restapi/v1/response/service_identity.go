package response

import "github.com/alfariesh/surau-backend/internal/entity"

// ServiceIdentityList preserves the public list envelope contract.
type ServiceIdentityList struct {
	Items []entity.ServicePrincipal `json:"items"`
	Total int                       `json:"total"`
} // @name response.ServiceIdentityList
