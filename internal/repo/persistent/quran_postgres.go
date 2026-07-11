package persistent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	sq "github.com/Masterminds/squirrel"
	"github.com/alfariesh/surau-backend/internal/contentlang"
	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/quranutil"
	"github.com/alfariesh/surau-backend/internal/readerutil"
	"github.com/alfariesh/surau-backend/internal/repo"
	"github.com/alfariesh/surau-backend/pkg/postgres"
	"github.com/jackc/pgx/v5"
)

const (
	defaultQuranTranslationSourceID  = "kemenag-id-translation"
	defaultIDTransliterationSourceID = "kemenag-id-latin"
	defaultENTransliterationSourceID = "local-en-syllables-transliteration"

	quranTrackTypeAyah  = "ayah"
	quranTrackTypeSurah = "surah"
)

const quranAudioTracksForSurahSQL = `
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
WHERE t.recitation_id = $1
  AND t.surah_id = $2
  AND t.track_type = $3
ORDER BY t.track_type ASC, t.surah_id ASC, t.ayah_number ASC NULLS FIRST, s.segment_index ASC`

const quranAyahSelectHeadSQL = `
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
       a.updated_at,`

const quranAyahTranslationColumnsSQL = `
       t.source_id,
       t.lang,
       t.text,
       t.footnotes,
       t.chunks,
       t.metadata,
       t.updated_at`

const quranAyahTranslationNullColumnsSQL = `
       NULL::text AS source_id,
       NULL::text AS lang,
       NULL::text AS text,
       NULL::jsonb AS footnotes,
       NULL::jsonb AS chunks,
       NULL::jsonb AS translation_metadata,
       NULL::timestamptz AS translation_updated_at`

const quranAyahTranslationJoinSQL = `
LEFT JOIN quran_ayah_translations t
       ON t.surah_id = a.surah_id
      AND t.ayah_number = a.ayah_number
      AND t.lang = $2
      AND t.source_id = $3`

const quranAyahTranslationDisabledJoinSQL = `
LEFT JOIN quran_ayah_translations t
       ON false
      AND t.lang = $2
      AND t.source_id = $3`

const quranAyahTransliterationColumnsSQL = `
       tn.source_id AS transliteration_source_id,
       tn.lang AS transliteration_lang,
       tn.text AS transliteration_text,
       tn.metadata AS transliteration_metadata,
       tn.updated_at AS transliteration_updated_at`

const quranAyahTransliterationNullColumnsSQL = `
       NULL::text AS transliteration_source_id,
       NULL::text AS transliteration_lang,
       NULL::text AS transliteration_text,
       NULL::jsonb AS transliteration_metadata,
       NULL::timestamptz AS transliteration_updated_at`

const quranAyahTransliterationJoinSQL = `
LEFT JOIN quran_ayah_transliterations tn
       ON tn.surah_id = a.surah_id
      AND tn.ayah_number = a.ayah_number
      AND tn.lang = $2
      AND tn.source_id = $4`

const quranAyahTransliterationDisabledJoinSQL = `
LEFT JOIN quran_ayah_transliterations tn
       ON false
      AND tn.lang = $2
      AND tn.source_id = $4`

const quranAyahAvailabilityColumnsSQL = `,
       COALESCE(ta.available_langs, ARRAY[]::TEXT[]) AS available_translation_langs`

const quranAyahFromSQL = `
FROM quran_ayahs a`

// Per-ayah editorial columns. The shape is CONSTANT (9 columns) across the
// null/light/full variants so scanQuranAyahInternal aligns regardless of flags.
// Heavy HTML/FAQ load only on the single-ayah detail read; list reads keep them
// NULL so a 286-ayah payload stays under the edge cache's MAX_CACHE_BYTES.
const quranAyahEditorialNullColumnsSQL = `,
       NULL::text AS ed_lang,
       NULL::text AS ed_meta_title,
       NULL::text AS ed_meta_description,
       NULL::text AS ed_tafsir_range,
       NULL::text AS ed_license_status,
       NULL::timestamptz AS ed_updated_at,
       NULL::text AS ed_intisari_html,
       NULL::text AS ed_keutamaan_html,
       NULL::jsonb AS ed_faq`

const quranAyahEditorialLightColumnsSQL = `,
       ae.lang AS ed_lang,
       ae.meta_title AS ed_meta_title,
       ae.meta_description AS ed_meta_description,
       ae.tafsir_range AS ed_tafsir_range,
       ae.license_status AS ed_license_status,
       ae.updated_at AS ed_updated_at,
       NULL::text AS ed_intisari_html,
       NULL::text AS ed_keutamaan_html,
       NULL::jsonb AS ed_faq`

const quranAyahEditorialFullColumnsSQL = `,
       ae.lang AS ed_lang,
       ae.meta_title AS ed_meta_title,
       ae.meta_description AS ed_meta_description,
       ae.tafsir_range AS ed_tafsir_range,
       ae.license_status AS ed_license_status,
       ae.updated_at AS ed_updated_at,
       ae.intisari_html AS ed_intisari_html,
       ae.keutamaan_html AS ed_keutamaan_html,
       ae.faq AS ed_faq`

// Only permitted (reviewed) editorial is joined, so drafts never leave the API.
const quranAyahEditorialJoinSQL = `
LEFT JOIN quran_ayah_editorial ae
       ON ae.surah_id = a.surah_id
      AND ae.ayah_number = a.ayah_number
      AND ae.lang = $2
      AND ae.license_status = 'permitted'`

const quranAyahAvailableLangsJoinSQL = `
LEFT JOIN LATERAL (
    SELECT array_agg(DISTINCT lang ORDER BY lang) AS available_langs
    FROM quran_ayah_translations
    WHERE surah_id = a.surah_id
      AND ayah_number = a.ayah_number
) ta ON true`

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
	rows, err := r.Pool.Query(ctx, quranSurahSelectSQL("\nORDER BY s.surah_id ASC", includeInfo, false), lang)
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
WHERE s.surah_id = $2`, true, true), lang, surahID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.QuranSurah{}, entity.ErrQuranSurahNotFound
		}

		return entity.QuranSurah{}, fmt.Errorf("QuranRepo - GetSurah - scanQuranSurah: %w", err)
	}

	return surah, nil
}

// ListRecitations returns imported recitation resources with track coverage.
// quranRecitationSelectColumns is the shared SELECT list feeding scanQuranRecitation.
// ListRecitations and getVisibleRecitation MUST both use it (with quranRecitationFromJoin)
// so the column set — including the coverage_percent CASE — can never drift out of sync
// with the scan targets. Guarded by TestQuranRecitationSelectColumnCountMatchesScan.
const quranRecitationSelectColumns = `r.id,
       r.name,
       COALESCE(NULLIF(r.display_name, ''), NULLIF(r.reciter_name, ''), r.name) AS display_name,
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
       r.sort_order,
       r.default_priority,
       r.is_visible,
       COUNT(t.recitation_id)::int AS track_count,
       COUNT(NULLIF(t.public_url, ''))::int AS public_track_count,
       COUNT(
           CASE
               WHEN COALESCE(NULLIF(t.public_url, ''), NULLIF(t.audio_url, '')) IS NOT NULL THEN 1
           END
       )::int AS playable_track_count,
       COALESCE(sc.segment_count, 0)::int AS segment_count,
       -- Playable coverage over the corpus: playable ayah-tracks / all ayahs (mode
       -- 'ayah') or playable surah-tracks / 114 (mode 'surah'). Kept in lockstep with
       -- defaultPlayableRecitationID so the flagged default matches the served default.
       COALESCE(
           CASE r.mode
               WHEN 'ayah' THEN COUNT(CASE WHEN COALESCE(NULLIF(t.public_url, ''), NULLIF(t.audio_url, '')) IS NOT NULL THEN 1 END)::float8
                                / NULLIF((SELECT COUNT(*) FROM quran_ayahs), 0)
               WHEN 'surah' THEN COUNT(CASE WHEN COALESCE(NULLIF(t.public_url, ''), NULLIF(t.audio_url, '')) IS NOT NULL THEN 1 END)::float8
                                / 114.0
               ELSE 0
           END,
       0)::float8 AS coverage_percent`

// quranRecitationFromJoin is the shared FROM/JOIN for the two recitation reads above.
const quranRecitationFromJoin = `FROM quran_recitations r
LEFT JOIN quran_audio_tracks t ON t.recitation_id = r.id
LEFT JOIN (
    SELECT recitation_id, COUNT(*)::int AS segment_count
    FROM quran_audio_segments
    GROUP BY recitation_id
) sc ON sc.recitation_id = r.id`

func (r *QuranRepo) ListRecitations(ctx context.Context) ([]entity.QuranRecitation, error) {
	rows, err := r.Pool.Query(ctx, `SELECT `+quranRecitationSelectColumns+`
`+quranRecitationFromJoin+`
WHERE r.is_visible = TRUE
GROUP BY r.id, sc.segment_count
ORDER BY r.sort_order ASC,
         COALESCE(NULLIF(r.display_name, ''), NULLIF(r.reciter_name, ''), r.name) ASC,
         r.id ASC`)
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

// GetSurahAudioManifest returns the selected recitation's playable audio manifest for one surah.
func (r *QuranRepo) GetSurahAudioManifest(
	ctx context.Context,
	surahID int,
	recitationID string,
) (entity.QuranSurahAudioManifest, error) {
	if _, err := r.ensureQuranSurah(ctx, surahID); err != nil {
		return entity.QuranSurahAudioManifest{}, err
	}

	recitationID, err := r.resolveAudioRecitationID(ctx, recitationID)
	if err != nil {
		return entity.QuranSurahAudioManifest{}, err
	}

	if recitationID == "" {
		return entity.QuranSurahAudioManifest{}, entity.ErrQuranRecitationNotFound
	}

	recitation, err := r.getVisibleRecitation(ctx, recitationID)
	if err != nil {
		return entity.QuranSurahAudioManifest{}, err
	}

	if defaultID, err := r.defaultPlayableRecitationID(ctx); err == nil && defaultID == recitation.ID {
		recitation.IsDefault = true
	}

	ayahKeys, err := r.surahAyahKeys(ctx, surahID)
	if err != nil {
		return entity.QuranSurahAudioManifest{}, err
	}

	tracks, err := r.audioTracksForSurah(ctx, surahID, &recitation)
	if err != nil {
		return entity.QuranSurahAudioManifest{}, err
	}

	missing, segmentMissing, hasFullSurahAudio := manifestAudioCoverage(ayahKeys, recitation.Mode, tracks)

	return entity.QuranSurahAudioManifest{
		SurahID:                surahID,
		Recitation:             recitation,
		Mode:                   recitation.Mode,
		Tracks:                 tracks,
		HasFullSurahAudio:      hasFullSurahAudio,
		MissingAyahKeys:        missing,
		SegmentMissingAyahKeys: segmentMissing,
	}, nil
}

// ListTranslationSources returns imported Quran translation sources for a language.
func (r *QuranRepo) ListTranslationSources(ctx context.Context, lang string) ([]entity.QuranTranslationSource, error) {
	rows, err := r.Pool.Query(
		ctx, `
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
WHERE a.ayah_key = $1`, includeTranslation, includeTransliteration, true, true), ayahKey, lang, resolvedSourceID, resolvedTransliterationSourceID))
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
	includeEditorial bool,
	recitationID string,
) ([]entity.QuranAyah, error) {
	ayahCount, err := r.ensureQuranSurah(ctx, surahID)
	if err != nil {
		return nil, err
	}

	// Reject a range that starts or ends past the surah's real length. Without this
	// an out-of-range from/to (e.g. from=999 on a 7-ayah surah) returns 200 + an
	// empty list, which reads as "surah has no ayahs" instead of a bad request.
	// from/to == 0 are the "open" sentinels (start / end) and never exceed ayahCount.
	if fromAyah > ayahCount || toAyah > ayahCount {
		return nil, entity.ErrInvalidQuranRange
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

	rows, err := r.Pool.Query(ctx, quranAyahSelectSQL(where, includeSelectedTranslation, includeTransliteration, includeEditorial, false), args...)
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
	includeEditorial bool,
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
ORDER BY a.surah_id ASC, a.ayah_number ASC`, column), includeSelectedTranslation, includeTransliteration, includeEditorial, false),
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

	like := "%" + escapeLike(searchQuery) + "%"

	// Run in a read-only tx so SET LOCAL scopes the trigram threshold to THIS query
	// (pooled connections must not leak session GUCs). The threshold lets the %
	// operator match the previous similarity()>0.18 recall while using the GIN trgm
	// indexes; the total is folded in via COUNT(*) OVER() (single pass, no 2nd query).
	tx, err := r.Pool.Begin(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("QuranRepo - SearchAyahs - begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, "SET LOCAL pg_trgm.similarity_threshold = 0.18"); err != nil {
		return nil, 0, fmt.Errorf("QuranRepo - SearchAyahs - set threshold: %w", err)
	}

	rows, err := tx.Query(ctx, quranSearchSQL, searchQuery, filter.Lang, resolvedSourceID, resolvedTransliterationSourceID, like, filter.Limit, filter.Offset)
	if err != nil {
		return nil, 0, fmt.Errorf("QuranRepo - SearchAyahs - Query: %w", err)
	}
	defer rows.Close()

	total := 0
	results := make([]entity.QuranSearchResult, 0, filter.Limit)

	for rows.Next() {
		ayah, score, matchedLang, matchedSourceID, matchedField, rowTotal, err := scanQuranAyahWithScore(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("QuranRepo - SearchAyahs - scanQuranAyahWithScore: %w", err)
		}

		total = rowTotal

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

	// On a page past the last match, no rows come back, so COUNT(*) OVER() never
	// folds the real total in and it stays 0 — recover it with a count-only pass
	// over the same scored CTE (same tx, so the SET LOCAL threshold still applies).
	if len(results) == 0 && filter.Offset > 0 {
		if err = tx.QueryRow(ctx, quranSearchCountSQL, searchQuery, filter.Lang, resolvedSourceID, resolvedTransliterationSourceID, like).
			Scan(&total); err != nil {
			return nil, 0, fmt.Errorf("QuranRepo - SearchAyahs - count: %w", err)
		}
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

	// During the resumable B-3 backfill, bridged rows come from the generic
	// registry while legacy rows without a bridge remain visible as a fallback.
	// The anti-join makes the cut-over duplicate-free at every checkpoint.
	countBuilder := r.Builder.
		Select("COUNT(*)").
		From(quranBookReferenceProjectionSQL).
		Where(sq.Eq{"qbr.book_id": filter.BookID})

	dataBuilder := r.Builder.
		Select(quranBookReferenceColumns()...).
		From(quranBookReferenceProjectionSQL).
		Where(sq.Eq{"qbr.book_id": filter.BookID}).
		OrderBy("qbr.page_id ASC", "qbr.created_at ASC").
		Limit(filter.Limit).
		Offset(filter.Offset)
	if filter.HeadingID != nil {
		countBuilder = countBuilder.Where(sq.Eq{"qbr.heading_id": *filter.HeadingID})
		dataBuilder = dataBuilder.Where(sq.Eq{"qbr.heading_id": *filter.HeadingID})
	}

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

	if err := r.attachBookReferenceAyahs(ctx, references, filter.Lang, filter.TranslationSource); err != nil {
		return nil, 0, err
	}

	return references, total, nil
}

// quranBookReferenceProjectionSQL keeps the old reader contract stable while
// quran_book_references is bridged in chunks. Registry moderation state and
// confidence win for bridged rows; every not-yet-bridged legacy row is included
// exactly once by the anti-join fallback.
const quranBookReferenceProjectionSQL = `(
    SELECT
        b.cross_reference_id AS id,
        b.book_id,
        b.page_id,
        b.heading_id,
        b.knowledge_mention_id,
        b.source_text,
        b.normalized_text,
        b.normalization_version,
        b.reference_kind,
        b.surah_id,
        b.from_ayah_number,
        b.to_ayah_number,
        b.from_ayah_key,
        b.to_ayah_key,
        b.match_strategy,
        cr.confidence,
        cr.review_status,
        b.metadata,
        b.created_at,
        b.updated_at
    FROM quran_cross_reference_bridge b
    JOIN cross_references cr ON cr.id = b.cross_reference_id

    UNION ALL

    SELECT
        legacy.id,
        legacy.book_id,
        legacy.page_id,
        legacy.heading_id,
        legacy.knowledge_mention_id,
        legacy.source_text,
        legacy.normalized_text,
        legacy.normalization_version,
        legacy.reference_kind,
        legacy.surah_id,
        legacy.from_ayah_number,
        legacy.to_ayah_number,
        legacy.from_ayah_key,
        legacy.to_ayah_key,
        legacy.match_strategy,
        legacy.confidence,
        legacy.review_status,
        legacy.metadata,
        legacy.created_at,
        legacy.updated_at
    FROM quran_book_references legacy
    WHERE NOT EXISTS (
        SELECT 1
        FROM quran_cross_reference_bridge b
        WHERE b.cross_reference_id = legacy.id
    )
) qbr`

// attachBookReferenceAyahs loads the ayah ranges for ALL references in ONE query
// (an unnest of (surah_id, from, to) ranges) and slices each reference's range from
// the shared result, replacing the previous per-reference N+1. Mirrors ListSurahAyahs'
// translation/transliteration resolution + light editorial join so payloads match.
//
//nolint:gocognit,gocyclo,cyclop,funlen // batched multi-range ayah attach; linear per-reference bucketing
func (r *QuranRepo) attachBookReferenceAyahs(
	ctx context.Context,
	references []entity.BookQuranReference,
	lang string,
	translationSource string,
) error {
	surahs := make([]int, 0, len(references))
	froms := make([]int, 0, len(references))

	tos := make([]int, 0, len(references))
	for i := range references {
		ref := &references[i]
		if ref.SurahID == nil || ref.FromAyahNumber == nil || ref.ToAyahNumber == nil {
			continue
		}

		surahs = append(surahs, *ref.SurahID)
		froms = append(froms, *ref.FromAyahNumber)
		tos = append(tos, *ref.ToAyahNumber)
	}

	if len(surahs) == 0 {
		return nil
	}

	resolvedSourceID, err := r.resolveTranslationSourceID(ctx, lang, translationSource)
	if err != nil {
		return err
	}

	includeSelectedTranslation := !contentlang.IsArabic(lang) && resolvedSourceID != ""

	resolvedTransliterationSourceID, err := r.defaultTransliterationSourceID(ctx, lang)
	if err != nil {
		return err
	}

	includeTransliteration := !contentlang.IsArabic(lang) && resolvedTransliterationSourceID != ""

	// $1=surahs $2=lang $3=source $4=transliteration source $5=froms $6=tos
	where := `
WHERE EXISTS (
    SELECT 1
    FROM unnest($1::int[], $5::int[], $6::int[]) AS ref(surah_id, from_ayah, to_ayah)
    WHERE ref.surah_id = a.surah_id
      AND a.ayah_number BETWEEN ref.from_ayah AND ref.to_ayah
)
ORDER BY a.surah_id ASC, a.ayah_number ASC`

	rows, err := r.Pool.Query(
		ctx,
		quranAyahSelectSQL(where, includeSelectedTranslation, includeTransliteration, true, false),
		surahs, lang, resolvedSourceID, resolvedTransliterationSourceID, froms, tos,
	)
	if err != nil {
		return fmt.Errorf("QuranRepo - attachBookReferenceAyahs - Query: %w", err)
	}
	defer rows.Close()

	bySurah := make(map[int][]entity.QuranAyah)

	for rows.Next() {
		ayah, err := scanQuranAyah(rows)
		if err != nil {
			return fmt.Errorf("QuranRepo - attachBookReferenceAyahs - scanQuranAyah: %w", err)
		}

		applyQuranAyahMetadata(&ayah, lang, true, false)
		bySurah[ayah.SurahID] = append(bySurah[ayah.SurahID], ayah)
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("QuranRepo - attachBookReferenceAyahs - rows.Err: %w", err)
	}

	// Bucket each reference's [from, to] slice from its surah's shared ayah list.
	for i := range references {
		ref := &references[i]
		if ref.SurahID == nil || ref.FromAyahNumber == nil || ref.ToAyahNumber == nil {
			continue
		}

		candidates := bySurah[*ref.SurahID]
		picked := make([]entity.QuranAyah, 0)

		for j := range candidates {
			if candidates[j].AyahNumber >= *ref.FromAyahNumber && candidates[j].AyahNumber <= *ref.ToAyahNumber {
				picked = append(picked, candidates[j])
			}
		}

		ref.Ayahs = picked
	}

	return nil
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

    -- Public audio is language-independent (no lang column on quran_audio_tracks), so
    -- emit ONE row per missing track with an empty target_lang. A CROSS JOIN to
    -- target_langs here would duplicate every missing audio asset once per target lang.
    SELECT 'audio_public'::TEXT AS asset_type,
           ''::TEXT AS target_lang,
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
    WHERE t.public_url IS NULL
),
filtered AS (
    SELECT *
    FROM missing
    WHERE ($2::TEXT = '' OR asset_type = $2)
      AND ($3::INT IS NULL OR surah_id = $3)
)`

func quranSurahSelectSQL(where string, includeInfo, includeEditorialHTML bool) string {
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

	// Editorial HTML is heavy (keutamaan/asbabun/pokok kandungan). It is selected
	// only for detail reads (GetSurah); list reads (ListSurahs) keep it NULL so a
	// 114-row include_info payload stays under the edge cache's MAX_CACHE_BYTES.
	// Light editorial metadata (slug/meta/license/freshness) is always selected
	// because the sitemap and listings need it. The column COUNT is constant
	// across both flags so scanQuranSurah aligns either way. Only permitted
	// (reviewed) editorial is joined, so unreviewed drafts never leave the API.
	editorialHTMLColumns := `
       NULL::text AS ed_keutamaan_html,
       NULL::text AS ed_asbabun_nuzul_html,
       NULL::text AS ed_pokok_kandungan_html`
	if includeEditorialHTML {
		editorialHTMLColumns = `
       ed.keutamaan_html AS ed_keutamaan_html,
       ed.asbabun_nuzul_html AS ed_asbabun_nuzul_html,
       ed.pokok_kandungan_html AS ed_pokok_kandungan_html`
	}

	return `
SELECT s.surah_id,
       s.slug,
       s.name_arabic,
       s.name_latin,
       s.name_translation,
       s.revelation_type,
       s.ayah_count,
       s.chronological_order,
       s.ruku_count,
       s.metadata,
       s.updated_at,
       GREATEST(s.updated_at, COALESCE(ed.updated_at, s.updated_at)) AS content_updated_at,` + infoColumns + `,
       ed.lang AS ed_lang,
       ed.meta_title AS ed_meta_title,
       ed.meta_description AS ed_meta_description,
       ed.arti_nama AS ed_arti_nama,` + editorialHTMLColumns + `,
       ed.author_name AS ed_author_name,
       ed.reviewed_by AS ed_reviewed_by,
       ed.reviewed_at AS ed_reviewed_at,
       ed.license_status AS ed_license_status,
       ed.created_at AS ed_created_at,
       ed.updated_at AS ed_updated_at,
       $1::text AS requested_lang,
       COALESCE(av.available_langs, ARRAY[]::TEXT[]) AS available_info_langs
FROM quran_surahs s` + infoJoin + `
LEFT JOIN quran_surah_editorial ed ON ed.surah_id = s.surah_id AND ed.lang = $1 AND ed.license_status = 'permitted'
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

// quranSearchScoredCTE is the shared `scored` CTE (bind params $1..$5) reused by
// both the paginated result query and the out-of-range total-count query.
const quranSearchScoredCTE = `
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
          mt.text ILIKE $5
          OR mt.text % $1
      )
    ORDER BY match_score DESC, mt.lang ASC, mt.source_id ASC
    LIMIT 1
) mt ON true
-- Bare columns (no COALESCE) so ILIKE and the % operator can use the GIN trgm
-- indexes; % is equivalent to similarity()>threshold with pg_trgm.similarity_threshold
-- set to 0.18 (SET LOCAL in SearchAyahs). NULL columns yield NULL → excluded, same
-- as the old COALESCE('',...) form.
WHERE a.search_text ILIKE $5
   OR a.text_qpc_hafs ILIKE $5
   OR t.text ILIKE $5
   OR tn.text ILIKE $5
   OR mt.source_id IS NOT NULL
   OR a.search_text % $1
   OR a.text_qpc_hafs % $1
   OR t.text % $1
   OR tn.text % $1
)`

// quranSearchSQL returns the ranked, paginated result rows; COUNT(*) OVER() folds
// the match total into each row in a single pass.
const quranSearchSQL = quranSearchScoredCTE + `
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
       END AS matched_field,
       COUNT(*) OVER()::int AS total_count
FROM scored
ORDER BY score DESC, surah_id ASC, ayah_number ASC
LIMIT $6 OFFSET $7`

// quranSearchCountSQL returns just the match total. It is only run when the paged
// query returns zero rows on a non-first page (offset past the last match), where
// COUNT(*) OVER() never gets a row to fold the total into and would report 0.
const quranSearchCountSQL = quranSearchScoredCTE + `
SELECT COUNT(*)::int FROM scored`

func quranAyahSelectSQL(where string, includeTranslation, includeTransliteration, includeEditorial, includeEditorialHTML bool) string {
	translationColumns := quranAyahTranslationColumnsSQL
	translationJoin := quranAyahTranslationJoinSQL

	if !includeTranslation {
		translationColumns = quranAyahTranslationNullColumnsSQL
		translationJoin = quranAyahTranslationDisabledJoinSQL
	}

	transliterationColumns := quranAyahTransliterationColumnsSQL
	transliterationJoin := quranAyahTransliterationJoinSQL

	if !includeTransliteration {
		transliterationColumns = quranAyahTransliterationNullColumnsSQL
		transliterationJoin = quranAyahTransliterationDisabledJoinSQL
	}

	// Editorial columns are ALWAYS selected (real or NULL) so the column count is
	// constant for scanQuranAyahInternal; the join is added only when included.
	editorialColumns := quranAyahEditorialNullColumnsSQL
	editorialJoin := ""
	contentUpdatedAtColumn := `,
       a.updated_at AS content_updated_at`

	if includeEditorial {
		editorialColumns = quranAyahEditorialLightColumnsSQL
		if includeEditorialHTML {
			editorialColumns = quranAyahEditorialFullColumnsSQL
		}

		editorialJoin = quranAyahEditorialJoinSQL
		contentUpdatedAtColumn = `,
       GREATEST(a.updated_at, COALESCE(ae.updated_at, a.updated_at)) AS content_updated_at`
	}

	return quranAyahSelectHeadSQL +
		translationColumns + `,
       ` + transliterationColumns +
		quranAyahAvailabilityColumnsSQL +
		editorialColumns +
		contentUpdatedAtColumn +
		quranAyahFromSQL +
		translationJoin +
		transliterationJoin +
		quranAyahAvailableLangsJoinSQL +
		editorialJoin +
		where
}

// audioTracksForAyahs maps each requested ayah to its playable tracks. A surah-mode
// track only maps to an ayah when a per-ayah segment pins the offset, so a full-surah
// track with no segments yields no per-ayah entry here by design (G6): the per-ayah
// reader needs a seek offset it does not have. The surah audio manifest
// (GetSurahAudioManifest) is the source of truth for full-surah coverage and exposes
// it via HasFullSurahAudio / SegmentMissingAyahKeys.
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

	rows, err := r.Pool.Query(
		ctx, `
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
	ORDER BY t.recitation_id ASC, t.track_type ASC, t.surah_id ASC, t.ayah_number ASC NULLS FIRST, s.segment_index ASC`,
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
			return quranAudioTrackLess(&result[ayahKey][i], &result[ayahKey][j])
		})
	}

	return result, nil
}

func (r *QuranRepo) getVisibleRecitation(ctx context.Context, recitationID string) (entity.QuranRecitation, error) {
	row := r.Pool.QueryRow(
		ctx, `SELECT `+quranRecitationSelectColumns+`
`+quranRecitationFromJoin+`
WHERE r.id = $1 AND r.is_visible = TRUE
GROUP BY r.id, sc.segment_count`,
		recitationID,
	)

	recitation, err := scanQuranRecitation(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return entity.QuranRecitation{}, entity.ErrQuranRecitationNotFound
		}

		return entity.QuranRecitation{}, fmt.Errorf("QuranRepo - getVisibleRecitation - scanQuranRecitation: %w", err)
	}

	return recitation, nil
}

func (r *QuranRepo) surahAyahKeys(ctx context.Context, surahID int) ([]string, error) {
	rows, err := r.Pool.Query(
		ctx, `
SELECT surah_id::text || ':' || ayah_number::text AS ayah_key
FROM quran_ayahs
WHERE surah_id = $1
ORDER BY ayah_number ASC`,
		surahID,
	)
	if err != nil {
		return nil, fmt.Errorf("QuranRepo - surahAyahKeys - Query: %w", err)
	}
	defer rows.Close()

	ayahKeys, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("QuranRepo - surahAyahKeys - CollectRows: %w", err)
	}

	return ayahKeys, nil
}

func (r *QuranRepo) audioTracksForSurah(
	ctx context.Context,
	surahID int,
	recitation *entity.QuranRecitation,
) ([]entity.QuranAudioTrack, error) {
	rows, err := r.Pool.Query(
		ctx,
		quranAudioTracksForSurahSQL,
		recitation.ID,
		surahID,
		quranAudioTrackTypeForRecitation(recitation),
	)
	if err != nil {
		return nil, fmt.Errorf("QuranRepo - audioTracksForSurah - Query: %w", err)
	}
	defer rows.Close()

	grouped, err := scanQuranAudioTrackGroups(rows, "QuranRepo - audioTracksForSurah")
	if err != nil {
		return nil, err
	}

	return sortedQuranAudioTracks(grouped), nil
}

func quranAudioTrackTypeForRecitation(recitation *entity.QuranRecitation) string {
	if recitation != nil && recitation.Mode == quranTrackTypeSurah {
		return quranTrackTypeSurah
	}

	return quranTrackTypeAyah
}

func scanQuranAudioTrackGroups(rows pgx.Rows, location string) (map[string]*entity.QuranAudioTrack, error) {
	grouped := make(map[string]*entity.QuranAudioTrack)

	for rows.Next() {
		track, _, segment, hasSegment, err := scanQuranAudioTrackRow(rows)
		if err != nil {
			return nil, fmt.Errorf("%s - scanQuranAudioTrackRow: %w", location, err)
		}

		trackID := track.RecitationID + ":" + track.TrackType + ":" + track.TrackKey

		existing := grouped[trackID]
		if existing == nil {
			existing = &track
			grouped[trackID] = existing
		}

		if hasSegment {
			existing.Segments = append(existing.Segments, segment)
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%s - rows.Err: %w", location, err)
	}

	return grouped, nil
}

func sortedQuranAudioTracks(grouped map[string]*entity.QuranAudioTrack) []entity.QuranAudioTrack {
	tracks := make([]entity.QuranAudioTrack, 0, len(grouped))
	for _, track := range grouped {
		tracks = append(tracks, *track)
	}

	sort.Slice(tracks, func(i, j int) bool {
		return quranAudioTrackLess(&tracks[i], &tracks[j])
	})

	return tracks
}

// resolveAudioRecitationID validates an explicit recitation id (falling back to the
// coverage-ranked default when blank). It intentionally checks visibility only, NOT
// per-request playability: a visible-but-unsynced recitation still resolves so the
// manifest can render. Playability is surfaced softly instead of via a hard error —
// the recitation's CoveragePercent/HasPlayableAudio and the manifest's
// MissingAyahKeys/SegmentMissingAyahKeys tell the client what is (not) playable.
func (r *QuranRepo) resolveAudioRecitationID(ctx context.Context, recitationID string) (string, error) {
	recitationID = strings.TrimSpace(recitationID)
	if recitationID == "" {
		return r.defaultPlayableRecitationID(ctx)
	}

	var exists bool
	if err := r.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM quran_recitations WHERE id = $1 AND is_visible = TRUE)`, recitationID).
		Scan(&exists); err != nil {
		return "", fmt.Errorf("QuranRepo - resolveAudioRecitationID - QueryRow: %w", err)
	}

	if !exists {
		return "", entity.ErrQuranRecitationNotFound
	}

	return recitationID, nil
}

func (r *QuranRepo) resolveTranslationSourceID(ctx context.Context, lang, sourceID string) (string, error) {
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
	// coverage_count is denormalized at import time, so this is a tiny lookup over
	// the small sources table — NOT a full GROUP BY aggregate per request.
	err := r.Pool.QueryRow(ctx, `
SELECT s.id
FROM quran_translation_sources s
WHERE s.lang = $1
ORDER BY CASE WHEN s.lang = 'id' AND s.id = $2 THEN 0 ELSE 1 END,
         s.coverage_count DESC,
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
	// coverage_count is denormalized at import time (tiny lookup, no per-request aggregate).
	err := r.Pool.QueryRow(ctx, `
SELECT s.id
FROM quran_transliteration_sources s
WHERE s.lang = $1
ORDER BY CASE
             WHEN s.lang = 'id' AND s.id = $2 THEN 0
             WHEN s.lang = 'en' AND s.id = $3 THEN 0
             ELSE 1
         END,
         s.coverage_count DESC,
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

	// Eligibility is "has at least one playable track" (playable_count > 0), then the
	// MOST COMPLETE recitation wins via coverage DESC — so a 1-track recitation can no
	// longer become the default over a full one. An explicit admin default_priority
	// still takes precedence. The coverage formula mirrors ListRecitations exactly.
	err := r.Pool.QueryRow(ctx, `
WITH recitation_coverage AS (
    SELECT r.id,
           r.mode,
           r.default_priority,
           COALESCE(NULLIF(r.display_name, ''), NULLIF(r.reciter_name, ''), r.name) AS sort_name,
           COUNT(CASE WHEN COALESCE(NULLIF(t.public_url, ''), NULLIF(t.audio_url, '')) IS NOT NULL THEN 1 END) AS playable_count,
           CASE r.mode
               WHEN 'ayah'  THEN NULLIF((SELECT COUNT(*) FROM quran_ayahs), 0)
               WHEN 'surah' THEN 114
               ELSE NULL
           END AS expected_count
    FROM quran_recitations r
    JOIN quran_audio_tracks t ON t.recitation_id = r.id
    WHERE r.is_visible = TRUE
    GROUP BY r.id
)
SELECT id
FROM recitation_coverage
WHERE playable_count > 0
ORDER BY CASE WHEN default_priority IS NULL THEN 1 ELSE 0 END,
         default_priority ASC,
         (playable_count::float8 / NULLIF(expected_count, 0)) DESC NULLS LAST,
         CASE mode WHEN 'ayah' THEN 0 WHEN 'surah' THEN 1 ELSE 2 END,
         sort_name ASC,
         id ASC
LIMIT 1`).Scan(&recitationID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}

		return "", fmt.Errorf("QuranRepo - defaultPlayableRecitationID - QueryRow: %w", err)
	}

	return recitationID, nil
}

func applyQuranAyahMetadata(ayah *entity.QuranAyah, requestedLang string, includeTranslation, includeAudio bool) {
	ayah.RequestedLang = requestedLang
	if ayah.AvailableTranslationLangs == nil {
		ayah.AvailableTranslationLangs = []string{}
	}

	hasTranslation := ayah.Translation != nil

	translationMissing := includeTranslation && !contentlang.IsArabic(requestedLang) && !hasTranslation

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

// ensureQuranSurah verifies a surah exists and returns its ayah_count so callers
// can validate a requested ayah range against the real surah length in the same
// round-trip (avoids a second lookup).
func (r *QuranRepo) ensureQuranSurah(ctx context.Context, surahID int) (int, error) {
	var ayahCount int
	if err := r.Pool.QueryRow(ctx, `SELECT ayah_count FROM quran_surahs WHERE surah_id = $1`, surahID).
		Scan(&ayahCount); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, entity.ErrQuranSurahNotFound
		}

		return 0, fmt.Errorf("QuranRepo - ensureQuranSurah - QueryRow: %w", err)
	}

	return ayahCount, nil
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
		"qbr.normalization_version",
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
	var defaultRecitation *entity.QuranRecitation

	for i := range recitations {
		recitation := &recitations[i]
		// Eligible = can play at least one track. Completeness is decided by the
		// coverage ranking below, not by requiring every imported track to be playable
		// (that wrongly excluded a 99%-covered recitation while a 1-track one qualified).
		if recitation.PlayableTrackCount <= 0 {
			continue
		}

		if defaultRecitation == nil {
			defaultRecitation = recitation

			continue
		}

		if quranDefaultRecitationLess(recitation, defaultRecitation) {
			defaultRecitation = recitation
		}
	}

	if defaultRecitation != nil {
		defaultRecitation.IsDefault = true
	}
}

func quranDefaultRecitationLess(left, right *entity.QuranRecitation) bool {
	if left.DefaultPriority != nil || right.DefaultPriority != nil {
		if left.DefaultPriority == nil {
			return false
		}

		if right.DefaultPriority == nil {
			return true
		}

		if *left.DefaultPriority != *right.DefaultPriority {
			return *left.DefaultPriority < *right.DefaultPriority
		}
	}

	// Prefer the more complete recitation (mirrors defaultPlayableRecitationID's
	// coverage DESC) before falling back to mode/name/id ordering.
	if left.CoveragePercent != right.CoveragePercent {
		return left.CoveragePercent > right.CoveragePercent
	}

	return quranRecitationLess(left, right)
}

func quranRecitationLess(left, right *entity.QuranRecitation) bool {
	leftRank := quranRecitationModeRank(left.Mode)

	rightRank := quranRecitationModeRank(right.Mode)
	if leftRank != rightRank {
		return leftRank < rightRank
	}

	if left.DisplayName != right.DisplayName {
		return left.DisplayName < right.DisplayName
	}

	return left.ID < right.ID
}

func quranRecitationModeRank(mode string) int {
	switch mode {
	case quranTrackTypeAyah:
		return 0
	case quranTrackTypeSurah:
		return 1
	default:
		return 2
	}
}

func quranAudioTrackLess(left, right *entity.QuranAudioTrack) bool {
	if left.RecitationID != right.RecitationID {
		return left.RecitationID < right.RecitationID
	}

	leftRank := quranRecitationModeRank(left.TrackType)

	rightRank := quranRecitationModeRank(right.TrackType)
	if leftRank != rightRank {
		return leftRank < rightRank
	}

	if left.SurahID != right.SurahID {
		return left.SurahID < right.SurahID
	}
	// Order by numeric ayah, not the "surah:ayah" string: "1:2" must precede "1:10".
	// A nil AyahNumber (surah-level track) sorts before ayah-level tracks of the surah.
	leftAyah, rightAyah := quranTrackAyahNumber(left), quranTrackAyahNumber(right)
	if leftAyah != rightAyah {
		return leftAyah < rightAyah
	}

	return left.TrackKey < right.TrackKey
}

func quranTrackAyahNumber(track *entity.QuranAudioTrack) int {
	if track.AyahNumber != nil {
		return *track.AyahNumber
	}

	return 0
}

func scanQuranRecitation(row rowScanner) (entity.QuranRecitation, error) {
	var (
		recitation      entity.QuranRecitation
		displayName     sql.NullString
		reciterName     sql.NullString
		style           sql.NullString
		sourceURL       sql.NullString
		resourceID      sql.NullString
		checksum        sql.NullString
		metadata        []byte
		importedAt      sql.NullTime
		defaultPriority sql.NullInt64
	)

	err := row.Scan(
		&recitation.ID,
		&recitation.Name,
		&displayName,
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
		&recitation.SortOrder,
		&defaultPriority,
		&recitation.IsVisible,
		&recitation.TrackCount,
		&recitation.PublicTrackCount,
		&recitation.PlayableTrackCount,
		&recitation.SegmentCount,
		&recitation.CoveragePercent,
	)
	if err != nil {
		return entity.QuranRecitation{}, err
	}

	recitation.DisplayName = displayName.String
	if recitation.DisplayName == "" {
		recitation.DisplayName = recitation.Name
	}

	recitation.ReciterName = nullableString(reciterName)
	recitation.Style = nullableString(style)
	recitation.SourceURL = nullableString(sourceURL)
	recitation.QULResourceID = nullableString(resourceID)
	recitation.Checksum = nullableString(checksum)
	recitation.Metadata = entity.RawJSON(metadata)
	recitation.ImportedAt = nullableTime(importedAt)
	recitation.DefaultPriority = nullableInt(defaultPriority)
	recitation.HasPublicAudio = recitation.PublicTrackCount > 0 && recitation.PublicTrackCount == recitation.TrackCount
	recitation.HasPlayableAudio = recitation.PlayableTrackCount > 0 && recitation.PlayableTrackCount == recitation.TrackCount

	return recitation, nil
}

// manifestAudioCoverage classifies a surah's ayahs against a recitation's playable
// tracks. It separates "no audio at all" (missing) from "full-surah audio present but
// no per-ayah segment to seek to" (segmentMissing), so a playable full-surah track is
// no longer mis-reported as every ayah missing.
func manifestAudioCoverage(
	ayahKeys []string,
	mode string,
	tracks []entity.QuranAudioTrack,
) (missing, segmentMissing []string, hasFullSurahAudio bool) {
	if mode == quranTrackTypeSurah {
		return surahManifestCoverage(ayahKeys, tracks)
	}

	// Ayah mode: an ayah is missing when it has no playable ayah-track.
	present := make(map[string]bool, len(ayahKeys))

	for i := range tracks {
		track := &tracks[i]
		if track.TrackType == quranTrackTypeAyah && quranAudioTrackPlayable(track) {
			present[track.TrackKey] = true
		}
	}

	return keysNotIn(ayahKeys, present), []string{}, false
}

// surahManifestCoverage handles the surah-mode branch of manifestAudioCoverage: a
// playable full-surah track covers every ayah (nothing "missing"), and only ayahs
// without a per-ayah segment are segment-missing.
func surahManifestCoverage(
	ayahKeys []string,
	tracks []entity.QuranAudioTrack,
) (missing, segmentMissing []string, hasFullSurahAudio bool) {
	segmentPresent := make(map[string]bool, len(ayahKeys))

	for i := range tracks {
		track := &tracks[i]
		if track.TrackType != quranTrackTypeSurah || !quranAudioTrackPlayable(track) {
			continue
		}

		hasFullSurahAudio = true

		for _, segment := range track.Segments {
			segmentPresent[segment.AyahKey] = true
		}
	}

	if !hasFullSurahAudio {
		// No playable surah track → the whole surah has no audio.
		return append([]string{}, ayahKeys...), []string{}, false
	}

	return []string{}, keysNotIn(ayahKeys, segmentPresent), true
}

// keysNotIn returns, as a non-nil slice, the ayah keys absent from present.
func keysNotIn(ayahKeys []string, present map[string]bool) []string {
	missing := make([]string, 0)

	for _, ayahKey := range ayahKeys {
		if !present[ayahKey] {
			missing = append(missing, ayahKey)
		}
	}

	return missing
}

func quranAudioTrackPlayable(track *entity.QuranAudioTrack) bool {
	if track == nil {
		return false
	}

	return (track.PublicURL != nil && *track.PublicURL != "") ||
		(track.AudioURL != nil && *track.AudioURL != "")
}

func scanQuranTranslationSource(row rowScanner) (entity.QuranTranslationSource, error) {
	var (
		source     entity.QuranTranslationSource
		translator sql.NullString
		sourceURL  sql.NullString
		resourceID sql.NullString
		checksum   sql.NullString
		metadata   []byte
		importedAt sql.NullTime
	)

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
	var (
		surah              entity.QuranSurah
		slug               sql.NullString
		nameArabic         sql.NullString
		nameLatin          sql.NullString
		nameTranslation    sql.NullString
		revelationType     sql.NullString
		chronologicalOrder sql.NullInt64
		rukuCount          sql.NullInt64
		metadata           []byte
		contentUpdatedAt   sql.NullTime
		infoLang           sql.NullString
		infoSurahName      sql.NullString
		infoTextHTML       sql.NullString
		infoShortText      sql.NullString
		infoSourceName     sql.NullString
		infoSourceURL      sql.NullString
		infoResourceID     sql.NullString
		infoFormat         sql.NullString
		infoLicenseStatus  sql.NullString
		infoChecksum       sql.NullString
		infoMetadata       []byte
		infoImportedAt     sql.NullTime
		infoUpdatedAt      sql.NullTime
		edLang             sql.NullString
		edMetaTitle        sql.NullString
		edMetaDescription  sql.NullString
		edArtiNama         sql.NullString
		edKeutamaan        sql.NullString
		edAsbabun          sql.NullString
		edPokok            sql.NullString
		edAuthorName       sql.NullString
		edReviewedBy       sql.NullString
		edReviewedAt       sql.NullTime
		edLicenseStatus    sql.NullString
		edCreatedAt        sql.NullTime
		edUpdatedAt        sql.NullTime
		requestedLang      string
		availableInfoLangs []string
	)

	err := row.Scan(
		&surah.SurahID,
		&slug,
		&nameArabic,
		&nameLatin,
		&nameTranslation,
		&revelationType,
		&surah.AyahCount,
		&chronologicalOrder,
		&rukuCount,
		&metadata,
		&surah.UpdatedAt,
		&contentUpdatedAt,
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
		&edLang,
		&edMetaTitle,
		&edMetaDescription,
		&edArtiNama,
		&edKeutamaan,
		&edAsbabun,
		&edPokok,
		&edAuthorName,
		&edReviewedBy,
		&edReviewedAt,
		&edLicenseStatus,
		&edCreatedAt,
		&edUpdatedAt,
		&requestedLang,
		&availableInfoLangs,
	)
	if err != nil {
		return entity.QuranSurah{}, err
	}

	surah.Slug = nullableString(slug)
	surah.NameArabic = nullableString(nameArabic)
	surah.NameLatin = nullableString(nameLatin)
	surah.NameTranslation = nullableString(nameTranslation)
	surah.RevelationType = nullableString(revelationType)
	surah.ChronologicalOrder = nullableInt(chronologicalOrder)
	surah.RukuCount = nullableInt(rukuCount)
	surah.Metadata = entity.RawJSON(metadata)

	surah.ContentUpdatedAt = nullableTime(contentUpdatedAt)
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

	if edLang.Valid {
		editorial := &entity.QuranSurahEditorial{
			Lang:            edLang.String,
			MetaTitle:       nullableString(edMetaTitle),
			MetaDescription: nullableString(edMetaDescription),
			ArtiNama:        nullableString(edArtiNama),
			AuthorName:      nullableString(edAuthorName),
			ReviewedBy:      nullableString(edReviewedBy),
			ReviewedAt:      nullableTime(edReviewedAt),
			LicenseStatus:   edLicenseStatus.String,
		}
		// HTML is sanitized on read (as TextHTML is); empties are omitted so the
		// frontend's "has editorial content" index gate is not tripped by "".
		if edKeutamaan.Valid {
			if sanitized := readerutil.SanitizeHTML(edKeutamaan.String); sanitized != "" {
				editorial.Keutamaan = &sanitized
			}
		}

		if edAsbabun.Valid {
			if sanitized := readerutil.SanitizeHTML(edAsbabun.String); sanitized != "" {
				editorial.AsbabunNuzul = &sanitized
			}
		}

		if edPokok.Valid {
			if sanitized := readerutil.SanitizeHTML(edPokok.String); sanitized != "" {
				editorial.PokokKandungan = &sanitized
			}
		}

		if edCreatedAt.Valid {
			editorial.CreatedAt = edCreatedAt.Time
		}

		if edUpdatedAt.Valid {
			editorial.UpdatedAt = edUpdatedAt.Time
		}

		surah.Editorial = editorial
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
	ayah, _, _, _, _, _, err := scanQuranAyahInternal(row, false, true, false) //nolint:dogsled // shared scanner returns score/lang/total columns; only ayah is needed here

	return ayah, err
}

//nolint:gocritic // multi-column search scanner; the extra results map 1:1 to SELECTed columns
func scanQuranAyahWithScore(row rowScanner) (entity.QuranAyah, float64, string, string, string, int, error) {
	return scanQuranAyahInternal(row, true, false, true)
}

func scanQuranNavigationSegment(row rowScanner) (entity.QuranNavigationSegment, error) {
	var (
		segment        entity.QuranNavigationSegment
		startSurahName sql.NullString
		endSurahName   sql.NullString
	)

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

//nolint:gocognit,gocyclo,cyclop,funlen,gocritic // column-mapping scanner: flat per-column assignment gated by withScore/withEditorial/withTotal; results map 1:1 to SELECTed columns
func scanQuranAyahInternal(row rowScanner, withScore, withEditorial, withTotal bool) (entity.QuranAyah, float64, string, string, string, int, error) {
	var (
		ayah                      entity.QuranAyah
		textQPCHafs               sql.NullString
		textImlaei                sql.NullString
		searchText                sql.NullString
		scriptType                sql.NullString
		fontFamily                sql.NullString
		pageNumber                sql.NullInt64
		juzNumber                 sql.NullInt64
		hizbNumber                sql.NullInt64
		metadata                  []byte
		sourceID                  sql.NullString
		lang                      sql.NullString
		translationText           sql.NullString
		footnotes                 []byte
		chunks                    []byte
		translationMetadata       []byte
		translationUpdatedAt      sql.NullTime
		transliterationSourceID   sql.NullString
		transliterationLang       sql.NullString
		transliterationText       sql.NullString
		transliterationMetadata   []byte
		transliterationUpdatedAt  sql.NullTime
		availableTranslationLangs []string
		score                     sql.NullFloat64
		matchedLang               sql.NullString
		matchedSourceID           sql.NullString
		matchedField              sql.NullString
		total                     sql.NullInt64
		edLang                    sql.NullString
		edMetaTitle               sql.NullString
		edMetaDescription         sql.NullString
		edTafsirRange             sql.NullString
		edLicenseStatus           sql.NullString
		edUpdatedAt               sql.NullTime
		edIntisari                sql.NullString
		edKeutamaan               sql.NullString
		edFAQ                     []byte
		contentUpdatedAt          sql.NullTime
	)

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
	if withEditorial {
		dest = append(
			dest,
			&edLang,
			&edMetaTitle,
			&edMetaDescription,
			&edTafsirRange,
			&edLicenseStatus,
			&edUpdatedAt,
			&edIntisari,
			&edKeutamaan,
			&edFAQ,
			&contentUpdatedAt,
		)
	}

	if withScore {
		dest = append(dest, &score, &matchedLang, &matchedSourceID, &matchedField)
	}

	if withTotal {
		dest = append(dest, &total)
	}

	if err := row.Scan(dest...); err != nil {
		return entity.QuranAyah{}, 0, "", "", "", 0, err
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
	if withEditorial { //nolint:nestif // shallow per-field editorial column mapping
		ayah.ContentUpdatedAt = nullableTime(contentUpdatedAt)

		if edLang.Valid {
			editorial := &entity.QuranAyahEditorial{
				Lang:            edLang.String,
				MetaTitle:       nullableString(edMetaTitle),
				MetaDescription: nullableString(edMetaDescription),
				TafsirRange:     nullableString(edTafsirRange),
				LicenseStatus:   edLicenseStatus.String,
			}
			if edUpdatedAt.Valid {
				editorial.UpdatedAt = edUpdatedAt.Time
			}
			// HTML is sanitized on read; empties are omitted so the frontend's
			// "has editorial" gate is not tripped by "".
			if edIntisari.Valid {
				if sanitized := readerutil.SanitizeHTML(edIntisari.String); sanitized != "" {
					editorial.Intisari = &sanitized
				}
			}

			if edKeutamaan.Valid {
				if sanitized := readerutil.SanitizeHTML(edKeutamaan.String); sanitized != "" {
					editorial.Keutamaan = &sanitized
				}
			}

			if len(edFAQ) > 0 {
				editorial.FAQ = sanitizeAyahEditorialFAQ(edFAQ)
			}

			ayah.Editorial = editorial
		}
	}

	return ayah, score.Float64, matchedLang.String, matchedSourceID.String, matchedField.String, int(total.Int64), nil
}

// sanitizeAyahEditorialFAQ unmarshals the stored FAQ JSONB and sanitizes every
// answer_html (author HTML, same gate as keutamaan/TextHTML), dropping entries
// that become empty.
func sanitizeAyahEditorialFAQ(raw []byte) []entity.QuranAyahEditorialFAQ {
	var items []entity.QuranAyahEditorialFAQ
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil
	}

	out := make([]entity.QuranAyahEditorialFAQ, 0, len(items))
	for _, item := range items {
		question := strings.TrimSpace(item.Question)

		answer := readerutil.SanitizeHTML(item.AnswerHTML)
		if question == "" || answer == "" {
			continue
		}

		out = append(out, entity.QuranAyahEditorialFAQ{Question: question, AnswerHTML: answer})
	}

	if len(out) == 0 {
		return nil
	}

	return out
}

func scanQuranAudioTrackRow(row rowScanner) (
	entity.QuranAudioTrack,
	string,
	entity.QuranAudioSegment,
	bool,
	error,
) {
	var (
		track           entity.QuranAudioTrack
		ayahNumber      sql.NullInt64
		audioURL        sql.NullString
		r2Key           sql.NullString
		publicURL       sql.NullString
		durationMS      sql.NullInt64
		durationSeconds sql.NullInt64
		mimeType        sql.NullString
		metadata        []byte
		segmentIndex    sql.NullInt64
		segmentAyahKey  sql.NullString
		timestampFrom   sql.NullInt64
		timestampTo     sql.NullInt64
		segmentDuration sql.NullInt64
		segmentMetadata []byte
	)

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
	var (
		item                  entity.EditorialMissingQuranAsset
		surahID               sql.NullInt64
		surahName             sql.NullString
		ayahNumber            sql.NullInt64
		ayahKey               sql.NullString
		translationSourceID   sql.NullString
		translationSourceName sql.NullString
		recitationID          sql.NullString
		trackType             sql.NullString
		trackKey              sql.NullString
		availableLangs        []string
		total                 int
	)

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
	var (
		reference            entity.BookQuranReference
		headingID            sql.NullInt64
		knowledgeMentionID   sql.NullString
		surahID              sql.NullInt64
		fromAyah             sql.NullInt64
		toAyah               sql.NullInt64
		fromAyahKey          sql.NullString
		toAyahKey            sql.NullString
		confidence           sql.NullFloat64
		normalizationVersion sql.NullInt64
		metadata             []byte
	)

	err := row.Scan(
		&reference.ID,
		&reference.BookID,
		&reference.PageID,
		&headingID,
		&knowledgeMentionID,
		&reference.SourceText,
		&reference.NormalizedText,
		&normalizationVersion,
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
	reference.NormalizationVersion = nullableInt(normalizationVersion)
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
