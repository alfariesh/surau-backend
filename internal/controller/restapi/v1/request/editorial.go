package request

// UpdatePublication -.
type UpdatePublication struct {
	Status    string `json:"status"     validate:"required" example:"published"`
	Featured  bool   `json:"featured"   example:"true"`
	SortOrder *int   `json:"sort_order" example:"10"`
} // @name v1.UpdatePublication

// SaveMetadataDraft -.
type SaveMetadataDraft struct {
	DisplayTitle *string `json:"display_title" validate:"omitempty,max=500"`
	Description  *string `json:"description"   validate:"omitempty,max=10000"`
	CoverURL     *string `json:"cover_url"     validate:"omitempty,url,max=2000"`
	CategoryID   *int    `json:"category_id"   validate:"omitempty,min=1"`
	Notes        *string `json:"notes"         validate:"omitempty,max=10000"`
} // @name v1.SaveMetadataDraft

// SavePageDraft -.
type SavePageDraft struct {
	ContentHTML string `json:"content_html" validate:"required"`
} // @name v1.SavePageDraft

// SaveHeadingDraft -.
type SaveHeadingDraft struct {
	Content string `json:"content" validate:"required,max=2000"`
} // @name v1.SaveHeadingDraft

// AddCollectionItem -.
type AddCollectionItem struct {
	BookID    int  `json:"book_id"    validate:"required,min=1" example:"797"`
	SortOrder *int `json:"sort_order" example:"10"`
} // @name v1.AddCollectionItem
