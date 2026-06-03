package persistent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	sq "github.com/Masterminds/squirrel"
	"github.com/evrone/go-clean-template/internal/contentlang"
	"github.com/evrone/go-clean-template/internal/entity"
	"github.com/evrone/go-clean-template/internal/quranutil"
	"github.com/evrone/go-clean-template/internal/readerutil"
	"github.com/evrone/go-clean-template/internal/repo"
	"github.com/evrone/go-clean-template/pkg/postgres"
	"github.com/jackc/pgx/v5"
)

const (
	defaultQuranTranslationSourceID  = "kemenag-id-translation"
	defaultIDTransliterationSourceID = "kemenag-id-latin"
	defaultENTransliterationSourceID = "local-en-syllables-transliteration"
)

// QuranRepo provides Quran browse/search queries.
type QuranRepo struct {
	*postgres.Postgres
}

// NewQuranRepo creates a Quran repository.
func NewQuranRepo(pg *postgres.Postgres) *QuranRepo {
	return &QuranRepo{pg}
}

// ListSurahs returns imported surahs in mushaf order.
func (r *QuranRepo) ListSurahs(ctx context.Context, lang string, includeInfo bool) ([]entity.QuranSurah, error) {
	rows, err := r.Pool.Query(ctx, quranSurahSelectSQL("\nORDER BY s.surah_id ASC", includeInfo), lang)
	if err != nil {
		return nil, fmt.Errorf("QuranRepo - ListSurahs - Query: %w", err)
	}
	defer rows.Close()

	surahs := make([]entity.QuranSurah, 0, 114)
	for rows.Next() {
		surah, err := scanQuranSurah(rows)
		if err != nil {
			return nil, fmt.Errorf("QuranRepo - ListSurahs - scanQuranSurah: %w", err)
		}

		surahs = append(surahs, surah)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("QuranRepo - ListSurahs - rows.Err: %w", err)
	}

	return surahs, nil
}

// GetSurah returns one imported surah with language-specific info.
func (r *QuranRepo) GetSurah(ctx context.Context, surahID int, lang string) (entity.QuranSurah, error) {
	surah, err := scanQuranSurah(r.Pool.QueryRow(ctx, quranSurahSelectSQL(`
WHERE s.surah_id = $2`, true), lang, surahID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.QuranSurah{}, entity.ErrQuranSurahNotFound
		}

		return entity.QuranSurah{}, fmt.Errorf("QuranRepo - GetSurah - scanQuranSurah: %w", err)
	}

	return surah, nil
}

// ListRecitations returns imported recitation resources with track coverage.
func (r *QuranRepo) ListRecitations(ctx context.Context) ([]entity.QuranRecitation, error) {
	rows, err := r.Pool.Query(ctx, `
SELECT r.id,
       r.name,
       r.reciter_name,
       r.style,
       r.mode,
       r.source_url,
       r.qul_resource_id,
       r.format,
       r.license_status,
       r.checksum,
       r.metadata,
       r.imported_at,
       r.updated_at,
       COUNT(t.recitation_id)::int AS track_count,
       COUNT(NULLIF(t.public_url, ''))::int AS public_track_count,
       COUNT(
           CASE
               WHEN COALESCE(NULLIF(t.public_url, ''), NULLIF(t.audio_url, '')) IS NOT NULL THEN 1
           END
       )::int AS playable_track_count
FROM quran_recitations r
LEFT JOIN quran_audio_tracks t ON t.recitation_id = r.id
GROUP BY r.id
ORDER BY r.mode ASC, r.name ASC, r.id ASC`)
	if err != nil {
		return nil, fmt.Errorf("QuranRepo - ListRecitations - Query: %w", err)
	}
	defer rows.Close()

	recitations := make([]entity.QuranRecitation, 0)
	for rows.Next() {
		recitation, err := scanQuranRecitation(rows)
		if err != nil {
			return nil, fmt.Errorf("QuranRepo - ListRecitations - scanQuranRecitation: %w", err)
		}

		recitations = append(recitations, recitation)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("QuranRepo - ListRecitations - rows.Err: %w", err)
	}

	markDefaultRecitation(recitations)

	return recitations, nil
}

// ListTranslationSources returns imported Quran translation sources for a language.
func (r *QuranRepo) ListTranslationSources(ctx context.Context, lang string) ([]entity.QuranTranslationSource, error) {
	rows, err := r.Pool.Query(ctx, `
WITH ayah_total AS (
    SELECT COUNT(*)::int AS total FROM quran_ayahs
),
source_counts AS (
    SELECT source_id, COUNT(*)::int AS translated_ayahs
    FROM quran_ayah_translations
    GROUP BY source_id
),
ranked AS (
    SELECT s.id,
           s.lang,
           s.name,
           s.translator,
           s.source_url,
           s.qul_resource_id,
           s.format,
           s.license_status,
           s.checksum,
           s.metadata,
           s.imported_at,
           s.updated_at,
           COALESCE(sc.translated_ayahs, 0)::int AS translated_ayahs,
           at.total,
           CASE
               WHEN at.total = 0 THEN 0::float8
               ELSE ROUND((COALESCE(sc.translated_ayahs, 0)::numeric * 100 / at.total::numeric), 2)::float8
           END AS coverage_percent,
           ROW_NUMBER() OVER (
               PARTITION BY s.lang
               ORDER BY CASE WHEN s.lang = 'id' AND s.id = $2 THEN 0 ELSE 1 END,
                        COALESCE(sc.translated_ayahs, 0) DESC,
                        s.name ASC,
                        s.id ASC
           ) AS default_rank
    FROM quran_translation_sources s
    CROSS JOIN ayah_total at
    LEFT JOIN source_counts sc ON sc.source_id = s.id
    WHERE s.lang = $1
)
SELECT id,
       lang,
       name,
       translator,
       source_url,
       qul_resource_id,
       format,
       license_status,
       checksum,
       metadata,
       imported_at,
       updated_at,
       translated_ayahs,
       total,
       coverage_percent,
       default_rank = 1 AS is_default
FROM ranked
ORDER BY is_default DESC, translated_ayahs DESC, name ASC, id ASC`,
		lang,
		defaultQuranTranslationSourceID,
	)
	if err != nil {
		return nil, fmt.Errorf("QuranRepo - ListTranslationSources - Query: %w", err)
	}
	defer rows.Close()

	sources := make([]entity.QuranTranslationSource, 0)
	for rows.Next() {
		source, err := scanQuranTranslationSource(rows)
		if err != nil {
			return nil, fmt.Errorf("QuranRepo - ListTranslationSources - scanQuranTranslationSource: %w", err)
		}

		sources = append(sources, source)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("QuranRepo - ListTranslationSources - rows.Err: %w", err)
	}

	return sources, nil
}

// ListNavigationSegments returns Quran juz or hizb boundaries from imported ayah metadata.
func (r *QuranRepo) ListNavigationSegments(
	ctx context.Context,
	kind string,
	lang string,
) ([]entity.QuranNavigationSegment, error) {
	column, err := quranNavigationColumn(kind)
	if err != nil {
		return nil, err
	}

	query := fmt.Sprintf(`
WITH segments AS (
    SELECT a.%[1]s AS number,
           COUNT(*)::int AS ayah_count
    FROM quran_ayahs a
    WHERE a.%[1]s IS NOT NULL
    GROUP BY a.%[1]s
)
SELECT $2::text AS kind,
       seg.number::int,
       seg.ayah_count,
       start_ayah.surah_id,
       start_ayah.ayah_number,
       start_ayah.ayah_key,
       start_ayah.surah_name,
       end_ayah.surah_id,
       end_ayah.ayah_number,
       end_ayah.ayah_key,
       end_ayah.surah_name
FROM segments seg
JOIN LATERAL (
    SELECT a.surah_id,
           a.ayah_number,
           a.ayah_key,
           CASE
               WHEN $1 = 'ar' THEN COALESCE(s.name_arabic, s.name_latin, s.name_translation)
               ELSE COALESCE(si.surah_name, s.name_translation, s.name_latin, s.name_arabic)
           END AS surah_name
    FROM quran_ayahs a
    JOIN quran_surahs s ON s.surah_id = a.surah_id
    LEFT JOIN quran_surah_infos si ON si.surah_id = s.surah_id AND si.lang = $1
    WHERE a.%[1]s = seg.number
    ORDER BY a.surah_id ASC, a.ayah_number ASC
    LIMIT 1
) start_ayah ON true
JOIN LATERAL (
    SELECT a.surah_id,
           a.ayah_number,
           a.ayah_key,
           CASE
               WHEN $1 = 'ar' THEN COALESCE(s.name_arabic, s.name_latin, s.name_translation)
               ELSE COALESCE(si.surah_name, s.name_translation, s.name_latin, s.name_arabic)
           END AS surah_name
    FROM quran_ayahs a
    JOIN quran_surahs s ON s.surah_id = a.surah_id
    LEFT JOIN quran_surah_infos si ON si.surah_id = s.surah_id AND si.lang = $1
    WHERE a.%[1]s = seg.number
    ORDER BY a.surah_id DESC, a.ayah_number DESC
    LIMIT 1
) end_ayah ON true
ORDER BY seg.number ASC`, column)

	rows, err := r.Pool.Query(ctx, query, lang, kind)
	if err != nil {
		return nil, fmt.Errorf("QuranRepo - ListNavigationSegments - Query: %w", err)
	}
	defer rows.Close()

	segments := make([]entity.QuranNavigationSegment, 0)
	for rows.Next() {
		segment, err := scanQuranNavigationSegment(rows)
		if err != nil {
			return nil, fmt.Errorf("QuranRepo - ListNavigationSegments - scanQuranNavigationSegment: %w", err)
		}

		segments = append(segments, segment)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("QuranRepo - ListNavigationSegments - rows.Err: %w", err)
	}

	return segments, nil
}

// GetAyah returns one ayah by ayah_key.
func (r *QuranRepo) GetAyah(
	ctx context.Context,
	ayahKey string,
	lang string,
	translationSource string,
	includeAudio bool,
	recitationID string,
) (entity.QuranAyah, error) {
	resolvedSourceID, err := r.resolveTranslationSourceID(ctx, lang, translationSource)
	if err != nil {
		return entity.QuranAyah{}, err
	}
	includeTranslation := !contentlang.IsArabic(lang) && resolvedSourceID != ""
	resolvedTransliterationSourceID, err := r.defaultTransliterationSourceID(ctx, lang)
	if err != nil {
		return entity.QuranAyah{}, err
	}
	includeTransliteration := !contentlang.IsArabic(lang) && resolvedTransliterationSourceID != ""

	ayah, err := scanQuranAyah(r.Pool.QueryRow(ctx, quranAyahSelectSQL(`
WHERE a.ayah_key = $1`, includeTranslation, includeTransliteration), ayahKey, lang, resolvedSourceID, resolvedTransliterationSourceID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.QuranAyah{}, entity.ErrQuranAyahNotFound
		}

		return entity.QuranAyah{}, fmt.Errorf("QuranRepo - GetAyah - scanQuranAyah: %w", err)
	}

	if includeAudio {
		audioByAyah, err := r.audioTracksForAyahs(ctx, []string{ayah.AyahKey}, recitationID)
		if err != nil {
			return entity.QuranAyah{}, err
		}
		ayah.Audio = audioByAyah[ayah.AyahKey]
	}
	applyQuranAyahMetadata(&ayah, lang, true, includeAudio)

	return ayah, nil
}

// ListSurahAyahs returns all ayahs or an ayah range for one surah.
func (r *QuranRepo) ListSurahAyahs(
	ctx context.Context,
	surahID int,
	fromAyah int,
	toAyah int,
	lang string,
	translationSource string,
	includeTranslation bool,
	includeAudio bool,
	recitationID string,
) ([]entity.QuranAyah, error) {
	if err := r.ensureQuranSurah(ctx, surahID); err != nil {
		return nil, err
	}

	resolvedSourceID := ""
	if includeTranslation || translationSource != "" {
		sourceID, err := r.resolveTranslationSourceID(ctx, lang, translationSource)
		if err != nil {
			return nil, err
		}
		resolvedSourceID = sourceID
	}
	includeSelectedTranslation := includeTranslation && !contentlang.IsArabic(lang) && resolvedSourceID != ""
	resolvedTransliterationSourceID, err := r.defaultTransliterationSourceID(ctx, lang)
	if err != nil {
		return nil, err
	}
	includeTransliteration := !contentlang.IsArabic(lang) && resolvedTransliterationSourceID != ""

	args := []any{surahID, lang, resolvedSourceID, resolvedTransliterationSourceID}
	conditions := []string{"a.surah_id = $1"}
	if fromAyah > 0 {
		args = append(args, fromAyah)
		conditions = append(conditions, fmt.Sprintf("a.ayah_number >= $%d", len(args)))
	}
	if toAyah > 0 {
		args = append(args, toAyah)
		conditions = append(conditions, fmt.Sprintf("a.ayah_number <= $%d", len(args)))
	}
	where := "\nWHERE " + strings.Join(conditions, " AND ") + "\nORDER BY a.ayah_number ASC"

	rows, err := r.Pool.Query(ctx, quranAyahSelectSQL(where, includeSelectedTranslation, includeTransliteration), args...)
	if err != nil {
		return nil, fmt.Errorf("QuranRepo - ListSurahAyahs - Query: %w", err)
	}
	defer rows.Close()

	ayahs := make([]entity.QuranAyah, 0)
	ayahKeys := make([]string, 0)
	for rows.Next() {
		ayah, err := scanQuranAyah(rows)
		if err != nil {
			return nil, fmt.Errorf("QuranRepo - ListSurahAyahs - scanQuranAyah: %w", err)
		}

		ayahs = append(ayahs, ayah)
		ayahKeys = append(ayahKeys, ayah.AyahKey)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("QuranRepo - ListSurahAyahs - rows.Err: %w", err)
	}

	if includeAudio && len(ayahKeys) > 0 {
		audioByAyah, err := r.audioTracksForAyahs(ctx, ayahKeys, recitationID)
		if err != nil {
			return nil, err
		}
		for i := range ayahs {
			ayahs[i].Audio = audioByAyah[ayahs[i].AyahKey]
		}
	}
	for i := range ayahs {
		applyQuranAyahMetadata(&ayahs[i], lang, includeTranslation, includeAudio)
	}

	return ayahs, nil
}

// ListNavigationAyahs returns ayahs inside one Quran juz or hizb segment.
func (r *QuranRepo) ListNavigationAyahs(
	ctx context.Context,
	kind string,
	number int,
	lang string,
	translationSource string,
	includeTranslation bool,
	includeAudio bool,
	recitationID string,
) ([]entity.QuranAyah, error) {
	column, err := quranNavigationColumn(kind)
	if err != nil {
		return nil, err
	}

	resolvedSourceID := ""
	if includeTranslation || translationSource != "" {
		sourceID, err := r.resolveTranslationSourceID(ctx, lang, translationSource)
		if err != nil {
			return nil, err
		}
		resolvedSourceID = sourceID
	}
	includeSelectedTranslation := includeTranslation && !contentlang.IsArabic(lang) && resolvedSourceID != ""
	resolvedTransliterationSourceID, err := r.defaultTransliterationSourceID(ctx, lang)
	if err != nil {
		return nil, err
	}
	includeTransliteration := !contentlang.IsArabic(lang) && resolvedTransliterationSourceID != ""

	rows, err := r.Pool.Query(
		ctx,
		quranAyahSelectSQL(fmt.Sprintf(`
WHERE a.%s = $1
ORDER BY a.surah_id ASC, a.ayah_number ASC`, column), includeSelectedTranslation, includeTransliteration),
		number,
		lang,
		resolvedSourceID,
		resolvedTransliterationSourceID,
	)
	if err != nil {
		return nil, fmt.Errorf("QuranRepo - ListNavigationAyahs - Query: %w", err)
	}
	defer rows.Close()

	ayahs := make([]entity.QuranAyah, 0)
	ayahKeys := make([]string, 0)
	for rows.Next() {
		ayah, err := scanQuranAyah(rows)
		if err != nil {
			return nil, fmt.Errorf("QuranRepo - ListNavigationAyahs - scanQuranAyah: %w", err)
		}

		ayahs = append(ayahs, ayah)
		ayahKeys = append(ayahKeys, ayah.AyahKey)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("QuranRepo - ListNavigationAyahs - rows.Err: %w", err)
	}
	if len(ayahs) == 0 {
		return nil, entity.ErrQuranNavigationNotFound
	}

	if includeAudio {
		audioByAyah, err := r.audioTracksForAyahs(ctx, ayahKeys, recitationID)
		if err != nil {
			return nil, err
		}
		for i := range ayahs {
			ayahs[i].Audio = audioByAyah[ayahs[i].AyahKey]
		}
	}
	for i := range ayahs {
		applyQuranAyahMetadata(&ayahs[i], lang, includeTranslation, includeAudio)
	}

	return ayahs, nil
}

// SearchAyahs searches Arabic Quran text, selected translation text, and requested transliteration text.
func (r *QuranRepo) SearchAyahs(
	ctx context.Context,
	filter repo.QuranSearchFilter,
) ([]entity.QuranSearchResult, int, error) {
	query := strings.TrimSpace(filter.Query)
	if query == "" {
		return []entity.QuranSearchResult{}, 0, nil
	}

	searchQuery := query
	if normalized := quranutil.NormalizeKey(query); normalized != "" {
		searchQuery = normalized
	}
	resolvedSourceID, err := r.resolveTranslationSourceID(ctx, filter.Lang, filter.TranslationSource)
	if err != nil {
		return nil, 0, err
	}
	resolvedTransliterationSourceID, err := r.defaultTransliterationSourceID(ctx, filter.Lang)
	if err != nil {
		return nil, 0, err
	}

	like := "%" + searchQuery + "%"
	countSQL := `
SELECT COUNT(*)
FROM quran_ayahs a
LEFT JOIN quran_ayah_translations t
       ON t.surah_id = a.surah_id
      AND t.ayah_number = a.ayah_number
      AND t.lang = $2
      AND t.source_id = $3
LEFT JOIN quran_ayah_transliterations tn
       ON tn.surah_id = a.surah_id
      AND tn.ayah_number = a.ayah_number
      AND tn.lang = $2
      AND tn.source_id = $4
LEFT JOIN LATERAL (
    SELECT mt.source_id,
           mt.lang,
           mt.text
    FROM quran_ayah_translations mt
    WHERE mt.surah_id = a.surah_id
      AND mt.ayah_number = a.ayah_number
      AND (
          COALESCE(mt.text, '') ILIKE $5
          OR similarity(COALESCE(mt.text, ''), $1) > 0.18
      )
    ORDER BY similarity(COALESCE(mt.text, ''), $1) DESC,
             mt.lang ASC,
             mt.source_id ASC
    LIMIT 1
) mt ON true
WHERE COALESCE(a.search_text, '') ILIKE $5
   OR COALESCE(a.text_qpc_hafs, '') ILIKE $5
   OR COALESCE(t.text, '') ILIKE $5
   OR COALESCE(tn.text, '') ILIKE $5
   OR mt.source_id IS NOT NULL
   OR similarity(COALESCE(a.search_text, ''), $1) > 0.18
   OR similarity(COALESCE(a.text_qpc_hafs, ''), $1) > 0.18
   OR similarity(COALESCE(t.text, ''), $1) > 0.18
   OR similarity(COALESCE(tn.text, ''), $1) > 0.18`

	var total int
	if err := r.Pool.QueryRow(ctx, countSQL, searchQuery, filter.Lang, resolvedSourceID, resolvedTransliterationSourceID, like).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("QuranRepo - SearchAyahs - count: %w", err)
	}

	rows, err := r.Pool.Query(ctx, quranSearchSQL, searchQuery, filter.Lang, resolvedSourceID, resolvedTransliterationSourceID, like, filter.Limit, filter.Offset)
	if err != nil {
		return nil, 0, fmt.Errorf("QuranRepo - SearchAyahs - Query: %w", err)
	}
	defer rows.Close()

	results := make([]entity.QuranSearchResult, 0, filter.Limit)
	for rows.Next() {
		ayah, score, matchedLang, matchedSourceID, matchedField, err := scanQuranAyahWithScore(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("QuranRepo - SearchAyahs - scanQuranAyahWithScore: %w", err)
		}

		applyQuranAyahMetadata(&ayah, filter.Lang, true, false)
		results = append(results, entity.QuranSearchResult{
			Ayah:            ayah,
			Score:           score,
			MatchedLang:     matchedLang,
			MatchedSourceID: matchedSourceID,
			MatchedField:    matchedField,
		})
	}
	if err = rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("QuranRepo - SearchAyahs - rows.Err: %w", err)
	}

	return results, total, nil
}

// ListBookQuranReferences returns linked Quran citations for a published kitab.
func (r *QuranRepo) ListBookQuranReferences(
	ctx context.Context,
	filter repo.QuranBookReferenceFilter,
) ([]entity.BookQuranReference, int, error) {
	if err := r.ensureQuranPublishedBook(ctx, filter.BookID); err != nil {
		return nil, 0, err
	}

	countBuilder := r.Builder.
		Select("COUNT(*)").
		From("quran_book_references qbr").
		Where(sq.Eq{"qbr.book_id": filter.BookID})
	dataBuilder := r.Builder.
		Select(quranBookReferenceColumns()...).
		From("quran_book_references qbr").
		Where(sq.Eq{"qbr.book_id": filter.BookID}).
		OrderBy("qbr.page_id ASC", "qbr.created_at ASC").
		Limit(filter.Limit).
		Offset(filter.Offset)
	if filter.Status != "" && filter.Status != "all" {
		countBuilder = countBuilder.Where(sq.Eq{"qbr.review_status": filter.Status})
		dataBuilder = dataBuilder.Where(sq.Eq{"qbr.review_status": filter.Status})
	}

	total, err := r.countQuran(ctx, countBuilder)
	if err != nil {
		return nil, 0, fmt.Errorf("QuranRepo - ListBookQuranReferences - count: %w", err)
	}

	sqlText, args, err := dataBuilder.ToSql()
	if err != nil {
		return nil, 0, fmt.Errorf("QuranRepo - ListBookQuranReferences - ToSql: %w", err)
	}
	rows, err := r.Pool.Query(ctx, sqlText, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("QuranRepo - ListBookQuranReferences - Query: %w", err)
	}
	defer rows.Close()

	references := make([]entity.BookQuranReference, 0, filter.Limit)
	for rows.Next() {
		reference, err := scanBookQuranReference(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("QuranRepo - ListBookQuranReferences - scanBookQuranReference: %w", err)
		}

		references = append(references, reference)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("QuranRepo - ListBookQuranReferences - rows.Err: %w", err)
	}

	for i := range references {
		ref := &references[i]
		if ref.SurahID == nil || ref.FromAyahNumber == nil || ref.ToAyahNumber == nil {
			continue
		}
		ayahs, err := r.ListSurahAyahs(
			ctx,
			*ref.SurahID,
			*ref.FromAyahNumber,
			*ref.ToAyahNumber,
			filter.Lang,
			filter.TranslationSource,
			true,
			false,
			"",
		)
		if err != nil {
			return nil, 0, err
		}
		ref.Ayahs = ayahs
	}

	return references, total, nil
}

// ListMissingQuranAssets returns missing Quran assets for admin queues.
func (r *QuranRepo) ListMissingQuranAssets(
	ctx context.Context,
	filter repo.MissingQuranAssetFilter,
) (entity.EditorialMissingQuranAssets, error) {
	const itemSQL = missingQuranAssetsCTE + `
SELECT asset_type,
       target_lang,
       surah_id,
       surah_name,
       ayah_number,
       ayah_key,
       translation_source_id,
       translation_source_name,
       recitation_id,
       track_type,
       track_key,
       available_langs,
       source_updated_at,
       COUNT(*) OVER() AS total
FROM filtered
ORDER BY asset_type ASC, target_lang ASC, surah_id ASC NULLS FIRST, ayah_number ASC NULLS FIRST, track_key ASC NULLS FIRST
LIMIT $4 OFFSET $5`

	rows, err := r.Pool.Query(
		ctx,
		itemSQL,
		filter.TargetLangs,
		filter.AssetType,
		filter.SurahID,
		filter.Limit,
		filter.Offset,
	)
	if err != nil {
		return entity.EditorialMissingQuranAssets{}, fmt.Errorf("QuranRepo - ListMissingQuranAssets - items: %w", err)
	}
	defer rows.Close()

	result := entity.EditorialMissingQuranAssets{
		Items:  make([]entity.EditorialMissingQuranAsset, 0, filter.Limit),
		Counts: []entity.EditorialMissingQuranAssetCount{},
	}
	for rows.Next() {
		item, total, err := scanEditorialMissingQuranAsset(rows)
		if err != nil {
			return entity.EditorialMissingQuranAssets{}, fmt.Errorf("QuranRepo - ListMissingQuranAssets - scan item: %w", err)
		}

		result.Total = total
		result.Items = append(result.Items, item)
	}
	if err = rows.Err(); err != nil {
		return entity.EditorialMissingQuranAssets{}, fmt.Errorf("QuranRepo - ListMissingQuranAssets - item rows: %w", err)
	}

	const countSQL = missingQuranAssetsCTE + `
SELECT asset_type, target_lang, COUNT(*) AS total
FROM filtered
GROUP BY asset_type, target_lang
ORDER BY asset_type ASC, target_lang ASC`

	countRows, err := r.Pool.Query(ctx, countSQL, filter.TargetLangs, filter.AssetType, filter.SurahID)
	if err != nil {
		return entity.EditorialMissingQuranAssets{}, fmt.Errorf("QuranRepo - ListMissingQuranAssets - counts: %w", err)
	}
	defer countRows.Close()

	for countRows.Next() {
		var count entity.EditorialMissingQuranAssetCount
		if err = countRows.Scan(&count.AssetType, &count.TargetLang, &count.Total); err != nil {
			return entity.EditorialMissingQuranAssets{}, fmt.Errorf("QuranRepo - ListMissingQuranAssets - scan count: %w", err)
		}

		result.Counts = append(result.Counts, count)
	}
	if err = countRows.Err(); err != nil {
		return entity.EditorialMissingQuranAssets{}, fmt.Errorf("QuranRepo - ListMissingQuranAssets - count rows: %w", err)
	}

	return result, nil
}

const missingQuranAssetsCTE = `
WITH target_langs AS (
    SELECT unnest($1::TEXT[]) AS lang
),
missing AS (
    SELECT 'translation_source'::TEXT AS asset_type,
           tl.lang AS target_lang,
           NULL::INT AS surah_id,
           NULL::TEXT AS surah_name,
           NULL::INT AS ayah_number,
           NULL::TEXT AS ayah_key,
           NULL::TEXT AS translation_source_id,
           NULL::TEXT AS translation_source_name,
           NULL::TEXT AS recitation_id,
           NULL::TEXT AS track_type,
           NULL::TEXT AS track_key,
           COALESCE(src.available_langs, ARRAY[]::TEXT[]) AS available_langs,
           now() AS source_updated_at
    FROM target_langs tl
    LEFT JOIN quran_translation_sources exact ON exact.lang = tl.lang
    LEFT JOIN LATERAL (
        SELECT array_agg(DISTINCT lang ORDER BY lang) AS available_langs
        FROM quran_translation_sources
    ) src ON true
    WHERE exact.id IS NULL

    UNION ALL

    SELECT 'surah_info'::TEXT AS asset_type,
           tl.lang AS target_lang,
           s.surah_id,
           COALESCE(s.name_latin, s.name_arabic) AS surah_name,
           NULL::INT AS ayah_number,
           NULL::TEXT AS ayah_key,
           NULL::TEXT AS translation_source_id,
           NULL::TEXT AS translation_source_name,
           NULL::TEXT AS recitation_id,
           NULL::TEXT AS track_type,
           NULL::TEXT AS track_key,
           COALESCE(av.available_langs, ARRAY[]::TEXT[]) AS available_langs,
           s.updated_at AS source_updated_at
    FROM quran_surahs s
    CROSS JOIN target_langs tl
    LEFT JOIN quran_surah_infos si ON si.surah_id = s.surah_id AND si.lang = tl.lang
    LEFT JOIN LATERAL (
        SELECT array_agg(DISTINCT lang ORDER BY lang) AS available_langs
        FROM quran_surah_infos
        WHERE surah_id = s.surah_id
    ) av ON true
    WHERE si.surah_id IS NULL

    UNION ALL

    SELECT 'ayah_translation'::TEXT AS asset_type,
           tl.lang AS target_lang,
           a.surah_id,
           COALESCE(s.name_latin, s.name_arabic) AS surah_name,
           a.ayah_number,
           a.ayah_key,
           NULL::TEXT AS translation_source_id,
           NULL::TEXT AS translation_source_name,
           NULL::TEXT AS recitation_id,
           NULL::TEXT AS track_type,
           NULL::TEXT AS track_key,
           COALESCE(av.available_langs, ARRAY[]::TEXT[]) AS available_langs,
           a.updated_at AS source_updated_at
    FROM quran_ayahs a
    JOIN quran_surahs s ON s.surah_id = a.surah_id
    CROSS JOIN target_langs tl
    LEFT JOIN quran_ayah_translations tr
           ON tr.surah_id = a.surah_id
          AND tr.ayah_number = a.ayah_number
          AND tr.lang = tl.lang
    LEFT JOIN LATERAL (
        SELECT array_agg(DISTINCT lang ORDER BY lang) AS available_langs
        FROM quran_ayah_translations
        WHERE surah_id = a.surah_id
          AND ayah_number = a.ayah_number
    ) av ON true
    WHERE tr.source_id IS NULL

    UNION ALL

    SELECT 'audio_public'::TEXT AS asset_type,
           tl.lang AS target_lang,
           t.surah_id,
           COALESCE(s.name_latin, s.name_arabic) AS surah_name,
           t.ayah_number,
           CASE WHEN t.track_type = 'ayah' THEN t.track_key ELSE NULL::TEXT END AS ayah_key,
           NULL::TEXT AS translation_source_id,
           NULL::TEXT AS translation_source_name,
           t.recitation_id,
           t.track_type,
           t.track_key,
           ARRAY[]::TEXT[] AS available_langs,
           t.updated_at AS source_updated_at
    FROM quran_audio_tracks t
    JOIN quran_surahs s ON s.surah_id = t.surah_id
    CROSS JOIN target_langs tl
    WHERE t.public_url IS NULL
),
filtered AS (
    SELECT *
    FROM missing
    WHERE ($2::TEXT = '' OR asset_type = $2)
      AND ($3::INT IS NULL OR surah_id = $3)
)`

func quranSurahSelectSQL(where string, includeInfo bool) string {
	infoColumns := `
       NULL::text AS lang,
       NULL::text AS surah_name,
       NULL::text AS text_html,
       NULL::text AS short_text,
       NULL::text AS source_name,
       NULL::text AS source_url,
       NULL::text AS qul_resource_id,
       NULL::text AS format,
       NULL::text AS license_status,
       NULL::text AS checksum,
       NULL::jsonb AS info_metadata,
       NULL::timestamptz AS imported_at,
       NULL::timestamptz AS info_updated_at`
	infoJoin := ""
	if includeInfo {
		infoColumns = `
       si.lang,
       si.surah_name,
       si.text_html,
       si.short_text,
       si.source_name,
       si.source_url,
       si.qul_resource_id,
       si.format,
       si.license_status,
       si.checksum,
       si.metadata,
       si.imported_at,
       si.updated_at`
		infoJoin = `
LEFT JOIN quran_surah_infos si ON si.surah_id = s.surah_id AND si.lang = $1`
	}

	return `
SELECT s.surah_id,
       s.name_arabic,
       s.name_latin,
       s.name_translation,
       s.revelation_type,
       s.ayah_count,
       s.metadata,
       s.updated_at,` + infoColumns + `,
       $1::text AS requested_lang,
       COALESCE(av.available_langs, ARRAY[]::TEXT[]) AS available_info_langs
FROM quran_surahs s` + infoJoin + `
LEFT JOIN LATERAL (
    SELECT array_agg(DISTINCT lang ORDER BY lang) AS available_langs
    FROM quran_surah_infos
    WHERE surah_id = s.surah_id
) av ON true` + where
}

func quranNavigationColumn(kind string) (string, error) {
	switch kind {
	case "juz":
		return "juz_number", nil
	case "hizb":
		return "hizb_number", nil
	default:
		return "", entity.ErrInvalidQuranRange
	}
}

const quranSearchSQL = `
WITH scored AS (
SELECT a.surah_id,
       a.ayah_number,
       a.ayah_key,
       a.text_qpc_hafs,
       a.text_imlaei_simple,
       a.search_text,
       a.script_type,
       a.font_family,
       a.page_number,
       a.juz_number,
       a.hizb_number,
       a.metadata AS ayah_metadata,
       a.updated_at AS ayah_updated_at,
       t.source_id,
       t.lang,
       t.text,
       t.footnotes,
       t.chunks,
       t.metadata AS translation_metadata,
       t.updated_at AS translation_updated_at,
       tn.source_id AS transliteration_source_id,
       tn.lang AS transliteration_lang,
       tn.text AS transliteration_text,
       tn.metadata AS transliteration_metadata,
       tn.updated_at AS transliteration_updated_at,
       COALESCE(ta.available_langs, ARRAY[]::TEXT[]) AS available_translation_langs,
       similarity(COALESCE(a.search_text, ''), $1)::float8 AS search_score,
       similarity(COALESCE(a.text_qpc_hafs, ''), $1)::float8 AS arabic_score,
       similarity(COALESCE(t.text, ''), $1)::float8 AS selected_translation_score,
       similarity(COALESCE(tn.text, ''), $1)::float8 AS transliteration_score,
       COALESCE(mt.match_score, 0)::float8 AS any_translation_score,
       mt.lang AS any_translation_lang,
       mt.source_id AS any_translation_source_id
FROM quran_ayahs a
LEFT JOIN quran_ayah_translations t
       ON t.surah_id = a.surah_id
      AND t.ayah_number = a.ayah_number
      AND t.lang = $2
      AND t.source_id = $3
LEFT JOIN quran_ayah_transliterations tn
       ON tn.surah_id = a.surah_id
      AND tn.ayah_number = a.ayah_number
      AND tn.lang = $2
      AND tn.source_id = $4
LEFT JOIN LATERAL (
    SELECT array_agg(DISTINCT lang ORDER BY lang) AS available_langs
    FROM quran_ayah_translations
    WHERE surah_id = a.surah_id
      AND ayah_number = a.ayah_number
) ta ON true
LEFT JOIN LATERAL (
    SELECT mt.source_id,
           mt.lang,
           similarity(COALESCE(mt.text, ''), $1)::float8 AS match_score
    FROM quran_ayah_translations mt
    WHERE mt.surah_id = a.surah_id
      AND mt.ayah_number = a.ayah_number
      AND (
          COALESCE(mt.text, '') ILIKE $5
          OR similarity(COALESCE(mt.text, ''), $1) > 0.18
      )
    ORDER BY match_score DESC, mt.lang ASC, mt.source_id ASC
    LIMIT 1
) mt ON true
WHERE COALESCE(a.search_text, '') ILIKE $5
   OR COALESCE(a.text_qpc_hafs, '') ILIKE $5
   OR COALESCE(t.text, '') ILIKE $5
   OR COALESCE(tn.text, '') ILIKE $5
   OR mt.source_id IS NOT NULL
   OR similarity(COALESCE(a.search_text, ''), $1) > 0.18
   OR similarity(COALESCE(a.text_qpc_hafs, ''), $1) > 0.18
   OR similarity(COALESCE(t.text, ''), $1) > 0.18
   OR similarity(COALESCE(tn.text, ''), $1) > 0.18
)
SELECT surah_id,
       ayah_number,
       ayah_key,
       text_qpc_hafs,
       text_imlaei_simple,
       search_text,
       script_type,
       font_family,
       page_number,
       juz_number,
       hizb_number,
       ayah_metadata,
       ayah_updated_at,
       source_id,
       lang,
       text,
       footnotes,
       chunks,
       translation_metadata,
       translation_updated_at,
       transliteration_source_id,
       transliteration_lang,
       transliteration_text,
       transliteration_metadata,
       transliteration_updated_at,
       available_translation_langs,
       GREATEST(search_score, arabic_score, selected_translation_score, transliteration_score, any_translation_score)::float8 AS score,
       CASE
           WHEN GREATEST(search_score, arabic_score) >= GREATEST(selected_translation_score, transliteration_score, any_translation_score) THEN 'ar'
           WHEN transliteration_score >= GREATEST(selected_translation_score, any_translation_score) AND transliteration_lang IS NOT NULL THEN transliteration_lang
           WHEN selected_translation_score >= any_translation_score AND lang IS NOT NULL THEN lang
           ELSE any_translation_lang
       END AS matched_lang,
       CASE
           WHEN GREATEST(search_score, arabic_score) >= GREATEST(selected_translation_score, transliteration_score, any_translation_score) THEN NULL::text
           WHEN transliteration_score >= GREATEST(selected_translation_score, any_translation_score) AND transliteration_source_id IS NOT NULL THEN transliteration_source_id
           WHEN selected_translation_score >= any_translation_score AND source_id IS NOT NULL THEN source_id
           ELSE any_translation_source_id
       END AS matched_source_id,
       CASE
           WHEN GREATEST(search_score, arabic_score) >= GREATEST(selected_translation_score, transliteration_score, any_translation_score) THEN 'arabic'
           WHEN transliteration_score >= GREATEST(selected_translation_score, any_translation_score) AND transliteration_source_id IS NOT NULL THEN 'transliteration'
           ELSE 'translation'
       END AS matched_field
FROM scored
ORDER BY score DESC, surah_id ASC, ayah_number ASC
LIMIT $6 OFFSET $7`

func quranAyahSelectSQL(where string, includeTranslation bool, includeTransliteration bool) string {
	translationColumns := `
       t.source_id,
       t.lang,
       t.text,
       t.footnotes,
       t.chunks,
       t.metadata,
       t.updated_at`
	translationJoin := `
LEFT JOIN quran_ayah_translations t
       ON t.surah_id = a.surah_id
      AND t.ayah_number = a.ayah_number
      AND t.lang = $2
      AND t.source_id = $3`
	if !includeTranslation {
		translationColumns = `
       NULL::text AS source_id,
       NULL::text AS lang,
       NULL::text AS text,
       NULL::jsonb AS footnotes,
       NULL::jsonb AS chunks,
       NULL::jsonb AS translation_metadata,
       NULL::timestamptz AS translation_updated_at`
		translationJoin = `
LEFT JOIN quran_ayah_translations t
       ON false
      AND t.lang = $2
      AND t.source_id = $3`
	}
	transliterationColumns := `
       tn.source_id AS transliteration_source_id,
       tn.lang AS transliteration_lang,
       tn.text AS transliteration_text,
       tn.metadata AS transliteration_metadata,
       tn.updated_at AS transliteration_updated_at`
	transliterationJoin := `
LEFT JOIN quran_ayah_transliterations tn
       ON tn.surah_id = a.surah_id
      AND tn.ayah_number = a.ayah_number
      AND tn.lang = $2
      AND tn.source_id = $4`
	if !includeTransliteration {
		transliterationColumns = `
       NULL::text AS transliteration_source_id,
       NULL::text AS transliteration_lang,
       NULL::text AS transliteration_text,
       NULL::jsonb AS transliteration_metadata,
       NULL::timestamptz AS transliteration_updated_at`
		transliterationJoin = `
LEFT JOIN quran_ayah_transliterations tn
       ON false
      AND tn.lang = $2
      AND tn.source_id = $4`
	}

	return `
SELECT a.surah_id,
       a.ayah_number,
       a.ayah_key,
       a.text_qpc_hafs,
       a.text_imlaei_simple,
       a.search_text,
       a.script_type,
       a.font_family,
       a.page_number,
       a.juz_number,
       a.hizb_number,
       a.metadata,
       a.updated_at,` + translationColumns + `,
       ` + transliterationColumns + `,
       COALESCE(ta.available_langs, ARRAY[]::TEXT[]) AS available_translation_langs
FROM quran_ayahs a` + translationJoin + transliterationJoin + `
LEFT JOIN LATERAL (
    SELECT array_agg(DISTINCT lang ORDER BY lang) AS available_langs
    FROM quran_ayah_translations
    WHERE surah_id = a.surah_id
      AND ayah_number = a.ayah_number
) ta ON true` + where
}

func (r *QuranRepo) audioTracksForAyahs(
	ctx context.Context,
	ayahKeys []string,
	recitationID string,
) (map[string][]entity.QuranAudioTrack, error) {
	recitationID, err := r.resolveAudioRecitationID(ctx, recitationID)
	if err != nil {
		return nil, err
	}
	if recitationID == "" {
		return map[string][]entity.QuranAudioTrack{}, nil
	}

	rows, err := r.Pool.Query(ctx, `
SELECT t.recitation_id,
       t.track_type,
       t.track_key,
       t.surah_id,
       t.ayah_number,
       t.audio_url,
       t.r2_key,
       t.public_url,
       t.duration_ms,
       t.duration_seconds,
       t.mime_type,
       t.metadata,
       t.updated_at,
       s.segment_index,
       (s.surah_id::text || ':' || s.ayah_number::text) AS segment_ayah_key,
       s.timestamp_from_ms,
       s.timestamp_to_ms,
       s.duration_ms,
       s.metadata
FROM quran_audio_tracks t
LEFT JOIN quran_audio_segments s
       ON s.recitation_id = t.recitation_id
      AND s.track_type = t.track_type
      AND s.track_key = t.track_key
WHERE (
    (t.track_type = 'ayah' AND t.track_key = ANY($1))
	    OR
	    (t.track_type = 'surah' AND (s.surah_id::text || ':' || s.ayah_number::text) = ANY($1))
	)
	  AND t.recitation_id = $2
	ORDER BY t.recitation_id ASC, t.track_type ASC, t.track_key ASC, s.segment_index ASC`,
		ayahKeys,
		recitationID,
	)
	if err != nil {
		return nil, fmt.Errorf("QuranRepo - audioTracksForAyahs - Query: %w", err)
	}
	defer rows.Close()

	grouped := make(map[string]map[string]*entity.QuranAudioTrack)
	for rows.Next() {
		track, mapKey, segment, hasSegment, err := scanQuranAudioTrackRow(rows)
		if err != nil {
			return nil, fmt.Errorf("QuranRepo - audioTracksForAyahs - scanQuranAudioTrackRow: %w", err)
		}
		if mapKey == "" {
			continue
		}
		if grouped[mapKey] == nil {
			grouped[mapKey] = make(map[string]*entity.QuranAudioTrack)
		}
		trackID := track.RecitationID + ":" + track.TrackType + ":" + track.TrackKey
		existing := grouped[mapKey][trackID]
		if existing == nil {
			existing = &track
			grouped[mapKey][trackID] = existing
		}
		if hasSegment {
			existing.Segments = append(existing.Segments, segment)
		}
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("QuranRepo - audioTracksForAyahs - rows.Err: %w", err)
	}

	result := make(map[string][]entity.QuranAudioTrack, len(grouped))
	for ayahKey, tracks := range grouped {
		for _, track := range tracks {
			result[ayahKey] = append(result[ayahKey], *track)
		}
		sort.Slice(result[ayahKey], func(i, j int) bool {
			return quranAudioTrackLess(result[ayahKey][i], result[ayahKey][j])
		})
	}

	return result, nil
}

func (r *QuranRepo) resolveAudioRecitationID(ctx context.Context, recitationID string) (string, error) {
	recitationID = strings.TrimSpace(recitationID)
	if recitationID == "" {
		return r.defaultPlayableRecitationID(ctx)
	}

	var exists bool
	if err := r.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM quran_recitations WHERE id = $1)`, recitationID).
		Scan(&exists); err != nil {
		return "", fmt.Errorf("QuranRepo - resolveAudioRecitationID - QueryRow: %w", err)
	}
	if !exists {
		return "", entity.ErrQuranRecitationNotFound
	}

	return recitationID, nil
}

func (r *QuranRepo) resolveTranslationSourceID(ctx context.Context, lang string, sourceID string) (string, error) {
	sourceID = strings.TrimSpace(sourceID)
	if contentlang.IsArabic(lang) {
		if sourceID == "" {
			return "", nil
		}
		return "", entity.ErrQuranTranslationSourceNotFound
	}

	if sourceID != "" {
		var exists bool
		if err := r.Pool.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM quran_translation_sources
    WHERE id = $1 AND lang = $2
)`, sourceID, lang).Scan(&exists); err != nil {
			return "", fmt.Errorf("QuranRepo - resolveTranslationSourceID - explicit source: %w", err)
		}
		if !exists {
			return "", entity.ErrQuranTranslationSourceNotFound
		}

		return sourceID, nil
	}

	return r.defaultTranslationSourceID(ctx, lang)
}

func (r *QuranRepo) defaultTranslationSourceID(ctx context.Context, lang string) (string, error) {
	var sourceID string
	err := r.Pool.QueryRow(ctx, `
SELECT s.id
FROM quran_translation_sources s
LEFT JOIN (
    SELECT source_id, COUNT(*)::int AS translated_ayahs
    FROM quran_ayah_translations
    GROUP BY source_id
) sc ON sc.source_id = s.id
WHERE s.lang = $1
ORDER BY CASE WHEN s.lang = 'id' AND s.id = $2 THEN 0 ELSE 1 END,
         COALESCE(sc.translated_ayahs, 0) DESC,
         s.name ASC,
         s.id ASC
LIMIT 1`, lang, defaultQuranTranslationSourceID).Scan(&sourceID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}

		return "", fmt.Errorf("QuranRepo - defaultTranslationSourceID - QueryRow: %w", err)
	}

	return sourceID, nil
}

func (r *QuranRepo) defaultTransliterationSourceID(ctx context.Context, lang string) (string, error) {
	if contentlang.IsArabic(lang) {
		return "", nil
	}

	var sourceID string
	err := r.Pool.QueryRow(ctx, `
SELECT s.id
FROM quran_transliteration_sources s
LEFT JOIN (
    SELECT source_id, COUNT(*)::int AS transliterated_ayahs
    FROM quran_ayah_transliterations
    GROUP BY source_id
) sc ON sc.source_id = s.id
WHERE s.lang = $1
ORDER BY CASE
             WHEN s.lang = 'id' AND s.id = $2 THEN 0
             WHEN s.lang = 'en' AND s.id = $3 THEN 0
             ELSE 1
         END,
         COALESCE(sc.transliterated_ayahs, 0) DESC,
         s.name ASC,
         s.id ASC
LIMIT 1`, lang, defaultIDTransliterationSourceID, defaultENTransliterationSourceID).Scan(&sourceID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}

		return "", fmt.Errorf("QuranRepo - defaultTransliterationSourceID - QueryRow: %w", err)
	}

	return sourceID, nil
}

func (r *QuranRepo) defaultPlayableRecitationID(ctx context.Context) (string, error) {
	var recitationID string
	err := r.Pool.QueryRow(ctx, `
SELECT r.id
FROM quran_recitations r
JOIN quran_audio_tracks t ON t.recitation_id = r.id
GROUP BY r.id, r.mode, r.name
HAVING COUNT(t.recitation_id) > 0
   AND COUNT(
       CASE
           WHEN COALESCE(NULLIF(t.public_url, ''), NULLIF(t.audio_url, '')) IS NOT NULL THEN 1
       END
   ) = COUNT(t.recitation_id)
ORDER BY CASE r.mode WHEN 'ayah' THEN 0 WHEN 'surah' THEN 1 ELSE 2 END,
         r.name ASC,
         r.id ASC
LIMIT 1`).Scan(&recitationID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}

		return "", fmt.Errorf("QuranRepo - defaultPlayableRecitationID - QueryRow: %w", err)
	}

	return recitationID, nil
}

func applyQuranAyahMetadata(ayah *entity.QuranAyah, requestedLang string, includeTranslation bool, includeAudio bool) {
	ayah.RequestedLang = requestedLang
	if ayah.AvailableTranslationLangs == nil {
		ayah.AvailableTranslationLangs = []string{}
	}

	hasTranslation := ayah.Translation != nil
	translationMissing := false
	if includeTranslation && !contentlang.IsArabic(requestedLang) && !hasTranslation {
		translationMissing = true
	}
	ayah.TranslationMissing = translationMissing

	translationAvailability := entity.TranslationAvailability(
		requestedLang,
		hasTranslation,
		translationMissing,
		ayah.AvailableTranslationLangs,
	)
	if !includeTranslation && !contentlang.IsArabic(requestedLang) {
		translationAvailability = entity.AvailabilityDecision{
			Action:         entity.AvailabilityActionHideTranslation,
			Reason:         entity.AvailabilityReasonUnavailable,
			RequestedLang:  requestedLang,
			DisplayLang:    contentlang.Arabic,
			IsFallback:     false,
			Missing:        false,
			AvailableLangs: ayah.AvailableTranslationLangs,
		}
	}

	ayah.Availability = entity.QuranAyahAvailability{
		Translation: translationAvailability,
		Audio:       entity.AudioAvailability(requestedLang, includeAudio && len(ayah.Audio) > 0, []string{}),
	}
}

func (r *QuranRepo) ensureQuranSurah(ctx context.Context, surahID int) error {
	var exists bool
	if err := r.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM quran_surahs WHERE surah_id = $1)`, surahID).
		Scan(&exists); err != nil {
		return fmt.Errorf("QuranRepo - ensureQuranSurah - QueryRow: %w", err)
	}
	if !exists {
		return entity.ErrQuranSurahNotFound
	}

	return nil
}

func (r *QuranRepo) ensureQuranPublishedBook(ctx context.Context, bookID int) error {
	var exists bool
	if err := r.Pool.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM books b
    JOIN book_publications p ON p.book_id = b.id AND p.status = 'published'
    WHERE b.id = $1 AND b.is_deleted = false
)`, bookID).Scan(&exists); err != nil {
		return fmt.Errorf("QuranRepo - ensureQuranPublishedBook - QueryRow: %w", err)
	}
	if !exists {
		return entity.ErrBookNotFound
	}

	return nil
}

func quranBookReferenceColumns() []string {
	return []string{
		"qbr.id::text",
		"qbr.book_id",
		"qbr.page_id",
		"qbr.heading_id",
		"qbr.knowledge_mention_id::text",
		"qbr.source_text",
		"qbr.normalized_text",
		"qbr.reference_kind",
		"qbr.surah_id",
		"qbr.from_ayah_number",
		"qbr.to_ayah_number",
		"qbr.from_ayah_key",
		"qbr.to_ayah_key",
		"qbr.match_strategy",
		"qbr.confidence::float8",
		"qbr.review_status",
		"qbr.metadata",
		"qbr.created_at",
		"qbr.updated_at",
	}
}

func (r *QuranRepo) countQuran(ctx context.Context, builder sq.SelectBuilder) (int, error) {
	sqlText, args, err := builder.ToSql()
	if err != nil {
		return 0, fmt.Errorf("building count query: %w", err)
	}

	var total int
	if err = r.Pool.QueryRow(ctx, sqlText, args...).Scan(&total); err != nil {
		return 0, fmt.Errorf("executing count query: %w", err)
	}

	return total, nil
}

func markDefaultRecitation(recitations []entity.QuranRecitation) {
	defaultIndex := -1
	for i := range recitations {
		if !recitations[i].HasPlayableAudio {
			continue
		}
		if defaultIndex == -1 || quranRecitationLess(recitations[i], recitations[defaultIndex]) {
			defaultIndex = i
		}
	}
	if defaultIndex >= 0 {
		recitations[defaultIndex].IsDefault = true
	}
}

func quranRecitationLess(left entity.QuranRecitation, right entity.QuranRecitation) bool {
	leftRank := quranRecitationModeRank(left.Mode)
	rightRank := quranRecitationModeRank(right.Mode)
	if leftRank != rightRank {
		return leftRank < rightRank
	}
	if left.Name != right.Name {
		return left.Name < right.Name
	}

	return left.ID < right.ID
}

func quranRecitationModeRank(mode string) int {
	switch mode {
	case "ayah":
		return 0
	case "surah":
		return 1
	default:
		return 2
	}
}

func quranAudioTrackLess(left entity.QuranAudioTrack, right entity.QuranAudioTrack) bool {
	if left.RecitationID != right.RecitationID {
		return left.RecitationID < right.RecitationID
	}
	leftRank := quranRecitationModeRank(left.TrackType)
	rightRank := quranRecitationModeRank(right.TrackType)
	if leftRank != rightRank {
		return leftRank < rightRank
	}

	return left.TrackKey < right.TrackKey
}

func scanQuranRecitation(row rowScanner) (entity.QuranRecitation, error) {
	var recitation entity.QuranRecitation
	var reciterName sql.NullString
	var style sql.NullString
	var sourceURL sql.NullString
	var resourceID sql.NullString
	var checksum sql.NullString
	var metadata []byte
	var importedAt sql.NullTime

	err := row.Scan(
		&recitation.ID,
		&recitation.Name,
		&reciterName,
		&style,
		&recitation.Mode,
		&sourceURL,
		&resourceID,
		&recitation.Format,
		&recitation.LicenseStatus,
		&checksum,
		&metadata,
		&importedAt,
		&recitation.UpdatedAt,
		&recitation.TrackCount,
		&recitation.PublicTrackCount,
		&recitation.PlayableTrackCount,
	)
	if err != nil {
		return entity.QuranRecitation{}, err
	}

	recitation.ReciterName = nullableString(reciterName)
	recitation.Style = nullableString(style)
	recitation.SourceURL = nullableString(sourceURL)
	recitation.QULResourceID = nullableString(resourceID)
	recitation.Checksum = nullableString(checksum)
	recitation.Metadata = entity.RawJSON(metadata)
	recitation.ImportedAt = nullableTime(importedAt)
	recitation.HasPublicAudio = recitation.PublicTrackCount > 0 && recitation.PublicTrackCount == recitation.TrackCount
	recitation.HasPlayableAudio = recitation.PlayableTrackCount > 0 && recitation.PlayableTrackCount == recitation.TrackCount

	return recitation, nil
}

func scanQuranTranslationSource(row rowScanner) (entity.QuranTranslationSource, error) {
	var source entity.QuranTranslationSource
	var translator sql.NullString
	var sourceURL sql.NullString
	var resourceID sql.NullString
	var checksum sql.NullString
	var metadata []byte
	var importedAt sql.NullTime

	err := row.Scan(
		&source.ID,
		&source.Lang,
		&source.Name,
		&translator,
		&sourceURL,
		&resourceID,
		&source.Format,
		&source.LicenseStatus,
		&checksum,
		&metadata,
		&importedAt,
		&source.UpdatedAt,
		&source.Coverage.TranslatedAyahs,
		&source.Coverage.TotalAyahs,
		&source.Coverage.Percent,
		&source.IsDefault,
	)
	if err != nil {
		return entity.QuranTranslationSource{}, err
	}

	source.Translator = nullableString(translator)
	source.SourceURL = nullableString(sourceURL)
	source.QULResourceID = nullableString(resourceID)
	source.Checksum = nullableString(checksum)
	source.Metadata = entity.RawJSON(metadata)
	source.ImportedAt = nullableTime(importedAt)

	return source, nil
}

func scanQuranSurah(row rowScanner) (entity.QuranSurah, error) {
	var surah entity.QuranSurah
	var nameArabic sql.NullString
	var nameLatin sql.NullString
	var nameTranslation sql.NullString
	var revelationType sql.NullString
	var metadata []byte
	var infoLang sql.NullString
	var infoSurahName sql.NullString
	var infoTextHTML sql.NullString
	var infoShortText sql.NullString
	var infoSourceName sql.NullString
	var infoSourceURL sql.NullString
	var infoResourceID sql.NullString
	var infoFormat sql.NullString
	var infoLicenseStatus sql.NullString
	var infoChecksum sql.NullString
	var infoMetadata []byte
	var infoImportedAt sql.NullTime
	var infoUpdatedAt sql.NullTime
	var requestedLang string
	var availableInfoLangs []string

	err := row.Scan(
		&surah.SurahID,
		&nameArabic,
		&nameLatin,
		&nameTranslation,
		&revelationType,
		&surah.AyahCount,
		&metadata,
		&surah.UpdatedAt,
		&infoLang,
		&infoSurahName,
		&infoTextHTML,
		&infoShortText,
		&infoSourceName,
		&infoSourceURL,
		&infoResourceID,
		&infoFormat,
		&infoLicenseStatus,
		&infoChecksum,
		&infoMetadata,
		&infoImportedAt,
		&infoUpdatedAt,
		&requestedLang,
		&availableInfoLangs,
	)
	if err != nil {
		return entity.QuranSurah{}, err
	}

	surah.NameArabic = nullableString(nameArabic)
	surah.NameLatin = nullableString(nameLatin)
	surah.NameTranslation = nullableString(nameTranslation)
	surah.RevelationType = nullableString(revelationType)
	surah.Metadata = entity.RawJSON(metadata)
	if infoLang.Valid {
		info := &entity.QuranSurahInfo{
			Lang:          infoLang.String,
			SurahName:     nullableString(infoSurahName),
			TextHTML:      readerutil.SanitizeHTML(infoTextHTML.String),
			ShortText:     nullableString(infoShortText),
			SourceName:    infoSourceName.String,
			SourceURL:     nullableString(infoSourceURL),
			QULResourceID: nullableString(infoResourceID),
			Format:        infoFormat.String,
			LicenseStatus: infoLicenseStatus.String,
			Checksum:      nullableString(infoChecksum),
			Metadata:      entity.RawJSON(infoMetadata),
			ImportedAt:    nullableTime(infoImportedAt),
		}
		if infoUpdatedAt.Valid {
			info.UpdatedAt = infoUpdatedAt.Time
		}
		surah.Info = info
	}
	displayLang := contentlang.Arabic
	isFallback := requestedLang != contentlang.Arabic
	if infoLang.Valid {
		displayLang = infoLang.String
		isFallback = false
	}
	fieldLangs := map[string]string{
		"name_arabic":      contentlang.Arabic,
		"name_latin":       contentlang.Arabic,
		"name_translation": displayLang,
		"info":             displayLang,
	}
	surah.Localization = localizationMeta(
		requestedLang,
		displayLang,
		isFallback,
		availableInfoLangs,
		fieldLangs,
	)

	return surah, nil
}

func scanQuranAyah(row rowScanner) (entity.QuranAyah, error) {
	ayah, _, _, _, _, err := scanQuranAyahInternal(row, false)
	return ayah, err
}

func scanQuranAyahWithScore(row rowScanner) (entity.QuranAyah, float64, string, string, string, error) {
	return scanQuranAyahInternal(row, true)
}

func scanQuranNavigationSegment(row rowScanner) (entity.QuranNavigationSegment, error) {
	var segment entity.QuranNavigationSegment
	var startSurahName sql.NullString
	var endSurahName sql.NullString

	err := row.Scan(
		&segment.Kind,
		&segment.Number,
		&segment.AyahCount,
		&segment.Start.SurahID,
		&segment.Start.AyahNumber,
		&segment.Start.AyahKey,
		&startSurahName,
		&segment.End.SurahID,
		&segment.End.AyahNumber,
		&segment.End.AyahKey,
		&endSurahName,
	)
	if err != nil {
		return entity.QuranNavigationSegment{}, err
	}

	segment.Start.SurahName = nullableString(startSurahName)
	segment.End.SurahName = nullableString(endSurahName)

	return segment, nil
}

func scanQuranAyahInternal(row rowScanner, withScore bool) (entity.QuranAyah, float64, string, string, string, error) {
	var ayah entity.QuranAyah
	var textQPCHafs sql.NullString
	var textImlaei sql.NullString
	var searchText sql.NullString
	var scriptType sql.NullString
	var fontFamily sql.NullString
	var pageNumber sql.NullInt64
	var juzNumber sql.NullInt64
	var hizbNumber sql.NullInt64
	var metadata []byte
	var sourceID sql.NullString
	var lang sql.NullString
	var translationText sql.NullString
	var footnotes []byte
	var chunks []byte
	var translationMetadata []byte
	var translationUpdatedAt sql.NullTime
	var transliterationSourceID sql.NullString
	var transliterationLang sql.NullString
	var transliterationText sql.NullString
	var transliterationMetadata []byte
	var transliterationUpdatedAt sql.NullTime
	var availableTranslationLangs []string
	var score sql.NullFloat64
	var matchedLang sql.NullString
	var matchedSourceID sql.NullString
	var matchedField sql.NullString

	dest := []any{
		&ayah.SurahID,
		&ayah.AyahNumber,
		&ayah.AyahKey,
		&textQPCHafs,
		&textImlaei,
		&searchText,
		&scriptType,
		&fontFamily,
		&pageNumber,
		&juzNumber,
		&hizbNumber,
		&metadata,
		&ayah.UpdatedAt,
		&sourceID,
		&lang,
		&translationText,
		&footnotes,
		&chunks,
		&translationMetadata,
		&translationUpdatedAt,
		&transliterationSourceID,
		&transliterationLang,
		&transliterationText,
		&transliterationMetadata,
		&transliterationUpdatedAt,
		&availableTranslationLangs,
	}
	if withScore {
		dest = append(dest, &score, &matchedLang, &matchedSourceID, &matchedField)
	}

	if err := row.Scan(dest...); err != nil {
		return entity.QuranAyah{}, 0, "", "", "", err
	}

	ayah.TextQPCHafs = nullableString(textQPCHafs)
	ayah.TextImlaeiSimple = nullableString(textImlaei)
	ayah.SearchText = nullableString(searchText)
	ayah.ScriptType = nullableString(scriptType)
	ayah.FontFamily = nullableString(fontFamily)
	ayah.PageNumber = nullableInt(pageNumber)
	ayah.JuzNumber = nullableInt(juzNumber)
	ayah.HizbNumber = nullableInt(hizbNumber)
	ayah.Metadata = entity.RawJSON(metadata)
	if sourceID.Valid {
		translation := &entity.QuranTranslation{
			SourceID:  sourceID.String,
			Lang:      lang.String,
			Text:      translationText.String,
			Footnotes: entity.RawJSON(footnotes),
			Chunks:    entity.RawJSON(chunks),
			Metadata:  entity.RawJSON(translationMetadata),
		}
		if translationUpdatedAt.Valid {
			translation.UpdatedAt = translationUpdatedAt.Time
		}
		ayah.Translation = translation
	}
	if transliterationSourceID.Valid {
		transliteration := &entity.QuranTransliteration{
			SourceID: transliterationSourceID.String,
			Lang:     transliterationLang.String,
			Text:     transliterationText.String,
			Metadata: entity.RawJSON(transliterationMetadata),
		}
		if transliterationUpdatedAt.Valid {
			transliteration.UpdatedAt = transliterationUpdatedAt.Time
		}
		ayah.Transliteration = transliteration
	}
	ayah.AvailableTranslationLangs = emptyStringSlice(availableTranslationLangs)

	return ayah, score.Float64, matchedLang.String, matchedSourceID.String, matchedField.String, nil
}

func scanQuranAudioTrackRow(row rowScanner) (
	entity.QuranAudioTrack,
	string,
	entity.QuranAudioSegment,
	bool,
	error,
) {
	var track entity.QuranAudioTrack
	var ayahNumber sql.NullInt64
	var audioURL sql.NullString
	var r2Key sql.NullString
	var publicURL sql.NullString
	var durationMS sql.NullInt64
	var durationSeconds sql.NullInt64
	var mimeType sql.NullString
	var metadata []byte
	var segmentIndex sql.NullInt64
	var segmentAyahKey sql.NullString
	var timestampFrom sql.NullInt64
	var timestampTo sql.NullInt64
	var segmentDuration sql.NullInt64
	var segmentMetadata []byte

	err := row.Scan(
		&track.RecitationID,
		&track.TrackType,
		&track.TrackKey,
		&track.SurahID,
		&ayahNumber,
		&audioURL,
		&r2Key,
		&publicURL,
		&durationMS,
		&durationSeconds,
		&mimeType,
		&metadata,
		&track.UpdatedAt,
		&segmentIndex,
		&segmentAyahKey,
		&timestampFrom,
		&timestampTo,
		&segmentDuration,
		&segmentMetadata,
	)
	if err != nil {
		return entity.QuranAudioTrack{}, "", entity.QuranAudioSegment{}, false, err
	}

	track.AyahNumber = nullableInt(ayahNumber)
	track.AudioURL = nullableString(audioURL)
	track.R2Key = nullableString(r2Key)
	track.PublicURL = nullableString(publicURL)
	track.DurationMS = nullableInt(durationMS)
	track.DurationSeconds = nullableInt(durationSeconds)
	track.MIMEType = nullableString(mimeType)
	track.Metadata = entity.RawJSON(metadata)

	mapKey := track.TrackKey
	var segment entity.QuranAudioSegment
	hasSegment := segmentIndex.Valid && segmentAyahKey.Valid
	if hasSegment {
		mapKey = segmentAyahKey.String
		segment = entity.QuranAudioSegment{
			SegmentIndex:    int(segmentIndex.Int64),
			AyahKey:         segmentAyahKey.String,
			TimestampFromMS: int(timestampFrom.Int64),
			TimestampToMS:   int(timestampTo.Int64),
			DurationMS:      nullableInt(segmentDuration),
			Metadata:        entity.RawJSON(segmentMetadata),
		}
	}

	return track, mapKey, segment, hasSegment, nil
}

func scanEditorialMissingQuranAsset(row rowScanner) (entity.EditorialMissingQuranAsset, int, error) {
	var item entity.EditorialMissingQuranAsset
	var surahID sql.NullInt64
	var surahName sql.NullString
	var ayahNumber sql.NullInt64
	var ayahKey sql.NullString
	var translationSourceID sql.NullString
	var translationSourceName sql.NullString
	var recitationID sql.NullString
	var trackType sql.NullString
	var trackKey sql.NullString
	var availableLangs []string
	var total int

	err := row.Scan(
		&item.AssetType,
		&item.TargetLang,
		&surahID,
		&surahName,
		&ayahNumber,
		&ayahKey,
		&translationSourceID,
		&translationSourceName,
		&recitationID,
		&trackType,
		&trackKey,
		&availableLangs,
		&item.SourceUpdatedAt,
		&total,
	)
	if err != nil {
		return entity.EditorialMissingQuranAsset{}, 0, err
	}

	item.SurahID = nullableInt(surahID)
	item.SurahName = nullableString(surahName)
	item.AyahNumber = nullableInt(ayahNumber)
	item.AyahKey = nullableString(ayahKey)
	item.TranslationSourceID = nullableString(translationSourceID)
	item.TranslationSourceName = nullableString(translationSourceName)
	item.RecitationID = nullableString(recitationID)
	item.TrackType = nullableString(trackType)
	item.TrackKey = nullableString(trackKey)
	item.AvailableLangs = emptyStringSlice(availableLangs)

	return item, total, nil
}

func scanBookQuranReference(row rowScanner) (entity.BookQuranReference, error) {
	var reference entity.BookQuranReference
	var headingID sql.NullInt64
	var knowledgeMentionID sql.NullString
	var surahID sql.NullInt64
	var fromAyah sql.NullInt64
	var toAyah sql.NullInt64
	var fromAyahKey sql.NullString
	var toAyahKey sql.NullString
	var confidence sql.NullFloat64
	var metadata []byte

	err := row.Scan(
		&reference.ID,
		&reference.BookID,
		&reference.PageID,
		&headingID,
		&knowledgeMentionID,
		&reference.SourceText,
		&reference.NormalizedText,
		&reference.ReferenceKind,
		&surahID,
		&fromAyah,
		&toAyah,
		&fromAyahKey,
		&toAyahKey,
		&reference.MatchStrategy,
		&confidence,
		&reference.ReviewStatus,
		&metadata,
		&reference.CreatedAt,
		&reference.UpdatedAt,
	)
	if err != nil {
		return entity.BookQuranReference{}, err
	}

	reference.HeadingID = nullableInt(headingID)
	reference.KnowledgeMentionID = nullableString(knowledgeMentionID)
	reference.SurahID = nullableInt(surahID)
	reference.FromAyahNumber = nullableInt(fromAyah)
	reference.ToAyahNumber = nullableInt(toAyah)
	reference.FromAyahKey = nullableString(fromAyahKey)
	reference.ToAyahKey = nullableString(toAyahKey)
	reference.Confidence = nullableFloat(confidence)
	reference.Metadata = entity.RawJSON(metadata)

	return reference, nil
}

func nullableFloat(value sql.NullFloat64) *float64 {
	if !value.Valid {
		return nil
	}

	return &value.Float64
}
