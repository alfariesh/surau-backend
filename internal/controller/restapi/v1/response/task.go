package response

import "github.com/alfariesh/surau-backend/internal/entity"

// TaskList -.
type TaskList struct {
	Tasks []entity.Task `json:"tasks"`
	Total int           `json:"total" example:"42"`
} // @name v1.TaskList
