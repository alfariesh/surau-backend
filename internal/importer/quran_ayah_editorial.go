package importer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/evrone/go-clean-template/internal/contentlang"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// QuranAyahEditorialOptions configures a per-ayah editorial (intisari / keutamaan
// / FAQ / SEO meta) enrichment import.
type QuranAyahEditorialOptions struct {
	PostgresURL string
	Paths       []string
	DryRun      bool
}

// QuranAyahEditorialStats summarizes one per-ayah editorial import run.
type QuranAyahEditorialStats struct {
	Files    int
	AyahRows int // total records parsed
	Upserted int // inserted or content-changed rows
	Skipped  int // re-import with byte-identical content (no-op, updated_at preserved)
	DryRun   bool
}

type quranAyahEditorialFAQ struct {
	Question   string `json:"question"`
	AnswerHTML string `json:"answer_html"`
}

// quranAyahEditorialRecord is one JSON entry. Content fields are optional; an
// absent (null) field keeps the existing value on re-import (COALESCE upsert).
type quranAyahEditorialRecord struct {
	SurahID         int                     `json:"surah_id"`
	AyahNumber      int                     `json:"ayah_number"`
	Lang            string                  `json:"lang"`
	MetaTitle       *string                 `json:"meta_title"`
	MetaDescription *string                 `json:"meta_description"`
	IntisariHTML    *string                 `json:"intisari_html"`
	KeutamaanHTML   *string                 `json:"keutamaan_html"`
	FAQ             []quranAyahEditorialFAQ `json:"faq"`
	TafsirRange     *string                 `json:"tafsir_range"`
	AuthorName      *string                 `json:"author_name"`
	ReviewedBy      *string                 `json:"reviewed_by"`
	ReviewedAt      *time.Time              `json:"reviewed_at"`
	LicenseStatus   *string                 `json:"license_status"`

	// Derived during validation (not from JSON).
	faqProvided     bool // was the "faq" key present in the JSON object?
	faqJSON         []byte
	license         string
	licenseOverride bool
	checksum        string
}

const maxAyahNumber = 286 // Al-Baqarah; cheap upper bound for dry-run validation.

const numSurahs = 114

const ayahEditorialUpsertBatchSize = 1000

var tafsirRangeRe = regexp.MustCompile(`^\d+(-\d+)?$`)

const quranAyahEditorialUpsertSQL = `
INSERT INTO quran_ayah_editorial (
    surah_id, ayah_number, ayah_key, lang,
    meta_title, meta_description, intisari_html, keutamaan_html,
    faq, tafsir_range, author_name, reviewed_by, reviewed_at,
    license_status, checksum, updated_at
)
VALUES (
    $1, $2, ($1::integer)::text || ':' || ($2::integer)::text, $3,
    $4, $5, $6, $7,
    -- faq is NULL when the key was absent (keep stored); '[]'/[...] when provided.
    COALESCE($8::jsonb, '[]'::jsonb), $9, $10, $11, $12,
    $13, $14, now()
)
ON CONFLICT (surah_id, ayah_number, lang) DO UPDATE SET
    meta_title = COALESCE(EXCLUDED.meta_title, quran_ayah_editorial.meta_title),
    meta_description = COALESCE(EXCLUDED.meta_description, quran_ayah_editorial.meta_description),
    intisari_html = COALESCE(EXCLUDED.intisari_html, quran_ayah_editorial.intisari_html),
    keutamaan_html = COALESCE(EXCLUDED.keutamaan_html, quran_ayah_editorial.keutamaan_html),
    -- $16 = faq key present. When present, overwrite (an explicit [] clears the FAQ);
    -- when absent, keep the stored FAQ. (checksum already encodes presence-vs-empty.)
    faq = CASE WHEN $16 THEN EXCLUDED.faq ELSE quran_ayah_editorial.faq END,
    tafsir_range = COALESCE(EXCLUDED.tafsir_range, quran_ayah_editorial.tafsir_range),
    author_name = COALESCE(EXCLUDED.author_name, quran_ayah_editorial.author_name),
    reviewed_by = COALESCE(EXCLUDED.reviewed_by, quran_ayah_editorial.reviewed_by),
    reviewed_at = COALESCE(EXCLUDED.reviewed_at, quran_ayah_editorial.reviewed_at),
    -- LICENSE-PRESERVE: only an explicit override ($15=true) may change a stored
    -- license, so the skill's default 'needs_review' never un-publishes a human's
    -- 'permitted' on re-import.
    license_status = CASE WHEN $15 THEN EXCLUDED.license_status ELSE quran_ayah_editorial.license_status END,
    checksum = EXCLUDED.checksum,
    -- Idempotent: don't advance freshness (sitemap lastmod) when CONTENT is unchanged.
    -- Provenance-only changes (below) persist but must NOT bump updated_at.
    updated_at = CASE WHEN EXCLUDED.checksum IS DISTINCT FROM quran_ayah_editorial.checksum THEN now() ELSE quran_ayah_editorial.updated_at END
-- Run the UPDATE when content changed, on an explicit license override, OR when a
-- provided provenance field actually differs (so reviewer/author back-fills are not
-- silently dropped by the content-only checksum gate).
WHERE EXCLUDED.checksum IS DISTINCT FROM quran_ayah_editorial.checksum
   OR $15
   OR (EXCLUDED.author_name IS NOT NULL AND EXCLUDED.author_name IS DISTINCT FROM quran_ayah_editorial.author_name)
   OR (EXCLUDED.reviewed_by IS NOT NULL AND EXCLUDED.reviewed_by IS DISTINCT FROM quran_ayah_editorial.reviewed_by)
   OR (EXCLUDED.reviewed_at IS NOT NULL AND EXCLUDED.reviewed_at IS DISTINCT FROM quran_ayah_editorial.reviewed_at)`

// RunQuranAyahEditorialImport parses per-ayah editorial JSON files and upserts them
// into quran_ayah_editorial in one transaction (batched). Self-authored content, so
// it does NOT write quran_import_runs.
//
//nolint:gocognit,gocyclo,cyclop,funlen // linear import pipeline (validate opts -> parse -> range-check -> batch upsert); each branch is a distinct guard, not tangled logic
func RunQuranAyahEditorialImport(ctx context.Context, opts QuranAyahEditorialOptions) (QuranAyahEditorialStats, error) {
	if len(opts.Paths) == 0 {
		return QuranAyahEditorialStats{}, errors.New("at least one -ayah-editorial-json path is required")
	}

	if !opts.DryRun && strings.TrimSpace(opts.PostgresURL) == "" {
		return QuranAyahEditorialStats{}, errors.New("postgres URL is required")
	}

	records, err := parseQuranAyahEditorialFiles(opts.Paths)
	if err != nil {
		return QuranAyahEditorialStats{}, err
	}

	stats := QuranAyahEditorialStats{
		Files:    len(opts.Paths),
		AyahRows: len(records),
		DryRun:   opts.DryRun,
	}
	if opts.DryRun {
		return stats, nil
	}

	pool, err := pgxpool.New(ctx, opts.PostgresURL)
	if err != nil {
		return QuranAyahEditorialStats{}, fmt.Errorf("connecting postgres: %w", err)
	}
	defer pool.Close()

	// Friendly per-surah ayah_number bound (the FK is the hard gate; this gives a
	// clear message instead of an opaque 23503).
	ayahMax, err := loadAyahMaxNumbers(ctx, pool)
	if err != nil {
		return QuranAyahEditorialStats{}, err
	}

	for i := range records {
		rec := &records[i]
		if maxAyah, ok := ayahMax[rec.SurahID]; ok && rec.AyahNumber > maxAyah {
			return QuranAyahEditorialStats{}, fmt.Errorf(
				"ayah %d:%d out of range (surah %d has %d ayat)", rec.SurahID, rec.AyahNumber, rec.SurahID, maxAyah,
			)
		}
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return QuranAyahEditorialStats{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for start := 0; start < len(records); start += ayahEditorialUpsertBatchSize {
		end := start + ayahEditorialUpsertBatchSize
		if end > len(records) {
			end = len(records)
		}

		batch := &pgx.Batch{}

		for i := start; i < end; i++ {
			rec := records[i]
			batch.Queue(
				quranAyahEditorialUpsertSQL,
				rec.SurahID, rec.AyahNumber, rec.Lang,
				rec.MetaTitle, rec.MetaDescription, rec.IntisariHTML, rec.KeutamaanHTML,
				rec.faqJSON, rec.TafsirRange, rec.AuthorName, rec.ReviewedBy, rec.ReviewedAt,
				rec.license, rec.checksum, rec.licenseOverride, rec.faqProvided,
			)
		}

		br := tx.SendBatch(ctx, batch)
		for i := start; i < end; i++ {
			tag, execErr := br.Exec()
			if execErr != nil {
				_ = br.Close()

				return QuranAyahEditorialStats{}, ayahEditorialExecError(records[i], execErr)
			}

			if tag.RowsAffected() > 0 {
				stats.Upserted++
			} else {
				stats.Skipped++
			}
		}

		if closeErr := br.Close(); closeErr != nil {
			return QuranAyahEditorialStats{}, fmt.Errorf("batch close: %w", closeErr)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return QuranAyahEditorialStats{}, fmt.Errorf("commit: %w", err)
	}

	return stats, nil
}

// parseQuranAyahEditorialFiles reads + validates all records before any DB work.
func parseQuranAyahEditorialFiles(paths []string) ([]quranAyahEditorialRecord, error) {
	records := make([]quranAyahEditorialRecord, 0)
	// Fail fast on a duplicate (surah_id, ayah_number, lang) across all files: two
	// records for the same key would silently last-write-wins inside the batch.
	seen := make(map[string]string)

	for _, path := range paths {
		raw, _, err := readAssetFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}

		var rawRecords []json.RawMessage
		if err := json.Unmarshal(raw, &rawRecords); err != nil {
			return nil, fmt.Errorf("parsing %s: %w", path, err)
		}

		for idx, rawRec := range rawRecords {
			rec, err := decodeQuranAyahEditorialRecord(rawRec)
			if err != nil {
				return nil, fmt.Errorf("%s record %d: %w", path, idx, err)
			}

			key := fmt.Sprintf("%d:%d:%s", rec.SurahID, rec.AyahNumber, rec.Lang)
			if first, dup := seen[key]; dup {
				return nil, fmt.Errorf("%s record %d: duplicate ayah %d:%d lang %s (already defined at %s)",
					path, idx, rec.SurahID, rec.AyahNumber, rec.Lang, first)
			}

			seen[key] = fmt.Sprintf("%s record %d", path, idx)

			records = append(records, rec)
		}
	}

	return records, nil
}

//nolint:gocognit,gocyclo,cyclop // a flat sequence of independent field validations (surah/ayah/lang/faq/tafsir_range)
func decodeQuranAyahEditorialRecord(rawRec json.RawMessage) (quranAyahEditorialRecord, error) {
	// Reject reproduced tafsir: the enrich contract emits tafsir_range (a pointer),
	// never the tafsir body.
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(rawRec, &probe); err != nil {
		return quranAyahEditorialRecord{}, fmt.Errorf("not a JSON object: %w", err)
	}

	if _, ok := probe["tafsir_html"]; ok {
		return quranAyahEditorialRecord{}, errors.New("forbidden field tafsir_html (this layer is SEO enrichment, not the tafsir body)")
	}

	var rec quranAyahEditorialRecord
	// Strict decode: a typo'd content key (e.g. "intisari" vs "intisari_html") must be
	// a hard error, not a silent no-op that the COALESCE upsert hides.
	dec := json.NewDecoder(bytes.NewReader(rawRec))
	dec.DisallowUnknownFields()

	if err := dec.Decode(&rec); err != nil {
		return quranAyahEditorialRecord{}, err
	}
	// Distinguish "faq key absent" (keep stored FAQ) from "faq present" (overwrite,
	// including an explicit [] to clear it).
	_, rec.faqProvided = probe["faq"]

	if rec.SurahID < 1 || rec.SurahID > 114 {
		return quranAyahEditorialRecord{}, fmt.Errorf("invalid surah_id %d (expected 1-114)", rec.SurahID)
	}

	if rec.AyahNumber < 1 || rec.AyahNumber > maxAyahNumber {
		return quranAyahEditorialRecord{}, fmt.Errorf("invalid ayah_number %d (expected 1-%d)", rec.AyahNumber, maxAyahNumber)
	}

	lang, err := contentlang.Normalize(rec.Lang)
	if err != nil {
		return quranAyahEditorialRecord{}, fmt.Errorf("invalid lang %q: %w", rec.Lang, err)
	}

	rec.Lang = lang

	for j, item := range rec.FAQ {
		if strings.TrimSpace(item.Question) == "" || strings.TrimSpace(item.AnswerHTML) == "" {
			return quranAyahEditorialRecord{}, fmt.Errorf("faq[%d] needs both question and answer_html", j)
		}
	}

	if rec.TafsirRange != nil && !tafsirRangeRe.MatchString(*rec.TafsirRange) {
		return quranAyahEditorialRecord{}, fmt.Errorf("invalid tafsir_range %q (expected N or N-M)", *rec.TafsirRange)
	}

	rec.license, rec.licenseOverride, err = resolveEditorialLicense(rec.LicenseStatus)
	if err != nil {
		return quranAyahEditorialRecord{}, err
	}

	switch {
	case !rec.faqProvided:
		rec.faqJSON = nil // absent → NULL param → keep stored FAQ on upsert
	case rec.FAQ == nil:
		rec.faqJSON = []byte("[]") // present-but-null → clear
	default:
		b, err := json.Marshal(rec.FAQ)
		if err != nil {
			return quranAyahEditorialRecord{}, fmt.Errorf("marshaling faq: %w", err)
		}

		rec.faqJSON = b
	}

	rec.checksum = ayahEditorialChecksum(rec)

	return rec, nil
}

// ayahEditorialChecksum hashes only the content-bearing fields (NOT license or
// provenance), so a provenance-only change or a publish (needs_review->permitted)
// does not bump updated_at / the sitemap lastmod.
func ayahEditorialChecksum(rec quranAyahEditorialRecord) string { //nolint:gocritic // small value struct; a copy here is negligible on the import path
	h := sha256.New()
	writeOpt := func(p *string) {
		if p != nil {
			h.Write([]byte(*p))
		}

		h.Write([]byte{0})
	}
	writeOpt(rec.MetaTitle)
	writeOpt(rec.MetaDescription)
	writeOpt(rec.IntisariHTML)
	writeOpt(rec.KeutamaanHTML)
	// Encode faq presence so "absent" (keep) and "present-empty" (clear) hash
	// differently — the checksum stays a pure function of the import payload.
	if rec.faqProvided {
		h.Write([]byte{1})
		h.Write(rec.faqJSON)
	} else {
		h.Write([]byte{0})
	}

	h.Write([]byte{0})
	writeOpt(rec.TafsirRange)

	return hex.EncodeToString(h.Sum(nil))
}

// loadAyahMaxNumbers returns surah_id -> highest imported ayah_number, used to
// validate ayah_number with a friendly message before the FK fires.
func loadAyahMaxNumbers(ctx context.Context, pool *pgxpool.Pool) (map[int]int, error) {
	rows, err := pool.Query(ctx, `SELECT surah_id, MAX(ayah_number) FROM quran_ayahs GROUP BY surah_id`)
	if err != nil {
		return nil, fmt.Errorf("loading ayah counts: %w", err)
	}
	defer rows.Close()

	out := make(map[int]int, numSurahs)

	for rows.Next() {
		var surahID, maxAyah int
		if err := rows.Scan(&surahID, &maxAyah); err != nil {
			return nil, fmt.Errorf("scanning ayah counts: %w", err)
		}

		out[surahID] = maxAyah
	}

	return out, rows.Err()
}

func ayahEditorialExecError(rec quranAyahEditorialRecord, err error) error { //nolint:gocritic // small value struct; a copy here is negligible on the error path
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23503":
			return fmt.Errorf("ayah %d:%d not found in quran_ayahs: %w", rec.SurahID, rec.AyahNumber, err)
		case "23514":
			return fmt.Errorf("ayah %d:%d lang %s violates a check (license/faq/tafsir_range): %w",
				rec.SurahID, rec.AyahNumber, rec.Lang, err)
		}
	}

	return fmt.Errorf("ayah %d:%d lang %s upsert: %w", rec.SurahID, rec.AyahNumber, rec.Lang, err)
}
