package importer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Masterminds/squirrel"
	"github.com/alfariesh/surau-backend/internal/anchor"
	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/quranutil"
	"github.com/alfariesh/surau-backend/internal/repo/persistent"
	"github.com/alfariesh/surau-backend/internal/searchtext"
	"github.com/alfariesh/surau-backend/internal/usecase/crossreference"
	"github.com/alfariesh/surau-backend/pkg/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	legacyQuranReferenceKindSurah     = "surah"
	legacyQuranReferenceKindSurahAyah = "surah_ayah"
	legacyQuranReferenceKindQuote     = "quote"
	legacyQuranReferenceKindAmbiguous = "ambiguous"
)

//nolint:gochecknoglobals // compiled patterns and immutable UUID namespace
var (
	explicitSurahAyahRE = regexp.MustCompile(`(?:سورة|سوره)\s+([^:：،؛\n]+?)\s*[:：]\s*(\d+)(?:\s*[-–]\s*(\d+))?`)
	surahOnlyRE         = regexp.MustCompile(`(?:سورة|سوره)\s+([^:：،؛\n]+)`)
	quranReferenceNS    = uuid.NewSHA1(uuid.NameSpaceURL, []byte("https://surau.org/quran-reference"))
)

var errUnmappableLegacyQuranReference = errors.New("legacy Quran reference has no valid pair of Anchors")

var errUnavailableLegacyQuranSource = errors.New("legacy Quran reference source Work is deleted")

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

type quranReferenceBridgeWriter interface {
	BridgeLegacy(
		ctx context.Context,
		ref entity.CrossReference,
		bridge entity.QuranCrossReferenceBridge,
	) (entity.CrossReference, error)
}

// ResolveQuranReferences links quran_reference knowledge mentions to Quran rows.
func ResolveQuranReferences(ctx context.Context, pool *pgxpool.Pool) (resolved, skipped int, err error) {
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

	svc := newCrossReferenceService(pool)

	for _, mention := range mentions {
		resolution, ok := resolveQuranMention(mention.ExtractionText, surahAliases, ayahs)
		if !ok {
			skipped++
			continue
		}

		if writeErr := bridgeResolvedQuranMention(ctx, svc, &mention, &resolution); writeErr != nil {
			return resolved, skipped, writeErr
		}

		resolved++
	}

	return resolved, skipped, nil
}

func bridgeResolvedQuranMention(
	ctx context.Context,
	writer quranReferenceBridgeWriter,
	mention *quranMentionSource,
	resolution *quranReferenceResolution,
) error {
	metadata := map[string]any{"source": "knowledge_mentions"}
	if len(mention.Attributes) > 0 {
		metadata["mention_attributes"] = mention.Attributes
	}

	metadataJSON, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("marshal Quran reference metadata for mention %s: %w", mention.ID, err)
	}

	now := time.Now().UTC()
	id := uuid.NewSHA1(quranReferenceNS, []byte(mention.ID)).String()
	bridge := entity.QuranCrossReferenceBridge{
		ID:                 id,
		BookID:             mention.BookID,
		PageID:             mention.PageID,
		HeadingID:          mention.HeadingID,
		KnowledgeMentionID: &mention.ID,
		SourceText:         mention.ExtractionText,
		NormalizedText:     searchtext.Normalize(mention.ExtractionText),
		ReferenceKind:      resolution.ReferenceKind,
		SurahID:            resolution.SurahID,
		FromAyahNumber:     resolution.FromAyahNumber,
		ToAyahNumber:       resolution.ToAyahNumber,
		FromAyahKey:        resolution.FromAyahKey,
		ToAyahKey:          resolution.ToAyahKey,
		MatchStrategy:      resolution.MatchStrategy,
		Metadata:           entity.RawJSON(metadataJSON),
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	confidence := resolution.Confidence

	ref, err := quranCrossReferenceFromBridge(
		&bridge,
		&confidence,
		resolution.ReviewStatus,
		entity.CrossReferenceOriginResolver,
	)
	if err != nil {
		return fmt.Errorf("build Quran Cross-Reference for mention %s: %w", mention.ID, err)
	}

	if _, err = writer.BridgeLegacy(ctx, ref, bridge); err != nil {
		return fmt.Errorf("dual-write Quran reference for mention %s: %w", mention.ID, err)
	}

	return nil
}

type legacyQuranReference struct {
	Bridge           entity.QuranCrossReferenceBridge
	SourceBookActive bool
	SourceHeadingID  *int
	Confidence       *float64
	ReviewStatus     string
}

// BridgeLegacyQuranReferencesForBook copies every mappable legacy reference
// for one book through the guarded Cross-Reference service. One service call
// atomically upserts quran_book_references, cross_references, and its bridge;
// a retry is therefore safe after a pause or failed process.
func BridgeLegacyQuranReferencesForBook(
	ctx context.Context,
	pool *pgxpool.Pool,
	bookID int,
) (int64, error) {
	legacy, err := loadUnbridgedQuranReferences(ctx, pool, bookID)
	if err != nil {
		return 0, err
	}

	svc := newCrossReferenceService(pool)

	var bridged int64

	for index := range legacy {
		item := &legacy[index]

		skip, sourceErr := legacyQuranSourceDisposition(item)
		if sourceErr != nil {
			return bridged, fmt.Errorf(
				"approved legacy Quran reference %s cannot be bridged: %w",
				item.Bridge.ID,
				sourceErr,
			)
		}

		if skip {
			continue
		}

		anchorProjection := item.Bridge
		anchorProjection.HeadingID = item.SourceHeadingID

		ref, buildErr := quranCrossReferenceFromBridge(
			&anchorProjection,
			item.Confidence,
			item.ReviewStatus,
			entity.CrossReferenceOriginLegacyQuran,
		)
		if buildErr != nil {
			if item.ReviewStatus == entity.CrossReferenceStatusApproved {
				return bridged, fmt.Errorf("approved legacy Quran reference %s cannot be bridged: %w", item.Bridge.ID, buildErr)
			}

			// Non-public legacy rows without a target are retained in the old
			// table for editorial repair; only rows with two valid Anchors enter
			// the content-to-content registry.
			continue
		}

		if _, writeErr := svc.BridgeLegacy(ctx, ref, item.Bridge); writeErr != nil {
			return bridged, fmt.Errorf("bridge legacy Quran reference %s: %w", item.Bridge.ID, writeErr)
		}

		bridged++
	}

	return bridged, nil
}

func legacyQuranSourceDisposition(item *legacyQuranReference) (skip bool, err error) {
	if item.SourceBookActive {
		return false, nil
	}

	if item.ReviewStatus == entity.CrossReferenceStatusApproved {
		return false, errors.Join(errUnmappableLegacyQuranReference, errUnavailableLegacyQuranSource)
	}

	return true, nil
}

// FreezeLegacyQuranReferenceWrites closes the old table to direct DML only
// after the repository has proved every approved row has a bridge. Resolver
// and repair writes continue through the guarded service transaction.
func FreezeLegacyQuranReferenceWrites(ctx context.Context, pool *pgxpool.Pool) error {
	if err := newCrossReferenceService(pool).FreezeLegacyQuranWrites(ctx); err != nil {
		return fmt.Errorf("freeze direct legacy Quran reference writes: %w", err)
	}

	return nil
}

// UnfreezeLegacyQuranReferenceWrites re-opens the compatibility writer only
// for an explicit rollback to the old binary. It uses the same guarded service
// transaction as freeze and is never part of the bridge job.
func UnfreezeLegacyQuranReferenceWrites(ctx context.Context, pool *pgxpool.Pool) error {
	if err := newCrossReferenceService(pool).UnfreezeLegacyQuranWrites(ctx); err != nil {
		return fmt.Errorf("unfreeze direct legacy Quran reference writes: %w", err)
	}

	return nil
}

//nolint:funlen // explicit one-to-one scanner preserves every legacy wire field
func loadUnbridgedQuranReferences(
	ctx context.Context,
	pool *pgxpool.Pool,
	bookID int,
) ([]legacyQuranReference, error) {
	rows, err := pool.Query(ctx, `
SELECT qbr.id::text,
       qbr.book_id,
	   NOT source_book.is_deleted AS source_book_active,
       qbr.page_id,
       qbr.heading_id,
	   CASE WHEN heading.is_deleted = FALSE THEN qbr.heading_id END AS source_heading_id,
       qbr.knowledge_mention_id::text,
       qbr.source_text,
       qbr.normalized_text,
       qbr.reference_kind,
       qbr.surah_id,
       qbr.from_ayah_number,
       qbr.to_ayah_number,
       qbr.from_ayah_key,
       qbr.to_ayah_key,
       qbr.match_strategy,
       qbr.confidence,
       qbr.review_status,
       qbr.metadata,
       qbr.created_at,
       qbr.updated_at
FROM quran_book_references qbr
JOIN books source_book
     ON source_book.id = qbr.book_id
LEFT JOIN book_headings heading
       ON heading.book_id = qbr.book_id AND heading.heading_id = qbr.heading_id
LEFT JOIN quran_cross_reference_bridge bridge
       ON bridge.cross_reference_id = qbr.id
WHERE qbr.book_id = $1
  AND bridge.cross_reference_id IS NULL
ORDER BY qbr.created_at, qbr.id`, bookID)
	if err != nil {
		return nil, fmt.Errorf("load unbridged Quran references for book %d: %w", bookID, err)
	}
	defer rows.Close()

	items := make([]legacyQuranReference, 0)

	for rows.Next() {
		var (
			item            legacyQuranReference
			headingID       sql.NullInt64
			sourceHeadingID sql.NullInt64
			mentionID       sql.NullString
			surahID         sql.NullInt64
			fromAyah        sql.NullInt64
			toAyah          sql.NullInt64
			fromKey         sql.NullString
			toKey           sql.NullString
			confidence      sql.NullFloat64
			metadataJSON    []byte
		)

		if scanErr := rows.Scan(
			&item.Bridge.ID,
			&item.Bridge.BookID,
			&item.SourceBookActive,
			&item.Bridge.PageID,
			&headingID,
			&sourceHeadingID,
			&mentionID,
			&item.Bridge.SourceText,
			&item.Bridge.NormalizedText,
			&item.Bridge.ReferenceKind,
			&surahID,
			&fromAyah,
			&toAyah,
			&fromKey,
			&toKey,
			&item.Bridge.MatchStrategy,
			&confidence,
			&item.ReviewStatus,
			&metadataJSON,
			&item.Bridge.CreatedAt,
			&item.Bridge.UpdatedAt,
		); scanErr != nil {
			return nil, fmt.Errorf("scan unbridged Quran reference for book %d: %w", bookID, scanErr)
		}

		item.Bridge.HeadingID = nullIntPointer(headingID)
		item.SourceHeadingID = nullIntPointer(sourceHeadingID)
		item.Bridge.KnowledgeMentionID = nullStringPointer(mentionID)
		item.Bridge.SurahID = nullIntPointer(surahID)
		item.Bridge.FromAyahNumber = nullIntPointer(fromAyah)
		item.Bridge.ToAyahNumber = nullIntPointer(toAyah)
		item.Bridge.FromAyahKey = nullStringPointer(fromKey)
		item.Bridge.ToAyahKey = nullStringPointer(toKey)
		item.Bridge.Metadata = entity.RawJSON(metadataJSON)

		if confidence.Valid {
			value := confidence.Float64
			item.Confidence = &value
		}

		items = append(items, item)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate unbridged Quran references for book %d: %w", bookID, err)
	}

	return items, nil
}

//nolint:cyclop,funlen,gocyclo // closed legacy-kind mapping plus validation is intentionally explicit
func quranCrossReferenceFromBridge(
	bridge *entity.QuranCrossReferenceBridge,
	confidence *float64,
	reviewStatus string,
	origin string,
) (entity.CrossReference, error) {
	sourceAnchor, err := legacyQuranSourceAnchor(bridge.BookID, bridge.HeadingID)
	if err != nil {
		return entity.CrossReference{}, errors.Join(errUnmappableLegacyQuranReference, err)
	}

	targetAnchor, err := legacyQuranTargetAnchor(bridge)
	if err != nil {
		return entity.CrossReference{}, errors.Join(errUnmappableLegacyQuranReference, err)
	}

	kind := entity.CrossReferenceKindCites

	switch bridge.ReferenceKind {
	case legacyQuranReferenceKindSurah, legacyQuranReferenceKindSurahAyah:
	case legacyQuranReferenceKindQuote:
		kind = entity.CrossReferenceKindQuotes
	case legacyQuranReferenceKindAmbiguous:
		if reviewStatus == entity.CrossReferenceStatusApproved {
			return entity.CrossReference{}, fmt.Errorf(
				"%w: legacy kind ambiguous cannot remain approved",
				errUnmappableLegacyQuranReference,
			)
		}

		reviewStatus = entity.CrossReferenceStatusAmbiguous
	default:
		return entity.CrossReference{}, fmt.Errorf(
			"%w: unsupported reference_kind %q",
			errUnmappableLegacyQuranReference,
			bridge.ReferenceKind,
		)
	}

	metadata := bridge.Metadata
	if len(metadata) == 0 {
		metadata = entity.RawJSON(`{}`)
	}

	evidenceNormalized := bridge.NormalizedText
	if evidenceNormalized == "" {
		evidenceNormalized = searchtext.Normalize(bridge.SourceText)
	}

	originKey := bridge.ID
	if origin == entity.CrossReferenceOriginResolver && bridge.KnowledgeMentionID != nil {
		originKey = *bridge.KnowledgeMentionID
	}

	return entity.CrossReference{
		ID:                   bridge.ID,
		SourceAnchor:         sourceAnchor,
		TargetAnchor:         targetAnchor,
		SourceCorpus:         string(anchor.CorpusKitab),
		TargetCorpus:         string(anchor.CorpusQuran),
		SourceWorkID:         &bridge.BookID,
		TargetQuranSurahID:   bridge.SurahID,
		TargetQuranFromAyah:  bridge.FromAyahNumber,
		TargetQuranToAyah:    bridge.ToAyahNumber,
		Kind:                 kind,
		Method:               entity.CrossReferenceMethodResolver,
		MethodDetail:         entity.CrossReferenceMethodDetail{Strategy: bridge.MatchStrategy},
		Confidence:           confidence,
		ReviewStatus:         reviewStatus,
		EvidenceText:         bridge.SourceText,
		EvidenceNormalized:   evidenceNormalized,
		NormalizationVersion: searchtext.ProfileVersion,
		Origin:               origin,
		OriginKey:            originKey,
		Metadata:             metadata,
		CreatedAt:            bridge.CreatedAt,
		UpdatedAt:            bridge.UpdatedAt,
	}, nil
}

func legacyQuranSourceAnchor(bookID int, headingID *int) (string, error) {
	if headingID != nil && *headingID > 0 {
		point, err := anchor.NewKitabHeading(bookID, *headingID)
		if err != nil {
			return "", err
		}

		return point.String(), nil
	}

	point, err := anchor.NewKitabWork(bookID)
	if err != nil {
		return "", err
	}

	return point.String(), nil
}

//nolint:cyclop,gocyclo // legacy kind and nullable range fields form a deliberately explicit closed matrix
func legacyQuranTargetAnchor(bridge *entity.QuranCrossReferenceBridge) (string, error) {
	if bridge.SurahID == nil {
		return "", errors.New("surah_id is NULL")
	}

	if bridge.ReferenceKind == legacyQuranReferenceKindSurah ||
		(bridge.ReferenceKind == legacyQuranReferenceKindAmbiguous &&
			bridge.FromAyahNumber == nil && bridge.ToAyahNumber == nil) {
		point, err := anchor.NewQuranSurah(*bridge.SurahID)
		if err != nil {
			return "", err
		}

		return point.String(), nil
	}

	if bridge.FromAyahNumber == nil || bridge.ToAyahNumber == nil {
		return "", errors.New("ayah range is incomplete")
	}

	from, err := anchor.NewQuranAyah(*bridge.SurahID, *bridge.FromAyahNumber)
	if err != nil {
		return "", err
	}

	to, err := anchor.NewQuranAyah(*bridge.SurahID, *bridge.ToAyahNumber)
	if err != nil {
		return "", err
	}

	if *bridge.FromAyahNumber == *bridge.ToAyahNumber {
		return from.String(), nil
	}

	rangeAnchor, err := anchor.NewRange(from, to)
	if err != nil {
		return "", err
	}

	return rangeAnchor.String(), nil
}

func newCrossReferenceService(pool *pgxpool.Pool) *crossreference.UseCase {
	pg := &postgres.Postgres{
		Pool:    pool,
		Builder: squirrel.StatementBuilder.PlaceholderFormat(squirrel.Dollar),
	}

	return crossreference.New(persistent.NewCrossReferenceRepo(pg))
}

func nullIntPointer(value sql.NullInt64) *int {
	if !value.Valid {
		return nil
	}

	result := int(value.Int64)

	return &result
}

func nullStringPointer(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}

	result := value.String

	return &result
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
