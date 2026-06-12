package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	defaultBaseURL             = "https://web-api.qurankemenag.net"
	defaultSiteURL             = "https://quran.kemenag.go.id"
	defaultTranslationFilename = "kemenag-translation-id.json"
	defaultLatinFilename       = "kemenag-transliteration-id.json"
	expectedSurahCount         = 114
	expectedAyahCount          = 6236
)

type apiListResponse[T any] struct {
	Data []T `json:"data"`
}

type kemenagSurah struct {
	ID              int     `json:"id"`
	Arabic          string  `json:"arabic"`
	Latin           string  `json:"latin"`
	Transliteration string  `json:"transliteration"`
	Translation     string  `json:"translation"`
	NumAyah         int     `json:"num_ayah"`
	Page            int     `json:"page"`
	Location        string  `json:"location"`
	UpdatedAt       *string `json:"updated_at"`
}

type kemenagAyah struct {
	ID          int          `json:"id"`
	SurahID     int          `json:"surah_id"`
	Ayah        int          `json:"ayah"`
	Page        int          `json:"page"`
	QuarterHizb float64      `json:"quarter_hizb"`
	Juz         int          `json:"juz"`
	Manzil      int          `json:"manzil"`
	Arabic      string       `json:"arabic"`
	Kitabah     string       `json:"kitabah"`
	Latin       string       `json:"latin"`
	ArabicWords []string     `json:"arabic_words"`
	Translation string       `json:"translation"`
	Footnotes   *string      `json:"footnotes"`
	UpdatedAt   *string      `json:"updated_at"`
	Surah       kemenagSurah `json:"surah"`
}

type translationRow struct {
	VerseKey  string          `json:"verse_key"`
	Text      string          `json:"text"`
	Footnotes []footnoteRow   `json:"footnotes,omitempty"`
	Metadata  translationMeta `json:"metadata"`
}

type footnoteRow struct {
	Number int    `json:"number"`
	Marker string `json:"marker"`
	Text   string `json:"text"`
}

type translationMeta struct {
	Source      string   `json:"source"`
	KemenagID   int      `json:"kemenag_id"`
	SurahID     int      `json:"surah_id"`
	Ayah        int      `json:"ayah"`
	Page        int      `json:"page"`
	Juz         int      `json:"juz"`
	QuarterHizb float64  `json:"quarter_hizb"`
	Manzil      int      `json:"manzil"`
	ArabicWords []string `json:"arabic_words,omitempty"`
	UpdatedAt   *string  `json:"updated_at,omitempty"`
}

func main() {
	baseURL := flag.String("base-url", defaultBaseURL, "Kemenag web API base URL")
	siteURL := flag.String("site-url", defaultSiteURL, "Kemenag Quran site URL for Origin/Referer headers")
	outDir := flag.String("out-dir", ".", "output directory")
	translationFilename := flag.String("translation-file", defaultTranslationFilename, "translation JSON output filename")
	latinFilename := flag.String("transliteration-file", defaultLatinFilename, "transliteration JSON output filename")
	timeout := flag.Duration("timeout", 60*time.Second, "HTTP request timeout")
	flag.Parse()

	if err := run(context.Background(), fetchOptions{
		BaseURL:             *baseURL,
		SiteURL:             *siteURL,
		OutDir:              *outDir,
		TranslationFilename: *translationFilename,
		LatinFilename:       *latinFilename,
		Timeout:             *timeout,
	}); err != nil {
		log.Fatalf("fetch kemenag quran assets failed: %v", err)
	}
}

type fetchOptions struct {
	BaseURL             string
	SiteURL             string
	OutDir              string
	TranslationFilename string
	LatinFilename       string
	Timeout             time.Duration
}

func run(ctx context.Context, opts fetchOptions) error {
	opts.BaseURL = strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	opts.SiteURL = strings.TrimRight(strings.TrimSpace(opts.SiteURL), "/")
	if opts.BaseURL == "" || opts.SiteURL == "" {
		return fmt.Errorf("base-url and site-url are required")
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 60 * time.Second
	}
	if err := os.MkdirAll(opts.OutDir, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	client := &http.Client{Timeout: opts.Timeout}
	surahs, err := fetchSurahs(ctx, client, opts)
	if err != nil {
		return err
	}
	if len(surahs) != expectedSurahCount {
		return fmt.Errorf("expected %d surahs, got %d", expectedSurahCount, len(surahs))
	}
	sort.Slice(surahs, func(i, j int) bool { return surahs[i].ID < surahs[j].ID })

	translationRows := make([]translationRow, 0, expectedAyahCount)
	transliterations := make(map[string]string, expectedAyahCount)
	for _, surah := range surahs {
		ayahs, err := fetchAyahs(ctx, client, opts, surah)
		if err != nil {
			return err
		}
		if len(ayahs) != surah.NumAyah {
			return fmt.Errorf("surah %d expected %d ayahs, got %d", surah.ID, surah.NumAyah, len(ayahs))
		}
		sort.Slice(ayahs, func(i, j int) bool { return ayahs[i].Ayah < ayahs[j].Ayah })
		for index, ayah := range ayahs {
			expectedAyah := index + 1
			if ayah.SurahID != surah.ID || ayah.Ayah != expectedAyah {
				return fmt.Errorf("surah %d ayah sequence mismatch at index %d: got %d:%d", surah.ID, index, ayah.SurahID, ayah.Ayah)
			}
			ayahKey := fmt.Sprintf("%d:%d", ayah.SurahID, ayah.Ayah)
			translationRows = append(translationRows, translationRow{
				VerseKey:  ayahKey,
				Text:      strings.TrimSpace(ayah.Translation),
				Footnotes: parseFootnotes(ayah.Footnotes),
				Metadata: translationMeta{
					Source:      "kemenag-quran-ayah",
					KemenagID:   ayah.ID,
					SurahID:     ayah.SurahID,
					Ayah:        ayah.Ayah,
					Page:        ayah.Page,
					Juz:         ayah.Juz,
					QuarterHizb: ayah.QuarterHizb,
					Manzil:      ayah.Manzil,
					ArabicWords: ayah.ArabicWords,
					UpdatedAt:   ayah.UpdatedAt,
				},
			})
			transliterations[ayahKey] = strings.TrimSpace(ayah.Latin)
		}
	}
	if len(translationRows) != expectedAyahCount || len(transliterations) != expectedAyahCount {
		return fmt.Errorf("expected %d ayahs, got translations=%d transliterations=%d", expectedAyahCount, len(translationRows), len(transliterations))
	}

	translationPath := filepath.Join(opts.OutDir, opts.TranslationFilename)
	translationChecksum, err := writeJSONWithChecksum(translationPath, translationRows)
	if err != nil {
		return err
	}
	latinPath := filepath.Join(opts.OutDir, opts.LatinFilename)
	latinChecksum, err := writeJSONWithChecksum(latinPath, transliterations)
	if err != nil {
		return err
	}

	fmt.Printf(
		"surahs=%d ayahs=%d translation=%s checksum=%s transliteration=%s checksum=%s\n",
		len(surahs),
		len(translationRows),
		translationPath,
		translationChecksum,
		latinPath,
		latinChecksum,
	)

	return nil
}

func fetchSurahs(ctx context.Context, client *http.Client, opts fetchOptions) ([]kemenagSurah, error) {
	var response apiListResponse[kemenagSurah]
	if err := fetchJSON(ctx, client, opts, "quran-surah", opts.SiteURL+"/quran/per-ayat/surah/1", &response); err != nil {
		return nil, fmt.Errorf("fetch surahs: %w", err)
	}

	return response.Data, nil
}

func fetchAyahs(ctx context.Context, client *http.Client, opts fetchOptions, surah kemenagSurah) ([]kemenagAyah, error) {
	params := url.Values{}
	params.Set("surah", fmt.Sprintf("%d", surah.ID))
	params.Set("start", "0")
	params.Set("limit", fmt.Sprintf("%d", surah.NumAyah))
	endpoint := "quran-ayah?" + params.Encode()
	referer := fmt.Sprintf("%s/quran/per-ayat/surah/%d", opts.SiteURL, surah.ID)

	var response apiListResponse[kemenagAyah]
	if err := fetchJSON(ctx, client, opts, endpoint, referer, &response); err != nil {
		return nil, fmt.Errorf("fetch surah %d ayahs: %w", surah.ID, err)
	}

	return response.Data, nil
}

func fetchJSON(ctx context.Context, client *http.Client, opts fetchOptions, endpoint string, referer string, target any) error {
	requestURL := opts.BaseURL + "/" + strings.TrimLeft(endpoint, "/")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Origin", opts.SiteURL)
	req.Header.Set("Referer", referer)
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; SurauBackendAssetFetcher/1.0)")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("GET %s returned %s", requestURL, resp.Status)
	}
	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(target); err != nil {
		return err
	}

	return nil
}

var footnoteMarkerRE = regexp.MustCompile(`(?m)(\d+)\)\s*`)

func parseFootnotes(raw *string) []footnoteRow {
	if raw == nil {
		return nil
	}
	text := strings.TrimSpace(*raw)
	if text == "" {
		return nil
	}
	matches := footnoteMarkerRE.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return []footnoteRow{{Text: text}}
	}

	footnotes := make([]footnoteRow, 0, len(matches))
	for i, match := range matches {
		start := match[0]
		end := match[1]
		numberStart := match[2]
		numberEnd := match[3]
		nextStart := len(text)
		if i+1 < len(matches) {
			nextStart = matches[i+1][0]
		}
		number := 0
		_, _ = fmt.Sscanf(text[numberStart:numberEnd], "%d", &number)
		footnotes = append(footnotes, footnoteRow{
			Number: number,
			Marker: strings.TrimSpace(text[start:end]),
			Text:   strings.TrimSpace(text[end:nextStart]),
		})
	}

	return footnotes
}

func writeJSONWithChecksum(path string, value any) (string, error) {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal %s: %w", path, err)
	}
	raw = append(raw, '\n')
	sum := sha256.Sum256(raw)
	checksum := hex.EncodeToString(sum[:])
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	checksumLine := fmt.Sprintf("%s  %s\n", checksum, filepath.Base(path))
	if err := os.WriteFile(path+".sha256", []byte(checksumLine), 0o644); err != nil {
		return "", fmt.Errorf("write %s.sha256: %w", path, err)
	}

	return checksum, nil
}
