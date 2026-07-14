package backfill

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/repo/persistent"
	bookraguc "github.com/alfariesh/surau-backend/internal/usecase/bookrag"
	"github.com/jackc/pgx/v5/pgxpool"
)

type catalogParitySample struct {
	bookID    int
	public    bool
	headingID int
	pageID    int
	quote     string
	unitID    string
	anchor    string
}

type catalogParityCandidate struct {
	bookID    int
	headingID int
	pageID    int
	unitText  string
	pageText  string
}

type catalogParityLLM struct {
	headingID int
	pageID    int
	quote     string
	calls     int
}

// A catalog proof quote is selected to occur in exactly one eligible unit at
// its legacy book/heading/page locator. One retrieved page is therefore
// sufficient to exercise the real retrieval, quote validator, dual
// projection, and Anchor resolver. Keeping the context at one also prevents
// the acceptance gate from issuing hundreds of progressively weaker token
// searches merely to fill unused context slots.
const catalogParityMaxContextPages = 1

const (
	catalogParityCandidatesPerBook = 64
	catalogParityLegacyHits        = 10
	catalogParityQuoteRunes        = 256
	catalogParityPageSourceRunes   = 4000
	catalogParityUnitSourceRunes   = 4000
	catalogParityMinimumQuoteRunes = 4
	catalogParityWindowDivisor     = 2
	catalogParityQuotesPerWindow   = 8
)

type catalogParityVerification struct {
	target                int64
	verified              int64
	mismatches            int64
	unresolved            int64
	samplesMissing        int64
	denialMismatches      int64
	requestMismatches     int64
	locatorMismatches     int64
	missingSampleBooks    []int
	denialMismatchBooks   []int
	requestMismatchBooks  []int
	locatorMismatchBooks  []int
	unresolvedAnchorBooks []int
}

var (
	errCatalogParityStreamUnsupported = errors.New("catalog parity stub does not stream")
	catalogParitySourceRefRE          = regexp.MustCompile(`(?m)^\[(\d+)\] heading_id=`)
)

//nolint:cyclop,gocognit,gocyclo,nestif // The deterministic stub intentionally parses the same nested source-block shape as Book-RAG.
func (l *catalogParityLLM) Complete(_ context.Context, messages []entity.RAGChatMessage) (string, error) {
	l.calls++

	if len(messages) > 0 && strings.Contains(messages[0].Content, "You answer questions") {
		ref := ""

		if len(messages) > 1 {
			sourceBlocks := messages[len(messages)-1].Content
			if start := strings.Index(sourceBlocks, "SOURCE BLOCKS:\n"); start >= 0 {
				sourceBlocks = sourceBlocks[start+len("SOURCE BLOCKS:\n"):]
			}

			headers := catalogParitySourceRefRE.FindAllStringSubmatchIndex(sourceBlocks, -1)
			for i, match := range headers {
				blockEnd := len(sourceBlocks)
				if i+1 < len(headers) {
					blockEnd = headers[i+1][0]
				}

				headerEnd := strings.IndexByte(sourceBlocks[match[0]:blockEnd], '\n')
				if headerEnd < 0 {
					continue
				}

				header := sourceBlocks[match[0] : match[0]+headerEnd]

				block := sourceBlocks[match[0]:blockEnd]
				if strings.Contains(header, " page_id="+strconv.Itoa(l.pageID)+" ") && strings.Contains(block, l.quote) {
					ref = sourceBlocks[match[2]:match[3]]

					break
				}
			}
		}

		if ref == "" {
			return `{"answer":"Bukti tidak ditemukan dalam source block.","citations":[]}`, nil
		}

		payload, err := json.Marshal(map[string]any{
			"answer": fmt.Sprintf("Bukti katalog [%s].", ref),
			"citations": []map[string]string{{
				"ref":   ref,
				"quote": l.quote,
			}},
		})

		return string(payload), err
	}

	return fmt.Sprintf(`{"thinking":"deterministic full-catalog parity stub","node_ids":[%d],"done":true}`,
		l.headingID), nil
}

func (l *catalogParityLLM) Stream(
	context.Context,
	[]entity.RAGChatMessage,
	func(string) error,
) error {
	return errCatalogParityStreamUnsupported
}

// verifyFullCatalogBookRAGParity runs one deterministic dual-mode request for
// every publicly retrievable published book. The LLM is a local stub: the
// proof exercises real tree/search/page retrieval, exact quote validation,
// dual projection, legacy locator preservation, and Anchor resolution without
// spending tokens or allowing model variance into the rollout gate.
//
//nolint:cyclop,funlen,gocognit,gocyclo // This acceptance verifier keeps every parity condition visible in one auditable loop.
func verifyFullCatalogBookRAGParity(
	ctx context.Context,
	pool *pgxpool.Pool,
	ragRepo *persistent.BookRAGRepo,
	anchorRepo *persistent.AnchorRepo,
) (catalogParityVerification, error) {
	result := catalogParityVerification{}

	samples, missingSampleBooks, target, err := loadCatalogParitySamples(ctx, pool, ragRepo)
	if err != nil {
		return result, err
	}

	result.target = target
	result.missingSampleBooks = missingSampleBooks
	result.samplesMissing = int64(len(missingSampleBooks))
	result.mismatches += result.samplesMissing

	for _, sample := range samples {
		llm := &catalogParityLLM{headingID: sample.headingID, pageID: sample.pageID, quote: sample.quote}
		if !sample.public {
			uc := bookraguc.New(ragRepo, llm, bookraguc.Options{
				CitationMode:   bookraguc.CitationModeUnit,
				LegacyFallback: true,
			})

			response, denialErr := uc.AskBook(ctx, sample.bookID, "denied catalog probe", "id", 1, true)
			if !errors.Is(denialErr, entity.ErrBookNotFound) || len(response.Citations) != 0 || llm.calls != 0 {
				result.mismatches++
				result.denialMismatches++
				result.denialMismatchBooks = append(result.denialMismatchBooks, sample.bookID)

				continue
			}

			result.verified++

			continue
		}

		uc := bookraguc.New(ragRepo, llm, bookraguc.Options{
			CitationMode:    bookraguc.CitationModeDual,
			LegacyFallback:  false,
			MaxContextPages: catalogParityMaxContextPages,
		})

		response, askErr := uc.AskBook(ctx, sample.bookID, sample.quote, "id", 1, true)
		if askErr != nil || len(response.Citations) != 1 {
			result.mismatches++
			result.requestMismatches++
			result.requestMismatchBooks = append(result.requestMismatchBooks, sample.bookID)

			continue
		}

		citation := response.Citations[0]

		expectedURL := fmt.Sprintf("/v1/books/%d/toc/%d/read?lang=id", sample.bookID, sample.headingID)
		if citation.BookID != sample.bookID || citation.HeadingID != sample.headingID ||
			citation.PageID != sample.pageID || citation.Quote != sample.quote ||
			citation.Anchor != fmt.Sprintf("toc-%d", sample.headingID) || citation.URL != expectedURL ||
			citation.UnitID == nil || *citation.UnitID != sample.unitID ||
			citation.UnitAnchor == nil || *citation.UnitAnchor != sample.anchor {
			result.mismatches++
			result.locatorMismatches++
			result.locatorMismatchBooks = append(result.locatorMismatchBooks, sample.bookID)

			continue
		}

		resolution, resolveErr := anchorRepo.ResolveCanonicalUnit(ctx, sample.anchor)
		if resolveErr != nil || resolution.CycleDetected || resolution.Status != entity.UnitLifecycleActive ||
			len(resolution.ActiveRecords) != 1 || resolution.ActiveRecords[0].UnitID == nil ||
			*resolution.ActiveRecords[0].UnitID != sample.unitID {
			result.unresolved++
			result.unresolvedAnchorBooks = append(result.unresolvedAnchorBooks, sample.bookID)

			continue
		}

		result.verified++
	}

	return result, nil
}

//nolint:cyclop,funlen,gocyclo // Row decoding deliberately distinguishes denied books from incomplete public samples.
func loadCatalogParitySamples(
	ctx context.Context,
	pool *pgxpool.Pool,
	ragRepo *persistent.BookRAGRepo,
) ([]catalogParitySample, []int, int64, error) {
	rows, err := pool.Query(ctx, `
WITH target AS (
    SELECT book.id,
           public_publication.book_id IS NOT NULL AS is_public
    FROM book_publications publication
    JOIN books book ON book.id = publication.book_id AND book.is_deleted = FALSE
    LEFT JOIN public_book_publications public_publication ON public_publication.book_id = book.id
    WHERE publication.status = 'published'
      AND ($1::integer[] IS NULL OR book.id = ANY($1))
)
SELECT target.id,
       target.is_public,
       candidate.heading_id,
       candidate.page_id,
       candidate.unit_text,
       candidate.page_text
FROM target
LEFT JOIN LATERAL (
    SELECT unit.heading_id,
           unit.page_id,
           unit.text AS unit_text,
           COALESCE(edit.content_text, page.content_text) AS page_text,
           heading.depth AS heading_depth,
           unit.position AS unit_position,
           unit.ordinal AS unit_ordinal
    FROM public_book_interpretive_citable_units unit
    JOIN book_pages page
      ON page.book_id = unit.book_id AND page.page_id = unit.page_id AND page.is_deleted = FALSE
    JOIN book_headings heading
      ON heading.book_id = unit.book_id AND heading.heading_id = unit.heading_id AND heading.is_deleted = FALSE
    JOIN book_heading_ranges heading_range
      ON heading_range.book_id = heading.book_id AND heading_range.heading_id = heading.heading_id
    LEFT JOIN book_page_edits edit
      ON edit.book_id = page.book_id AND edit.page_id = page.page_id AND edit.status = 'published'
    WHERE target.is_public
      AND unit.book_id = target.id
      AND unit.content_role = 'book_page'
      AND unit.heading_id IS NOT NULL
      AND unit.page_id IS NOT NULL
    ORDER BY unit.page_id, heading.depth DESC, unit.position, unit.ordinal
    LIMIT $2
) candidate ON TRUE
ORDER BY target.id,
         candidate.page_id,
         candidate.heading_depth DESC,
         candidate.unit_position,
         candidate.unit_ordinal`,
		CitableCatalogBookIDs, catalogParityCandidatesPerBook)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("verify Book-RAG parity samples: %w", err)
	}
	defer rows.Close()

	targets := make([]catalogParitySample, 0)
	candidates := make(map[int][]catalogParityCandidate)
	seenTargets := make(map[int]struct{})

	for rows.Next() {
		var (
			sample             catalogParitySample
			headingID, pageID  *int
			unitText, pageText *string
		)
		if err := rows.Scan(&sample.bookID, &sample.public, &headingID, &pageID, &unitText, &pageText); err != nil {
			return nil, nil, int64(len(targets)), fmt.Errorf("verify Book-RAG parity samples scan: %w", err)
		}

		if _, exists := seenTargets[sample.bookID]; !exists {
			seenTargets[sample.bookID] = struct{}{}
			targets = append(targets, sample)
		}

		if !sample.public || headingID == nil || pageID == nil || unitText == nil || pageText == nil {
			continue
		}

		candidates[sample.bookID] = append(candidates[sample.bookID], catalogParityCandidate{
			bookID:    sample.bookID,
			headingID: *headingID,
			pageID:    *pageID,
			unitText:  *unitText,
			pageText:  *pageText,
		})
	}

	if err := rows.Err(); err != nil {
		return nil, nil, int64(len(targets)), fmt.Errorf("verify Book-RAG parity samples rows: %w", err)
	}

	rows.Close()

	samples := make([]catalogParitySample, 0, len(targets))
	missingSampleBooks := make([]int, 0)
	for i := range targets {
		targetItem := targets[i]
		if !targetItem.public {
			samples = append(samples, targetItem)

			continue
		}

		for _, candidate := range candidates[targetItem.bookID] {
			quote, locator, resolveErr := catalogParityCandidateQuote(ctx, ragRepo, candidate)
			if resolveErr != nil {
				return nil, nil, int64(len(targets)), resolveErr
			}

			if !locator.Found {
				continue
			}

			targetItem.headingID = candidate.headingID
			targetItem.pageID = candidate.pageID
			targetItem.quote = quote
			targetItem.unitID = locator.UnitID
			targetItem.anchor = locator.UnitAnchor
			samples = append(samples, targetItem)

			break
		}

		if len(samples) == 0 || samples[len(samples)-1].bookID != targetItem.bookID {
			missingSampleBooks = append(missingSampleBooks, targetItem.bookID)
		}
	}

	return samples, missingSampleBooks, int64(len(targets)), nil
}

func catalogParityCandidateQuote(
	ctx context.Context,
	ragRepo *persistent.BookRAGRepo,
	candidate catalogParityCandidate,
) (string, entity.RAGUnitLocator, error) {
	unitRunes := []rune(candidate.unitText)
	if len(unitRunes) > catalogParityUnitSourceRunes {
		unitRunes = unitRunes[:catalogParityUnitSourceRunes]
	}

	for window := min(catalogParityQuoteRunes, len(unitRunes)); window >= catalogParityMinimumQuoteRunes; window /= 2 {
		accepted := 0

		step := max(1, window/catalogParityWindowDivisor)
		for start := 0; start < len(unitRunes) && accepted < catalogParityQuotesPerWindow; start += step {
			end := min(start+window, len(unitRunes))
			quote := strings.TrimSpace(string(unitRunes[start:end]))
			quoteRunes := len([]rune(quote))

			if quoteRunes < catalogParityMinimumQuoteRunes || quoteRunes > catalogParityQuoteRunes ||
				!catalogParityQuoteVisibleInLegacySource(candidate, quote) {
				if end == len(unitRunes) {
					break
				}

				continue
			}

			locator, err := ragRepo.ResolveRAGUnitCitation(
				ctx, candidate.bookID, candidate.headingID, candidate.pageID, quote,
			)
			if err != nil {
				return "", entity.RAGUnitLocator{}, fmt.Errorf(
					"verify Book-RAG parity locator book %d page %d: %w",
					candidate.bookID, candidate.pageID, err,
				)
			}

			accepted++

			if locator.Found {
				legacyHits, searchErr := ragRepo.SearchRAGPages(
					ctx, candidate.bookID, quote, "id", catalogParityLegacyHits,
				)
				if searchErr != nil {
					return "", entity.RAGUnitLocator{}, fmt.Errorf(
						"verify Book-RAG parity legacy search book %d page %d: %w",
						candidate.bookID, candidate.pageID, searchErr,
					)
				}

				if catalogParityLegacyFirstHitMatches(legacyHits, candidate) {
					return quote, locator, nil
				}
			}

			if end == len(unitRunes) {
				break
			}
		}

		if window == catalogParityMinimumQuoteRunes {
			break
		}
	}

	return "", entity.RAGUnitLocator{}, nil
}

func catalogParityQuoteVisibleInLegacySource(candidate catalogParityCandidate, quote string) bool {
	pageRunes := []rune(candidate.pageText)
	if len(pageRunes) > catalogParityPageSourceRunes {
		pageRunes = pageRunes[:catalogParityPageSourceRunes]
	}

	pagePrefix := string(pageRunes)

	return strings.Contains(pagePrefix, quote)
}

func catalogParityLegacyFirstHitMatches(
	hits []entity.RAGSearchResult,
	candidate catalogParityCandidate,
) bool {
	return len(hits) > 0 &&
		hits[0].HeadingID == candidate.headingID &&
		hits[0].PageID == candidate.pageID
}
