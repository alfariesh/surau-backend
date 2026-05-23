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
	Translations int
	Audio        int
	Skipped      int
}

// ReaderAsset is one JSONL record for future translation/audio pipelines.
type ReaderAsset struct {
	Kind            string          `json:"kind"`
	BookID          int             `json:"book_id"`
	HeadingID       int             `json:"heading_id"`
	Lang            string          `json:"lang"`
	Title           *string         `json:"title,omitempty"`
	Content         string          `json:"content,omitempty"`
	Source          *string         `json:"source,omitempty"`
	URL             string          `json:"url,omitempty"`
	Narrator        *string         `json:"narrator,omitempty"`
	DurationSeconds *int            `json:"duration_seconds,omitempty"`
	MIMEType        *string         `json:"mime_type,omitempty"`
	Metadata        json.RawMessage `json:"metadata,omitempty"`
}

// Validate checks the minimum shape of one asset record.
func (a ReaderAsset) Validate() error {
	if a.BookID <= 0 {
		return errors.New("book_id is required")
	}

	if a.HeadingID <= 0 {
		return errors.New("heading_id is required")
	}

	if strings.TrimSpace(a.Lang) == "" {
		return errors.New("lang is required")
	}

	switch a.Kind {
	case "translation":
		if strings.TrimSpace(a.Content) == "" {
			return errors.New("content is required for translation")
		}
	case "audio":
		if strings.TrimSpace(a.URL) == "" {
			return errors.New("url is required for audio")
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

		asset.Lang = strings.ToLower(strings.TrimSpace(asset.Lang))
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
			batch.Queue(`
INSERT INTO section_translations (book_id, heading_id, lang, title, content, source, metadata, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, nullif($7, '')::jsonb, now())
ON CONFLICT (book_id, heading_id, lang) DO UPDATE SET
    title = EXCLUDED.title,
    content = EXCLUDED.content,
    source = EXCLUDED.source,
    metadata = EXCLUDED.metadata,
    updated_at = now()`,
				asset.BookID,
				asset.HeadingID,
				asset.Lang,
				asset.Title,
				asset.Content,
				asset.Source,
				metadata,
			)
		case "audio":
			stats.Audio++
			batch.Queue(`
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
