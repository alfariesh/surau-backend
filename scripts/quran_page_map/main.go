// Command generate-page-map freezes the Quran Foundation Content API's
// verse-level QPC Hafs page metadata into a deterministic Go lookup table.
// It is a build-time maintenance tool; Surau never calls an external Quran
// service at runtime.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/format"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	sourceURLTemplate = "https://api.quran.com/api/v4/verses/by_juz/%d?fields=page_number&per_page=1000"
	expectedSnapshot  = "6acff20b3a70942e7e3980f99a1fc03df53bf891165a6cd63b714e028dd75c14"
	expectedAyahs     = 6236
	expectedPages     = 604
	profileVersion    = 1
	juzCount          = 30
	surahCount        = 114
	surahSlots        = surahCount + 1
	generatorTimeout  = 2 * time.Minute
	requestTimeout    = 20 * time.Second
	maxResponseBytes  = int64(4 << 20)
	generatedFilePerm = 0o600
)

var (
	errUnexpectedStatus = errors.New("unexpected Quran page API status")
	errSourceData       = errors.New("invalid Quran page source data")
)

type apiResponse struct {
	Verses []apiVerse `json:"verses"`
}

type apiVerse struct {
	VerseKey   string `json:"verse_key"`
	PageNumber int    `json:"page_number"`
}

type versePage struct {
	surah int
	ayah  int
	page  int
}

//nolint:wsl_v5 // flags, frozen checksum validation, rendering, and write are one generator pipeline
func main() {
	output := flag.String("output", "qpc_hafs_page_map_v1_gen.go", "generated Go output")

	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), generatorTimeout)
	defer cancel()

	verses, err := fetchVerses(ctx, &http.Client{Timeout: requestTimeout})
	if err != nil {
		fatalf("fetch page map: %v", err)
	}

	snapshot := canonicalSnapshot(verses)
	sum := sha256.Sum256(snapshot)
	checksum := hex.EncodeToString(sum[:])

	if checksum != expectedSnapshot {
		fatalf("source snapshot changed: got %s, want %s; audit before creating a new profile", checksum, expectedSnapshot)
	}

	source, err := render(verses, checksum)
	if err != nil {
		fatalf("render page map: %v", err)
	}
	if err := os.WriteFile(*output, source, generatedFilePerm); err != nil {
		fatalf("write %s: %v", *output, err)
	}
}

//nolint:wsl_v5 // the bounded source fan-in and one final coverage validation are intentionally adjacent
func fetchVerses(ctx context.Context, client *http.Client) ([]versePage, error) {
	byKey := make(map[string]versePage, expectedAyahs)

	for juz := 1; juz <= juzCount; juz++ {
		payload, err := fetchJuz(ctx, client, juz)
		if err != nil {
			return nil, err
		}

		for _, rawVerse := range payload.Verses {
			verse, err := normalizeVerse(rawVerse)
			if err != nil {
				return nil, err
			}
			if _, exists := byKey[rawVerse.VerseKey]; exists {
				return nil, fmt.Errorf("%w: duplicate ayah %s", errSourceData, rawVerse.VerseKey)
			}

			byKey[rawVerse.VerseKey] = verse
		}
	}

	return finalizeVerses(byKey)
}

//nolint:wsl_v5 // request, bounded body read, close, status, and decode form one audited fetch
func fetchJuz(ctx context.Context, client *http.Client, juz int) (apiResponse, error) {
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		fmt.Sprintf(sourceURLTemplate, juz),
		http.NoBody,
	)
	if err != nil {
		return apiResponse{}, fmt.Errorf("juz %d request: %w", juz, err)
	}
	request.Header.Set("User-Agent", "surau-qpc-page-map-generator/1")

	response, err := client.Do(request)
	if err != nil {
		return apiResponse{}, fmt.Errorf("juz %d: %w", juz, err)
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	closeErr := response.Body.Close()

	if readErr != nil {
		return apiResponse{}, fmt.Errorf("juz %d body: %w", juz, readErr)
	}
	if closeErr != nil {
		return apiResponse{}, fmt.Errorf("juz %d close body: %w", juz, closeErr)
	}
	if response.StatusCode != http.StatusOK {
		return apiResponse{}, fmt.Errorf("%w: juz %d status %d", errUnexpectedStatus, juz, response.StatusCode)
	}

	var payload apiResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return apiResponse{}, fmt.Errorf("juz %d decode: %w", juz, err)
	}

	return payload, nil
}

func normalizeVerse(raw apiVerse) (versePage, error) {
	surah, ayah, err := parseAyahKey(raw.VerseKey)
	if err != nil {
		return versePage{}, err
	}

	if raw.PageNumber < 1 || raw.PageNumber > expectedPages {
		return versePage{}, fmt.Errorf(
			"%w: ayah %s has page %d",
			errSourceData,
			raw.VerseKey,
			raw.PageNumber,
		)
	}

	return versePage{surah: surah, ayah: ayah, page: raw.PageNumber}, nil
}

//nolint:wsl_v5 // flattening, canonical sort, and page coverage are one final source validation
func finalizeVerses(byKey map[string]versePage) ([]versePage, error) {
	if len(byKey) != expectedAyahs {
		return nil, fmt.Errorf("%w: ayah coverage %d, want %d", errSourceData, len(byKey), expectedAyahs)
	}

	verses := make([]versePage, 0, len(byKey))
	pageSet := make(map[int]struct{}, expectedPages)

	for _, verse := range byKey {
		verses = append(verses, verse)
		pageSet[verse.page] = struct{}{}
	}
	sort.Slice(verses, func(i, j int) bool {
		if verses[i].surah != verses[j].surah {
			return verses[i].surah < verses[j].surah
		}

		return verses[i].ayah < verses[j].ayah
	})
	if len(pageSet) != expectedPages {
		return nil, fmt.Errorf("%w: page coverage %d, want %d", errSourceData, len(pageSet), expectedPages)
	}

	return verses, nil
}

func parseAyahKey(value string) (surahID, ayahNumber int, err error) {
	surahText, ayahText, ok := strings.Cut(value, ":")
	if !ok || strings.Contains(ayahText, ":") {
		return 0, 0, fmt.Errorf("%w: invalid ayah key %q", errSourceData, value)
	}

	surahID, err = strconv.Atoi(surahText)
	if err != nil || surahID < 1 || surahID > surahCount {
		return 0, 0, fmt.Errorf("%w: invalid ayah key %q", errSourceData, value)
	}

	ayahNumber, err = strconv.Atoi(ayahText)
	if err != nil || ayahNumber < 1 {
		return 0, 0, fmt.Errorf("%w: invalid ayah key %q", errSourceData, value)
	}

	return surahID, ayahNumber, nil
}

func canonicalSnapshot(verses []versePage) []byte {
	var output bytes.Buffer
	for _, verse := range verses {
		fmt.Fprintf(&output, "%d:%d\t%d\n", verse.surah, verse.ayah, verse.page)
	}

	return output.Bytes()
}

//nolint:wsl_v5 // generated declarations and the complete surah table are emitted sequentially
func render(verses []versePage, checksum string) ([]byte, error) {
	bySurah := make([][]versePage, surahSlots)

	for _, verse := range verses {
		bySurah[verse.surah] = append(bySurah[verse.surah], verse)
	}

	var output bytes.Buffer
	output.WriteString("// Code generated by go generate; DO NOT EDIT.\n")
	output.WriteString("package quranutil\n\n")
	fmt.Fprintf(&output, "const (\n\tQPCHafsPageMapProfileVersion = %d\n", profileVersion)
	fmt.Fprintf(&output, "\tQPCHafsPageMapSourceURL = %q\n", sourceURLTemplate)
	fmt.Fprintf(&output, "\tQPCHafsPageMapSnapshotSHA256 = %q\n", checksum)
	output.WriteString("\tQPCHafsPageMapSnapshotDate = \"2026-07-12\"\n)\n\n")
	output.WriteString("var qpcHafsPageNumbersV1 = [...][]uint16{\n\tnil,\n")

	for surah := 1; surah <= surahCount; surah++ {
		if err := writeSurah(&output, surah, bySurah[surah]); err != nil {
			return nil, err
		}
	}
	output.WriteString("}\n")

	formatted, err := format.Source(output.Bytes())
	if err != nil {
		return nil, fmt.Errorf("format generated source: %w", err)
	}

	return formatted, nil
}

//nolint:wsl_v5 // every ayah must be checked immediately before its page is emitted
func writeSurah(output *bytes.Buffer, surah int, ayahs []versePage) error {
	if len(ayahs) == 0 {
		return fmt.Errorf("%w: surah %d has no ayahs", errSourceData, surah)
	}

	output.WriteString("\t{0")

	for expectedAyah, verse := range ayahs {
		if verse.ayah != expectedAyah+1 {
			return fmt.Errorf("%w: surah %d missing ayah %d", errSourceData, surah, expectedAyah+1)
		}

		fmt.Fprintf(output, ", %d", verse.page)
	}
	output.WriteString("},\n")

	return nil
}

func fatalf(message string, args ...any) {
	fmt.Fprintf(os.Stderr, message+"\n", args...)
	os.Exit(1)
}
