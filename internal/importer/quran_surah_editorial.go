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

	"github.com/alfariesh/surau-backend/internal/contentlang"
	"github.com/alfariesh/surau-backend/internal/repo/persistent"
	"github.com/alfariesh/surau-backend/pkg/postgres"
)

// QuranSurahEditorialOptions configures a surah editorial (keutamaan / asbabun
// nuzul / pokok kandungan / SEO meta) enrichment import.
type QuranSurahEditorialOptions struct {
	PostgresURL string
	Paths       []string
	DryRun      bool
	// Publish promotes every imported draft in the same transaction. The safe
	// default is false: importing content alone must never make it public.
	Publish bool
}

// QuranSurahEditorialStats summarizes one editorial import run.
type QuranSurahEditorialStats struct {
	Files         int
	SurahRows     int // distinct surah_id with slug/chronological_order/ruku_count set
	EditorialRows int // surah_id+lang records parsed
	Changed       int // drafts effectively changed (no-op imports excluded)
	Published     int // published rows effectively changed (requires Publish)
	DryRun        bool
	Publish       bool
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

// RunQuranSurahEditorialImport parses editorial JSON files and sends one atomic
// partial-update batch through the protected Quran editorial workflow. The safe
// default creates/updates drafts only. --publish additionally promotes every
// permitted draft and then applies slug/order/ruku metadata in the same commit.
// Self-authored content does not write quran_import_runs.
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
	chronoSeen := make(map[int]struct{})

	for i := range records {
		rec := &records[i]
		if rec.SurahID < 1 || rec.SurahID > 114 {
			return QuranSurahEditorialStats{}, fmt.Errorf("invalid surah_id %d (expected 1-114)", rec.SurahID)
		}

		// Reject values the surah SEO constraints (20260628000001) forbid, with a
		// clearer per-record message than a raw constraint violation at COMMIT.
		if rec.Slug != nil && strings.TrimSpace(*rec.Slug) == "" {
			return QuranSurahEditorialStats{}, fmt.Errorf("surah %d: slug must not be empty", rec.SurahID)
		}

		if rec.RukuCount != nil && *rec.RukuCount < 1 {
			return QuranSurahEditorialStats{}, fmt.Errorf("surah %d: ruku_count must be >= 1, got %d", rec.SurahID, *rec.RukuCount)
		}

		// chronological_order is a 1-114 permutation; catch an in-run duplicate before
		// it hits the unique index (the index also guards duplicates across runs).
		if rec.ChronologicalOrder != nil {
			if _, dup := chronoSeen[*rec.ChronologicalOrder]; dup {
				return QuranSurahEditorialStats{}, fmt.Errorf("chronological_order %d appears in more than one record", *rec.ChronologicalOrder)
			}

			chronoSeen[*rec.ChronologicalOrder] = struct{}{}
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

		rec.checksum = surahEditorialChecksum(rec)
		if rec.Slug != nil || rec.ChronologicalOrder != nil || rec.RukuCount != nil {
			surahSeen[rec.SurahID] = struct{}{}
		}
	}

	stats := QuranSurahEditorialStats{
		Files:         len(opts.Paths),
		SurahRows:     len(surahSeen),
		EditorialRows: len(records),
		DryRun:        opts.DryRun,
		Publish:       opts.Publish,
	}
	if opts.DryRun {
		return stats, nil
	}

	pg, err := postgres.New(opts.PostgresURL)
	if err != nil {
		return QuranSurahEditorialStats{}, fmt.Errorf("connecting postgres: %w", err)
	}
	defer pg.Close()

	patches := make([]persistent.QuranSurahEditorialPatch, 0, len(records))
	metadataBySurah := make(map[int]persistent.QuranSurahMetadataUpdate, len(surahSeen))

	for i := range records {
		rec := &records[i]
		patches = append(patches, persistent.QuranSurahEditorialPatch{
			SurahID:            rec.SurahID,
			Lang:               rec.Lang,
			MetaTitle:          rec.MetaTitle,
			MetaDescription:    rec.MetaDescription,
			ArtiNama:           rec.ArtiNama,
			KeutamaanHTML:      rec.KeutamaanHTML,
			AsbabunNuzulHTML:   rec.AsbabunNuzulHTML,
			PokokKandunganHTML: rec.PokokKandunganHTML,
			AuthorName:         rec.AuthorName,
			ReviewedBy:         rec.ReviewedBy,
			ReviewedAt:         rec.ReviewedAt,
			LicenseStatus:      rec.license,
			LicenseOverride:    rec.licenseOverride,
		})

		if mergeErr := mergeSurahMetadataUpdate(metadataBySurah, rec); mergeErr != nil {
			return QuranSurahEditorialStats{}, mergeErr
		}
	}

	metadata := make([]persistent.QuranSurahMetadataUpdate, 0, len(metadataBySurah))
	for _, update := range metadataBySurah {
		metadata = append(metadata, update)
	}

	changed, published, err := persistent.NewEditorialRepo(pg).ImportSurahEditorialBatch(
		ctx, patches, metadata, opts.Publish,
	)
	if err != nil {
		return QuranSurahEditorialStats{}, fmt.Errorf("quran surah editorial workflow: %w", err)
	}

	stats.Changed = changed
	stats.Published = published

	return stats, nil
}

func mergeSurahMetadataUpdate(
	updates map[int]persistent.QuranSurahMetadataUpdate,
	rec *quranSurahEditorialRecord,
) error {
	if rec.Slug == nil && rec.ChronologicalOrder == nil && rec.RukuCount == nil {
		return nil
	}

	update := updates[rec.SurahID]

	update.SurahID = rec.SurahID
	if err := mergeConsistentString(&update.Slug, rec.Slug, "slug", rec.SurahID); err != nil {
		return err
	}

	if err := mergeConsistentInt(
		&update.ChronologicalOrder, rec.ChronologicalOrder, "chronological_order", rec.SurahID,
	); err != nil {
		return err
	}

	if err := mergeConsistentInt(&update.RukuCount, rec.RukuCount, "ruku_count", rec.SurahID); err != nil {
		return err
	}

	updates[rec.SurahID] = update

	return nil
}

func mergeConsistentString(target **string, incoming *string, field string, surahID int) error {
	if incoming == nil {
		return nil
	}

	if *target != nil && **target != *incoming {
		return fmt.Errorf("surah %d has conflicting %s values", surahID, field)
	}

	*target = incoming

	return nil
}

func mergeConsistentInt(target **int, incoming *int, field string, surahID int) error {
	if incoming == nil {
		return nil
	}

	if *target != nil && **target != *incoming {
		return fmt.Errorf("surah %d has conflicting %s values", surahID, field)
	}

	*target = incoming

	return nil
}

// surahEditorialChecksum hashes only the content-bearing fields (NOT license or
// provenance), so a no-op re-import or a publish does not bump updated_at / the
// sitemap lastmod.
func surahEditorialChecksum(rec *quranSurahEditorialRecord) string {
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
