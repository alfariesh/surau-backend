package importer

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// QuranAudioR2SyncOptions configure R2 audio metadata sync.
type QuranAudioR2SyncOptions struct {
	PostgresURL   string
	ManifestPath  string
	PublicBaseURL string
	DryRun        bool
}

// QuranAudioR2SyncStats describes parsed and updated R2 audio metadata.
type QuranAudioR2SyncStats struct {
	Tracks     int
	Updated    int
	Missing    int
	PublicURLs int
	DryRun     bool
}

type quranAudioR2ManifestEntry struct {
	RecitationID string `json:"recitation_id"`
	TrackType    string `json:"track_type"`
	TrackKey     string `json:"track_key"`
	R2Key        string `json:"r2_key"`
	PublicURL    string
}

// RunQuranAudioR2Sync updates Quran audio track rows with Cloudflare R2 keys and URLs.
func RunQuranAudioR2Sync(ctx context.Context, opts QuranAudioR2SyncOptions) (QuranAudioR2SyncStats, error) {
	entries, stats, err := loadQuranAudioR2Manifest(opts.ManifestPath, opts.PublicBaseURL)
	if err != nil {
		return stats, err
	}
	stats.DryRun = opts.DryRun
	if opts.DryRun {
		return stats, nil
	}
	if strings.TrimSpace(opts.PostgresURL) == "" {
		return stats, errors.New("pg-url is required")
	}

	pool, err := pgxpool.New(ctx, opts.PostgresURL)
	if err != nil {
		return stats, fmt.Errorf("connect postgres: %w", err)
	}
	defer pool.Close()

	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return stats, fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	for _, entry := range entries {
		tag, err := tx.Exec(ctx, `
UPDATE quran_audio_tracks
SET r2_key = $4,
    public_url = CASE WHEN $5 = '' THEN public_url ELSE $5 END,
    updated_at = now()
WHERE recitation_id = $1
  AND track_type = $2
  AND track_key = $3`,
			entry.RecitationID,
			entry.TrackType,
			entry.TrackKey,
			entry.R2Key,
			entry.PublicURL,
		)
		if err != nil {
			return stats, fmt.Errorf("update quran audio track %s/%s/%s: %w", entry.RecitationID, entry.TrackType, entry.TrackKey, err)
		}
		if tag.RowsAffected() == 0 {
			stats.Missing++
			continue
		}
		stats.Updated++
	}

	if err := tx.Commit(ctx); err != nil {
		return stats, fmt.Errorf("commit transaction: %w", err)
	}
	return stats, nil
}

func loadQuranAudioR2Manifest(path string, publicBaseURL string) ([]quranAudioR2ManifestEntry, QuranAudioR2SyncStats, error) {
	var stats QuranAudioR2SyncStats
	if strings.TrimSpace(path) == "" {
		return nil, stats, errors.New("manifest-jsonl is required")
	}

	file, err := os.Open(path) // #nosec G304 -- sync CLI intentionally reads an operator-supplied manifest file.
	if err != nil {
		return nil, stats, fmt.Errorf("open manifest: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024), 1024*1024)

	entries := make([]quranAudioR2ManifestEntry, 0)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var entry quranAudioR2ManifestEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			return nil, stats, fmt.Errorf("decode manifest line %d: %w", lineNumber, err)
		}
		if err := validateQuranAudioR2ManifestEntry(entry, lineNumber); err != nil {
			return nil, stats, err
		}

		entry.PublicURL = quranAudioPublicURL(publicBaseURL, entry.R2Key)
		if entry.PublicURL != "" {
			stats.PublicURLs++
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return nil, stats, fmt.Errorf("read manifest: %w", err)
	}

	stats.Tracks = len(entries)
	return entries, stats, nil
}

func validateQuranAudioR2ManifestEntry(entry quranAudioR2ManifestEntry, lineNumber int) error {
	switch {
	case strings.TrimSpace(entry.RecitationID) == "":
		return fmt.Errorf("manifest line %d missing recitation_id", lineNumber)
	case strings.TrimSpace(entry.TrackType) == "":
		return fmt.Errorf("manifest line %d missing track_type", lineNumber)
	case strings.TrimSpace(entry.TrackKey) == "":
		return fmt.Errorf("manifest line %d missing track_key", lineNumber)
	case strings.TrimSpace(entry.R2Key) == "":
		return fmt.Errorf("manifest line %d missing r2_key", lineNumber)
	}
	return nil
}

func quranAudioPublicURL(publicBaseURL string, r2Key string) string {
	publicBaseURL = strings.TrimSpace(publicBaseURL)
	if publicBaseURL == "" {
		return ""
	}
	return strings.TrimRight(publicBaseURL, "/") + "/" + strings.TrimLeft(r2Key, "/")
}
