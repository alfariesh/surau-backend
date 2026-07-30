//nolint:gocritic,wsl_v5 // Judge inputs are immutable snapshots and every advisory failure remains explicit.
package rageval

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/evalrubric"
)

const (
	humanSamplePercent = 20
	humanSampleMinimum = 2
	judgeMaxBodySize   = 1 << 20
	judgeErrorClipSize = 200
	percentDenominator = 100
	judgeStatusError   = "error"
)

// JudgeResult is advisory and never participates in deterministic GateReport.
type JudgeResult struct {
	RubricVersion string                   `json:"rubric_version"`
	RubricSHA256  string                   `json:"rubric_sha256,omitempty"`
	Status        string                   `json:"status"`
	Passed        bool                     `json:"passed"`
	Reason        string                   `json:"reason,omitempty"`
	Error         string                   `json:"error,omitempty"`
	Inference     *entity.BookRAGInference `json:"inference,omitempty"`
}

// JudgeSummary is the separate advisory pass-rate row.
type JudgeSummary struct {
	RubricVersion string  `json:"rubric_version"`
	Total         int     `json:"total"`
	Passed        int     `json:"passed"`
	Failed        int     `json:"failed"`
	Skipped       int     `json:"skipped"`
	PassRate      float64 `json:"pass_rate"`
}

// HumanSample is the deterministic monthly review packet.
type HumanSample struct {
	Month       string      `json:"month"`
	CaseID      string      `json:"case_id"`
	Question    string      `json:"question"`
	Answer      string      `json:"answer"`
	Citations   []Citation  `json:"citations"`
	Judge       JudgeResult `json:"judge"`
	ReviewState string      `json:"review_state"`
}

//nolint:cyclop,funlen,gocognit,gocyclo // Fail-closed same-origin, rubric, transport, and attribution checks stay adjacent.
func evaluateJudge(
	ctx context.Context,
	baseURL string,
	opts Options,
	tc GoldenCase,
	result CaseResult,
) JudgeResult {
	version := strings.TrimSpace(tc.JudgeRubricVersion)
	if version == "" {
		return JudgeResult{}
	}

	rubric, err := evalrubric.Resolve(version)
	if err != nil {
		return JudgeResult{
			RubricVersion: version, Status: judgeStatusError, Error: err.Error(),
		}
	}

	token := strings.TrimSpace(opts.JudgeToken)
	if token == "" {
		token = strings.TrimSpace(opts.ServiceToken)
	}
	if token == "" {
		return JudgeResult{
			RubricVersion: version,
			RubricSHA256:  rubric.SHA256,
			Status:        "skipped",
			Reason:        "judge service token is not configured",
		}
	}

	endpoint := strings.TrimSpace(opts.JudgeURL)
	baseOrigin, baseParseErr := url.Parse(strings.TrimSpace(baseURL))
	if baseParseErr != nil || baseOrigin.Scheme == "" || baseOrigin.Host == "" {
		return JudgeResult{
			RubricVersion: version, RubricSHA256: rubric.SHA256,
			Status: judgeStatusError, Error: "judge base URL is invalid",
		}
	}
	if endpoint == "" {
		parsed := *baseOrigin
		parsed.Path = strings.TrimRight(parsed.Path, "/") + "/v1/eval/judge"
		parsed.RawQuery = ""
		endpoint = parsed.String()
	}
	judgeEndpoint, parseErr := url.Parse(endpoint)
	if parseErr != nil || !sameHTTPOrigin(judgeEndpoint, baseOrigin) {
		return JudgeResult{
			RubricVersion: version, RubricSHA256: rubric.SHA256,
			Status: judgeStatusError, Error: "judge URL must share the BookRAG base URL origin",
		}
	}

	evidence := make([]entity.EvalJudgeEvidence, 0, len(result.Citations))
	for _, citation := range result.Citations {
		item := entity.EvalJudgeEvidence{
			Ref: citation.Ref, Quote: citation.Quote, Anchor: citation.Anchor,
		}
		if citation.UnitID != nil {
			item.UnitID = *citation.UnitID
		}
		if citation.UnitAnchor != nil {
			item.UnitAnchor = *citation.UnitAnchor
		}
		evidence = append(evidence, item)
	}
	requestBody := entity.EvalJudgeRequest{
		CaseID: tc.Name, RubricVersion: version, Question: tc.Question,
		Answer: result.Answer, Evidence: evidence,
	}
	payload, err := json.Marshal(requestBody)
	if err != nil {
		return JudgeResult{
			RubricVersion: version, RubricSHA256: rubric.SHA256,
			Status: judgeStatusError, Error: fmt.Sprintf("marshal judge request: %v", err),
		}
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return JudgeResult{
			RubricVersion: version, RubricSHA256: rubric.SHA256,
			Status: judgeStatusError, Error: fmt.Sprintf("create judge request: %v", err),
		}
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Internal-Token", token)

	client := &http.Client{Timeout: opts.Timeout}
	response, err := client.Do(request)
	if err != nil {
		return JudgeResult{
			RubricVersion: version, RubricSHA256: rubric.SHA256,
			Status: judgeStatusError, Error: err.Error(),
		}
	}
	defer response.Body.Close()

	body, readErr := io.ReadAll(io.LimitReader(response.Body, judgeMaxBodySize))
	if readErr != nil {
		return JudgeResult{
			RubricVersion: version, RubricSHA256: rubric.SHA256,
			Status: judgeStatusError, Error: readErr.Error(),
		}
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return JudgeResult{
			RubricVersion: version, RubricSHA256: rubric.SHA256,
			Status: judgeStatusError,
			Error: fmt.Sprintf(
				"judge status %d: %s",
				response.StatusCode,
				clip(string(body), judgeErrorClipSize),
			),
		}
	}

	var judged entity.EvalJudgeResponse
	if err = json.Unmarshal(body, &judged); err != nil {
		return JudgeResult{
			RubricVersion: version, RubricSHA256: rubric.SHA256,
			Status: judgeStatusError, Error: fmt.Sprintf("decode judge response: %v", err),
		}
	}
	if judged.RubricVersion != version || judged.RubricSHA256 != rubric.SHA256 {
		return JudgeResult{
			RubricVersion: version, RubricSHA256: rubric.SHA256,
			Status: judgeStatusError, Error: "judge rubric identity mismatch",
		}
	}

	return JudgeResult{
		RubricVersion: version,
		RubricSHA256:  rubric.SHA256,
		Status:        "completed",
		Passed:        judged.Passed,
		Reason:        judged.Reason,
		Inference:     &judged.Inference,
	}
}

func summarizeJudges(results []CaseResult) []JudgeSummary {
	byRubric := make(map[string]*JudgeSummary)
	for _, result := range results {
		if result.Judge == nil || strings.TrimSpace(result.Judge.RubricVersion) == "" {
			continue
		}
		summary := byRubric[result.Judge.RubricVersion]
		if summary == nil {
			summary = &JudgeSummary{RubricVersion: result.Judge.RubricVersion}
			byRubric[result.Judge.RubricVersion] = summary
		}
		if result.Judge.Status != "completed" {
			summary.Skipped++

			continue
		}
		summary.Total++
		if result.Judge.Passed {
			summary.Passed++
		} else {
			summary.Failed++
		}
	}

	versions := make([]string, 0, len(byRubric))
	for version := range byRubric {
		versions = append(versions, version)
	}
	slices.Sort(versions)

	summaries := make([]JudgeSummary, 0, len(versions))
	for _, version := range versions {
		summary := byRubric[version]
		summary.PassRate = percent(summary.Passed, summary.Total)
		summaries = append(summaries, *summary)
	}

	return summaries
}

func selectHumanSamples(results []CaseResult, month string) []HumanSample {
	if strings.TrimSpace(month) == "" {
		month = time.Now().UTC().Format("2006-01")
	}

	type scored struct {
		score  uint64
		result CaseResult
	}
	candidates := make([]scored, 0)
	for _, result := range results {
		if result.Judge == nil || result.Judge.Status != "completed" {
			continue
		}
		sum := sha256.Sum256([]byte(month + "\x00" + result.Name))
		candidates = append(candidates, scored{
			score: binary.BigEndian.Uint64(sum[:8]), result: result,
		})
	}
	slices.SortFunc(candidates, func(left, right scored) int {
		switch {
		case left.score < right.score:
			return -1
		case left.score > right.score:
			return 1
		default:
			return strings.Compare(left.result.Name, right.result.Name)
		}
	})

	count := (len(candidates)*humanSamplePercent + percentDenominator - 1) / percentDenominator
	count = max(count, min(humanSampleMinimum, len(candidates)))

	samples := make([]HumanSample, 0, count)
	for _, candidate := range candidates[:count] {
		samples = append(samples, HumanSample{
			Month: month, CaseID: candidate.result.Name,
			Question: candidate.result.Question, Answer: candidate.result.Answer,
			Citations: candidate.result.Citations, Judge: *candidate.result.Judge,
			ReviewState: "pending_human_review",
		})
	}

	return samples
}

// SelectHumanSamples builds the deterministic monthly advisory review queue.
func SelectHumanSamples(results []CaseResult, month string) []HumanSample {
	return selectHumanSamples(results, month)
}
