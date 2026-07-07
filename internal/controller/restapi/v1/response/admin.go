package response

import "github.com/alfariesh/surau-backend/internal/entity"

// AdminUserList wraps paginated admin user accounts.
type AdminUserList struct {
	Users []entity.UserAccount `json:"users"`
	Total int                  `json:"total" example:"42"`
} // @name v1.AdminUserList

// AdminUserActivityList wraps admin-visible account activity.
type AdminUserActivityList struct {
	Activity []entity.UserActivity `json:"activity"`
	Total    int                   `json:"total" example:"42"`
} // @name v1.AdminUserActivityList
