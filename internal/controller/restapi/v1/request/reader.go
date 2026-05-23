package request

// SaveProgress -.
type SaveProgress struct {
	PageID          *int     `json:"page_id"           validate:"omitempty,min=1"        example:"12"`
	HeadingID       *int     `json:"heading_id"        validate:"omitempty,min=1"        example:"10"`
	ProgressPercent *float64 `json:"progress_percent"  validate:"omitempty,min=0,max=100" example:"32.5"`
} // @name v1.SaveProgress

// SaveTOCProgress -.
type SaveTOCProgress struct {
	ProgressPercent *float64 `json:"progress_percent" validate:"omitempty,min=0,max=100" example:"32.5"`
} // @name v1.SaveTOCProgress

// CreateBookmark -.
type CreateBookmark struct {
	BookID    int     `json:"book_id"    validate:"required,min=1" example:"797"`
	PageID    *int    `json:"page_id"    validate:"omitempty,min=1" example:"12"`
	HeadingID *int    `json:"heading_id" validate:"omitempty,min=1" example:"10"`
	Label     *string `json:"label"      validate:"omitempty,max=255"`
	Note      *string `json:"note"       validate:"omitempty,max=2000"`
} // @name v1.CreateBookmark

// CreateTOCBookmark -.
type CreateTOCBookmark struct {
	Label *string `json:"label" validate:"omitempty,max=255"`
	Note  *string `json:"note"  validate:"omitempty,max=2000"`
} // @name v1.CreateTOCBookmark
