package importer

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/evrone/go-clean-template/internal/contentlang"
	"github.com/evrone/go-clean-template/internal/quranutil"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const defaultQuranTranslationSourceID = "qul-kfgqpc-id-simple"

// QuranAssetOptions configure local QUL export import.
type QuranAssetOptions struct {
	PostgresURL                 string
	SurahNamesPath              string
	SurahInfoPaths              []string
	ScriptQPCHafsPath           string
	ScriptImlaeiSimplePath      string
	TranslationSimplePath       string
	TranslationFootnoteTagsPath string
	RecitationPath              string
	RecitationPaths             []string
	TranslationLang             string
	SurahInfoLang               string
	DryRun                      bool
	ResolveReferences           bool
	TranslationSourceID         string
	TranslationSourceName       string
	LicenseStatus               string
}

// QuranAssetStats describes parsed/imported Quran assets.
type QuranAssetStats struct {
	Surahs            int
	SurahInfos        int
	Ayahs             int
	Translations      int
	Recitations       int
	AudioTracks       int
	AudioSegments     int
	BookReferences    int
	SkippedReferences int
	DryRun            bool
}

type quranAssetSet struct {
	surahs        map[int]*quranSurahImport
	surahInfos    map[string]*quranSurahInfoImport
	ayahs         map[string]*quranAyahImport
	translations  map[string]*quranTranslationImport
	recitations   map[string]*quranRecitationImport
	audioTracks   map[string]*quranAudioTrackImport
	audioSegments []quranAudioSegmentImport
	checksums     map[string]string
}

type quranSurahImport struct {
	SurahID         int
	NameArabic      *string
	NameLatin       *string
	NameTranslation *string
	RevelationType  *string
	AyahCount       int
	Metadata        json.RawMessage
}

type quranSurahInfoImport struct {
	SurahID       int
	Lang          string
	SurahName     *string
	TextHTML      string
	ShortText     *string
	SourceName    string
	SourceURL     string
	QULResourceID string
	Format        string
	Checksum      string
	Metadata      json.RawMessage
}

type quranAyahImport struct {
	SurahID          int
	AyahNumber       int
	AyahKey          string
	TextQPCHafs      *string
	TextImlaeiSimple *string
	SearchText       *string
	ScriptType       *string
	FontFamily       *string
	PageNumber       *int
	JuzNumber        *int
	HizbNumber       *int
	Metadata         json.RawMessage
}

type quranTranslationImport struct {
	SurahID    int
	AyahNumber int
	AyahKey    string
	Text       string
	Footnotes  json.RawMessage
	Chunks     json.RawMessage
	Metadata   json.RawMessage
}

type quranRecitationImport struct {
	ID          string
	Name        string
	ReciterName *string
	Style       *string
	Mode        string
	SourceURL   string
	ResourceID  string
	Format      string
	Checksum    string
	Metadata    json.RawMessage
}

type quranAudioTrackImport struct {
	RecitationID    string
	TrackType       string
	TrackKey        string
	SurahID         int
	AyahNumber      *int
	AudioURL        *string
	DurationMS      *int
	DurationSeconds *int
	MIMEType        *string
	Metadata        json.RawMessage
}

type quranAudioSegmentImport struct {
	RecitationID    string
	TrackType       string
	TrackKey        string
	SurahID         int
	AyahNumber      int
	SegmentIndex    int
	TimestampFromMS int
	TimestampToMS   int
	DurationMS      *int
	Metadata        json.RawMessage
}

// RunQuranAssetImport imports local QUL exports into PostgreSQL.
func RunQuranAssetImport(ctx context.Context, opts QuranAssetOptions) (QuranAssetStats, error) {
	if err := opts.validate(); err != nil {
		return QuranAssetStats{}, err
	}

	assets, err := parseQuranAssets(opts)
	if err != nil {
		return QuranAssetStats{}, err
	}

	stats := QuranAssetStats{
		Surahs:        len(assets.surahs),
		SurahInfos:    len(assets.surahInfos),
		Ayahs:         len(assets.ayahs),
		Translations:  len(assets.translations),
		Recitations:   len(assets.recitations),
		AudioTracks:   len(assets.audioTracks),
		AudioSegments: len(assets.audioSegments),
		DryRun:        opts.DryRun,
	}
	if opts.DryRun {
		return stats, nil
	}

	pool, err := pgxpool.New(ctx, opts.PostgresURL)
	if err != nil {
		return QuranAssetStats{}, fmt.Errorf("connecting postgres: %w", err)
	}
	defer pool.Close()

	if err := importQuranAssets(ctx, pool, opts.withDefaults(), assets); err != nil {
		return QuranAssetStats{}, err
	}

	if opts.ResolveReferences {
		resolved, skipped, err := ResolveQuranReferences(ctx, pool)
		if err != nil {
			return QuranAssetStats{}, err
		}
		stats.BookReferences = resolved
		stats.SkippedReferences = skipped
	}

	return stats, nil
}

func (opts QuranAssetOptions) validate() error {
	if !opts.DryRun && strings.TrimSpace(opts.PostgresURL) == "" {
		return errors.New("postgres URL is required")
	}
	if strings.TrimSpace(opts.SurahNamesPath) == "" {
		return errors.New("surah names JSON path is required")
	}
	if strings.TrimSpace(opts.ScriptQPCHafsPath) == "" {
		return errors.New("QPC Hafs script JSON path is required")
	}
	if strings.TrimSpace(opts.ScriptImlaeiSimplePath) == "" {
		return errors.New("Imlaei simple script JSON path is required")
	}
	if strings.TrimSpace(opts.TranslationSimplePath) == "" {
		return errors.New("translation simple JSON path is required")
	}
	if _, err := contentlang.Normalize(opts.TranslationLang); err != nil {
		return fmt.Errorf("translation lang: %w", err)
	}
	if strings.TrimSpace(opts.SurahInfoLang) != "" {
		if _, err := contentlang.Normalize(opts.SurahInfoLang); err != nil {
			return fmt.Errorf("surah info lang: %w", err)
		}
	}

	return nil
}

func (opts QuranAssetOptions) withDefaults() QuranAssetOptions {
	if opts.TranslationSourceID == "" {
		opts.TranslationSourceID = defaultQuranTranslationSourceID
	}
	if opts.TranslationSourceName == "" {
		opts.TranslationSourceName = "King Fahad Quran Complex"
	}
	opts.TranslationLang = contentlang.MustNormalize(opts.TranslationLang)
	if strings.TrimSpace(opts.SurahInfoLang) != "" {
		opts.SurahInfoLang = contentlang.MustNormalize(opts.SurahInfoLang)
	}
	if opts.LicenseStatus == "" {
		opts.LicenseStatus = "needs_review"
	}

	return opts
}

func parseQuranAssets(opts QuranAssetOptions) (quranAssetSet, error) {
	opts = opts.withDefaults()
	assets := quranAssetSet{
		surahs:       make(map[int]*quranSurahImport),
		surahInfos:   make(map[string]*quranSurahInfoImport),
		ayahs:        make(map[string]*quranAyahImport),
		translations: make(map[string]*quranTranslationImport),
		recitations:  make(map[string]*quranRecitationImport),
		audioTracks:  make(map[string]*quranAudioTrackImport),
		checksums:    make(map[string]string),
	}

	if err := parseSurahNames(opts.SurahNamesPath, &assets); err != nil {
		return quranAssetSet{}, err
	}
	for _, path := range opts.SurahInfoPaths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		if err := parseSurahInfo(path, opts.SurahInfoLang, &assets); err != nil {
			return quranAssetSet{}, err
		}
	}
	if err := parseScriptResource(opts.ScriptQPCHafsPath, "qpc_hafs", &assets); err != nil {
		return quranAssetSet{}, err
	}
	if err := parseScriptResource(opts.ScriptImlaeiSimplePath, "imlaei_simple", &assets); err != nil {
		return quranAssetSet{}, err
	}
	if err := parseTranslationSimple(opts.TranslationSimplePath, &assets); err != nil {
		return quranAssetSet{}, err
	}
	if opts.TranslationFootnoteTagsPath != "" {
		if err := parseTranslationFootnoteTags(opts.TranslationFootnoteTagsPath, &assets); err != nil {
			return quranAssetSet{}, err
		}
	}
	recitationPaths := append([]string{}, opts.RecitationPaths...)
	if opts.RecitationPath != "" {
		recitationPaths = append(recitationPaths, opts.RecitationPath)
	}
	for _, path := range recitationPaths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		if err := parseRecitation(path, &assets); err != nil {
			return quranAssetSet{}, err
		}
	}

	fillSurahCounts(&assets)

	return assets, nil
}

func parseSurahNames(path string, assets *quranAssetSet) error {
	raw, checksum, err := readAssetFile(path)
	if err != nil {
		return fmt.Errorf("read surah names: %w", err)
	}
	assets.checksums["surah_names"] = checksum

	rows, err := jsonRows(raw)
	if err != nil {
		return fmt.Errorf("parse surah names: %w", err)
	}
	for key, row := range rows {
		surahID := firstInt(row, "surah_id", "id", "number", "surah")
		if surahID == 0 {
			surahID, _ = strconv.Atoi(key)
		}
		if surahID <= 0 {
			continue
		}

		assets.surahs[surahID] = &quranSurahImport{
			SurahID:         surahID,
			NameArabic:      firstString(row, "name_arabic", "arabic_name", "name", "name_complex"),
			NameLatin:       firstString(row, "name_latin", "latin_name", "name_simple", "transliterated_name"),
			NameTranslation: firstString(row, "name_translation", "translated_name", "translation", "name_english"),
			RevelationType:  firstString(row, "revelation_type", "revelation_place", "type"),
			AyahCount:       firstInt(row, "ayah_count", "ayahs_count", "verses_count", "verses"),
			Metadata:        cloneRaw(row),
		}
	}

	return nil
}

func parseSurahInfo(path string, langOverride string, assets *quranAssetSet) error {
	raw, checksum, err := readAssetFile(path)
	if err != nil {
		return fmt.Errorf("read surah info: %w", err)
	}

	lang := strings.TrimSpace(langOverride)
	if lang == "" {
		lang = inferSurahInfoLang(path)
	}
	lang = contentlang.MustNormalize(lang)
	assets.checksums["surah_info:"+lang] = checksum

	rows, err := jsonRows(raw)
	if err != nil {
		return fmt.Errorf("parse surah info: %w", err)
	}
	for key, row := range rows {
		surahID := firstInt(row, "surah_number", "surah_id", "id", "number", "surah")
		if surahID == 0 {
			surahID, _ = strconv.Atoi(key)
		}
		textHTML := firstStringValue(row, "text", "text_html", "html")
		if surahID <= 0 || strings.TrimSpace(textHTML) == "" {
			continue
		}

		if assets.surahs[surahID] == nil {
			assets.surahs[surahID] = &quranSurahImport{
				SurahID:  surahID,
				Metadata: json.RawMessage(`{}`),
			}
		}
		assets.surahInfos[fmt.Sprintf("%d:%s", surahID, lang)] = &quranSurahInfoImport{
			SurahID:       surahID,
			Lang:          lang,
			SurahName:     firstString(row, "surah_name", "name"),
			TextHTML:      textHTML,
			ShortText:     firstString(row, "short_text", "summary"),
			SourceName:    "QUL Surah information",
			SourceURL:     "https://qul.tarteel.ai/resources/surah-info",
			QULResourceID: "",
			Format:        "json",
			Checksum:      checksum,
			Metadata:      cloneRaw(row),
		}
	}

	return nil
}

func parseScriptResource(path string, scriptKind string, assets *quranAssetSet) error {
	raw, checksum, err := readAssetFile(path)
	if err != nil {
		return fmt.Errorf("read %s script: %w", scriptKind, err)
	}
	assets.checksums[scriptKind] = checksum

	rows, err := jsonRows(raw)
	if err != nil {
		return fmt.Errorf("parse %s script: %w", scriptKind, err)
	}
	for key, row := range rows {
		ayahKey := firstStringValue(row, "verse_key", "ayah_key", "key")
		if ayahKey == "" && strings.Contains(key, ":") {
			ayahKey = key
		}

		surahID := firstInt(row, "surah_id", "surah")
		ayahNumber := firstInt(row, "ayah_number", "ayah", "verse_number", "verse")
		if ayahKey != "" {
			parsedSurahID, parsedAyahNumber, err := quranutil.ParseAyahKey(ayahKey)
			if err == nil {
				surahID = parsedSurahID
				ayahNumber = parsedAyahNumber
			}
		}
		if surahID <= 0 || ayahNumber <= 0 {
			continue
		}

		ayahKey = quranutil.AyahKey(surahID, ayahNumber)
		ayah := ensureAyah(assets, surahID, ayahNumber)
		if text := firstString(row, "text", "verse_text", "uthmani"); text != nil {
			if scriptKind == "qpc_hafs" {
				ayah.TextQPCHafs = text
			} else {
				ayah.TextImlaeiSimple = text
				ayah.SearchText = text
			}
		}
		if scriptKind == "qpc_hafs" {
			ayah.ScriptType = firstString(row, "script_type")
			ayah.FontFamily = firstString(row, "font_family")
			ayah.PageNumber = firstIntPtr(row, "page_number", "page")
			ayah.JuzNumber = firstIntPtr(row, "juz_number", "juz")
			ayah.HizbNumber = firstIntPtr(row, "hizb_number", "hizb")
			ayah.Metadata = cloneRaw(row)
		}
		ayah.AyahKey = ayahKey
	}

	return nil
}

func parseTranslationSimple(path string, assets *quranAssetSet) error {
	raw, checksum, err := readAssetFile(path)
	if err != nil {
		return fmt.Errorf("read simple translation: %w", err)
	}
	assets.checksums["translation_simple"] = checksum

	if rows, err := stringByAyahKey(raw); err == nil {
		for ayahKey, text := range rows {
			addTranslation(assets, ayahKey, text, nil, nil, nil)
		}
		return nil
	}

	var nested [][]string
	if err := json.Unmarshal(raw, &nested); err != nil {
		return fmt.Errorf("parse simple translation: %w", err)
	}
	for surahIndex, ayahs := range nested {
		for ayahIndex, text := range ayahs {
			addTranslation(assets, quranutil.AyahKey(surahIndex+1, ayahIndex+1), text, nil, nil, nil)
		}
	}

	return nil
}

func parseTranslationFootnoteTags(path string, assets *quranAssetSet) error {
	raw, checksum, err := readAssetFile(path)
	if err != nil {
		return fmt.Errorf("read footnote translation: %w", err)
	}
	assets.checksums["translation_footnote_tags"] = checksum

	rows, err := jsonRows(raw)
	if err != nil {
		return fmt.Errorf("parse footnote translation: %w", err)
	}
	for key, row := range rows {
		ayahKey := firstStringValue(row, "verse_key", "ayah_key", "key")
		if ayahKey == "" && strings.Contains(key, ":") {
			ayahKey = key
		}
		text := firstStringValue(row, "t", "text", "translation")
		if ayahKey == "" || text == "" {
			continue
		}
		footnotes := rawField(row, "f", "footnotes")
		chunks := rawField(row, "chunks")
		addTranslation(assets, ayahKey, text, footnotes, chunks, cloneRaw(row))
	}

	return nil
}

func parseRecitation(path string, assets *quranAssetSet) error {
	raw, checksum, err := readAssetFilePreferred(path, "segments.json")
	if err != nil && strings.Contains(err.Error(), "preferred JSON file") {
		raw, checksum, err = readAssetFile(path)
	}
	if err != nil {
		return fmt.Errorf("read recitation: %w", err)
	}
	assets.checksums["recitation"] = checksum

	recitationID := inferRecitationID(path)
	recitation := &quranRecitationImport{
		ID:         recitationID,
		Name:       inferRecitationName(path),
		Mode:       "unknown",
		ResourceID: "",
		Format:     "json",
		Checksum:   checksum,
		Metadata:   json.RawMessage(`{}`),
	}
	assets.recitations[recitationID] = recitation

	if strings.EqualFold(filepath.Ext(path), ".zip") {
		if surahRaw, _, err := readAssetFilePreferred(path, "surah.json"); err == nil {
			if err := parseRecitationSurahTracks(surahRaw, recitationID, assets); err != nil {
				return err
			}
			recitation.Mode = "surah"
		}
	}

	rows, err := jsonRows(raw)
	if err != nil {
		return fmt.Errorf("parse recitation: %w", err)
	}
	for key, row := range rows {
		ayahKey := firstStringValue(row, "verse_key", "ayah_key", "key")
		if ayahKey == "" && strings.Contains(key, ":") {
			ayahKey = key
		}
		surahID := firstInt(row, "surah_id", "surah")
		ayahNumber := firstInt(row, "ayah_number", "ayah", "verse_number", "verse")
		if ayahKey != "" {
			parsedSurahID, parsedAyahNumber, err := quranutil.ParseAyahKey(ayahKey)
			if err == nil {
				surahID = parsedSurahID
				ayahNumber = parsedAyahNumber
			}
		}
		if surahID <= 0 {
			continue
		}

		trackType := "surah"
		trackKey := strconv.Itoa(surahID)
		var trackAyah *int
		if ayahNumber > 0 {
			trackType = "ayah"
			trackKey = quranutil.AyahKey(surahID, ayahNumber)
			trackAyah = &ayahNumber
			ensureAyah(assets, surahID, ayahNumber)
		}
		if assets.audioTracks[recitationID+":surah:"+strconv.Itoa(surahID)] != nil {
			trackType = "surah"
			trackKey = strconv.Itoa(surahID)
			trackAyah = nil
			recitation.Mode = "surah"
		} else if ayahNumber > 0 {
			recitation.Mode = "ayah"
		} else if recitation.Mode == "unknown" {
			recitation.Mode = "surah"
		}

		trackID := recitationID + ":" + trackType + ":" + trackKey
		if assets.audioTracks[trackID] == nil {
			track := &quranAudioTrackImport{
				RecitationID:    recitationID,
				TrackType:       trackType,
				TrackKey:        trackKey,
				SurahID:         surahID,
				AyahNumber:      trackAyah,
				AudioURL:        firstString(row, "audio_url", "url"),
				DurationMS:      firstIntPtr(row, "duration_ms"),
				DurationSeconds: firstIntPtr(row, "duration_sec", "duration_seconds", "duration"),
				MIMEType:        firstString(row, "mime_type"),
				Metadata:        cloneRaw(row),
			}
			assets.audioTracks[trackID] = track
		}
		addSegments(assets, recitationID, trackType, trackKey, surahID, ayahNumber, row)
	}

	return nil
}

func parseRecitationSurahTracks(raw []byte, recitationID string, assets *quranAssetSet) error {
	rows, err := jsonRows(raw)
	if err != nil {
		return fmt.Errorf("parse recitation surah tracks: %w", err)
	}
	for key, row := range rows {
		surahID := firstInt(row, "surah_number", "surah_id", "surah", "id", "number")
		if surahID == 0 {
			surahID, _ = strconv.Atoi(key)
		}
		if surahID <= 0 {
			continue
		}
		if assets.surahs[surahID] == nil {
			assets.surahs[surahID] = &quranSurahImport{SurahID: surahID, Metadata: json.RawMessage(`{}`)}
		}
		trackKey := strconv.Itoa(surahID)
		trackID := recitationID + ":surah:" + trackKey
		assets.audioTracks[trackID] = &quranAudioTrackImport{
			RecitationID:    recitationID,
			TrackType:       "surah",
			TrackKey:        trackKey,
			SurahID:         surahID,
			AudioURL:        firstString(row, "audio_url", "url"),
			DurationMS:      firstIntPtr(row, "duration_ms"),
			DurationSeconds: firstIntPtr(row, "duration_sec", "duration_seconds", "duration"),
			MIMEType:        firstString(row, "mime_type"),
			Metadata:        cloneRaw(row),
		}
	}

	return nil
}

func addSegments(
	assets *quranAssetSet,
	recitationID string,
	trackType string,
	trackKey string,
	surahID int,
	ayahNumber int,
	row map[string]json.RawMessage,
) {
	segmentsRaw := rawField(row, "segments")
	if len(segmentsRaw) == 0 {
		return
	}

	var segments [][]int
	if err := json.Unmarshal(segmentsRaw, &segments); err != nil {
		return
	}
	if ayahNumber <= 0 {
		return
	}
	for _, segment := range segments {
		if len(segment) < 3 {
			continue
		}
		duration := segment[2] - segment[1]
		assets.audioSegments = append(assets.audioSegments, quranAudioSegmentImport{
			RecitationID:    recitationID,
			TrackType:       trackType,
			TrackKey:        trackKey,
			SurahID:         surahID,
			AyahNumber:      ayahNumber,
			SegmentIndex:    segment[0],
			TimestampFromMS: segment[1],
			TimestampToMS:   segment[2],
			DurationMS:      &duration,
			Metadata:        json.RawMessage(`{}`),
		})
	}
}

func importQuranAssets(ctx context.Context, pool *pgxpool.Pool, opts QuranAssetOptions, assets quranAssetSet) error {
	if err := insertQuranImportRuns(ctx, pool, opts, assets); err != nil {
		return err
	}

	batch := &pgx.Batch{}
	for _, surah := range assets.surahs {
		batch.Queue(`
INSERT INTO quran_surahs (
    surah_id, name_arabic, name_latin, name_translation, revelation_type, ayah_count, metadata, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, nullif($7, '')::jsonb, now())
ON CONFLICT (surah_id) DO UPDATE SET
    name_arabic = COALESCE(EXCLUDED.name_arabic, quran_surahs.name_arabic),
    name_latin = COALESCE(EXCLUDED.name_latin, quran_surahs.name_latin),
    name_translation = COALESCE(EXCLUDED.name_translation, quran_surahs.name_translation),
    revelation_type = COALESCE(EXCLUDED.revelation_type, quran_surahs.revelation_type),
    ayah_count = GREATEST(EXCLUDED.ayah_count, quran_surahs.ayah_count),
    metadata = COALESCE(EXCLUDED.metadata, quran_surahs.metadata),
    updated_at = now()`,
			surah.SurahID,
			surah.NameArabic,
			surah.NameLatin,
			surah.NameTranslation,
			surah.RevelationType,
			surah.AyahCount,
			stringOrEmpty(surah.Metadata),
		)
	}
	if err := execBatch(ctx, pool, batch); err != nil {
		return fmt.Errorf("upsert quran surahs: %w", err)
	}

	if err := upsertQuranSurahInfos(ctx, pool, opts, assets); err != nil {
		return err
	}

	batch = &pgx.Batch{}
	for _, ayah := range assets.ayahs {
		batch.Queue(`
INSERT INTO quran_ayahs (
    surah_id, ayah_number, ayah_key, text_qpc_hafs, text_imlaei_simple, search_text,
    script_type, font_family, page_number, juz_number, hizb_number, metadata, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, nullif($12, '')::jsonb, now())
ON CONFLICT (surah_id, ayah_number) DO UPDATE SET
    text_qpc_hafs = COALESCE(EXCLUDED.text_qpc_hafs, quran_ayahs.text_qpc_hafs),
    text_imlaei_simple = COALESCE(EXCLUDED.text_imlaei_simple, quran_ayahs.text_imlaei_simple),
    search_text = COALESCE(EXCLUDED.search_text, quran_ayahs.search_text),
    script_type = COALESCE(EXCLUDED.script_type, quran_ayahs.script_type),
    font_family = COALESCE(EXCLUDED.font_family, quran_ayahs.font_family),
    page_number = COALESCE(EXCLUDED.page_number, quran_ayahs.page_number),
    juz_number = COALESCE(EXCLUDED.juz_number, quran_ayahs.juz_number),
    hizb_number = COALESCE(EXCLUDED.hizb_number, quran_ayahs.hizb_number),
    metadata = COALESCE(EXCLUDED.metadata, quran_ayahs.metadata),
    updated_at = now()`,
			ayah.SurahID,
			ayah.AyahNumber,
			ayah.AyahKey,
			ayah.TextQPCHafs,
			ayah.TextImlaeiSimple,
			ayah.SearchText,
			ayah.ScriptType,
			ayah.FontFamily,
			ayah.PageNumber,
			ayah.JuzNumber,
			ayah.HizbNumber,
			stringOrEmpty(ayah.Metadata),
		)
	}
	if err := execBatch(ctx, pool, batch); err != nil {
		return fmt.Errorf("upsert quran ayahs: %w", err)
	}

	if err := upsertQuranTranslationSource(ctx, pool, opts, assets); err != nil {
		return err
	}
	if err := upsertQuranTranslations(ctx, pool, opts, assets); err != nil {
		return err
	}
	if err := upsertQuranAudio(ctx, pool, opts, assets); err != nil {
		return err
	}

	return nil
}

func upsertQuranSurahInfos(
	ctx context.Context,
	pool *pgxpool.Pool,
	opts QuranAssetOptions,
	assets quranAssetSet,
) error {
	if len(assets.surahInfos) == 0 {
		return nil
	}

	batch := &pgx.Batch{}
	for _, info := range assets.surahInfos {
		batch.Queue(`
INSERT INTO quran_surah_infos (
    surah_id, lang, surah_name, text_html, short_text, source_name, source_url,
    qul_resource_id, format, license_status, checksum, metadata, imported_at, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, nullif($12, '')::jsonb, now(), now())
ON CONFLICT (surah_id, lang) DO UPDATE SET
    surah_name = EXCLUDED.surah_name,
    text_html = EXCLUDED.text_html,
    short_text = EXCLUDED.short_text,
    source_name = EXCLUDED.source_name,
    source_url = EXCLUDED.source_url,
    qul_resource_id = EXCLUDED.qul_resource_id,
    format = EXCLUDED.format,
    license_status = EXCLUDED.license_status,
    checksum = EXCLUDED.checksum,
    metadata = COALESCE(EXCLUDED.metadata, quran_surah_infos.metadata),
    imported_at = EXCLUDED.imported_at,
    updated_at = now()`,
			info.SurahID,
			info.Lang,
			info.SurahName,
			info.TextHTML,
			info.ShortText,
			info.SourceName,
			info.SourceURL,
			info.QULResourceID,
			info.Format,
			opts.LicenseStatus,
			info.Checksum,
			stringOrEmpty(info.Metadata),
		)
	}

	if err := execBatch(ctx, pool, batch); err != nil {
		return fmt.Errorf("upsert quran surah infos: %w", err)
	}

	return nil
}

func insertQuranImportRuns(ctx context.Context, pool *pgxpool.Pool, opts QuranAssetOptions, assets quranAssetSet) error {
	resources := []struct {
		key          string
		name         string
		sourceURL    string
		resourceID   string
		resourceType string
		format       string
	}{
		{"surah_names", "QUL Surah names", "https://qul.tarteel.ai/resources/quran-metadata", "", "surah_metadata", "json"},
		{"qpc_hafs", "QPC Hafs script - Ayah by Ayah", "https://qul.tarteel.ai/resources/quran-script/86", "86", "script", "json"},
		{"imlaei_simple", "Imlaei simple script", "https://qul.tarteel.ai/resources/quran-script", "", "script", "json"},
		{"translation_simple", opts.TranslationSourceName, "https://qul.tarteel.ai/resources/translation/173", "173", "translation", "simple.json"},
		{"translation_footnote_tags", opts.TranslationSourceName, "https://qul.tarteel.ai/resources/translation/173", "173", "translation", "translation-with-footnote-tags.json"},
		{"recitation", "QUL Recitation", "https://qul.tarteel.ai/resources/recitation", "", "recitation", "json"},
	}
	for key := range assets.checksums {
		if !strings.HasPrefix(key, "surah_info:") {
			continue
		}
		lang := strings.TrimPrefix(key, "surah_info:")
		resources = append(resources, struct {
			key          string
			name         string
			sourceURL    string
			resourceID   string
			resourceType string
			format       string
		}{
			key:          key,
			name:         "QUL Surah information (" + lang + ")",
			sourceURL:    "https://qul.tarteel.ai/resources/surah-info",
			resourceID:   "",
			resourceType: "surah_info",
			format:       "json",
		})
	}

	batch := &pgx.Batch{}
	for _, resource := range resources {
		checksum := assets.checksums[resource.key]
		if checksum == "" {
			continue
		}
		batch.Queue(`
INSERT INTO quran_import_runs (
    id, source_name, source_url, qul_resource_id, resource_type, format,
    checksum, license_status, metadata, dry_run, imported_at, created_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, '{}'::jsonb, false, now(), now())`,
			uuid.New().String(),
			resource.name,
			resource.sourceURL,
			resource.resourceID,
			resource.resourceType,
			resource.format,
			checksum,
			opts.LicenseStatus,
		)
	}

	if err := execBatch(ctx, pool, batch); err != nil {
		return fmt.Errorf("insert quran import runs: %w", err)
	}

	return nil
}

func upsertQuranTranslationSource(
	ctx context.Context,
	pool *pgxpool.Pool,
	opts QuranAssetOptions,
	assets quranAssetSet,
) error {
	footnoteChecksum := assets.checksums["translation_footnote_tags"]
	checksum := assets.checksums["translation_simple"]
	metadata := map[string]string{}
	if footnoteChecksum != "" {
		metadata["footnote_checksum"] = footnoteChecksum
	}
	metadataJSON, _ := json.Marshal(metadata)

	_, err := pool.Exec(ctx, `
INSERT INTO quran_translation_sources (
    id, lang, name, source_url, qul_resource_id, format, license_status,
    checksum, metadata, imported_at, updated_at
)
VALUES ($1, $2, $3, $4, '173', 'simple.json', $5, $6, $7::jsonb, now(), now())
ON CONFLICT (id) DO UPDATE SET
    lang = EXCLUDED.lang,
    name = EXCLUDED.name,
    source_url = EXCLUDED.source_url,
    qul_resource_id = EXCLUDED.qul_resource_id,
    format = EXCLUDED.format,
    license_status = EXCLUDED.license_status,
    checksum = EXCLUDED.checksum,
    metadata = EXCLUDED.metadata,
    imported_at = EXCLUDED.imported_at,
    updated_at = now()`,
		opts.TranslationSourceID,
		opts.TranslationLang,
		opts.TranslationSourceName,
		"https://qul.tarteel.ai/resources/translation/173",
		opts.LicenseStatus,
		checksum,
		string(metadataJSON),
	)
	if err != nil {
		return fmt.Errorf("upsert quran translation source: %w", err)
	}

	return nil
}

func upsertQuranTranslations(
	ctx context.Context,
	pool *pgxpool.Pool,
	opts QuranAssetOptions,
	assets quranAssetSet,
) error {
	batch := &pgx.Batch{}
	for _, translation := range assets.translations {
		batch.Queue(`
INSERT INTO quran_ayah_translations (
    source_id, surah_id, ayah_number, ayah_key, lang, text, footnotes, chunks, metadata, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, nullif($7, '')::jsonb, nullif($8, '')::jsonb, COALESCE(nullif($9, '')::jsonb, '{}'::jsonb), now())
ON CONFLICT (source_id, surah_id, ayah_number) DO UPDATE SET
    lang = EXCLUDED.lang,
    text = EXCLUDED.text,
    footnotes = COALESCE(EXCLUDED.footnotes, quran_ayah_translations.footnotes),
    chunks = COALESCE(EXCLUDED.chunks, quran_ayah_translations.chunks),
    metadata = COALESCE(EXCLUDED.metadata, quran_ayah_translations.metadata),
    updated_at = now()`,
			opts.TranslationSourceID,
			translation.SurahID,
			translation.AyahNumber,
			translation.AyahKey,
			opts.TranslationLang,
			translation.Text,
			stringOrEmpty(translation.Footnotes),
			stringOrEmpty(translation.Chunks),
			stringOrEmpty(translation.Metadata),
		)
	}

	if err := execBatch(ctx, pool, batch); err != nil {
		return fmt.Errorf("upsert quran translations: %w", err)
	}

	return nil
}

func upsertQuranAudio(
	ctx context.Context,
	pool *pgxpool.Pool,
	opts QuranAssetOptions,
	assets quranAssetSet,
) error {
	if len(assets.recitations) == 0 {
		return nil
	}

	batch := &pgx.Batch{}
	for _, recitation := range assets.recitations {
		batch.Queue(`
INSERT INTO quran_recitations (
    id, name, reciter_name, style, mode, source_url, qul_resource_id, format,
    license_status, checksum, metadata, imported_at, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, 'json', $8, $9, nullif($10, '')::jsonb, now(), now())
ON CONFLICT (id) DO UPDATE SET
    name = EXCLUDED.name,
    reciter_name = EXCLUDED.reciter_name,
    style = EXCLUDED.style,
    mode = EXCLUDED.mode,
    source_url = EXCLUDED.source_url,
    qul_resource_id = EXCLUDED.qul_resource_id,
    format = EXCLUDED.format,
    license_status = EXCLUDED.license_status,
    checksum = EXCLUDED.checksum,
    metadata = EXCLUDED.metadata,
    imported_at = EXCLUDED.imported_at,
    updated_at = now()`,
			recitation.ID,
			recitation.Name,
			recitation.ReciterName,
			recitation.Style,
			recitation.Mode,
			recitation.SourceURL,
			recitation.ResourceID,
			opts.LicenseStatus,
			recitation.Checksum,
			stringOrEmpty(recitation.Metadata),
		)
	}
	if err := execBatch(ctx, pool, batch); err != nil {
		return fmt.Errorf("upsert quran recitations: %w", err)
	}

	batch = &pgx.Batch{}
	for _, track := range assets.audioTracks {
		batch.Queue(`
INSERT INTO quran_audio_tracks (
    recitation_id, track_type, track_key, surah_id, ayah_number, audio_url,
    duration_ms, duration_seconds, mime_type, metadata, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, nullif($10, '')::jsonb, now())
ON CONFLICT (recitation_id, track_type, track_key) DO UPDATE SET
    audio_url = EXCLUDED.audio_url,
    duration_ms = EXCLUDED.duration_ms,
    duration_seconds = EXCLUDED.duration_seconds,
    mime_type = EXCLUDED.mime_type,
    metadata = EXCLUDED.metadata,
    updated_at = now()`,
			track.RecitationID,
			track.TrackType,
			track.TrackKey,
			track.SurahID,
			track.AyahNumber,
			track.AudioURL,
			track.DurationMS,
			track.DurationSeconds,
			track.MIMEType,
			stringOrEmpty(track.Metadata),
		)
	}
	if err := execBatch(ctx, pool, batch); err != nil {
		return fmt.Errorf("upsert quran audio tracks: %w", err)
	}

	batch = &pgx.Batch{}
	for _, segment := range assets.audioSegments {
		batch.Queue(`
INSERT INTO quran_audio_segments (
    recitation_id, track_type, track_key, surah_id, ayah_number, segment_index,
    timestamp_from_ms, timestamp_to_ms, duration_ms, metadata, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, nullif($10, '')::jsonb, now())
ON CONFLICT (recitation_id, track_type, track_key, ayah_number, segment_index) DO UPDATE SET
    timestamp_from_ms = EXCLUDED.timestamp_from_ms,
    timestamp_to_ms = EXCLUDED.timestamp_to_ms,
    duration_ms = EXCLUDED.duration_ms,
    metadata = EXCLUDED.metadata,
    updated_at = now()`,
			segment.RecitationID,
			segment.TrackType,
			segment.TrackKey,
			segment.SurahID,
			segment.AyahNumber,
			segment.SegmentIndex,
			segment.TimestampFromMS,
			segment.TimestampToMS,
			segment.DurationMS,
			stringOrEmpty(segment.Metadata),
		)
	}
	if err := execBatch(ctx, pool, batch); err != nil {
		return fmt.Errorf("upsert quran audio segments: %w", err)
	}

	return nil
}

func addTranslation(
	assets *quranAssetSet,
	ayahKey string,
	text string,
	footnotes json.RawMessage,
	chunks json.RawMessage,
	metadata json.RawMessage,
) {
	surahID, ayahNumber, err := quranutil.ParseAyahKey(ayahKey)
	if err != nil || strings.TrimSpace(text) == "" {
		return
	}
	ensureAyah(assets, surahID, ayahNumber)
	existing := assets.translations[ayahKey]
	if existing == nil {
		existing = &quranTranslationImport{
			SurahID:    surahID,
			AyahNumber: ayahNumber,
			AyahKey:    ayahKey,
			Text:       text,
		}
		assets.translations[ayahKey] = existing
	}
	if existing.Text == "" || (len(footnotes) == 0 && len(chunks) == 0) {
		existing.Text = text
	}
	if len(footnotes) > 0 {
		existing.Footnotes = footnotes
	}
	if len(chunks) > 0 {
		existing.Chunks = chunks
	}
	if len(metadata) > 0 {
		existing.Metadata = metadata
	}
}

func ensureAyah(assets *quranAssetSet, surahID, ayahNumber int) *quranAyahImport {
	ayahKey := quranutil.AyahKey(surahID, ayahNumber)
	ayah := assets.ayahs[ayahKey]
	if ayah == nil {
		ayah = &quranAyahImport{
			SurahID:    surahID,
			AyahNumber: ayahNumber,
			AyahKey:    ayahKey,
			Metadata:   json.RawMessage(`{}`),
		}
		assets.ayahs[ayahKey] = ayah
	}
	if assets.surahs[surahID] == nil {
		assets.surahs[surahID] = &quranSurahImport{
			SurahID:  surahID,
			Metadata: json.RawMessage(`{}`),
		}
	}

	return ayah
}

func fillSurahCounts(assets *quranAssetSet) {
	counts := make(map[int]int)
	for _, ayah := range assets.ayahs {
		if ayah.AyahNumber > counts[ayah.SurahID] {
			counts[ayah.SurahID] = ayah.AyahNumber
		}
	}
	for surahID, count := range counts {
		surah := assets.surahs[surahID]
		if surah == nil {
			surah = &quranSurahImport{SurahID: surahID, Metadata: json.RawMessage(`{}`)}
			assets.surahs[surahID] = surah
		}
		if surah.AyahCount == 0 {
			surah.AyahCount = count
		}
	}
}

var surahInfoLangRE = regexp.MustCompile(`(?i)surah-info-([a-z]{2,3})`)

func inferSurahInfoLang(path string) string {
	name := strings.ToLower(filepath.Base(path))
	if match := surahInfoLangRE.FindStringSubmatch(name); len(match) == 2 {
		return match[1]
	}
	for _, lang := range []string{"id", "en", "ar"} {
		if strings.Contains(name, "-"+lang+".") || strings.Contains(name, "_"+lang+".") {
			return lang
		}
	}

	return "id"
}

func inferRecitationID(path string) string {
	name := strings.ToLower(trimKnownExtensions(filepath.Base(path)))
	name = regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(name, "-")
	name = strings.Trim(name, "-")
	if name == "" {
		return "qul-recitation"
	}
	if !strings.HasPrefix(name, "qul-") {
		name = "qul-" + name
	}

	return name
}

func inferRecitationName(path string) string {
	name := trimKnownExtensions(filepath.Base(path))
	name = strings.ReplaceAll(name, "-", " ")
	name = strings.ReplaceAll(name, "_", " ")
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		return "QUL Recitation"
	}

	return "QUL " + name
}

func trimKnownExtensions(name string) string {
	for {
		ext := strings.ToLower(filepath.Ext(name))
		if ext != ".zip" && ext != ".json" && ext != ".sqlite" {
			return name
		}
		name = strings.TrimSuffix(name, filepath.Ext(name))
	}
}

func readAssetFile(path string) ([]byte, string, error) {
	return readAssetFilePreferred(path)
}

func readAssetFilePreferred(path string, preferredNames ...string) ([]byte, string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(raw)
	checksum := hex.EncodeToString(sum[:])

	if strings.EqualFold(filepath.Ext(path), ".zip") {
		unzipped, err := readJSONFromZip(raw, preferredNames...)
		if err != nil {
			return nil, "", err
		}

		return unzipped, checksum, nil
	}

	return raw, checksum, nil
}

func readJSONFromZip(raw []byte, preferredNames ...string) ([]byte, error) {
	reader, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, err
	}

	for _, preferred := range preferredNames {
		for _, file := range reader.File {
			if file.FileInfo().IsDir() {
				continue
			}
			if filepath.Base(file.Name) == preferred {
				return readZipFile(file)
			}
		}
	}
	if len(preferredNames) > 0 {
		return nil, fmt.Errorf("zip does not contain preferred JSON file %q", preferredNames[0])
	}

	for _, file := range reader.File {
		if file.FileInfo().IsDir() || !strings.EqualFold(filepath.Ext(file.Name), ".json") {
			continue
		}

		return readZipFile(file)
	}

	return nil, errors.New("zip does not contain a JSON file")
}

func readZipFile(file *zip.File) ([]byte, error) {
	reader, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()

	var buf bytes.Buffer
	if _, err = buf.ReadFrom(reader); err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func jsonRows(raw []byte) (map[string]map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err == nil {
		for _, key := range []string{"data", "surahs", "ayahs", "verses", "rows"} {
			if nested, ok := object[key]; ok {
				if rows, err := jsonRows(nested); err == nil && len(rows) > 0 {
					return rows, nil
				}
			}
		}

		rows := make(map[string]map[string]json.RawMessage, len(object))
		for key, value := range object {
			var row map[string]json.RawMessage
			if err := json.Unmarshal(value, &row); err == nil {
				rows[key] = row
				continue
			}
			var text string
			if err := json.Unmarshal(value, &text); err == nil {
				rows[key] = map[string]json.RawMessage{"text": mustRaw(text)}
			}
		}
		return rows, nil
	}

	var array []json.RawMessage
	if err := json.Unmarshal(raw, &array); err != nil {
		return nil, err
	}
	rows := make(map[string]map[string]json.RawMessage, len(array))
	for i, value := range array {
		var row map[string]json.RawMessage
		if err := json.Unmarshal(value, &row); err != nil {
			continue
		}
		rows[strconv.Itoa(i+1)] = row
	}

	return rows, nil
}

func stringByAyahKey(raw []byte) (map[string]string, error) {
	var rows map[string]string
	if err := json.Unmarshal(raw, &rows); err == nil && len(rows) > 0 {
		return rows, nil
	}

	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, err
	}
	for _, key := range []string{"data", "translations", "rows"} {
		if nested, ok := object[key]; ok {
			return stringByAyahKey(nested)
		}
	}

	objectRows := make(map[string]string, len(object))
	for key, rawValue := range object {
		var row map[string]json.RawMessage
		if err := json.Unmarshal(rawValue, &row); err != nil {
			continue
		}
		value := firstStringValue(row, "t", "text", "translation")
		if value != "" {
			objectRows[key] = value
		}
	}
	if len(objectRows) > 0 {
		return objectRows, nil
	}

	return nil, errors.New("not a key-value translation map")
}

func firstString(row map[string]json.RawMessage, keys ...string) *string {
	value := firstStringValue(row, keys...)
	if value == "" {
		return nil
	}

	return &value
}

func firstStringValue(row map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		raw, ok := row[key]
		if !ok {
			continue
		}
		var value string
		if err := json.Unmarshal(raw, &value); err == nil {
			value = strings.TrimSpace(value)
			if value != "" {
				return value
			}
		}
		var nested map[string]json.RawMessage
		if err := json.Unmarshal(raw, &nested); err == nil {
			if value := firstStringValue(nested, "name", "text", "translation", "english", "arabic"); value != "" {
				return value
			}
		}
	}

	return ""
}

func firstIntPtr(row map[string]json.RawMessage, keys ...string) *int {
	value := firstInt(row, keys...)
	if value == 0 {
		return nil
	}

	return &value
}

func firstInt(row map[string]json.RawMessage, keys ...string) int {
	for _, key := range keys {
		raw, ok := row[key]
		if !ok {
			continue
		}
		var intValue int
		if err := json.Unmarshal(raw, &intValue); err == nil {
			return intValue
		}
		var floatValue float64
		if err := json.Unmarshal(raw, &floatValue); err == nil {
			return int(floatValue)
		}
		var stringValue string
		if err := json.Unmarshal(raw, &stringValue); err == nil {
			parsed, err := strconv.Atoi(strings.TrimSpace(stringValue))
			if err == nil {
				return parsed
			}
		}
	}

	return 0
}

func rawField(row map[string]json.RawMessage, keys ...string) json.RawMessage {
	for _, key := range keys {
		if raw, ok := row[key]; ok && len(raw) > 0 && string(raw) != "null" {
			return cloneBytes(raw)
		}
	}

	return nil
}

func cloneRaw(row map[string]json.RawMessage) json.RawMessage {
	raw, err := json.Marshal(row)
	if err != nil {
		return json.RawMessage(`{}`)
	}

	return raw
}

func cloneBytes(value []byte) []byte {
	if len(value) == 0 {
		return nil
	}
	cloned := make([]byte, len(value))
	copy(cloned, value)

	return cloned
}

func mustRaw(value any) json.RawMessage {
	raw, _ := json.Marshal(value)
	return raw
}

func stringOrEmpty(value json.RawMessage) string {
	if len(value) == 0 {
		return ""
	}

	return string(value)
}

// quranImportNow is isolated for tests that need deterministic timestamps later.
func quranImportNow() time.Time {
	return time.Now().UTC()
}
