package rageval

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"text/tabwriter"
	"time"
)

const (
	DefaultBaseURL              = "http://127.0.0.1:8080"
	DefaultCasesPath            = "eval/bookrag_smoke.jsonl"
	DefaultTimeout              = 150 * time.Second
	anchorResolutionMaxBodySize = 1 << 20
)

// Options configures a BookRAG evaluation run.
type Options struct {
	BaseURL        string
	CasesPath      string
	Output         string
	Timeout        time.Duration
	FailFast       bool
	Limit          int
	Retries        int
	StrictAnswer   bool
	ProgressWriter io.Writer
}

// GoldenCase is one JSONL evaluation case.
type GoldenCase struct {
	Name                  string   `json:"name"`
	BookID                int      `json:"book_id"`
	Lang                  string   `json:"lang,omitempty"`
	Question              string   `json:"question"`
	MaxCitations          int      `json:"max_citations,omitempty"`
	ExpectNotFound        bool     `json:"expect_not_found,omitempty"`
	ExpectedRetrievalMode string   `json:"expected_retrieval_mode,omitempty"`
	MaxTreeLLMCalls       int      `json:"max_tree_llm_calls,omitempty"`
	ExpectedHeadingIDs    []int    `json:"expected_heading_ids,omitempty"`
	ExpectedPageIDs       []int    `json:"expected_page_ids,omitempty"`
	AnswerMustContain     []string `json:"answer_must_contain,omitempty"`
	QuoteMustContain      []string `json:"quote_must_contain,omitempty"`
	RequireUnitCitations  bool     `json:"require_unit_citations,omitempty"`
}

// Summary is the machine-readable output for one evaluation run.
type Summary struct {
	BaseURL    string        `json:"base_url"`
	CasesPath  string        `json:"cases_path"`
	Total      int           `json:"total"`
	Passed     int           `json:"passed"`
	Failed     int           `json:"failed"`
	Warnings   int           `json:"warnings"`
	DurationMS int64         `json:"duration_ms"`
	Results    []CaseResult  `json:"results"`
	StartedAt  time.Time     `json:"started_at"`
	FinishedAt time.Time     `json:"finished_at"`
	Timeout    time.Duration `json:"timeout"`
}

// CaseResult is the assertion result for one golden case.
type CaseResult struct {
	Name       string     `json:"name"`
	BookID     int        `json:"book_id"`
	Question   string     `json:"question"`
	Passed     bool       `json:"passed"`
	Errors     []string   `json:"errors,omitempty"`
	Warnings   []string   `json:"warnings,omitempty"`
	DurationMS int64      `json:"duration_ms"`
	StatusCode int        `json:"status_code"`
	Attempt    int        `json:"attempt"`
	Answer     string     `json:"answer,omitempty"`
	Citations  []Citation `json:"citations,omitempty"`
	Trace      *Trace     `json:"trace,omitempty"`
	HTTPBody   string     `json:"http_body,omitempty"`
	Error      string     `json:"error,omitempty"`
	Case       GoldenCase `json:"case"`
}

// Citation mirrors the public BookRAG citation payload.
type Citation struct {
	Ref          string  `json:"ref"`
	BookID       int     `json:"book_id"`
	HeadingID    int     `json:"heading_id"`
	HeadingTitle string  `json:"heading_title"`
	PageID       int     `json:"page_id"`
	PrintedPage  *string `json:"printed_page,omitempty"`
	Part         *string `json:"part,omitempty"`
	Anchor       string  `json:"anchor"`
	UnitID       *string `json:"unit_id,omitempty"`
	UnitAnchor   *string `json:"unit_anchor,omitempty"`
	Quote        string  `json:"quote"`
	URL          string  `json:"url"`
}

// Trace mirrors the public BookRAG trace payload.
type Trace struct {
	TreeReasoning      string   `json:"tree_reasoning,omitempty"`
	SelectedHeadingIDs []int    `json:"selected_heading_ids,omitempty"`
	LexicalHeadingIDs  []int    `json:"lexical_heading_ids,omitempty"`
	FocusPageIDs       []int    `json:"focus_page_ids,omitempty"`
	SourceRefs         []string `json:"source_refs,omitempty"`
	RetrievalMode      string   `json:"retrieval_mode,omitempty"`
	CitationMode       string   `json:"citation_mode,omitempty"`
	LegacyFallback     bool     `json:"legacy_fallback,omitempty"`
	FallbackReason     string   `json:"fallback_reason,omitempty"`
	TreeLLMCalls       int      `json:"tree_llm_calls,omitempty"`
	TreeBlocks         int      `json:"tree_blocks,omitempty"`
	TreeCandidateCount int      `json:"tree_candidate_count,omitempty"`
	Repaired           bool     `json:"repaired"`
}

type ragResponse struct {
	BookID    int        `json:"book_id"`
	Question  string     `json:"question"`
	Answer    string     `json:"answer"`
	Citations []Citation `json:"citations"`
	Trace     *Trace     `json:"trace"`
}

type anchorResolution struct {
	Boundaries []anchorResolutionBoundary `json:"boundaries"`
}

type anchorResolutionBoundary struct {
	ActiveTargets []anchorResolutionTarget `json:"active_targets"`
}

type anchorResolutionTarget struct {
	UnitID *string `json:"unit_id"`
}

// LoadCases reads JSONL golden cases. Blank lines and lines beginning with # are ignored.
func LoadCases(path string) ([]GoldenCase, error) {
	file, err := os.Open(path) // #nosec G304 -- rag-eval is a CLI tool that intentionally reads an operator-supplied cases file.
	if err != nil {
		return nil, fmt.Errorf("open cases: %w", err)
	}
	defer file.Close()

	return DecodeCases(file)
}

// DecodeCases reads JSONL golden cases from r.
func DecodeCases(r io.Reader) ([]GoldenCase, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	cases := make([]GoldenCase, 0)
	for lineNo := 1; scanner.Scan(); lineNo++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		var tc GoldenCase
		if err := json.Unmarshal([]byte(line), &tc); err != nil {
			return nil, fmt.Errorf("decode case line %d: %w", lineNo, err)
		}
		if err := validateCase(tc); err != nil {
			return nil, fmt.Errorf("invalid case line %d: %w", lineNo, err)
		}
		cases = append(cases, tc)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan cases: %w", err)
	}
	if len(cases) == 0 {
		return nil, errors.New("no cases found")
	}

	return cases, nil
}

// Run loads cases, calls the BookRAG endpoint, and validates each response.
func Run(ctx context.Context, opts Options) (Summary, error) {
	opts = normalizeOptions(opts)
	cases, err := LoadCases(opts.CasesPath)
	if err != nil {
		return Summary{}, err
	}
	if opts.Limit > 0 && opts.Limit < len(cases) {
		cases = cases[:opts.Limit]
	}

	startedAt := time.Now()
	client := &http.Client{Timeout: opts.Timeout}
	results := make([]CaseResult, 0, len(cases))
	for _, tc := range cases {
		var result CaseResult
		for attempt := 1; attempt <= opts.Retries+1; attempt++ {
			writeProgress(opts.ProgressWriter, "start", tc, attempt, CaseResult{})
			result = EvaluateCase(ctx, client, opts.BaseURL, tc, opts.StrictAnswer)
			result.Attempt = attempt
			writeProgress(opts.ProgressWriter, "finish", tc, attempt, result)
			if result.Passed {
				break
			}
		}
		results = append(results, result)
		if opts.FailFast && !result.Passed {
			break
		}
	}
	finishedAt := time.Now()

	summary := Summary{
		BaseURL:    opts.BaseURL,
		CasesPath:  opts.CasesPath,
		Total:      len(results),
		Results:    results,
		StartedAt:  startedAt,
		FinishedAt: finishedAt,
		DurationMS: finishedAt.Sub(startedAt).Milliseconds(),
		Timeout:    opts.Timeout,
	}
	for _, result := range results {
		if result.Passed {
			summary.Passed++
		} else {
			summary.Failed++
		}
		summary.Warnings += len(result.Warnings)
	}

	return summary, nil
}

func writeProgress(w io.Writer, event string, tc GoldenCase, attempt int, result CaseResult) {
	if w == nil {
		return
	}
	switch event {
	case "start":
		fmt.Fprintf(w, "rag-eval: start case=%s book_id=%d attempt=%d\n", tc.Name, tc.BookID, attempt)
	case "finish":
		status := "pass"
		detail := clip(result.Answer, 100)
		if !result.Passed {
			status = "fail"
			if result.Error != "" {
				detail = result.Error
			} else {
				detail = strings.Join(result.Errors, "; ")
			}
		} else if len(result.Warnings) > 0 {
			status = "warn"
			detail = strings.Join(result.Warnings, "; ")
		}
		fmt.Fprintf(
			w,
			"rag-eval: finish case=%s status=%s attempt=%d status_code=%d duration_ms=%d detail=%s\n",
			tc.Name,
			status,
			attempt,
			result.StatusCode,
			result.DurationMS,
			detail,
		)
	}
}

// EvaluateCase calls the public BookRAG endpoint once and validates the response.
func EvaluateCase(
	ctx context.Context,
	client *http.Client,
	baseURL string,
	tc GoldenCase,
	strictAnswer bool,
) (result CaseResult) {
	start := time.Now()
	result = CaseResult{
		Name:     tc.Name,
		BookID:   tc.BookID,
		Question: tc.Question,
		Case:     tc,
	}
	defer func() {
		result.DurationMS = time.Since(start).Milliseconds()
		result.Passed = len(result.Errors) == 0 && result.Error == ""
	}()

	req, err := buildRequest(ctx, baseURL, tc)
	if err != nil {
		result.Error = err.Error()
		return result
	}

	resp, err := client.Do(req)
	if err != nil {
		result.Error = err.Error()
		return result
	}
	defer resp.Body.Close()

	result.StatusCode = resp.StatusCode
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		result.Error = err.Error()
		return result
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		result.HTTPBody = clip(string(body), 500)
		result.Errors = append(result.Errors, fmt.Sprintf("unexpected HTTP status %d", resp.StatusCode))
		return result
	}

	var payload ragResponse
	if err = json.Unmarshal(body, &payload); err != nil {
		result.HTTPBody = clip(string(body), 500)
		result.Error = fmt.Sprintf("decode response: %v", err)
		return result
	}

	result.Answer = payload.Answer
	result.Citations = payload.Citations
	result.Trace = payload.Trace
	errors, warnings := validateResponse(tc, payload, strictAnswer)
	result.Errors = append(result.Errors, errors...)
	result.Warnings = append(result.Warnings, warnings...)
	if tc.RequireUnitCitations && len(errors) == 0 {
		result.Errors = append(result.Errors, validateCitationAnchors(ctx, client, baseURL, payload.Citations)...)
	}

	return result
}

func validateCitationAnchors(
	ctx context.Context,
	client *http.Client,
	baseURL string,
	citations []Citation,
) []string {
	errs := make([]string, 0)

	for i := range citations {
		citation := &citations[i]
		if citation.UnitID == nil || citation.UnitAnchor == nil {
			continue
		}

		if validationErr := validateCitationAnchor(ctx, client, baseURL, i, citation); validationErr != "" {
			errs = append(errs, validationErr)
		}
	}

	return errs
}

func validateCitationAnchor(
	ctx context.Context,
	client *http.Client,
	baseURL string,
	index int,
	citation *Citation,
) string {
	endpoint, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return fmt.Sprintf("citation[%d] anchor resolver URL: %v", index, err)
	}

	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/v1/anchors/resolve"
	query := endpoint.Query()
	query.Set("anchor", *citation.UnitAnchor)
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), http.NoBody)
	if err != nil {
		return fmt.Sprintf("citation[%d] anchor request: %v", index, err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Sprintf("citation[%d] unit_anchor does not resolve: %v", index, err)
	}
	defer func() { _ = resp.Body.Close() }()

	var resolution anchorResolution

	decodeErr := json.NewDecoder(io.LimitReader(resp.Body, anchorResolutionMaxBodySize)).Decode(&resolution)
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices || decodeErr != nil {
		return fmt.Sprintf(
			"citation[%d] unit_anchor resolver status=%d decode=%v",
			index,
			resp.StatusCode,
			decodeErr,
		)
	}

	if anchorResolutionContainsUnit(resolution, *citation.UnitID) {
		return ""
	}

	return fmt.Sprintf("citation[%d] unit_anchor did not resolve to unit_id", index)
}

func anchorResolutionContainsUnit(resolution anchorResolution, unitID string) bool {
	for i := range resolution.Boundaries {
		for j := range resolution.Boundaries[i].ActiveTargets {
			target := &resolution.Boundaries[i].ActiveTargets[j]
			if target.UnitID != nil && *target.UnitID == unitID {
				return true
			}
		}
	}

	return false
}

// WriteSummary writes either a table or JSON report.
func WriteSummary(w io.Writer, summary Summary, output string) error {
	switch strings.ToLower(strings.TrimSpace(output)) {
	case "", "table":
		return writeTable(w, summary)
	case "json":
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		return encoder.Encode(summary)
	default:
		return fmt.Errorf("unsupported output %q", output)
	}
}

func normalizeOptions(opts Options) Options {
	if strings.TrimSpace(opts.BaseURL) == "" {
		opts.BaseURL = DefaultBaseURL
	}
	if strings.TrimSpace(opts.CasesPath) == "" {
		opts.CasesPath = DefaultCasesPath
	}
	if opts.Timeout <= 0 {
		opts.Timeout = DefaultTimeout
	}
	if opts.Retries < 0 {
		opts.Retries = 0
	}
	if strings.TrimSpace(opts.Output) == "" {
		opts.Output = "table"
	}

	return opts
}

func validateCase(tc GoldenCase) error {
	if strings.TrimSpace(tc.Name) == "" {
		return errors.New("name is required")
	}
	if tc.BookID <= 0 {
		return errors.New("book_id must be positive")
	}
	if strings.TrimSpace(tc.Question) == "" {
		return errors.New("question is required")
	}
	if tc.MaxCitations < 0 {
		return errors.New("max_citations must not be negative")
	}
	if tc.MaxTreeLLMCalls < 0 {
		return errors.New("max_tree_llm_calls must not be negative")
	}

	return nil
}

func buildRequest(ctx context.Context, baseURL string, tc GoldenCase) (*http.Request, error) {
	endpoint, err := endpointURL(baseURL, tc)
	if err != nil {
		return nil, err
	}

	maxCitations := tc.MaxCitations
	if maxCitations <= 0 {
		maxCitations = 5
	}
	body := map[string]any{
		"question":      tc.Question,
		"stream":        false,
		"include_trace": true,
		"max_citations": maxCitations,
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	return req, nil
}

func endpointURL(baseURL string, tc GoldenCase) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return "", fmt.Errorf("parse base url: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("base url must include scheme and host: %q", baseURL)
	}

	parsed.Path = strings.TrimRight(parsed.Path, "/") + fmt.Sprintf("/v1/books/%d/rag", tc.BookID)
	query := parsed.Query()
	lang := strings.TrimSpace(tc.Lang)
	if lang == "" {
		lang = "id"
	}
	query.Set("lang", lang)
	parsed.RawQuery = query.Encode()

	return parsed.String(), nil
}

func validateResponse(tc GoldenCase, resp ragResponse, strictAnswer bool) ([]string, []string) {
	var errs []string
	var warnings []string
	if resp.BookID != 0 && resp.BookID != tc.BookID {
		errs = append(errs, fmt.Sprintf("book_id=%d, want %d", resp.BookID, tc.BookID))
	}

	if tc.ExpectNotFound {
		if len(resp.Citations) != 0 {
			errs = append(errs, fmt.Sprintf("expected no citations, got %d", len(resp.Citations)))
		}
		if !looksNotFound(resp.Answer) {
			errs = append(errs, "expected not-found answer")
		}
		return errs, warnings
	}

	if len(resp.Citations) == 0 {
		errs = append(errs, "expected at least one citation")
	}

	if tc.RequireUnitCitations {
		for i := range resp.Citations {
			citation := &resp.Citations[i]
			if citation.UnitID == nil || strings.TrimSpace(*citation.UnitID) == "" ||
				citation.UnitAnchor == nil || strings.TrimSpace(*citation.UnitAnchor) == "" {
				errs = append(errs, fmt.Sprintf("citation[%d] missing unit_id/unit_anchor", i))
			}
		}
	}
	if len(tc.ExpectedHeadingIDs) > 0 && !anyCitationHeading(resp.Citations, tc.ExpectedHeadingIDs) {
		errs = append(errs, fmt.Sprintf("expected citation heading in %v", tc.ExpectedHeadingIDs))
	}
	if len(tc.ExpectedPageIDs) > 0 && !anyCitationPage(resp.Citations, tc.ExpectedPageIDs) {
		errs = append(errs, fmt.Sprintf("expected citation page in %v", tc.ExpectedPageIDs))
	}
	for _, needle := range tc.AnswerMustContain {
		if !containsFold(resp.Answer, needle) {
			message := fmt.Sprintf("answer missing %q", needle)
			if strictAnswer {
				errs = append(errs, message)
			} else {
				warnings = append(warnings, message)
			}
		}
	}
	for _, needle := range tc.QuoteMustContain {
		if !anyQuoteContains(resp.Citations, needle) {
			errs = append(errs, fmt.Sprintf("citation quote missing %q", needle))
		}
	}
	if tc.ExpectedRetrievalMode != "" {
		if resp.Trace == nil {
			errs = append(errs, "missing trace")
		} else if resp.Trace.RetrievalMode != tc.ExpectedRetrievalMode {
			errs = append(errs, fmt.Sprintf("retrieval_mode=%q, want %q", resp.Trace.RetrievalMode, tc.ExpectedRetrievalMode))
		}
	}
	if tc.MaxTreeLLMCalls > 0 {
		if resp.Trace == nil {
			errs = append(errs, "missing trace")
		} else if resp.Trace.TreeLLMCalls > tc.MaxTreeLLMCalls {
			errs = append(errs, fmt.Sprintf("tree_llm_calls=%d, max %d", resp.Trace.TreeLLMCalls, tc.MaxTreeLLMCalls))
		}
	}

	return errs, warnings
}

func anyCitationHeading(citations []Citation, ids []int) bool {
	allowed := intSet(ids)
	for _, citation := range citations {
		if _, ok := allowed[citation.HeadingID]; ok {
			return true
		}
	}

	return false
}

func anyCitationPage(citations []Citation, ids []int) bool {
	allowed := intSet(ids)
	for _, citation := range citations {
		if _, ok := allowed[citation.PageID]; ok {
			return true
		}
	}

	return false
}

func anyQuoteContains(citations []Citation, needle string) bool {
	for _, citation := range citations {
		if containsFold(citation.Quote, needle) {
			return true
		}
	}

	return false
}

func intSet(values []int) map[int]struct{} {
	result := make(map[int]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}

	return result
}

func containsFold(value, needle string) bool {
	return strings.Contains(strings.ToLower(value), strings.ToLower(needle))
}

func looksNotFound(answer string) bool {
	answer = strings.ToLower(answer)
	phrases := []string{
		"tidak ditemukan",
		"belum menemukan",
		"not found",
		"could not find",
		"لم أجد",
		"لا أجد",
	}
	for _, phrase := range phrases {
		if strings.Contains(answer, phrase) {
			return true
		}
	}

	return false
}

func writeTable(w io.Writer, summary Summary) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "STATUS\tNAME\tBOOK\tMODE\tCALLS\tCITES\tWARN\tATTEMPT\tMS\tDETAIL")
	for _, result := range summary.Results {
		status := "PASS"
		detail := clip(result.Answer, 90)
		if !result.Passed {
			status = "FAIL"
			if result.Error != "" {
				detail = result.Error
			} else {
				detail = strings.Join(result.Errors, "; ")
			}
		} else if len(result.Warnings) > 0 {
			status = "WARN"
			detail = strings.Join(result.Warnings, "; ")
		}
		mode := ""
		calls := 0
		if result.Trace != nil {
			mode = result.Trace.RetrievalMode
			calls = result.Trace.TreeLLMCalls
		}
		fmt.Fprintf(
			tw,
			"%s\t%s\t%d\t%s\t%d\t%d\t%d\t%d\t%d\t%s\n",
			status,
			result.Name,
			result.BookID,
			mode,
			calls,
			len(result.Citations),
			len(result.Warnings),
			result.Attempt,
			result.DurationMS,
			detail,
		)
	}
	fmt.Fprintf(
		tw,
		"SUMMARY\tpassed=%d failed=%d warnings=%d total=%d duration_ms=%d\t\t\t\t\t\t\t\n",
		summary.Passed,
		summary.Failed,
		summary.Warnings,
		summary.Total,
		summary.DurationMS,
	)

	return tw.Flush()
}

func clip(value string, maxRunes int) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}

	return string(runes[:maxRunes]) + "..."
}
