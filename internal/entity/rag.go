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
	UnitID          *string `json:"-"`
	UnitAnchor      *string `json:"-"`
	ContentText     string  `json:"-"`
	TranslationText *string `json:"-"`
}

// RAGUnitLocator is the exact Citable Unit projection for one legacy citation.
// Found is false when the quote is missing or spans more than one unit; callers
// record that as a parity mismatch without inventing a locator.
type RAGUnitLocator struct {
	UnitID     string
	UnitAnchor string
	Found      bool
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
	UnitID       *string `json:"unit_id,omitempty" example:"018f25dc-18a8-7c26-a3c4-20ec5f6f6b1e"`
	UnitAnchor   *string `json:"unit_anchor,omitempty" example:"kitab/797/h/11/u/42"`
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
	CitationMode       string   `json:"citation_mode,omitempty" example:"unit"`
	LegacyFallback     bool     `json:"legacy_fallback,omitempty"`
	FallbackReason     string   `json:"fallback_reason,omitempty" example:"incomplete"`
	TreeLLMCalls       int      `json:"tree_llm_calls,omitempty"`
	TreeBlocks         int      `json:"tree_blocks,omitempty"`
	TreeCandidateCount int      `json:"tree_candidate_count,omitempty"`
	Repaired           bool     `json:"repaired"`
}

// BookRAGResponse is the public non-streaming RAG response.
type BookRAGResponse struct {
	BookID        int               `json:"book_id" example:"797"`
	RequestedLang string            `json:"requested_lang" example:"id"`
	Question      string            `json:"question" example:"Apa definisi hadis sahih?"`
	Answer        string            `json:"answer"`
	Citations     []BookRAGCitation `json:"citations"`
	Trace         *BookRAGTrace     `json:"trace"`
}
