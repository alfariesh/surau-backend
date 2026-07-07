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
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
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

// ReaderAsset is one JSONL record for future translation/audio pipelines.
type ReaderAsset struct {
	Kind              string          `json:"kind"`
	BookID            int             `json:"book_id"`
	AuthorID          int             `json:"author_id,omitempty"`
	CategoryID        int             `json:"category_id,omitempty"`
	HeadingID         int             `json:"heading_id"`
	Lang              string          `json:"lang"`
	Title             *string         `json:"title,omitempty"`
	DisplayTitle      *string         `json:"display_title,omitempty"`
	Name              *string         `json:"name,omitempty"`
	Summary           string          `json:"summary,omitempty"`
	Biography         *string         `json:"biography,omitempty"`
	DeathText         *string         `json:"death_text,omitempty"`
	Bibliography      *string         `json:"bibliography,omitempty"`
	Hint              *string         `json:"hint,omitempty"`
	Description       *string         `json:"description,omitempty"`
	Content           string          `json:"content,omitempty"`
	Source            *string         `json:"source,omitempty"`
	URL               string          `json:"url,omitempty"`
	Narrator          *string         `json:"narrator,omitempty"`
	DurationSeconds   *int            `json:"duration_seconds,omitempty"`
	MIMEType          *string         `json:"mime_type,omitempty"`
	Status            string          `json:"translation_status,omitempty"`
	SummaryStatus     string          `json:"summary_status,omitempty"`
	ReviewedBy        *string         `json:"translation_reviewed_by,omitempty"`
	ReviewedAt        *time.Time      `json:"translation_reviewed_at,omitempty"`
	SummaryReviewedBy *string         `json:"summary_reviewed_by,omitempty"`
	SummaryReviewedAt *time.Time      `json:"summary_reviewed_at,omitempty"`
	Metadata          json.RawMessage `json:"metadata,omitempty"`
}

// Validate checks the minimum shape of one asset record.
func (a ReaderAsset) Validate() error {
	if strings.TrimSpace(a.Lang) == "" {
		return errors.New("lang is required")
	}
	if _, err := readerlang.Normalize(a.Lang); err != nil {
		return err
	}
	if a.Kind == "heading_summary" {
		if err := validateSummaryStatus(a.SummaryStatus, a.SummaryReviewedBy, a.Status, a.ReviewedBy); err != nil {
			return err
		}
	} else if err := validateTranslationStatus(a.Status, a.ReviewedBy); err != nil {
		return err
	}

	switch a.Kind {
	case "translation":
		if a.BookID <= 0 {
			return errors.New("book_id is required")
		}
		if a.HeadingID <= 0 {
			return errors.New("heading_id is required")
		}
		if strings.TrimSpace(a.Content) == "" {
			return errors.New("content is required for translation")
		}
	case "heading_summary":
		if a.BookID <= 0 {
			return errors.New("book_id is required")
		}
		if a.HeadingID <= 0 {
			return errors.New("heading_id is required")
		}
		if strings.TrimSpace(a.Summary) == "" {
			return errors.New("summary is required for heading summary")
		}
	case "audio":
		if a.BookID <= 0 {
			return errors.New("book_id is required")
		}
		if a.HeadingID <= 0 {
			return errors.New("heading_id is required")
		}
		if strings.TrimSpace(a.URL) == "" {
			return errors.New("url is required for audio")
		}
	case "book_metadata_translation":
		if a.BookID <= 0 {
			return errors.New("book_id is required")
		}
		if stringPtrBlank(a.DisplayTitle) && stringPtrBlank(a.Title) && stringPtrBlank(a.Name) {
			return errors.New("display_title is required for book metadata translation")
		}
	case "author_translation":
		if a.AuthorID <= 0 {
			return errors.New("author_id is required")
		}
		if stringPtrBlank(a.Name) {
			return errors.New("name is required for author translation")
		}
	case "category_translation":
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

func importAssets(ctx context.Context, pool *pgxpool.Pool, reader io.Reader) (AssetStats, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 10*1024*1024)

	stats := AssetStats{}
	batch := &pgx.Batch{}

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var asset ReaderAsset
		if err := json.Unmarshal([]byte(line), &asset); err != nil {
			stats.Skipped++
			continue
		}

		lang, err := readerlang.Normalize(asset.Lang)
		if err != nil {
			stats.Skipped++
			continue
		}
		asset.Lang = lang
		if err := asset.Validate(); err != nil {
			stats.Skipped++
			continue
		}

		metadata := ""
		if len(asset.Metadata) > 0 {
			metadata = string(asset.Metadata)
		}

		switch asset.Kind {
		case "translation":
			stats.Translations++
			status := normalizeTranslationStatus(asset.Status)
			reviewedAt := reviewedAtOrNow(status, asset.ReviewedAt)
			batch.Queue(
				`
INSERT INTO section_translations (
    book_id, heading_id, lang, title, content, source, translation_status,
    reviewed_by, reviewed_at, metadata, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, nullif($10, '')::jsonb, now())
ON CONFLICT (book_id, heading_id, lang) DO UPDATE SET
    title = EXCLUDED.title,
    content = EXCLUDED.content,
    source = EXCLUDED.source,
    translation_status = EXCLUDED.translation_status,
    reviewed_by = EXCLUDED.reviewed_by,
    reviewed_at = EXCLUDED.reviewed_at,
    metadata = EXCLUDED.metadata,
    updated_at = now()`,
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
			)
		case "heading_summary":
			stats.Summaries++
			status := normalizeSummaryStatus(asset.SummaryStatus, asset.Status)
			reviewedBy := firstNonBlankPtr(asset.SummaryReviewedBy, asset.ReviewedBy)
			reviewedAt := reviewedAtOrNow(status, firstNonNilTime(asset.SummaryReviewedAt, asset.ReviewedAt))
			batch.Queue(
				`
INSERT INTO book_heading_summaries (
    book_id, heading_id, lang, summary, source, summary_status,
    reviewed_by, reviewed_at, metadata, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, nullif($9, '')::jsonb, now())
ON CONFLICT (book_id, heading_id, lang) DO UPDATE SET
    summary = EXCLUDED.summary,
    source = EXCLUDED.source,
    summary_status = EXCLUDED.summary_status,
    reviewed_by = EXCLUDED.reviewed_by,
    reviewed_at = EXCLUDED.reviewed_at,
    metadata = EXCLUDED.metadata,
    updated_at = now()`,
				asset.BookID,
				asset.HeadingID,
				asset.Lang,
				asset.Summary,
				asset.Source,
				status,
				reviewedBy,
				reviewedAt,
				metadata,
			)
		case "audio":
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
    updated_at = now()`,
				asset.BookID,
				asset.HeadingID,
				asset.Lang,
				asset.URL,
				asset.Narrator,
				asset.DurationSeconds,
				asset.MIMEType,
				metadata,
			)
		case "book_metadata_translation":
			stats.BookMetadataTranslations++
			displayTitle := firstNonBlankPtr(asset.DisplayTitle, asset.Title, asset.Name)
			status := normalizeTranslationStatus(asset.Status)
			reviewedAt := reviewedAtOrNow(status, asset.ReviewedAt)
			batch.Queue(
				`
INSERT INTO book_metadata_translations (
    book_id, lang, display_title, bibliography, hint, description, source,
    translation_status, reviewed_by, reviewed_at, metadata, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, nullif($11, '')::jsonb, now())
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
    updated_at = now()`,
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
			)
		case "author_translation":
			stats.AuthorTranslations++
			status := normalizeTranslationStatus(asset.Status)
			reviewedAt := reviewedAtOrNow(status, asset.ReviewedAt)
			batch.Queue(
				`
INSERT INTO author_translations (
    author_id, lang, name, biography, death_text, source, translation_status,
    reviewed_by, reviewed_at, metadata, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, nullif($10, '')::jsonb, now())
ON CONFLICT (author_id, lang) DO UPDATE SET
    name = EXCLUDED.name,
    biography = EXCLUDED.biography,
    death_text = EXCLUDED.death_text,
    source = EXCLUDED.source,
    translation_status = EXCLUDED.translation_status,
    reviewed_by = EXCLUDED.reviewed_by,
    reviewed_at = EXCLUDED.reviewed_at,
    metadata = EXCLUDED.metadata,
    updated_at = now()`,
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
			)
		case "category_translation":
			stats.CategoryTranslations++
			status := normalizeTranslationStatus(asset.Status)
			reviewedAt := reviewedAtOrNow(status, asset.ReviewedAt)
			batch.Queue(
				`
INSERT INTO category_translations (
    category_id, lang, name, source, translation_status, reviewed_by,
    reviewed_at, metadata, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, nullif($8, '')::jsonb, now())
ON CONFLICT (category_id, lang) DO UPDATE SET
    name = EXCLUDED.name,
    source = EXCLUDED.source,
    translation_status = EXCLUDED.translation_status,
    reviewed_by = EXCLUDED.reviewed_by,
    reviewed_at = EXCLUDED.reviewed_at,
    metadata = EXCLUDED.metadata,
    updated_at = now()`,
				asset.CategoryID,
				asset.Lang,
				asset.Name,
				asset.Source,
				status,
				asset.ReviewedBy,
				reviewedAt,
				metadata,
			)
		}
	}

	if err := scanner.Err(); err != nil {
		return stats, fmt.Errorf("scan asset JSONL: %w", err)
	}

	if err := execBatch(ctx, pool, batch); err != nil {
		return stats, fmt.Errorf("upsert assets: %w", err)
	}

	return stats, nil
}

func validateTranslationStatus(status string, reviewedBy *string) error {
	status = normalizeTranslationStatus(status)
	switch status {
	case "generated":
		return nil
	case "reviewed":
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
	case "reviewed":
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
	if status != "reviewed" || reviewedAt != nil {
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
