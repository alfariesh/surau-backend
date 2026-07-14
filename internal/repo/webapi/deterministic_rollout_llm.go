package webapi

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/alfariesh/surau-backend/internal/entity"
)

// DeterministicRolloutLLMClient is a dev-only acceptance client. It selects a
// real heading from the supplied prompt and copies an exact quote from the
// supplied source blocks, so rollout smoke tests exercise the deployed HTTP,
// retrieval, citation, and Anchor paths without an external model dependency.
type DeterministicRolloutLLMClient struct{}

const rolloutRegexpCaptureCount = 2

var (
	rolloutLexicalHeadingRE = regexp.MustCompile(`(?m)^- id=(\d+).*lexical_hint=true`)
	rolloutBlockHeadingRE   = regexp.MustCompile(`(?m)^- id=(\d+)`)
	rolloutJSONHeadingRE    = regexp.MustCompile(`"id"\s*:\s*(\d+)`)
	rolloutFlatHeadingRE    = regexp.MustCompile(`(?m)^- id=(\d+)`)
	rolloutSourceHeaderRE   = regexp.MustCompile(`(?m)^\[(\d+)\] heading_id=`)
)

// NewDeterministicRolloutLLMClient creates the isolated rollout acceptance
// client. Config validation prevents this client from running in production or
// alongside background workers.
func NewDeterministicRolloutLLMClient() *DeterministicRolloutLLMClient {
	return &DeterministicRolloutLLMClient{}
}

// Complete returns strict JSON matching either the tree-planner or answer
// contract used by Book-RAG.
func (c *DeterministicRolloutLLMClient) Complete(
	ctx context.Context,
	messages []entity.RAGChatMessage,
) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	if rolloutIsAnswerPrompt(messages) {
		return rolloutAnswer(messages)
	}

	headingID := rolloutHeadingID(messages)
	if headingID == 0 {
		return `{"thinking":"no candidate heading","node_ids":[],"done":true}`, nil
	}

	return fmt.Sprintf(
		`{"thinking":"deterministic rollout evidence","node_ids":[%d],"done":true}`,
		headingID,
	), nil
}

// Stream satisfies the Book-RAG LLM contract. The public Book-RAG SSE endpoint
// validates a complete answer before emitting application-level events, so a
// single deterministic chunk is sufficient here.
func (c *DeterministicRolloutLLMClient) Stream(
	ctx context.Context,
	messages []entity.RAGChatMessage,
	emit func(string) error,
) error {
	content, err := c.Complete(ctx, messages)
	if err != nil {
		return err
	}

	return emit(content)
}

func rolloutIsAnswerPrompt(messages []entity.RAGChatMessage) bool {
	for i := range messages {
		if messages[i].Role == "system" && strings.Contains(messages[i].Content, "You answer questions") {
			return true
		}
	}

	return false
}

func rolloutHeadingID(messages []entity.RAGChatMessage) int {
	user := rolloutLastMessage(messages)
	for _, expression := range []*regexp.Regexp{
		rolloutLexicalHeadingRE,
		rolloutBlockHeadingRE,
		rolloutJSONHeadingRE,
		rolloutFlatHeadingRE,
	} {
		match := expression.FindStringSubmatch(user)
		if len(match) != rolloutRegexpCaptureCount {
			continue
		}

		headingID, err := strconv.Atoi(match[1])
		if err == nil && headingID > 0 {
			return headingID
		}
	}

	return 0
}

func rolloutAnswer(messages []entity.RAGChatMessage) (string, error) {
	user := rolloutLastMessage(messages)
	question := rolloutBetween(user, "Question:\n", "\n\nSOURCE BLOCKS:\n")

	sources := rolloutAfter(user, "\n\nSOURCE BLOCKS:\n")
	if repairStart := strings.Index(sources, "\nInvalid previous answer:\n"); repairStart >= 0 {
		sources = sources[:repairStart]
	}

	headers := rolloutSourceHeaderRE.FindAllStringSubmatchIndex(sources, -1)
	for i, header := range headers {
		blockEnd := len(sources)
		if i+1 < len(headers) {
			blockEnd = headers[i+1][0]
		}

		block := sources[header[0]:blockEnd]

		quote := ""
		if question != "" && strings.Contains(block, question) {
			quote = question
		} else {
			quote = rolloutFirstSourceLine(block)
		}

		if quote == "" {
			continue
		}

		ref := sources[header[2]:header[3]]
		payload, err := json.Marshal(map[string]any{
			"answer": fmt.Sprintf("Bukti rollout deterministik [%s].", ref),
			"citations": []map[string]string{{
				"ref":   ref,
				"quote": quote,
			}},
		})

		return string(payload), err
	}

	return `{"answer":"Bukti tidak ditemukan dalam source block.","citations":[]}`, nil
}

func rolloutLastMessage(messages []entity.RAGChatMessage) string {
	if len(messages) == 0 {
		return ""
	}

	return messages[len(messages)-1].Content
}

func rolloutBetween(value, prefix, suffix string) string {
	start := strings.Index(value, prefix)
	if start < 0 {
		return ""
	}

	start += len(prefix)

	end := strings.Index(value[start:], suffix)
	if end < 0 {
		return ""
	}

	return strings.TrimSpace(value[start : start+end])
}

func rolloutAfter(value, marker string) string {
	_, after, ok := strings.Cut(value, marker)
	if !ok {
		return ""
	}

	return after
}

func rolloutFirstSourceLine(block string) string {
	source := rolloutAfter(block, "Arabic source:\n")
	for line := range strings.SplitSeq(source, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "Translation aid:" {
			continue
		}

		return line
	}

	return ""
}
