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

	"github.com/alfariesh/surau-backend/internal/contentlang"
	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/repo/persistent"
	"github.com/alfariesh/surau-backend/pkg/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

// QuranAyahEditorialOptions configures a per-ayah editorial (intisari / keutamaan
// / FAQ / SEO meta) enrichment import.
type QuranAyahEditorialOptions struct {
	PostgresURL string
	Paths       []string
	DryRun      bool
	// Publish promotes every imported draft in the same transaction. The safe
	// default is false: importing content alone must never make it public.
	Publish bool
}

// QuranAyahEditorialStats summarizes one per-ayah editorial import run.
type QuranAyahEditorialStats struct {
	Files     int
	AyahRows  int // total records parsed
	Upserted  int // inserted or content-changed rows
	Skipped   int // re-import with byte-identical content (no-op, updated_at preserved)
	Published int // published rows effectively changed (requires Publish)
	DryRun    bool
	Publish   bool
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

var tafsirRangeRe = regexp.MustCompile(`^\d+(-\d+)?$`)

// RunQuranAyahEditorialImport parses per-ayah editorial JSON files and sends one
// atomic partial-update batch through the protected workflow. The safe default
// writes drafts; publishing is possible only with the explicit option and only
// when every resulting draft is permitted. Self-authored content does not write
// quran_import_runs.
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
		Publish:  opts.Publish,
	}
	if opts.DryRun {
		return stats, nil
	}

	pg, err := postgres.New(opts.PostgresURL)
	if err != nil {
		return QuranAyahEditorialStats{}, fmt.Errorf("connecting postgres: %w", err)
	}
	defer pg.Close()

	// Friendly per-surah ayah_number bound (the FK is the hard gate; this gives a
	// clear message instead of an opaque 23503).
	ayahMax, err := loadAyahMaxNumbers(ctx, pg.Pool)
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

	patches := make([]persistent.QuranAyahEditorialPatch, 0, len(records))
	for i := range records {
		rec := records[i]
		patches = append(patches, persistent.QuranAyahEditorialPatch{
			SurahID:         rec.SurahID,
			AyahNumber:      rec.AyahNumber,
			Lang:            rec.Lang,
			MetaTitle:       rec.MetaTitle,
			MetaDescription: rec.MetaDescription,
			IntisariHTML:    rec.IntisariHTML,
			KeutamaanHTML:   rec.KeutamaanHTML,
			FAQ:             quranAyahEditorialFAQs(rec.FAQ),
			FAQProvided:     rec.faqProvided,
			TafsirRange:     rec.TafsirRange,
			AuthorName:      rec.AuthorName,
			ReviewedBy:      rec.ReviewedBy,
			ReviewedAt:      rec.ReviewedAt,
			LicenseStatus:   rec.license,
			LicenseOverride: rec.licenseOverride,
		})
	}

	changed, published, err := persistent.NewEditorialRepo(pg).ImportAyahEditorialBatch(
		ctx, patches, opts.Publish,
	)
	if err != nil {
		return QuranAyahEditorialStats{}, fmt.Errorf("quran ayah editorial workflow: %w", err)
	}

	stats.Upserted = changed
	stats.Skipped = len(records) - changed
	stats.Published = published

	return stats, nil
}

func quranAyahEditorialFAQs(items []quranAyahEditorialFAQ) []entity.QuranAyahEditorialFAQ {
	result := make([]entity.QuranAyahEditorialFAQ, 0, len(items))
	for _, item := range items {
		result = append(result, entity.QuranAyahEditorialFAQ{
			Question:   item.Question,
			AnswerHTML: item.AnswerHTML,
		})
	}

	return result
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
