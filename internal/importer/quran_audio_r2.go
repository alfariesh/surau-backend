package importer

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/alfariesh/surau-backend/internal/quranutil"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	quranAudioR2TrackTypeAyah  = "ayah"
	quranAudioR2TrackTypeSurah = "surah"

	quranAudioR2DefaultFormat        = "jsonl"
	quranAudioR2DefaultLicenseStatus = "needs_review"
)

var (
	errQuranAudioR2PGURLRequired             = errors.New("pg-url is required")
	errQuranAudioR2ManifestPathRequired      = errors.New("manifest-jsonl is required")
	errQuranAudioR2RecitationMetadataID      = errors.New("recitation metadata item missing id")
	errQuranAudioR2ManifestMissingRecitation = errors.New("missing recitation_id")
	errQuranAudioR2ManifestMissingTrackType  = errors.New("missing track_type")
	errQuranAudioR2ManifestMissingTrackKey   = errors.New("missing track_key")
	errQuranAudioR2ManifestMissingR2Key      = errors.New("missing r2_key")
	errQuranAudioR2ManifestInvalidAyahKey    = errors.New("invalid ayah track_key")
	errQuranAudioR2ManifestInvalidSurahKey   = errors.New("invalid surah track_key")
	errQuranAudioR2ManifestTrackKeyMismatch  = errors.New("track_key does not match surah_id/ayah_number")
	errQuranAudioR2ManifestInvalidTrackType  = errors.New("invalid track_type")
)

// QuranAudioR2SyncOptions configure R2 audio metadata sync.
type QuranAudioR2SyncOptions struct {
	PostgresURL            string
	ManifestPath           string
	RecitationMetadataPath string
	PublicBaseURL          string
	DryRun                 bool
}

// QuranAudioR2SyncStats describes parsed and updated R2 audio metadata.
type QuranAudioR2SyncStats struct {
	Recitations int
	Tracks      int
	Updated     int
	PublicURLs  int
	DryRun      bool
}

type quranAudioR2ManifestEntry struct {
	RecitationID    string          `json:"recitation_id"`
	TrackType       string          `json:"track_type"`
	TrackKey        string          `json:"track_key"`
	SurahID         int             `json:"surah_id"`
	AyahNumber      int             `json:"ayah_number"`
	AudioURL        string          `json:"audio_url"`
	R2Key           string          `json:"r2_key"`
	DurationMS      *int            `json:"duration_ms"`
	DurationSeconds *int            `json:"duration_seconds"`
	MIMEType        string          `json:"mime_type"`
	Metadata        json.RawMessage `json:"metadata"`
	PublicURL       string
	ClearPublicURL  bool
}

type quranAudioR2RecitationMetadata struct {
	ID              string          `json:"id"`
	Name            string          `json:"name"`
	DisplayName     string          `json:"display_name"`
	ReciterName     string          `json:"reciter_name"`
	Style           string          `json:"style"`
	Mode            string          `json:"mode"`
	SourceURL       string          `json:"source_url"`
	QULResourceID   string          `json:"qul_resource_id"`
	Format          string          `json:"format"`
	LicenseStatus   string          `json:"license_status"`
	SortOrder       int             `json:"sort_order"`
	DefaultPriority *int            `json:"default_priority"`
	IsVisible       *bool           `json:"is_visible"`
	UsePublicURL    *bool           `json:"use_public_url"`
	Metadata        json.RawMessage `json:"metadata"`
}

// RunQuranAudioR2Sync upserts Quran recitation rows and R2-backed audio track metadata.
func RunQuranAudioR2Sync(ctx context.Context, opts QuranAudioR2SyncOptions) (QuranAudioR2SyncStats, error) {
	metadata, err := loadQuranAudioR2RecitationMetadata(opts.RecitationMetadataPath)
	if err != nil {
		return QuranAudioR2SyncStats{}, err
	}

	entries, stats, err := loadQuranAudioR2Manifest(opts.ManifestPath, opts.PublicBaseURL)
	if err != nil {
		return stats, err
	}

	stats.PublicURLs = applyQuranAudioR2PublicURLPolicy(entries, metadata)
	stats.DryRun = opts.DryRun

	stats.Recitations = len(recitationsFromManifest(entries, metadata))
	if opts.DryRun {
		return stats, nil
	}
	if strings.TrimSpace(opts.PostgresURL) == "" {
		return stats, errQuranAudioR2PGURLRequired
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

	recitations := recitationsFromManifest(entries, metadata)
	if err := upsertQuranAudioR2Recitations(ctx, tx, recitations); err != nil {
		return stats, err
	}

	if err := upsertQuranAudioR2Tracks(ctx, tx, entries); err != nil {
		return stats, err
	}

	stats.Updated = len(entries)

	if err = tx.Commit(ctx); err != nil {
		return stats, fmt.Errorf("commit transaction: %w", err)
	}
	return stats, nil
}

func loadQuranAudioR2Manifest(path, publicBaseURL string) ([]quranAudioR2ManifestEntry, QuranAudioR2SyncStats, error) {
	var stats QuranAudioR2SyncStats
	if strings.TrimSpace(path) == "" {
		return nil, stats, errQuranAudioR2ManifestPathRequired
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

		if err := normalizeQuranAudioR2ManifestEntry(&entry, lineNumber); err != nil {
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

func loadQuranAudioR2RecitationMetadata(path string) (map[string]quranAudioR2RecitationMetadata, error) {
	if strings.TrimSpace(path) == "" {
		return map[string]quranAudioR2RecitationMetadata{}, nil
	}

	raw, err := os.ReadFile(path) // #nosec G304 -- sync CLI intentionally reads an operator-supplied metadata file.
	if err != nil {
		return nil, fmt.Errorf("open recitation metadata: %w", err)
	}

	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return map[string]quranAudioR2RecitationMetadata{}, nil
	}

	if raw[0] == '[' {
		return decodeQuranAudioR2RecitationMetadataList(raw)
	}

	return decodeQuranAudioR2RecitationMetadataObject(raw)
}

func decodeQuranAudioR2RecitationMetadataList(raw []byte) (map[string]quranAudioR2RecitationMetadata, error) {
	var list []quranAudioR2RecitationMetadata
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("decode recitation metadata: %w", err)
	}

	items := make(map[string]quranAudioR2RecitationMetadata, len(list))
	for i := range list {
		item := &list[i]
		if strings.TrimSpace(item.ID) == "" {
			return nil, errQuranAudioR2RecitationMetadataID
		}

		items[item.ID] = *item
	}

	return items, nil
}

func decodeQuranAudioR2RecitationMetadataObject(raw []byte) (map[string]quranAudioR2RecitationMetadata, error) {
	var keyed map[string]quranAudioR2RecitationMetadata
	if err := json.Unmarshal(raw, &keyed); err != nil {
		return nil, fmt.Errorf("decode recitation metadata: %w", err)
	}

	items := make(map[string]quranAudioR2RecitationMetadata, len(keyed))
	for id := range keyed {
		item := keyed[id]
		if strings.TrimSpace(item.ID) == "" {
			item.ID = id
		}

		if strings.TrimSpace(item.ID) == "" {
			return nil, errQuranAudioR2RecitationMetadataID
		}

		items[item.ID] = item
	}

	return items, nil
}

func normalizeQuranAudioR2ManifestEntry(entry *quranAudioR2ManifestEntry, lineNumber int) error {
	entry.RecitationID = strings.TrimSpace(entry.RecitationID)
	entry.TrackType = strings.TrimSpace(entry.TrackType)
	entry.TrackKey = strings.TrimSpace(entry.TrackKey)
	entry.R2Key = strings.TrimSpace(entry.R2Key)
	entry.AudioURL = strings.TrimSpace(entry.AudioURL)

	entry.MIMEType = strings.TrimSpace(entry.MIMEType)
	if len(entry.Metadata) == 0 {
		entry.Metadata = json.RawMessage(`{}`)
	}

	if err := validateQuranAudioR2ManifestRequired(entry); err != nil {
		return quranAudioR2ManifestLineError(lineNumber, err)
	}

	switch entry.TrackType {
	case quranAudioR2TrackTypeAyah:
		return normalizeQuranAudioR2AyahManifestEntry(entry, lineNumber)
	case quranAudioR2TrackTypeSurah:
		return normalizeQuranAudioR2SurahManifestEntry(entry, lineNumber)
	default:
		return quranAudioR2ManifestLineError(lineNumber, errQuranAudioR2ManifestInvalidTrackType)
	}
}

func validateQuranAudioR2ManifestRequired(entry *quranAudioR2ManifestEntry) error {
	switch {
	case entry.RecitationID == "":
		return errQuranAudioR2ManifestMissingRecitation
	case entry.TrackType == "":
		return errQuranAudioR2ManifestMissingTrackType
	case entry.TrackKey == "":
		return errQuranAudioR2ManifestMissingTrackKey
	case entry.R2Key == "":
		return errQuranAudioR2ManifestMissingR2Key
	}

	return nil
}

func normalizeQuranAudioR2AyahManifestEntry(entry *quranAudioR2ManifestEntry, lineNumber int) error {
	surahID, ayahNumber, err := quranutil.ParseAyahKey(entry.TrackKey)
	if err != nil {
		return quranAudioR2ManifestLineError(lineNumber, errQuranAudioR2ManifestInvalidAyahKey)
	}

	if entry.SurahID == 0 {
		entry.SurahID = surahID
	}

	if entry.AyahNumber == 0 {
		entry.AyahNumber = ayahNumber
	}

	if entry.SurahID != surahID || entry.AyahNumber != ayahNumber {
		return quranAudioR2ManifestLineError(lineNumber, errQuranAudioR2ManifestTrackKeyMismatch)
	}

	return nil
}

func normalizeQuranAudioR2SurahManifestEntry(entry *quranAudioR2ManifestEntry, lineNumber int) error {
	surahID, err := strconv.Atoi(entry.TrackKey)
	if err != nil || surahID <= 0 {
		return quranAudioR2ManifestLineError(lineNumber, errQuranAudioR2ManifestInvalidSurahKey)
	}

	if entry.SurahID == 0 {
		entry.SurahID = surahID
	}

	if entry.SurahID != surahID || entry.AyahNumber != 0 {
		return quranAudioR2ManifestLineError(lineNumber, errQuranAudioR2ManifestTrackKeyMismatch)
	}

	return nil
}

func quranAudioR2ManifestLineError(lineNumber int, err error) error {
	return fmt.Errorf("manifest line %d: %w", lineNumber, err)
}

func recitationsFromManifest(
	entries []quranAudioR2ManifestEntry,
	metadata map[string]quranAudioR2RecitationMetadata,
) []quranAudioR2RecitationMetadata {
	byID := make(map[string]quranAudioR2RecitationMetadata)

	for i := range entries {
		entry := &entries[i]
		metadataItem := metadata[entry.RecitationID]
		item := quranAudioR2RecitationFromManifestEntry(entry, &metadataItem)
		byID[item.ID] = item
	}

	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}

	sort.Strings(ids)

	items := make([]quranAudioR2RecitationMetadata, 0, len(ids))
	for _, id := range ids {
		items = append(items, byID[id])
	}

	return items
}

func quranAudioR2RecitationFromManifestEntry(
	entry *quranAudioR2ManifestEntry,
	metadataItem *quranAudioR2RecitationMetadata,
) quranAudioR2RecitationMetadata {
	item := quranAudioR2RecitationMetadata{}
	if metadataItem != nil {
		item = *metadataItem
	}

	if strings.TrimSpace(item.ID) == "" {
		item.ID = entry.RecitationID
	}

	if strings.TrimSpace(item.Mode) == "" {
		item.Mode = entry.TrackType
	}

	if strings.TrimSpace(item.Name) == "" {
		item.Name = "QUL " + humanizeRecitationID(entry.RecitationID)
	}

	if strings.TrimSpace(item.DisplayName) == "" {
		item.DisplayName = humanizeRecitationID(entry.RecitationID)
	}

	if strings.TrimSpace(item.Format) == "" {
		item.Format = quranAudioR2DefaultFormat
	}

	if strings.TrimSpace(item.LicenseStatus) == "" {
		item.LicenseStatus = quranAudioR2DefaultLicenseStatus
	}

	if item.IsVisible == nil {
		visible := true
		item.IsVisible = &visible
	}

	if len(item.Metadata) == 0 {
		item.Metadata = json.RawMessage(`{}`)
	}

	return item
}

func applyQuranAudioR2PublicURLPolicy(
	entries []quranAudioR2ManifestEntry,
	metadata map[string]quranAudioR2RecitationMetadata,
) int {
	publicURLs := 0

	for i := range entries {
		item := metadata[entries[i].RecitationID]
		if item.UsePublicURL != nil && !*item.UsePublicURL {
			entries[i].PublicURL = ""
			entries[i].ClearPublicURL = true

			continue
		}

		if entries[i].PublicURL != "" {
			publicURLs++
		}
	}

	return publicURLs
}

func upsertQuranAudioR2Recitations(
	ctx context.Context,
	tx pgx.Tx,
	recitations []quranAudioR2RecitationMetadata,
) error {
	batch := &pgx.Batch{}

	for i := range recitations {
		recitation := &recitations[i]
		batch.Queue(
			`
INSERT INTO quran_recitations (
    id, name, display_name, reciter_name, style, mode, source_url, qul_resource_id,
    format, license_status, metadata, sort_order, default_priority, is_visible, imported_at, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, COALESCE(nullif($11, '')::jsonb, '{}'::jsonb), $12, $13, $14, now(), now())
ON CONFLICT (id) DO UPDATE SET
    name = EXCLUDED.name,
    display_name = EXCLUDED.display_name,
    reciter_name = EXCLUDED.reciter_name,
    style = EXCLUDED.style,
    mode = EXCLUDED.mode,
    source_url = EXCLUDED.source_url,
    qul_resource_id = EXCLUDED.qul_resource_id,
    format = EXCLUDED.format,
    license_status = EXCLUDED.license_status,
    metadata = EXCLUDED.metadata,
    sort_order = EXCLUDED.sort_order,
    default_priority = EXCLUDED.default_priority,
    is_visible = EXCLUDED.is_visible,
    updated_at = now()`,
			recitation.ID,
			recitation.Name,
			recitation.DisplayName,
			emptyStringToNil(recitation.ReciterName),
			emptyStringToNil(recitation.Style),
			recitation.Mode,
			emptyStringToNil(recitation.SourceURL),
			emptyStringToNil(recitation.QULResourceID),
			recitation.Format,
			recitation.LicenseStatus,
			string(recitation.Metadata),
			recitation.SortOrder,
			recitation.DefaultPriority,
			*recitation.IsVisible,
		)
	}

	if err := execTxBatch(ctx, tx, batch); err != nil {
		return fmt.Errorf("upsert quran r2 recitations: %w", err)
	}

	return nil
}

func upsertQuranAudioR2Tracks(
	ctx context.Context,
	tx pgx.Tx,
	entries []quranAudioR2ManifestEntry,
) error {
	batch := &pgx.Batch{}

	for i := range entries {
		entry := &entries[i]

		var ayahNumber *int
		if entry.TrackType == quranAudioR2TrackTypeAyah {
			ayahNumber = &entry.AyahNumber
		}

		batch.Queue(
			`
INSERT INTO quran_audio_tracks (
    recitation_id, track_type, track_key, surah_id, ayah_number, audio_url,
    r2_key, public_url, duration_ms, duration_seconds, mime_type, metadata, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, COALESCE(nullif($12, '')::jsonb, '{}'::jsonb), now())
ON CONFLICT (recitation_id, track_type, track_key) DO UPDATE SET
    audio_url = COALESCE(NULLIF(EXCLUDED.audio_url, ''), quran_audio_tracks.audio_url),
    r2_key = EXCLUDED.r2_key,
    public_url = CASE
        WHEN $13 THEN NULL
        ELSE COALESCE(NULLIF(EXCLUDED.public_url, ''), quran_audio_tracks.public_url)
    END,
    duration_ms = COALESCE(EXCLUDED.duration_ms, quran_audio_tracks.duration_ms),
    duration_seconds = COALESCE(EXCLUDED.duration_seconds, quran_audio_tracks.duration_seconds),
    mime_type = COALESCE(NULLIF(EXCLUDED.mime_type, ''), quran_audio_tracks.mime_type),
    metadata = EXCLUDED.metadata,
    updated_at = now()`,
			entry.RecitationID,
			entry.TrackType,
			entry.TrackKey,
			entry.SurahID,
			ayahNumber,
			entry.AudioURL,
			entry.R2Key,
			emptyStringToNil(entry.PublicURL),
			entry.DurationMS,
			entry.DurationSeconds,
			entry.MIMEType,
			string(entry.Metadata),
			entry.ClearPublicURL,
		)
	}

	if err := execTxBatch(ctx, tx, batch); err != nil {
		return fmt.Errorf("upsert quran r2 tracks: %w", err)
	}

	return nil
}

func quranAudioPublicURL(publicBaseURL, r2Key string) string {
	publicBaseURL = strings.TrimSpace(publicBaseURL)
	if publicBaseURL == "" {
		return ""
	}
	return strings.TrimRight(publicBaseURL, "/") + "/" + strings.TrimLeft(r2Key, "/")
}

func emptyStringToNil(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}

	return value
}

func humanizeRecitationID(recitationID string) string {
	name := recitationID
	for _, prefix := range []string{"qul-ayah-recitation-", "qul-surah-recitation-", "qul-recitation-"} {
		name = strings.TrimPrefix(name, prefix)
	}

	name = regexp.MustCompile(`-\d+$`).ReplaceAllString(name, "")
	name = strings.ReplaceAll(name, "-", " ")

	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		return "Quran Recitation"
	}

	words := strings.Fields(name)
	for i, word := range words {
		if word == "al" {
			words[i] = "Al"

			continue
		}

		words[i] = strings.ToUpper(word[:1]) + word[1:]
	}

	return strings.Join(words, " ")
}
