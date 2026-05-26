package entity

// RAGChatMessage is a provider-neutral chat completion message.
type RAGChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// RAGBookDocument is the published book metadata needed by the QA pipeline.
type RAGBookDocument struct {
	BookID      int     `json:"book_id" example:"797"`
	Title       string  `json:"title" example:"الزبد في مصطلح الحديث"`
	Description *string `json:"description,omitempty"`
}

// RAGStructureNode is one compact TOC/range row used for PageIndex-like tree search.
type RAGStructureNode struct {
	BookID      int     `json:"book_id" example:"797"`
	HeadingID   int     `json:"heading_id" example:"11"`
	ParentID    *int    `json:"parent_id,omitempty" example:"1"`
	PageID      int     `json:"page_id" example:"12"`
	Depth       int     `json:"depth" example:"0"`
	Ordinal     int     `json:"ordinal" example:"10"`
	Title       string  `json:"title" example:"النوع الأول: الصحيح"`
	Summary     *string `json:"summary,omitempty"`
	SummaryLang *string `json:"summary_lang,omitempty" example:"ar"`
	StartPageID int     `json:"start_page_id" example:"12"`
	EndPageID   int     `json:"end_page_id" example:"15"`
}

// RAGSearchResult is a lexical fallback hit for a heading/page pair.
type RAGSearchResult struct {
	HeadingID int     `json:"heading_id" example:"11"`
	PageID    int     `json:"page_id" example:"12"`
	Score     float64 `json:"score" example:"0.76"`
}

// RAGPageSource is one page-level source block supplied to the answer model.
type RAGPageSource struct {
	Ref             string  `json:"ref" example:"1"`
	BookID          int     `json:"book_id" example:"797"`
	HeadingID       int     `json:"heading_id" example:"11"`
	HeadingTitle    string  `json:"heading_title" example:"النوع الأول: الصحيح"`
	StartPageID     int     `json:"start_page_id" example:"12"`
	EndPageID       int     `json:"end_page_id" example:"15"`
	PageID          int     `json:"page_id" example:"12"`
	PrintedPage     *string `json:"printed_page,omitempty" example:"12"`
	Part            *string `json:"part,omitempty" example:"1"`
	Number          *string `json:"number,omitempty" example:"42"`
	Anchor          string  `json:"anchor" example:"toc-11"`
	URL             string  `json:"url" example:"/v1/books/797/toc/11/read?lang=id"`
	ContentText     string  `json:"-"`
	TranslationText *string `json:"-"`
}

// BookRAGCitation is a validated source reference returned with an answer.
type BookRAGCitation struct {
	Ref          string  `json:"ref" example:"1"`
	BookID       int     `json:"book_id" example:"797"`
	HeadingID    int     `json:"heading_id" example:"11"`
	HeadingTitle string  `json:"heading_title" example:"النوع الأول: الصحيح"`
	PageID       int     `json:"page_id" example:"12"`
	PrintedPage  *string `json:"printed_page,omitempty" example:"12"`
	Part         *string `json:"part,omitempty" example:"1"`
	Anchor       string  `json:"anchor" example:"toc-11"`
	Quote        string  `json:"quote" example:"الحديث الصحيح هو..."`
	URL          string  `json:"url" example:"/v1/books/797/toc/11/read?lang=id"`
}

// BookRAGTrace contains optional retrieval diagnostics for RAG review.
type BookRAGTrace struct {
	TreeReasoning      string   `json:"tree_reasoning,omitempty"`
	SelectedHeadingIDs []int    `json:"selected_heading_ids,omitempty"`
	LexicalHeadingIDs  []int    `json:"lexical_heading_ids,omitempty"`
	FocusPageIDs       []int    `json:"focus_page_ids,omitempty"`
	SourceRefs         []string `json:"source_refs,omitempty"`
	RetrievalMode      string   `json:"retrieval_mode,omitempty"`
	TreeLLMCalls       int      `json:"tree_llm_calls,omitempty"`
	TreeBlocks         int      `json:"tree_blocks,omitempty"`
	TreeCandidateCount int      `json:"tree_candidate_count,omitempty"`
	Repaired           bool     `json:"repaired"`
}

// BookRAGResponse is the public non-streaming RAG response.
type BookRAGResponse struct {
	BookID    int               `json:"book_id" example:"797"`
	Question  string            `json:"question" example:"Apa definisi hadis sahih?"`
	Answer    string            `json:"answer"`
	Citations []BookRAGCitation `json:"citations"`
	Trace     *BookRAGTrace     `json:"trace"`
}
