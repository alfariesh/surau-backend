// Command quran-page-bench measures the public Quran page reader contract.
// It is intentionally bounded so the same binary is safe to run against a
// production origin from the reviewed diagnostics workflow.
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alfariesh/surau-backend/internal/anchor"
	"github.com/alfariesh/surau-backend/internal/quranutil"
	"github.com/google/uuid"
)

const (
	exitSuccess           = 0
	exitFailure           = 1
	exitUsage             = 2
	maxConcurrency        = 10
	maxRequests           = 200
	maxRunDuration        = time.Minute
	maxResponse           = 4 << 20
	defaultRequests       = 20
	defaultRequestTimeout = 15 * time.Second
	versionResponseLimit  = 64 << 10
	privateFileMode       = 0o600
	requestIDByteLength   = 12
	percentileP50         = 50
	percentileP95         = 95
	percentileBase        = 100
	cachePolicyNone       = ""
	cachePolicyOrigin     = "origin-revalidate"
	cachePolicyCloudflare = "cloudflare-bypass"
	revalidateCacheHeader = "public, max-age=0, must-revalidate"
)

var (
	errInvalidConfig  = errors.New("invalid benchmark configuration")
	errBenchmark      = errors.New("benchmark failed")
	errVersion        = errors.New("version check failed")
	errReaderContract = errors.New("quran reader contract failed")
)

type options struct {
	baseURL         string
	pages           []int
	query           url.Values
	requests        int
	concurrency     int
	warmupsPerPage  int
	requestTimeout  time.Duration
	maxDuration     time.Duration
	expectedVersion string
	p95Budget       time.Duration
	cachePolicy     string
	output          string
}

type pagePresentation struct {
	UnitID        string             `json:"unit_id"`
	Anchor        string             `json:"anchor"`
	LicenseStatus string             `json:"license_status"`
	FootnoteUnits []pageFootnoteUnit `json:"footnote_units"`
}

type pageFootnoteUnit struct {
	UnitID       string `json:"unit_id"`
	Anchor       string `json:"anchor"`
	ParentUnitID string `json:"parent_unit_id"`
}

type pageItem struct {
	AyahKey           string            `json:"ayah_key"`
	PageNumber        *int              `json:"page_number"`
	PrimaryUnitID     string            `json:"primary_unit_id"`
	PrimaryUnitAnchor string            `json:"primary_unit_anchor"`
	Translation       *pagePresentation `json:"translation"`
	Transliteration   *pagePresentation `json:"transliteration"`
}

type pageEnvelope struct {
	Items []pageItem `json:"items"`
	Total int        `json:"total"`
}

type sample struct {
	Page              int     `json:"page"`
	Status            int     `json:"status"`
	DurationMS        float64 `json:"duration_ms"`
	TTFBMS            float64 `json:"ttfb_ms"`
	DNSMS             float64 `json:"dns_ms,omitempty"`
	ConnectMS         float64 `json:"connect_ms,omitempty"`
	TLSMS             float64 `json:"tls_ms,omitempty"`
	Bytes             int     `json:"bytes"`
	ETag              string  `json:"etag,omitempty"`
	CFCacheStatus     string  `json:"cf_cache_status,omitempty"`
	SurauCache        string  `json:"surau_cache,omitempty"`
	CacheControl      string  `json:"cache_control,omitempty"`
	ResponseSHA256    string  `json:"response_sha256,omitempty"`
	ResponseRequestID string  `json:"response_request_id,omitempty"`
	TraceID           string  `json:"trace_id,omitempty"`
	Error             string  `json:"error,omitempty"`
}

type latencySummary struct {
	Samples int     `json:"samples"`
	P50MS   float64 `json:"p50_ms"`
	P95MS   float64 `json:"p95_ms"`
	MaxMS   float64 `json:"max_ms"`
}

type report struct {
	StartedAt      time.Time                 `json:"started_at"`
	FinishedAt     time.Time                 `json:"finished_at"`
	BaseURL        string                    `json:"base_url"`
	Query          string                    `json:"query"`
	Version        string                    `json:"version"`
	Requests       int                       `json:"requests"`
	Concurrency    int                       `json:"concurrency"`
	WarmupsPerPage int                       `json:"warmups_per_page"`
	Overall        latencySummary            `json:"overall"`
	ByPage         map[string]latencySummary `json:"by_page"`
	CacheStatuses  map[string]int            `json:"cache_statuses"`
	ContentHashes  map[string][]string       `json:"content_hashes"`
	Errors         int                       `json:"errors"`
	Samples        []sample                  `json:"samples"`
}

type traceTimes struct {
	start, firstByte          time.Time
	dnsStart, dnsDone         time.Time
	connectStart, connectDone time.Time
	tlsStart, tlsDone         time.Time
}

func main() {
	os.Exit(realMain())
}

//nolint:wsl_v5 // Exit-code handling stays linear and explicit.
func realMain() int {
	opts, err := parseOptions(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return exitUsage
	}

	ctx, cancel := context.WithTimeout(context.Background(), opts.maxDuration)
	defer cancel()

	result, err := run(ctx, http.DefaultTransport, &opts)
	if result != nil {
		if writeErr := writeReport(result, opts.output); writeErr != nil {
			fmt.Fprintln(os.Stderr, writeErr)

			return exitFailure
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)

		return exitFailure
	}

	return exitSuccess
}

//nolint:cyclop,funlen,gocognit,gocyclo,wsl_v5 // Sequential validation keeps every production load bound explicit.
func parseOptions(args []string) (options, error) {
	flags := flag.NewFlagSet("quran-page-bench", flag.ContinueOnError)
	flags.SetOutput(io.Discard)

	var (
		baseURL         = flags.String("base-url", "", "API base URL")
		pagesValue      = flags.String("pages", "1,48,421,585,604", "comma-separated page numbers")
		queryValue      = flags.String("query", "view=reader_minimal", "fixed endpoint query string")
		requests        = flags.Int("requests", defaultRequests, "number of measured requests")
		concurrency     = flags.Int("concurrency", 1, "concurrent requests (maximum 10)")
		warmups         = flags.Int("warmups-per-page", 1, "unreported warm-up requests per page")
		requestTimeout  = flags.Duration("request-timeout", defaultRequestTimeout, "per-request timeout")
		maxDuration     = flags.Duration("max-duration", maxRunDuration, "whole-run timeout (maximum 60s)")
		expectedVersion = flags.String("expected-version", "", "required /version value")
		p95BudgetMS     = flags.Int("p95-budget-ms", 0, "fail when overall p95 exceeds this budget")
		cachePolicy     = flags.String(
			"cache-policy",
			cachePolicyNone,
			"required cache policy: origin-revalidate or cloudflare-bypass",
		)
		output = flags.String("output", "", "optional JSON output path")
	)
	if err := flags.Parse(args); err != nil {
		return options{}, fmt.Errorf("parse flags: %w", err)
	}

	pages, err := parsePages(*pagesValue)
	if err != nil {
		return options{}, err
	}
	query, err := url.ParseQuery(*queryValue)
	if err != nil {
		return options{}, fmt.Errorf("parse query: %w", err)
	}
	parsedBaseURL, err := url.Parse(strings.TrimRight(strings.TrimSpace(*baseURL), "/"))
	if err != nil || parsedBaseURL.Scheme == "" || parsedBaseURL.Host == "" {
		return options{}, fmt.Errorf("%w: base-url must be an absolute HTTP(S) URL", errInvalidConfig)
	}
	if parsedBaseURL.Scheme != "http" && parsedBaseURL.Scheme != "https" {
		return options{}, fmt.Errorf("%w: base-url scheme must be http or https", errInvalidConfig)
	}
	if *requests < 1 || *requests > maxRequests {
		return options{}, fmt.Errorf("%w: requests must be between 1 and %d", errInvalidConfig, maxRequests)
	}
	if *concurrency < 1 || *concurrency > maxConcurrency {
		return options{}, fmt.Errorf("%w: concurrency must be between 1 and %d", errInvalidConfig, maxConcurrency)
	}
	if *warmups < 0 || *warmups*len(pages)+*requests > maxRequests {
		return options{}, fmt.Errorf(
			"%w: measured plus warm-up requests must not exceed %d",
			errInvalidConfig,
			maxRequests,
		)
	}
	if *requestTimeout <= 0 || *requestTimeout > maxRunDuration {
		return options{}, fmt.Errorf("%w: request-timeout must be in (0s, 60s]", errInvalidConfig)
	}
	if *maxDuration <= 0 || *maxDuration > maxRunDuration {
		return options{}, fmt.Errorf("%w: max-duration must be in (0s, 60s]", errInvalidConfig)
	}
	if *p95BudgetMS < 0 {
		return options{}, fmt.Errorf("%w: p95-budget-ms must not be negative", errInvalidConfig)
	}
	switch *cachePolicy {
	case cachePolicyNone, cachePolicyOrigin, cachePolicyCloudflare:
	default:
		return options{}, fmt.Errorf("%w: invalid cache-policy %q", errInvalidConfig, *cachePolicy)
	}

	return options{
		baseURL:         parsedBaseURL.String(),
		pages:           pages,
		query:           query,
		requests:        *requests,
		concurrency:     *concurrency,
		warmupsPerPage:  *warmups,
		requestTimeout:  *requestTimeout,
		maxDuration:     *maxDuration,
		expectedVersion: strings.TrimSpace(*expectedVersion),
		p95Budget:       time.Duration(*p95BudgetMS) * time.Millisecond,
		cachePolicy:     *cachePolicy,
		output:          strings.TrimSpace(*output),
	}, nil
}

//nolint:wsl_v5 // Parsing and de-duplication are intentionally kept in one short pass.
func parsePages(value string) ([]int, error) {
	parts := strings.Split(value, ",")
	pages := make([]int, 0, len(parts))
	seen := make(map[int]struct{}, len(parts))
	for _, part := range parts {
		page, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || page < 1 || page > 604 {
			return nil, fmt.Errorf("%w: invalid Quran page %q", errInvalidConfig, part)
		}
		if _, exists := seen[page]; exists {
			continue
		}
		seen[page] = struct{}{}
		pages = append(pages, page)
	}
	if len(pages) == 0 {
		return nil, fmt.Errorf("%w: at least one page is required", errInvalidConfig)
	}

	return pages, nil
}

//nolint:cyclop,funlen,gocognit,gocyclo,wsl_v5 // Bounded worker orchestration is kept together so abort behavior is auditable.
func run(ctx context.Context, transport http.RoundTripper, opts *options) (*report, error) {
	client := &http.Client{Transport: transport, Timeout: opts.requestTimeout}
	version, err := verifyVersion(ctx, client, opts.baseURL, opts.expectedVersion)
	if err != nil {
		return nil, err
	}

	for _, page := range opts.pages {
		for range opts.warmupsPerPage {
			if _, err := requestPage(ctx, client, opts, page); err != nil {
				return nil, fmt.Errorf("warm-up page %d: %w", page, err)
			}
		}
	}

	startedAt := time.Now().UTC()
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan int)
	results := make(chan sample, opts.requests)
	var workers sync.WaitGroup
	for range opts.concurrency {
		workers.Go(func() {
			for requestIndex := range jobs {
				page := opts.pages[requestIndex%len(opts.pages)]
				result, requestErr := requestPage(runCtx, client, opts, page)
				if requestErr != nil {
					result.Error = requestErr.Error()
					cancel()
				}
				results <- result
			}
		})
	}

	go func() {
		defer close(jobs)
		for index := range opts.requests {
			select {
			case jobs <- index:
			case <-runCtx.Done():
				return
			}
		}
	}()
	go func() {
		workers.Wait()
		close(results)
	}()

	samples := make([]sample, 0, opts.requests)
	for result := range results {
		samples = append(samples, result)
	}
	report := buildReport(startedAt, time.Now().UTC(), opts, version, samples)
	if report.Errors > 0 {
		return &report, fmt.Errorf("%w: aborted after %d error(s)", errBenchmark, report.Errors)
	}
	if len(samples) != opts.requests {
		return &report, fmt.Errorf(
			"%w: stopped early after %d of %d requests",
			errBenchmark,
			len(samples),
			opts.requests,
		)
	}
	if opts.p95Budget > 0 && report.Overall.P95MS >= milliseconds(opts.p95Budget) {
		return &report, fmt.Errorf(
			"%w: p95 %.3fms exceeds budget %s",
			errBenchmark,
			report.Overall.P95MS,
			opts.p95Budget,
		)
	}

	return &report, nil
}

//nolint:wsl_v5 // Each request lifecycle boundary is intentionally explicit.
func verifyVersion(ctx context.Context, client *http.Client, baseURL, expected string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/version", http.NoBody)
	if err != nil {
		return "", err
	}
	response, err := client.Do(request)
	if err != nil {
		return "", fmt.Errorf("version request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%w: request returned HTTP %d", errVersion, response.StatusCode)
	}
	var payload struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, versionResponseLimit)).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode version: %w", err)
	}
	if expected != "" && payload.Version != expected {
		return payload.Version, fmt.Errorf(
			"%w: version mismatch: expected %q, got %q",
			errVersion,
			expected,
			payload.Version,
		)
	}

	return payload.Version, nil
}

//nolint:funlen,wsl_v5 // Trace capture is sequential by design and mirrors the HTTP lifecycle.
func requestPage(ctx context.Context, client *http.Client, opts *options, page int) (sample, error) {
	result := sample{Page: page}
	endpoint := fmt.Sprintf("%s/v1/quran/pages/%d/ayahs", opts.baseURL, page)
	if encoded := opts.query.Encode(); encoded != "" {
		endpoint += "?" + encoded
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return result, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-Request-ID", newRequestID())

	times := &traceTimes{start: time.Now()}
	request = request.WithContext(httptrace.WithClientTrace(request.Context(), &httptrace.ClientTrace{
		DNSStart:             func(httptrace.DNSStartInfo) { times.dnsStart = time.Now() },
		DNSDone:              func(httptrace.DNSDoneInfo) { times.dnsDone = time.Now() },
		ConnectStart:         func(_, _ string) { times.connectStart = time.Now() },
		ConnectDone:          func(_, _ string, _ error) { times.connectDone = time.Now() },
		TLSHandshakeStart:    func() { times.tlsStart = time.Now() },
		TLSHandshakeDone:     func(_ tls.ConnectionState, _ error) { times.tlsDone = time.Now() },
		GotFirstResponseByte: func() { times.firstByte = time.Now() },
	}))

	response, err := client.Do(request)
	responseReceived := time.Now()
	result.DurationMS = milliseconds(responseReceived.Sub(times.start))
	result.TTFBMS = durationBetween(times.start, times.firstByte)
	result.DNSMS = durationBetween(times.dnsStart, times.dnsDone)
	result.ConnectMS = durationBetween(times.connectStart, times.connectDone)
	result.TLSMS = durationBetween(times.tlsStart, times.tlsDone)
	if err != nil {
		return result, err
	}
	defer response.Body.Close()

	result.Status = response.StatusCode
	result.ETag = response.Header.Get("ETag")
	result.CFCacheStatus = response.Header.Get("CF-Cache-Status")
	result.SurauCache = response.Header.Get("X-Surau-Cache")
	result.CacheControl = response.Header.Get("Cache-Control")
	result.ResponseRequestID = response.Header.Get("X-Request-ID")
	result.TraceID = response.Header.Get("X-Trace-ID")
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponse+1))
	result.DurationMS = milliseconds(time.Since(times.start))
	if err != nil {
		return result, fmt.Errorf("read page %d: %w", page, err)
	}
	result.Bytes = len(body)
	if len(body) > maxResponse {
		return result, fmt.Errorf(
			"%w: page %d response exceeds %d bytes",
			errReaderContract,
			page,
			maxResponse,
		)
	}
	if response.StatusCode != http.StatusOK {
		return result, fmt.Errorf(
			"%w: page %d returned HTTP %d",
			errReaderContract,
			page,
			response.StatusCode,
		)
	}
	if err := validateCachePolicy(opts.cachePolicy, response.Header); err != nil {
		return result, err
	}
	hash := sha256.Sum256(body)
	result.ResponseSHA256 = hex.EncodeToString(hash[:])
	if err := validatePageBody(page, body); err != nil {
		return result, err
	}

	return result, nil
}

//nolint:cyclop,funlen,gocognit,gocyclo,wsl_v5 // Every publication-safety invariant is checked explicitly.
func validatePageBody(page int, body []byte) error {
	var payload pageEnvelope
	if err := json.Unmarshal(body, &payload); err != nil {
		return fmt.Errorf("decode page %d: %w", page, err)
	}
	if payload.Total != len(payload.Items) {
		return fmt.Errorf(
			"%w: page %d total=%d differs from items=%d",
			errReaderContract,
			page,
			payload.Total,
			len(payload.Items),
		)
	}
	if expected, exists := expectedRepresentativePageCount(page); exists && payload.Total != expected {
		return fmt.Errorf(
			"%w: page %d total=%d, expected frozen QPC total=%d",
			errReaderContract,
			page,
			payload.Total,
			expected,
		)
	}
	seen := make(map[string]struct{}, len(payload.Items))
	seenUnitIDs := make(map[string]struct{}, len(payload.Items))
	seenUnitAnchors := make(map[string]struct{}, len(payload.Items))
	for _, item := range payload.Items {
		if item.PageNumber == nil || *item.PageNumber != page {
			return fmt.Errorf(
				"%w: page %d contains ayah %s with wrong page_number",
				errReaderContract,
				page,
				item.AyahKey,
			)
		}
		if _, exists := seen[item.AyahKey]; exists {
			return fmt.Errorf(
				"%w: page %d contains duplicate ayah %s",
				errReaderContract,
				page,
				item.AyahKey,
			)
		}
		seen[item.AyahKey] = struct{}{}

		if err := validateCitableIdentity(
			item.AyahKey,
			"primary text",
			item.PrimaryUnitID,
			item.PrimaryUnitAnchor,
			seenUnitIDs,
			seenUnitAnchors,
		); err != nil {
			return err
		}

		if err := validatePresentation(
			item.AyahKey,
			"translation",
			item.Translation,
			seenUnitIDs,
			seenUnitAnchors,
		); err != nil {
			return err
		}

		if err := validatePresentation(
			item.AyahKey,
			"transliteration",
			item.Transliteration,
			seenUnitIDs,
			seenUnitAnchors,
		); err != nil {
			return err
		}
	}

	return nil
}

func validatePresentation(
	ayahKey, role string,
	presentation *pagePresentation,
	seenUnitIDs, seenUnitAnchors map[string]struct{},
) error {
	if presentation == nil {
		return nil
	}

	if presentation.LicenseStatus != "permitted" {
		return fmt.Errorf(
			"%w: ayah %s exposes %s with license_status=%q",
			errReaderContract,
			ayahKey,
			role,
			presentation.LicenseStatus,
		)
	}

	if err := validateCitableIdentity(
		ayahKey,
		role,
		presentation.UnitID,
		presentation.Anchor,
		seenUnitIDs,
		seenUnitAnchors,
	); err != nil {
		return err
	}

	for _, footnote := range presentation.FootnoteUnits {
		if footnote.ParentUnitID != presentation.UnitID {
			return fmt.Errorf(
				"%w: ayah %s %s footnote has parent_unit_id=%q, expected %q",
				errReaderContract,
				ayahKey,
				role,
				footnote.ParentUnitID,
				presentation.UnitID,
			)
		}

		if err := validateCitableIdentity(
			ayahKey,
			role+" footnote",
			footnote.UnitID,
			footnote.Anchor,
			seenUnitIDs,
			seenUnitAnchors,
		); err != nil {
			return err
		}
	}

	return nil
}

func validateCitableIdentity(
	ayahKey, role, unitID, unitAnchor string,
	seenUnitIDs, seenUnitAnchors map[string]struct{},
) error {
	parsedUnitID, err := uuid.Parse(unitID)
	if err != nil || parsedUnitID.String() != unitID {
		return fmt.Errorf(
			"%w: ayah %s has invalid %s Citable Unit identity UUID %q",
			errReaderContract,
			ayahKey,
			role,
			unitID,
		)
	}

	point, err := anchor.ParsePoint(unitAnchor)
	if err != nil || point.Kind() != anchor.PointKindQuranUnit {
		return fmt.Errorf(
			"%w: ayah %s has invalid %s Citable Unit Anchor %q",
			errReaderContract,
			ayahKey,
			role,
			unitAnchor,
		)
	}

	surahID, ayahNumber, err := quranutil.ParseAyahKey(ayahKey)
	if err != nil || point.Surah() != surahID || point.Ayah() != ayahNumber {
		return fmt.Errorf(
			"%w: ayah %s has mismatched %s Citable Unit Anchor %q",
			errReaderContract,
			ayahKey,
			role,
			unitAnchor,
		)
	}

	if _, exists := seenUnitIDs[unitID]; exists {
		return fmt.Errorf("%w: duplicate Citable Unit UUID %s", errReaderContract, unitID)
	}

	if _, exists := seenUnitAnchors[unitAnchor]; exists {
		return fmt.Errorf("%w: duplicate Citable Unit Anchor %s", errReaderContract, unitAnchor)
	}

	seenUnitIDs[unitID] = struct{}{}
	seenUnitAnchors[unitAnchor] = struct{}{}

	return nil
}

func validateCachePolicy(policy string, headers http.Header) error {
	if policy == cachePolicyNone {
		return nil
	}

	if headers.Get("Cache-Control") != revalidateCacheHeader {
		return fmt.Errorf(
			"%w: Cache-Control=%q, expected %q",
			errReaderContract,
			headers.Get("Cache-Control"),
			revalidateCacheHeader,
		)
	}

	if policy == cachePolicyCloudflare &&
		(headers.Get("X-Surau-Cache") != "BYPASS" || headers.Get("CF-Cache-Status") != "DYNAMIC") {
		return fmt.Errorf(
			"%w: Cloudflare cache policy is X-Surau-Cache=%q CF-Cache-Status=%q",
			errReaderContract,
			headers.Get("X-Surau-Cache"),
			headers.Get("CF-Cache-Status"),
		)
	}

	return nil
}

//nolint:mnd // These are frozen Quran Printing Complex page counts, not tunable thresholds.
func expectedRepresentativePageCount(page int) (int, bool) {
	switch page {
	case 1:
		return 7, true
	case 48:
		return 1, true
	case 421:
		return 8, true
	case 585:
		return 42, true
	case 604:
		return 15, true
	default:
		return 0, false
	}
}

//nolint:wsl_v5 // Report assembly keeps aggregation mutations next to their inputs.
func buildReport(started, finished time.Time, opts *options, version string, samples []sample) report {
	result := report{
		StartedAt:      started,
		FinishedAt:     finished,
		BaseURL:        opts.baseURL,
		Query:          opts.query.Encode(),
		Version:        version,
		Requests:       len(samples),
		Concurrency:    opts.concurrency,
		WarmupsPerPage: opts.warmupsPerPage,
		ByPage:         make(map[string]latencySummary),
		CacheStatuses:  make(map[string]int),
		ContentHashes:  make(map[string][]string),
		Samples:        samples,
	}
	durations := make([]float64, 0, len(samples))
	byPage := make(map[int][]float64)
	hashes := make(map[int]map[string]struct{})
	for index := range samples {
		item := &samples[index]
		if item.Error != "" {
			result.Errors++

			continue
		}
		durations = append(durations, item.DurationMS)
		byPage[item.Page] = append(byPage[item.Page], item.DurationMS)
		cacheKey := strings.TrimSpace(item.SurauCache + "/" + item.CFCacheStatus)
		result.CacheStatuses[cacheKey]++
		if hashes[item.Page] == nil {
			hashes[item.Page] = make(map[string]struct{})
		}
		hashes[item.Page][item.ResponseSHA256] = struct{}{}
	}
	result.Overall = summarize(durations)
	for page, values := range byPage {
		key := strconv.Itoa(page)
		result.ByPage[key] = summarize(values)
		pageHashes := make([]string, 0, len(hashes[page]))
		for hash := range hashes[page] {
			pageHashes = append(pageHashes, hash)
		}
		sort.Strings(pageHashes)

		result.ContentHashes[key] = pageHashes
	}

	return result
}

//nolint:wsl_v5 // Copy, sort, and summarize form one immutable operation.
func summarize(values []float64) latencySummary {
	if len(values) == 0 {
		return latencySummary{}
	}
	values = append([]float64(nil), values...)
	sort.Float64s(values)

	return latencySummary{
		Samples: len(values),
		P50MS:   percentile(values, percentileP50),
		P95MS:   percentile(values, percentileP95),
		MaxMS:   values[len(values)-1],
	}
}

func percentile(values []float64, percent int) float64 {
	index := max((len(values)*percent+percentileBase-1)/percentileBase, 1)

	return values[index-1]
}

//nolint:wsl_v5 // Encoding and destination selection form one output operation.
func writeReport(result *report, output string) error {
	payload, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("encode report: %w", err)
	}
	payload = append(payload, '\n')
	if output == "" {
		_, err = os.Stdout.Write(payload)

		return err
	}
	if err := os.WriteFile(output, payload, privateFileMode); err != nil {
		return fmt.Errorf("write report: %w", err)
	}

	return nil
}

func newRequestID() string {
	value := make([]byte, requestIDByteLength)
	if _, err := rand.Read(value); err != nil {
		return "quran-perf-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}

	return "quran-perf-" + hex.EncodeToString(value)
}

func milliseconds(value time.Duration) float64 {
	return float64(value) / float64(time.Millisecond)
}

func durationBetween(start, end time.Time) float64 {
	if start.IsZero() || end.IsZero() || end.Before(start) {
		return 0
	}

	return milliseconds(end.Sub(start))
}
