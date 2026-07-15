package persistent

import (
	"context"
	"fmt"

	"github.com/alfariesh/surau-backend/internal/entity"
)

const quranEditorialCoverageRowCount = 6

// ListQuranEditorialCoverage reports mutually-exclusive readiness states for
// every supported editorial language and public page type. It lives outside
// quran_postgres.go so protected operator reads cannot weaken the source-level
// contract that public Quran reads only use the published+permitted views.
func (r *QuranRepo) ListQuranEditorialCoverage(
	ctx context.Context,
) ([]entity.QuranEditorialCoverage, error) {
	rows, err := r.Pool.Query(ctx, quranEditorialCoverageSQL)
	if err != nil {
		return nil, fmt.Errorf("QuranRepo - ListQuranEditorialCoverage - Query: %w", err)
	}
	defer rows.Close()

	items := make([]entity.QuranEditorialCoverage, 0, quranEditorialCoverageRowCount)

	for rows.Next() {
		var item entity.QuranEditorialCoverage
		if err = rows.Scan(
			&item.Lang,
			&item.PageType,
			&item.TotalTargets,
			&item.Indexable,
			&item.PublishedBlockedLicense,
			&item.WorkflowIncomplete,
			&item.MissingEditorial,
			&item.MissingSlug,
			&item.SitemapItems,
			&item.CoveragePercent,
		); err != nil {
			return nil, fmt.Errorf("QuranRepo - ListQuranEditorialCoverage - Scan: %w", err)
		}

		items = append(items, item)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("QuranRepo - ListQuranEditorialCoverage - rows.Err: %w", err)
	}

	return items, nil
}

const quranEditorialCoverageSQL = `
WITH languages(lang) AS (
    VALUES ('ar'::TEXT), ('id'::TEXT), ('en'::TEXT)
),
script_visibility AS (
    SELECT EXISTS (SELECT 1 FROM public_quran_script_sources WHERE id = 'qpc-hafs') AS visible
),
targets AS (
    SELECT language.lang,
           'surah'::TEXT AS page_type,
           published.status AS published_status,
           published.license_status AS published_license,
           draft.status AS draft_status,
           registry.slug AS registered_slug,
           script.visible AS script_visible
    FROM quran_surahs surah
    CROSS JOIN languages language
    CROSS JOIN script_visibility script
    LEFT JOIN quran_surah_editorial published
      ON published.surah_id = surah.surah_id
     AND published.lang = language.lang
     AND published.status = 'published'
    LEFT JOIN quran_surah_editorial draft
      ON draft.surah_id = surah.surah_id
     AND draft.lang = language.lang
     AND draft.status = 'draft'
    LEFT JOIN quran_surah_slug_registry registry
      ON registry.slug = surah.slug
     AND registry.surah_id = surah.surah_id

    UNION ALL

    SELECT language.lang,
           'ayah'::TEXT AS page_type,
           published.status AS published_status,
           published.license_status AS published_license,
           draft.status AS draft_status,
           registry.slug AS registered_slug,
           script.visible AS script_visible
    FROM quran_ayahs ayah
    JOIN quran_surahs surah ON surah.surah_id = ayah.surah_id
    CROSS JOIN languages language
    CROSS JOIN script_visibility script
    LEFT JOIN quran_ayah_editorial published
      ON published.surah_id = ayah.surah_id
     AND published.ayah_number = ayah.ayah_number
     AND published.lang = language.lang
     AND published.status = 'published'
    LEFT JOIN quran_ayah_editorial draft
      ON draft.surah_id = ayah.surah_id
     AND draft.ayah_number = ayah.ayah_number
     AND draft.lang = language.lang
     AND draft.status = 'draft'
    LEFT JOIN quran_surah_slug_registry registry
      ON registry.slug = surah.slug
     AND registry.surah_id = surah.surah_id
),
classified AS (
    SELECT lang,
           page_type,
           CASE
               WHEN published_status = 'published'
                AND published_license = 'permitted'
                AND registered_slug IS NOT NULL
                AND script_visible THEN 'indexable'
               WHEN published_status = 'published'
                AND published_license = 'permitted'
                AND registered_slug IS NULL THEN 'missing_slug'
               WHEN published_status = 'published' THEN 'published_blocked_license'
               WHEN draft_status = 'draft' THEN 'workflow_incomplete'
               ELSE 'missing_editorial'
           END AS readiness
    FROM targets
)
SELECT lang,
       page_type,
       COUNT(*)::INTEGER AS total_targets,
       COUNT(*) FILTER (WHERE readiness = 'indexable')::INTEGER AS indexable,
       COUNT(*) FILTER (WHERE readiness = 'published_blocked_license')::INTEGER AS published_blocked_license,
       COUNT(*) FILTER (WHERE readiness = 'workflow_incomplete')::INTEGER AS workflow_incomplete,
       COUNT(*) FILTER (WHERE readiness = 'missing_editorial')::INTEGER AS missing_editorial,
       COUNT(*) FILTER (WHERE readiness = 'missing_slug')::INTEGER AS missing_slug,
       CASE WHEN lang IN ('id', 'en')
            THEN COUNT(*) FILTER (WHERE readiness = 'indexable')
            ELSE 0 END::INTEGER AS sitemap_items,
       ROUND(
           100.0 * COUNT(*) FILTER (WHERE readiness = 'indexable') / NULLIF(COUNT(*), 0),
           2
       )::DOUBLE PRECISION AS coverage_percent
FROM classified
GROUP BY lang, page_type
ORDER BY CASE lang WHEN 'ar' THEN 0 WHEN 'id' THEN 1 ELSE 2 END,
         CASE page_type WHEN 'surah' THEN 0 ELSE 1 END`
