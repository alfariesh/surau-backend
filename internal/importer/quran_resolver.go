package importer

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/evrone/go-clean-template/internal/quranutil"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	explicitSurahAyahRE = regexp.MustCompile(`(?:سورة|سوره)\s+([^:：،؛\n]+?)\s*[:：]\s*(\d+)(?:\s*[-–]\s*(\d+))?`)
	surahOnlyRE         = regexp.MustCompile(`(?:سورة|سوره)\s+([^:：،؛\n]+)`)
)

type quranMentionSource struct {
	ID             string
	BookID         int
	PageID         int
	HeadingID      *int
	ExtractionText string
	Attributes     json.RawMessage
}

type surahLookup struct {
	SurahID   int
	AyahCount int
}

type ayahLookup struct {
	SurahID    int
	AyahNumber int
	AyahKey    string
	Text       string
	SearchText string
}

type quranReferenceResolution struct {
	ReferenceKind  string
	SurahID        *int
	FromAyahNumber *int
	ToAyahNumber   *int
	FromAyahKey    *string
	ToAyahKey      *string
	MatchStrategy  string
	Confidence     float64
	ReviewStatus   string
}

// ResolveQuranReferences links quran_reference knowledge mentions to Quran rows.
func ResolveQuranReferences(ctx context.Context, pool *pgxpool.Pool) (resolved int, skipped int, err error) {
	surahAliases, err := loadSurahAliases(ctx, pool)
	if err != nil {
		return 0, 0, err
	}
	ayahs, err := loadAyahLookups(ctx, pool)
	if err != nil {
		return 0, 0, err
	}
	mentions, err := loadUnresolvedQuranMentions(ctx, pool)
	if err != nil {
		return 0, 0, err
	}

	batch := &pgx.Batch{}
	for _, mention := range mentions {
		resolution, ok := resolveQuranMention(mention.ExtractionText, surahAliases, ayahs)
		if !ok {
			skipped++
			continue
		}

		metadata := map[string]any{"source": "knowledge_mentions"}
		if len(mention.Attributes) > 0 {
			metadata["mention_attributes"] = json.RawMessage(mention.Attributes)
		}
		metadataJSON, _ := json.Marshal(metadata)
		batch.Queue(`
INSERT INTO quran_book_references (
    id, book_id, page_id, heading_id, knowledge_mention_id, source_text,
    normalized_text, reference_kind, surah_id, from_ayah_number, to_ayah_number,
    from_ayah_key, to_ayah_key, match_strategy, confidence, review_status,
    metadata, created_at, updated_at
)
VALUES (
    $1, $2, $3, $4, $5, $6,
    $7, $8, $9, $10, $11,
    $12, $13, $14, $15, $16,
    $17::jsonb, now(), now()
)
ON CONFLICT (knowledge_mention_id) WHERE knowledge_mention_id IS NOT NULL
DO UPDATE SET
    source_text = EXCLUDED.source_text,
    normalized_text = EXCLUDED.normalized_text,
    reference_kind = EXCLUDED.reference_kind,
    surah_id = EXCLUDED.surah_id,
    from_ayah_number = EXCLUDED.from_ayah_number,
    to_ayah_number = EXCLUDED.to_ayah_number,
    from_ayah_key = EXCLUDED.from_ayah_key,
    to_ayah_key = EXCLUDED.to_ayah_key,
    match_strategy = EXCLUDED.match_strategy,
    confidence = EXCLUDED.confidence,
    review_status = EXCLUDED.review_status,
    metadata = EXCLUDED.metadata,
    updated_at = now()`,
			uuid.New().String(),
			mention.BookID,
			mention.PageID,
			mention.HeadingID,
			mention.ID,
			mention.ExtractionText,
			quranutil.NormalizeKey(mention.ExtractionText),
			resolution.ReferenceKind,
			resolution.SurahID,
			resolution.FromAyahNumber,
			resolution.ToAyahNumber,
			resolution.FromAyahKey,
			resolution.ToAyahKey,
			resolution.MatchStrategy,
			resolution.Confidence,
			resolution.ReviewStatus,
			string(metadataJSON),
		)
		resolved++
	}

	if err := execBatch(ctx, pool, batch); err != nil {
		return 0, 0, fmt.Errorf("upsert quran book references: %w", err)
	}

	return resolved, skipped, nil
}

func loadUnresolvedQuranMentions(ctx context.Context, pool *pgxpool.Pool) ([]quranMentionSource, error) {
	rows, err := pool.Query(ctx, `
SELECT m.id,
       m.book_id,
       m.page_id,
       m.heading_id,
       m.extraction_text,
       m.attributes
FROM knowledge_mentions m
LEFT JOIN quran_book_references qbr ON qbr.knowledge_mention_id = m.id
WHERE m.extraction_class = 'quran_reference'
  AND qbr.id IS NULL
ORDER BY m.created_at ASC, m.book_id ASC, m.page_id ASC`)
	if err != nil {
		return nil, fmt.Errorf("load quran mentions: %w", err)
	}
	defer rows.Close()

	mentions := make([]quranMentionSource, 0)
	for rows.Next() {
		var mention quranMentionSource
		var headingID sql.NullInt64
		if err = rows.Scan(
			&mention.ID,
			&mention.BookID,
			&mention.PageID,
			&headingID,
			&mention.ExtractionText,
			&mention.Attributes,
		); err != nil {
			return nil, fmt.Errorf("scan quran mention: %w", err)
		}
		if headingID.Valid {
			value := int(headingID.Int64)
			mention.HeadingID = &value
		}
		mentions = append(mentions, mention)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate quran mentions: %w", err)
	}

	return mentions, nil
}

func loadSurahAliases(ctx context.Context, pool *pgxpool.Pool) (map[string]surahLookup, error) {
	rows, err := pool.Query(ctx, `
SELECT surah_id, ayah_count, name_arabic, name_latin, name_translation, metadata
FROM quran_surahs`)
	if err != nil {
		return nil, fmt.Errorf("load quran surah aliases: %w", err)
	}
	defer rows.Close()

	aliases := make(map[string]surahLookup)
	for rows.Next() {
		var surahID int
		var ayahCount int
		var nameArabic sql.NullString
		var nameLatin sql.NullString
		var nameTranslation sql.NullString
		var metadata json.RawMessage
		if err = rows.Scan(&surahID, &ayahCount, &nameArabic, &nameLatin, &nameTranslation, &metadata); err != nil {
			return nil, fmt.Errorf("scan quran surah alias: %w", err)
		}
		lookup := surahLookup{SurahID: surahID, AyahCount: ayahCount}
		addSurahAlias(aliases, lookup, strconv.Itoa(surahID))
		for _, value := range []sql.NullString{nameArabic, nameLatin, nameTranslation} {
			if value.Valid {
				addSurahAlias(aliases, lookup, value.String)
				addSurahAlias(aliases, lookup, "سورة "+value.String)
			}
		}
		for _, value := range stringValuesFromJSON(metadata) {
			addSurahAlias(aliases, lookup, value)
			addSurahAlias(aliases, lookup, "سورة "+value)
		}
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate quran surah aliases: %w", err)
	}

	return aliases, nil
}

func loadAyahLookups(ctx context.Context, pool *pgxpool.Pool) ([]ayahLookup, error) {
	rows, err := pool.Query(ctx, `
SELECT surah_id, ayah_number, ayah_key, text_qpc_hafs, text_imlaei_simple, search_text
FROM quran_ayahs`)
	if err != nil {
		return nil, fmt.Errorf("load quran ayah lookups: %w", err)
	}
	defer rows.Close()

	ayahs := make([]ayahLookup, 0, 6236)
	for rows.Next() {
		var ayah ayahLookup
		var textQPCHafs sql.NullString
		var textImlaei sql.NullString
		var searchText sql.NullString
		if err = rows.Scan(
			&ayah.SurahID,
			&ayah.AyahNumber,
			&ayah.AyahKey,
			&textQPCHafs,
			&textImlaei,
			&searchText,
		); err != nil {
			return nil, fmt.Errorf("scan quran ayah lookup: %w", err)
		}
		if textQPCHafs.Valid {
			ayah.Text = textQPCHafs.String
		}
		if searchText.Valid {
			ayah.SearchText = searchText.String
		} else if textImlaei.Valid {
			ayah.SearchText = textImlaei.String
		}
		ayahs = append(ayahs, ayah)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate quran ayah lookups: %w", err)
	}

	return ayahs, nil
}

func resolveQuranMention(
	text string,
	surahAliases map[string]surahLookup,
	ayahs []ayahLookup,
) (quranReferenceResolution, bool) {
	if match := explicitSurahAyahRE.FindStringSubmatch(text); len(match) >= 3 {
		if surah, ok := lookupSurah(match[1], surahAliases); ok {
			fromAyah, _ := strconv.Atoi(match[2])
			toAyah := fromAyah
			if len(match) > 3 && strings.TrimSpace(match[3]) != "" {
				toAyah, _ = strconv.Atoi(match[3])
			}
			if fromAyah > 0 && toAyah >= fromAyah && (surah.AyahCount == 0 || toAyah <= surah.AyahCount) {
				fromKey := quranutil.AyahKey(surah.SurahID, fromAyah)
				toKey := quranutil.AyahKey(surah.SurahID, toAyah)
				return quranReferenceResolution{
					ReferenceKind:  "surah_ayah",
					SurahID:        &surah.SurahID,
					FromAyahNumber: &fromAyah,
					ToAyahNumber:   &toAyah,
					FromAyahKey:    &fromKey,
					ToAyahKey:      &toKey,
					MatchStrategy:  "explicit_surah_ayah",
					Confidence:     1.0,
					ReviewStatus:   "approved",
				}, true
			}
		}

		return quranReferenceResolution{}, false
	}

	if match := surahOnlyRE.FindStringSubmatch(text); len(match) >= 2 {
		if surah, ok := lookupSurah(match[1], surahAliases); ok {
			return quranReferenceResolution{
				ReferenceKind: "surah",
				SurahID:       &surah.SurahID,
				MatchStrategy: "explicit_surah",
				Confidence:    0.9,
				ReviewStatus:  "approved",
			}, true
		}
	}

	if ayah, exact, ok := resolveQuote(text, ayahs); ok {
		fromAyah := ayah.AyahNumber
		toAyah := ayah.AyahNumber
		fromKey := ayah.AyahKey
		toKey := ayah.AyahKey
		confidence := 0.8
		strategy := "normalized_quote_substring"
		if exact {
			confidence = 0.85
			strategy = "normalized_quote_exact"
		}

		return quranReferenceResolution{
			ReferenceKind:  "quote",
			SurahID:        &ayah.SurahID,
			FromAyahNumber: &fromAyah,
			ToAyahNumber:   &toAyah,
			FromAyahKey:    &fromKey,
			ToAyahKey:      &toKey,
			MatchStrategy:  strategy,
			Confidence:     confidence,
			ReviewStatus:   "needs_review",
		}, true
	}

	return quranReferenceResolution{}, false
}

func lookupSurah(value string, aliases map[string]surahLookup) (surahLookup, bool) {
	key := quranutil.NormalizeKey(strings.Trim(value, " .،؛:：()[]{}«»\"'"))
	key = strings.TrimPrefix(key, "سورة ")
	key = strings.TrimPrefix(key, "سوره ")
	surah, ok := aliases[key]
	if ok {
		return surah, true
	}

	surah, ok = aliases[quranutil.NormalizeKey("سورة "+key)]
	return surah, ok
}

func resolveQuote(text string, ayahs []ayahLookup) (ayahLookup, bool, bool) {
	query := quranutil.NormalizeKey(text)
	if len([]rune(query)) < 6 {
		return ayahLookup{}, false, false
	}

	var exactMatches []ayahLookup
	var substringMatches []ayahLookup
	for _, ayah := range ayahs {
		for _, candidate := range []string{ayah.Text, ayah.SearchText} {
			normalized := quranutil.NormalizeKey(candidate)
			if normalized == "" {
				continue
			}
			if normalized == query {
				exactMatches = append(exactMatches, ayah)
				break
			}
			if strings.Contains(normalized, query) {
				substringMatches = append(substringMatches, ayah)
				break
			}
		}
	}
	if len(exactMatches) == 1 {
		return exactMatches[0], true, true
	}
	if len(exactMatches) > 1 {
		return ayahLookup{}, false, false
	}
	if len(substringMatches) == 1 {
		return substringMatches[0], false, true
	}

	return ayahLookup{}, false, false
}

func addSurahAlias(aliases map[string]surahLookup, surah surahLookup, value string) {
	key := quranutil.NormalizeKey(value)
	if key == "" {
		return
	}
	key = strings.TrimPrefix(key, "سورة ")
	key = strings.TrimPrefix(key, "سوره ")
	if key != "" {
		aliases[key] = surah
	}
}

func stringValuesFromJSON(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil
	}

	values := make([]string, 0)
	collectStringValues(value, &values)

	return values
}

func collectStringValues(value any, values *[]string) {
	switch typed := value.(type) {
	case string:
		if strings.TrimSpace(typed) != "" {
			*values = append(*values, typed)
		}
	case []any:
		for _, item := range typed {
			collectStringValues(item, values)
		}
	case map[string]any:
		for _, item := range typed {
			collectStringValues(item, values)
		}
	}
}
