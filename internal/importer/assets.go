package importer

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/alfariesh/surau-backend/internal/readerlang"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	maxReaderAssetLineBytes          = 10 * 1024 * 1024
	assetKindTranslation             = "translation"
	assetKindHeadingSummary          = "heading_summary"
	assetKindAudio                   = "audio"
	assetKindBookMetadataTranslation = "book_metadata_translation"
	assetKindAuthorTranslation       = "author_translation"
	assetKindCategoryTranslation     = "category_translation"
	reviewedAssetStatus              = "reviewed"
)

// AssetOptions configure JSONL asset import.
type AssetOptions struct {
	PostgresURL string
	Path        string
}

// AssetStats describes imported JSONL assets.
type AssetStats struct {
	Translations             int
	Summaries                int
	Audio                    int
	BookMetadataTranslations int
	AuthorTranslations       int
	CategoryTranslations     int
	Skipped                  int
}

// ReaderAssetGeneration is the immutable identity of one machine generation.
type ReaderAssetGeneration struct {
	RunID         string `json:"run_id"`
	ModelID       string `json:"model_id"`
	PromptVersion string `json:"prompt_version"`
}

type assetGenerationRegistration struct {
	taskName      string
	modelID       string
	promptVersion string
	lineNumber    int
}

type assetTransactionBeginner interface {
	Begin(context.Context) (pgx.Tx, error)
}

// ReaderAsset is one JSONL record for future translation/audio pipelines.
type ReaderAsset struct {
	Kind              string                 `json:"kind"`
	BookID            int                    `json:"book_id"`
	AuthorID          int                    `json:"author_id,omitempty"`
	CategoryID        int                    `json:"category_id,omitempty"`
	HeadingID         int                    `json:"heading_id"`
	Lang              string                 `json:"lang"`
	Title             *string                `json:"title,omitempty"`
	DisplayTitle      *string                `json:"display_title,omitempty"`
	Name              *string                `json:"name,omitempty"`
	Summary           string                 `json:"summary,omitempty"`
	Biography         *string                `json:"biography,omitempty"`
	DeathText         *string                `json:"death_text,omitempty"`
	Bibliography      *string                `json:"bibliography,omitempty"`
	Hint              *string                `json:"hint,omitempty"`
	Description       *string                `json:"description,omitempty"`
	Content           string                 `json:"content,omitempty"`
	Source            *string                `json:"source,omitempty"`
	URL               string                 `json:"url,omitempty"`
	Narrator          *string                `json:"narrator,omitempty"`
	DurationSeconds   *int                   `json:"duration_seconds,omitempty"`
	MIMEType          *string                `json:"mime_type,omitempty"`
	Status            string                 `json:"translation_status,omitempty"`
	SummaryStatus     string                 `json:"summary_status,omitempty"`
	ReviewedBy        *string                `json:"translation_reviewed_by,omitempty"`
	ReviewedAt        *time.Time             `json:"translation_reviewed_at,omitempty"`
	SummaryReviewedBy *string                `json:"summary_reviewed_by,omitempty"`
	SummaryReviewedAt *time.Time             `json:"summary_reviewed_at,omitempty"`
	ProvenanceClass   string                 `json:"provenance_class,omitempty"`
	Generation        *ReaderAssetGeneration `json:"generation,omitempty"`
	Metadata          json.RawMessage        `json:"metadata,omitempty"`
	lineNumber        int
	skipWrite         bool
}

// Validate checks the minimum shape of one asset record.
func (a ReaderAsset) Validate() error {
	if strings.TrimSpace(a.Lang) == "" {
		return errors.New("lang is required")
	}
	if _, err := readerlang.Normalize(a.Lang); err != nil {
		return err
	}

	if a.Kind == assetKindHeadingSummary {
		if err := validateSummaryStatus(a.SummaryStatus, a.SummaryReviewedBy, a.Status, a.ReviewedBy); err != nil {
			return err
		}
	} else if err := validateTranslationStatus(a.Status, a.ReviewedBy); err != nil {
		return err
	}

	switch a.Kind {
	case assetKindTranslation:
		if a.BookID <= 0 {
			return errors.New("book_id is required")
		}
		if a.HeadingID <= 0 {
			return errors.New("heading_id is required")
		}
		if strings.TrimSpace(a.Content) == "" {
			return errors.New("content is required for translation")
		}
	case assetKindHeadingSummary:
		if a.BookID <= 0 {
			return errors.New("book_id is required")
		}
		if a.HeadingID <= 0 {
			return errors.New("heading_id is required")
		}
		if strings.TrimSpace(a.Summary) == "" {
			return errors.New("summary is required for heading summary")
		}
	case assetKindAudio:
		if a.BookID <= 0 {
			return errors.New("book_id is required")
		}
		if a.HeadingID <= 0 {
			return errors.New("heading_id is required")
		}
		if strings.TrimSpace(a.URL) == "" {
			return errors.New("url is required for audio")
		}
	case assetKindBookMetadataTranslation:
		if a.BookID <= 0 {
			return errors.New("book_id is required")
		}
		if stringPtrBlank(a.DisplayTitle) && stringPtrBlank(a.Title) && stringPtrBlank(a.Name) {
			return errors.New("display_title is required for book metadata translation")
		}
	case assetKindAuthorTranslation:
		if a.AuthorID <= 0 {
			return errors.New("author_id is required")
		}
		if stringPtrBlank(a.Name) {
			return errors.New("name is required for author translation")
		}
	case assetKindCategoryTranslation:
		if a.CategoryID <= 0 {
			return errors.New("category_id is required")
		}
		if stringPtrBlank(a.Name) {
			return errors.New("name is required for category translation")
		}
	default:
		return fmt.Errorf("unsupported kind %q", a.Kind)
	}

	if len(a.Metadata) > 0 && !json.Valid(a.Metadata) {
		return errors.New("metadata must be valid JSON")
	}

	if a.Kind == assetKindAudio {
		if a.ProvenanceClass != "" || a.Generation != nil {
			return errors.New("audio assets must not carry text provenance")
		}

		return nil
	}

	if a.ProvenanceClass != "machine" {
		return errors.New("text assets require provenance_class machine")
	}

	if a.Generation == nil {
		return errors.New("text assets require generation identity")
	}

	if _, err := uuid.Parse(a.Generation.RunID); err != nil {
		return errors.New("generation.run_id must be a UUID")
	}

	if strings.TrimSpace(a.Generation.ModelID) == "" {
		return errors.New("generation.model_id is required")
	}

	if strings.TrimSpace(a.Generation.PromptVersion) == "" {
		return errors.New("generation.prompt_version is required")
	}

	if _, err := generationTask(a.Kind, a.Generation.PromptVersion); err != nil {
		return err
	}

	return nil
}

// RunAssetImport imports translation/audio JSONL records.
func RunAssetImport(ctx context.Context, opts AssetOptions) (AssetStats, error) {
	if opts.PostgresURL == "" {
		return AssetStats{}, errors.New("postgres URL is required")
	}

	if opts.Path == "" {
		return AssetStats{}, errors.New("asset JSONL path is required")
	}

	file, err := os.Open(opts.Path)
	if err != nil {
		return AssetStats{}, fmt.Errorf("open asset JSONL: %w", err)
	}
	defer file.Close()

	pool, err := pgxpool.New(ctx, opts.PostgresURL)
	if err != nil {
		return AssetStats{}, fmt.Errorf("connecting postgres: %w", err)
	}
	defer pool.Close()

	return importAssets(ctx, pool, file)
}

func importAssets(ctx context.Context, database assetTransactionBeginner, reader io.Reader) (AssetStats, error) {
	assets, err := readReaderAssets(reader)
	if err != nil {
		return AssetStats{}, err
	}

	runs, err := collectAssetGenerationRuns(assets)
	if err != nil {
		return AssetStats{}, err
	}

	tx, err := database.Begin(ctx)
	if err != nil {
		return AssetStats{}, fmt.Errorf("begin asset import: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := resolveAssetReviewTimestamps(ctx, tx, assets); err != nil {
		return AssetStats{}, err
	}

	if err := ensureVisibleAssetWritesPermitted(ctx, tx, assets); err != nil {
		return AssetStats{}, err
	}

	if err := registerAssetGenerationRuns(ctx, tx, runs); err != nil {
		return AssetStats{}, err
	}

	stats := AssetStats{}
	batch := &pgx.Batch{}
	for i := range assets {
		if assets[i].skipWrite {
			stats.Skipped++

			continue
		}

		queueReaderAsset(batch, &assets[i], &stats)
	}

	if err := execTxBatch(ctx, tx, batch); err != nil {
		return stats, fmt.Errorf("upsert assets: %w", mapLicenseGateError(err))
	}

	if err := tx.Commit(ctx); err != nil {
		return stats, fmt.Errorf("commit asset import: %w", mapLicenseGateError(err))
	}

	return stats, nil
}

func readReaderAssets(reader io.Reader) ([]ReaderAsset, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, bufio.MaxScanTokenSize), maxReaderAssetLineBytes)
	assets := make([]ReaderAsset, 0)
	lineNumber := 0

	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var asset ReaderAsset
		if err := json.Unmarshal([]byte(line), &asset); err != nil {
			return nil, fmt.Errorf("line %d: invalid JSON: %w", lineNumber, err)
		}

		lang, err := readerlang.Normalize(asset.Lang)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNumber, err)
		}
		asset.Lang = lang
		asset.lineNumber = lineNumber

		if err := asset.Validate(); err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNumber, err)
		}

		assets = append(assets, asset)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan asset JSONL: %w", err)
	}

	return assets, nil
}

//nolint:funlen // Keeping the per-kind SQL together makes the atomic JSONL contract auditable.
func queueReaderAsset(batch *pgx.Batch, asset *ReaderAsset, stats *AssetStats) {
	metadata := ""
	if len(asset.Metadata) > 0 {
		metadata = string(asset.Metadata)
	}

	switch asset.Kind {
	case assetKindTranslation:
		stats.Translations++
		status := normalizeTranslationStatus(asset.Status)
		reviewedAt := reviewedAtOrNow(status, asset.ReviewedAt)
		batch.Queue(
			`
INSERT INTO section_translations (
    book_id, heading_id, lang, title, content, source, translation_status,
    reviewed_by, reviewed_at, metadata, provenance_class, generation_run_id, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, nullif($10, '')::jsonb, 'machine', $11, now())
ON CONFLICT (book_id, heading_id, lang) DO UPDATE SET
    title = EXCLUDED.title,
    content = EXCLUDED.content,
    source = EXCLUDED.source,
    translation_status = EXCLUDED.translation_status,
    reviewed_by = EXCLUDED.reviewed_by,
    reviewed_at = EXCLUDED.reviewed_at,
    metadata = EXCLUDED.metadata,
    provenance_class = EXCLUDED.provenance_class,
    generation_run_id = EXCLUDED.generation_run_id,
    updated_at = now()
WHERE ROW(
    section_translations.title, section_translations.content, section_translations.source,
    section_translations.translation_status, section_translations.reviewed_by,
    section_translations.reviewed_at, section_translations.metadata,
    section_translations.provenance_class, section_translations.generation_run_id
) IS DISTINCT FROM ROW(
    EXCLUDED.title, EXCLUDED.content, EXCLUDED.source, EXCLUDED.translation_status,
    EXCLUDED.reviewed_by, EXCLUDED.reviewed_at, EXCLUDED.metadata,
    EXCLUDED.provenance_class, EXCLUDED.generation_run_id
)`,
			asset.BookID,
			asset.HeadingID,
			asset.Lang,
			asset.Title,
			asset.Content,
			asset.Source,
			status,
			asset.ReviewedBy,
			reviewedAt,
			metadata,
			asset.Generation.RunID,
		)
	case assetKindHeadingSummary:
		stats.Summaries++
		status := normalizeSummaryStatus(asset.SummaryStatus, asset.Status)
		reviewedBy := firstNonBlankPtr(asset.SummaryReviewedBy, asset.ReviewedBy)
		reviewedAt := reviewedAtOrNow(status, firstNonNilTime(asset.SummaryReviewedAt, asset.ReviewedAt))
		batch.Queue(
			`
INSERT INTO book_heading_summaries (
    book_id, heading_id, lang, summary, source, summary_status,
    reviewed_by, reviewed_at, metadata, provenance_class, generation_run_id, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, nullif($9, '')::jsonb, 'machine', $10, now())
ON CONFLICT (book_id, heading_id, lang) DO UPDATE SET
    summary = EXCLUDED.summary,
    source = EXCLUDED.source,
    summary_status = EXCLUDED.summary_status,
    reviewed_by = EXCLUDED.reviewed_by,
    reviewed_at = EXCLUDED.reviewed_at,
    metadata = EXCLUDED.metadata,
    provenance_class = EXCLUDED.provenance_class,
    generation_run_id = EXCLUDED.generation_run_id,
    updated_at = now()
WHERE ROW(
    book_heading_summaries.summary, book_heading_summaries.source,
    book_heading_summaries.summary_status, book_heading_summaries.reviewed_by,
    book_heading_summaries.reviewed_at, book_heading_summaries.metadata,
    book_heading_summaries.provenance_class, book_heading_summaries.generation_run_id
) IS DISTINCT FROM ROW(
    EXCLUDED.summary, EXCLUDED.source, EXCLUDED.summary_status, EXCLUDED.reviewed_by,
    EXCLUDED.reviewed_at, EXCLUDED.metadata, EXCLUDED.provenance_class,
    EXCLUDED.generation_run_id
)`,
			asset.BookID,
			asset.HeadingID,
			asset.Lang,
			asset.Summary,
			asset.Source,
			status,
			reviewedBy,
			reviewedAt,
			metadata,
			asset.Generation.RunID,
		)
	case assetKindAudio:
		stats.Audio++

		batch.Queue(
			`
INSERT INTO section_audio (book_id, heading_id, lang, url, narrator, duration_seconds, mime_type, metadata, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, nullif($8, '')::jsonb, now())
ON CONFLICT (book_id, heading_id, lang) DO UPDATE SET
    url = EXCLUDED.url,
    narrator = EXCLUDED.narrator,
    duration_seconds = EXCLUDED.duration_seconds,
    mime_type = EXCLUDED.mime_type,
    metadata = EXCLUDED.metadata,
    updated_at = now()
WHERE ROW(
    section_audio.url, section_audio.narrator, section_audio.duration_seconds,
    section_audio.mime_type, section_audio.metadata
) IS DISTINCT FROM ROW(
    EXCLUDED.url, EXCLUDED.narrator, EXCLUDED.duration_seconds,
    EXCLUDED.mime_type, EXCLUDED.metadata
)`,
			asset.BookID,
			asset.HeadingID,
			asset.Lang,
			asset.URL,
			asset.Narrator,
			asset.DurationSeconds,
			asset.MIMEType,
			metadata,
		)
	case assetKindBookMetadataTranslation:
		stats.BookMetadataTranslations++
		displayTitle := firstNonBlankPtr(asset.DisplayTitle, asset.Title, asset.Name)
		status := normalizeTranslationStatus(asset.Status)
		reviewedAt := reviewedAtOrNow(status, asset.ReviewedAt)
		batch.Queue(
			`
INSERT INTO book_metadata_translations (
    book_id, lang, display_title, bibliography, hint, description, source,
    translation_status, reviewed_by, reviewed_at, metadata, provenance_class, generation_run_id, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, nullif($11, '')::jsonb, 'machine', $12, now())
ON CONFLICT (book_id, lang) DO UPDATE SET
    display_title = EXCLUDED.display_title,
    bibliography = EXCLUDED.bibliography,
    hint = EXCLUDED.hint,
    description = EXCLUDED.description,
    source = EXCLUDED.source,
    translation_status = EXCLUDED.translation_status,
    reviewed_by = EXCLUDED.reviewed_by,
    reviewed_at = EXCLUDED.reviewed_at,
    metadata = EXCLUDED.metadata,
    provenance_class = EXCLUDED.provenance_class,
    generation_run_id = EXCLUDED.generation_run_id,
    updated_at = now()
WHERE ROW(
    book_metadata_translations.display_title, book_metadata_translations.bibliography,
    book_metadata_translations.hint, book_metadata_translations.description,
    book_metadata_translations.source, book_metadata_translations.translation_status,
    book_metadata_translations.reviewed_by, book_metadata_translations.reviewed_at,
    book_metadata_translations.metadata, book_metadata_translations.provenance_class,
    book_metadata_translations.generation_run_id
) IS DISTINCT FROM ROW(
    EXCLUDED.display_title, EXCLUDED.bibliography, EXCLUDED.hint, EXCLUDED.description,
    EXCLUDED.source, EXCLUDED.translation_status, EXCLUDED.reviewed_by,
    EXCLUDED.reviewed_at, EXCLUDED.metadata, EXCLUDED.provenance_class,
    EXCLUDED.generation_run_id
)`,
			asset.BookID,
			asset.Lang,
			displayTitle,
			asset.Bibliography,
			asset.Hint,
			asset.Description,
			asset.Source,
			status,
			asset.ReviewedBy,
			reviewedAt,
			metadata,
			asset.Generation.RunID,
		)
	case assetKindAuthorTranslation:
		stats.AuthorTranslations++
		status := normalizeTranslationStatus(asset.Status)
		reviewedAt := reviewedAtOrNow(status, asset.ReviewedAt)
		batch.Queue(
			`
INSERT INTO author_translations (
    author_id, lang, name, biography, death_text, source, translation_status,
    reviewed_by, reviewed_at, metadata, provenance_class, generation_run_id, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, nullif($10, '')::jsonb, 'machine', $11, now())
ON CONFLICT (author_id, lang) DO UPDATE SET
    name = EXCLUDED.name,
    biography = EXCLUDED.biography,
    death_text = EXCLUDED.death_text,
    source = EXCLUDED.source,
    translation_status = EXCLUDED.translation_status,
    reviewed_by = EXCLUDED.reviewed_by,
    reviewed_at = EXCLUDED.reviewed_at,
    metadata = EXCLUDED.metadata,
    provenance_class = EXCLUDED.provenance_class,
    generation_run_id = EXCLUDED.generation_run_id,
    updated_at = now()
WHERE ROW(
    author_translations.name, author_translations.biography,
    author_translations.death_text, author_translations.source,
    author_translations.translation_status, author_translations.reviewed_by,
    author_translations.reviewed_at, author_translations.metadata,
    author_translations.provenance_class, author_translations.generation_run_id
) IS DISTINCT FROM ROW(
    EXCLUDED.name, EXCLUDED.biography, EXCLUDED.death_text, EXCLUDED.source,
    EXCLUDED.translation_status, EXCLUDED.reviewed_by, EXCLUDED.reviewed_at,
    EXCLUDED.metadata, EXCLUDED.provenance_class, EXCLUDED.generation_run_id
)`,
			asset.AuthorID,
			asset.Lang,
			asset.Name,
			asset.Biography,
			asset.DeathText,
			asset.Source,
			status,
			asset.ReviewedBy,
			reviewedAt,
			metadata,
			asset.Generation.RunID,
		)
	case assetKindCategoryTranslation:
		stats.CategoryTranslations++
		status := normalizeTranslationStatus(asset.Status)
		reviewedAt := reviewedAtOrNow(status, asset.ReviewedAt)
		batch.Queue(
			`
INSERT INTO category_translations (
    category_id, lang, name, source, translation_status, reviewed_by,
    reviewed_at, metadata, provenance_class, generation_run_id, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, nullif($8, '')::jsonb, 'machine', $9, now())
ON CONFLICT (category_id, lang) DO UPDATE SET
    name = EXCLUDED.name,
    source = EXCLUDED.source,
    translation_status = EXCLUDED.translation_status,
    reviewed_by = EXCLUDED.reviewed_by,
    reviewed_at = EXCLUDED.reviewed_at,
    metadata = EXCLUDED.metadata,
    provenance_class = EXCLUDED.provenance_class,
    generation_run_id = EXCLUDED.generation_run_id,
    updated_at = now()
WHERE ROW(
    category_translations.name, category_translations.source,
    category_translations.translation_status, category_translations.reviewed_by,
    category_translations.reviewed_at, category_translations.metadata,
    category_translations.provenance_class, category_translations.generation_run_id
) IS DISTINCT FROM ROW(
    EXCLUDED.name, EXCLUDED.source, EXCLUDED.translation_status, EXCLUDED.reviewed_by,
    EXCLUDED.reviewed_at, EXCLUDED.metadata, EXCLUDED.provenance_class,
    EXCLUDED.generation_run_id
)`,
			asset.CategoryID,
			asset.Lang,
			asset.Name,
			asset.Source,
			status,
			asset.ReviewedBy,
			reviewedAt,
			metadata,
			asset.Generation.RunID,
		)
	}
}

func collectAssetGenerationRuns(assets []ReaderAsset) (map[string]assetGenerationRegistration, error) {
	runs := make(map[string]assetGenerationRegistration)

	for i := range assets {
		asset := &assets[i]
		if asset.Generation == nil {
			continue
		}

		taskName, err := generationTask(asset.Kind, asset.Generation.PromptVersion)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", asset.lineNumber, err)
		}

		candidate := assetGenerationRegistration{
			taskName:      taskName,
			modelID:       strings.TrimSpace(asset.Generation.ModelID),
			promptVersion: strings.TrimSpace(asset.Generation.PromptVersion),
			lineNumber:    asset.lineNumber,
		}
		if previous, ok := runs[asset.Generation.RunID]; ok && !sameAssetGeneration(previous, candidate) {
			return nil, fmt.Errorf(
				"line %d: generation run %s conflicts with line %d",
				asset.lineNumber,
				asset.Generation.RunID,
				previous.lineNumber,
			)
		}

		runs[asset.Generation.RunID] = candidate
	}

	return runs, nil
}

func registerAssetGenerationRuns(
	ctx context.Context,
	tx pgx.Tx,
	runs map[string]assetGenerationRegistration,
) error {
	for runID, run := range runs {
		if _, err := tx.Exec(ctx, `
INSERT INTO generation_runs (id, task_name, model_id, prompt_version, metadata)
VALUES ($1, $2, $3, $4, '{"source":"reader_asset_import"}'::jsonb)
ON CONFLICT (id) DO NOTHING`, runID, run.taskName, run.modelID, run.promptVersion); err != nil {
			return fmt.Errorf("register generation run %s: %w", runID, err)
		}

		var (
			existing          assetGenerationRegistration
			descriptorMatches bool
		)

		if err := tx.QueryRow(ctx, `
SELECT task_name,
       model_id,
       prompt_version,
       provider IS NULL
       AND metadata = '{"source":"reader_asset_import"}'::jsonb AS descriptor_matches
FROM generation_runs
WHERE id = $1`, runID).Scan(
			&existing.taskName,
			&existing.modelID,
			&existing.promptVersion,
			&descriptorMatches,
		); err != nil {
			return fmt.Errorf("line %d: verify generation run %s: %w", run.lineNumber, runID, err)
		}

		if !sameAssetGeneration(existing, run) || !descriptorMatches {
			return fmt.Errorf("line %d: generation run %s conflicts with the registered identity", run.lineNumber, runID)
		}
	}

	return nil
}

func sameAssetGeneration(first, second assetGenerationRegistration) bool {
	return first.taskName == second.taskName &&
		first.modelID == second.modelID &&
		first.promptVersion == second.promptVersion
}

func generationTask(kind, promptVersion string) (string, error) {
	switch strings.TrimSpace(promptVersion) {
	case "reader-translation-v1":
		if kind == assetKindTranslation {
			return "reader_translation", nil
		}
	case "reader-summary-v1":
		if kind == assetKindHeadingSummary {
			return "reader_summary", nil
		}
	case "reader-summary-translation-v1":
		if kind == assetKindHeadingSummary {
			return "reader_summary_translation", nil
		}
	case "catalog-translation-v1":
		switch kind {
		case assetKindBookMetadataTranslation, assetKindAuthorTranslation, assetKindCategoryTranslation:
			return "catalog_translation", nil
		}
	}

	return "", fmt.Errorf("prompt_version %q is not valid for asset kind %q", promptVersion, kind)
}

func validateTranslationStatus(status string, reviewedBy *string) error {
	status = normalizeTranslationStatus(status)
	switch status {
	case "generated":
		return nil
	case reviewedAssetStatus:
		if stringPtrBlank(reviewedBy) {
			return errors.New("translation_reviewed_by is required when translation_status is reviewed")
		}
		return nil
	default:
		return fmt.Errorf("unsupported translation_status %q", status)
	}
}

func normalizeTranslationStatus(status string) string {
	status = strings.ToLower(strings.TrimSpace(status))
	if status == "" {
		return "generated"
	}

	return status
}

func validateSummaryStatus(summaryStatus string, summaryReviewedBy *string, legacyStatus string, legacyReviewedBy *string) error {
	status := normalizeSummaryStatus(summaryStatus, legacyStatus)
	reviewedBy := firstNonBlankPtr(summaryReviewedBy, legacyReviewedBy)
	switch status {
	case "generated":
		return nil
	case reviewedAssetStatus:
		if stringPtrBlank(reviewedBy) {
			return errors.New("summary_reviewed_by is required when summary_status is reviewed")
		}
		return nil
	default:
		return fmt.Errorf("unsupported summary_status %q", status)
	}
}

func normalizeSummaryStatus(summaryStatus, legacyStatus string) string {
	summaryStatus = strings.ToLower(strings.TrimSpace(summaryStatus))
	if summaryStatus != "" {
		return summaryStatus
	}

	return normalizeTranslationStatus(legacyStatus)
}

func reviewedAtOrNow(status string, reviewedAt *time.Time) *time.Time {
	if status != reviewedAssetStatus || reviewedAt != nil {
		return reviewedAt
	}

	now := time.Now().UTC()

	return &now
}

func firstNonNilTime(values ...*time.Time) *time.Time {
	for _, value := range values {
		if value != nil {
			return value
		}
	}

	return nil
}

func stringPtrBlank(value *string) bool {
	return value == nil || strings.TrimSpace(*value) == ""
}

func firstNonBlankPtr(values ...*string) *string {
	for _, value := range values {
		if !stringPtrBlank(value) {
			trimmed := strings.TrimSpace(*value)
			return &trimmed
		}
	}

	return nil
}
