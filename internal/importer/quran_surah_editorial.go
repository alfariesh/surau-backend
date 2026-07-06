package importer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/evrone/go-clean-template/internal/contentlang"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// QuranSurahEditorialOptions configures a surah editorial (keutamaan / asbabun
// nuzul / pokok kandungan / SEO meta) enrichment import.
type QuranSurahEditorialOptions struct {
	PostgresURL string
	Paths       []string
	DryRun      bool
}

// QuranSurahEditorialStats summarizes one editorial import run.
type QuranSurahEditorialStats struct {
	Files         int
	SurahRows     int // distinct surah_id with slug/chronological_order/ruku_count set
	EditorialRows int // surah_id+lang rows upserted
	DryRun        bool
}

// quranSurahEditorialRecord is one JSON entry. All editorial fields are optional;
// an absent (null) field keeps the existing value on re-import (COALESCE upsert).
type quranSurahEditorialRecord struct {
	SurahID            int        `json:"surah_id"`
	Slug               *string    `json:"slug"`
	ChronologicalOrder *int       `json:"chronological_order"`
	RukuCount          *int       `json:"ruku_count"`
	Lang               string     `json:"lang"`
	MetaTitle          *string    `json:"meta_title"`
	MetaDescription    *string    `json:"meta_description"`
	ArtiNama           *string    `json:"arti_nama"`
	KeutamaanHTML      *string    `json:"keutamaan_html"`
	AsbabunNuzulHTML   *string    `json:"asbabun_nuzul_html"`
	PokokKandunganHTML *string    `json:"pokok_kandungan_html"`
	AuthorName         *string    `json:"author_name"`
	ReviewedBy         *string    `json:"reviewed_by"`
	ReviewedAt         *time.Time `json:"reviewed_at"`
	LicenseStatus      *string    `json:"license_status"`

	// Derived during validation (not from JSON).
	license         string
	licenseOverride bool
	checksum        string
}

// RunQuranSurahEditorialImport parses editorial JSON files and upserts them into
// quran_surahs (slug/chronological_order/ruku_count) and quran_surah_editorial
// (per-language editorial + SEO copy) within a single transaction. Self-authored
// content, so it does NOT write quran_import_runs.
func RunQuranSurahEditorialImport(ctx context.Context, opts QuranSurahEditorialOptions) (QuranSurahEditorialStats, error) {
	if len(opts.Paths) == 0 {
		return QuranSurahEditorialStats{}, errors.New("at least one -editorial-json path is required")
	}

	if !opts.DryRun && strings.TrimSpace(opts.PostgresURL) == "" {
		return QuranSurahEditorialStats{}, errors.New("postgres URL is required")
	}

	records := make([]quranSurahEditorialRecord, 0)

	for _, path := range opts.Paths {
		raw, _, err := readAssetFile(path)
		if err != nil {
			return QuranSurahEditorialStats{}, fmt.Errorf("reading %s: %w", path, err)
		}

		var rawRecords []json.RawMessage
		if err := json.Unmarshal(raw, &rawRecords); err != nil {
			return QuranSurahEditorialStats{}, fmt.Errorf("parsing %s: %w", path, err)
		}

		for idx, rawRec := range rawRecords {
			var rec quranSurahEditorialRecord
			// Strict decode: a typo'd content key must be a hard error, not a silent
			// no-op the COALESCE upsert hides.
			dec := json.NewDecoder(bytes.NewReader(rawRec))
			dec.DisallowUnknownFields()

			if err := dec.Decode(&rec); err != nil {
				return QuranSurahEditorialStats{}, fmt.Errorf("%s record %d: %w", path, idx, err)
			}

			records = append(records, rec)
		}
	}

	surahSeen := make(map[int]struct{})
	editorialSeen := make(map[string]struct{})

	for i := range records {
		rec := &records[i]
		if rec.SurahID < 1 || rec.SurahID > 114 {
			return QuranSurahEditorialStats{}, fmt.Errorf("invalid surah_id %d (expected 1-114)", rec.SurahID)
		}

		lang, err := contentlang.Normalize(rec.Lang)
		if err != nil {
			return QuranSurahEditorialStats{}, fmt.Errorf("surah %d: invalid lang %q: %w", rec.SurahID, rec.Lang, err)
		}

		rec.Lang = lang
		// Fail fast on a duplicate (surah_id, lang) so two records can't silently
		// last-write-wins within the same transaction.
		key := fmt.Sprintf("%d:%s", rec.SurahID, rec.Lang)
		if _, dup := editorialSeen[key]; dup {
			return QuranSurahEditorialStats{}, fmt.Errorf("duplicate surah %d lang %s editorial record", rec.SurahID, rec.Lang)
		}

		editorialSeen[key] = struct{}{}

		rec.license, rec.licenseOverride, err = resolveEditorialLicense(rec.LicenseStatus)
		if err != nil {
			return QuranSurahEditorialStats{}, fmt.Errorf("surah %d: %w", rec.SurahID, err)
		}

		rec.checksum = surahEditorialChecksum(*rec)
		if rec.Slug != nil || rec.ChronologicalOrder != nil || rec.RukuCount != nil {
			surahSeen[rec.SurahID] = struct{}{}
		}
	}

	stats := QuranSurahEditorialStats{
		Files:         len(opts.Paths),
		SurahRows:     len(surahSeen),
		EditorialRows: len(records),
		DryRun:        opts.DryRun,
	}
	if opts.DryRun {
		return stats, nil
	}

	pool, err := pgxpool.New(ctx, opts.PostgresURL)
	if err != nil {
		return QuranSurahEditorialStats{}, fmt.Errorf("connecting postgres: %w", err)
	}
	defer pool.Close()

	tx, err := pool.Begin(ctx)
	if err != nil {
		return QuranSurahEditorialStats{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for i := range records {
		rec := records[i]

		if rec.Slug != nil || rec.ChronologicalOrder != nil || rec.RukuCount != nil {
			tag, err := tx.Exec(
				ctx, `
UPDATE quran_surahs SET
    slug = COALESCE($2, slug),
    chronological_order = COALESCE($3, chronological_order),
    ruku_count = COALESCE($4, ruku_count),
    updated_at = now()
WHERE surah_id = $1`,
				rec.SurahID, rec.Slug, rec.ChronologicalOrder, rec.RukuCount,
			)
			if err != nil {
				return QuranSurahEditorialStats{}, surahEditorialExecError("surah", rec, err)
			}

			if tag.RowsAffected() == 0 {
				return QuranSurahEditorialStats{}, fmt.Errorf("surah %d not found in quran_surahs", rec.SurahID)
			}
		}

		if _, err := tx.Exec(
			ctx, `
INSERT INTO quran_surah_editorial (
    surah_id, lang, meta_title, meta_description, arti_nama,
    keutamaan_html, asbabun_nuzul_html, pokok_kandungan_html,
    author_name, reviewed_by, reviewed_at, license_status, checksum, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, now())
ON CONFLICT (surah_id, lang) DO UPDATE SET
    meta_title = COALESCE(EXCLUDED.meta_title, quran_surah_editorial.meta_title),
    meta_description = COALESCE(EXCLUDED.meta_description, quran_surah_editorial.meta_description),
    arti_nama = COALESCE(EXCLUDED.arti_nama, quran_surah_editorial.arti_nama),
    keutamaan_html = COALESCE(EXCLUDED.keutamaan_html, quran_surah_editorial.keutamaan_html),
    asbabun_nuzul_html = COALESCE(EXCLUDED.asbabun_nuzul_html, quran_surah_editorial.asbabun_nuzul_html),
    pokok_kandungan_html = COALESCE(EXCLUDED.pokok_kandungan_html, quran_surah_editorial.pokok_kandungan_html),
    author_name = COALESCE(EXCLUDED.author_name, quran_surah_editorial.author_name),
    reviewed_by = COALESCE(EXCLUDED.reviewed_by, quran_surah_editorial.reviewed_by),
    reviewed_at = COALESCE(EXCLUDED.reviewed_at, quran_surah_editorial.reviewed_at),
    license_status = CASE WHEN $14 THEN EXCLUDED.license_status ELSE quran_surah_editorial.license_status END,
    checksum = EXCLUDED.checksum,
    -- Idempotent + backfill-safe: only bump lastmod when the row ALREADY had a
    -- checksum that differs. A pre-existing row whose checksum is still NULL (added
    -- by migration 20260623000002 with no backfill) gets its checksum populated on
    -- the first re-import WITHOUT a one-time mass updated_at/lastmod churn.
    updated_at = CASE WHEN quran_surah_editorial.checksum IS NOT NULL AND EXCLUDED.checksum IS DISTINCT FROM quran_surah_editorial.checksum THEN now() ELSE quran_surah_editorial.updated_at END
-- Run the UPDATE when content changed (or checksum is being backfilled), on an
-- explicit license override, OR when a provided provenance field differs (so
-- reviewer/author back-fills are not dropped by the content-only checksum gate).
WHERE EXCLUDED.checksum IS DISTINCT FROM quran_surah_editorial.checksum
   OR $14
   OR (EXCLUDED.author_name IS NOT NULL AND EXCLUDED.author_name IS DISTINCT FROM quran_surah_editorial.author_name)
   OR (EXCLUDED.reviewed_by IS NOT NULL AND EXCLUDED.reviewed_by IS DISTINCT FROM quran_surah_editorial.reviewed_by)
   OR (EXCLUDED.reviewed_at IS NOT NULL AND EXCLUDED.reviewed_at IS DISTINCT FROM quran_surah_editorial.reviewed_at)`,
			rec.SurahID, rec.Lang, rec.MetaTitle, rec.MetaDescription, rec.ArtiNama,
			rec.KeutamaanHTML, rec.AsbabunNuzulHTML, rec.PokokKandunganHTML,
			rec.AuthorName, rec.ReviewedBy, rec.ReviewedAt, rec.license, rec.checksum, rec.licenseOverride,
		); err != nil {
			return QuranSurahEditorialStats{}, surahEditorialExecError("editorial", rec, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return QuranSurahEditorialStats{}, fmt.Errorf("commit: %w", err)
	}

	return stats, nil
}

// surahEditorialExecError gives a readable message for a slug collision (the most
// likely failure when hand-authoring slugs) and falls back to a generic wrap.
func surahEditorialExecError(stage string, rec quranSurahEditorialRecord, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" && rec.Slug != nil {
		return fmt.Errorf("slug %q already assigned to another surah (surah %d): %w", *rec.Slug, rec.SurahID, err)
	}

	return fmt.Errorf("surah %d lang %s %s upsert: %w", rec.SurahID, rec.Lang, stage, err)
}

// surahEditorialChecksum hashes only the content-bearing fields (NOT license or
// provenance), so a no-op re-import or a publish does not bump updated_at / the
// sitemap lastmod.
func surahEditorialChecksum(rec quranSurahEditorialRecord) string { //nolint:gocritic // small value struct; a copy here is negligible on the import path
	h := sha256.New()
	writeOpt := func(p *string) {
		if p != nil {
			h.Write([]byte(*p))
		}

		h.Write([]byte{0})
	}
	writeOpt(rec.MetaTitle)
	writeOpt(rec.MetaDescription)
	writeOpt(rec.ArtiNama)
	writeOpt(rec.KeutamaanHTML)
	writeOpt(rec.AsbabunNuzulHTML)
	writeOpt(rec.PokokKandunganHTML)

	return hex.EncodeToString(h.Sum(nil))
}
