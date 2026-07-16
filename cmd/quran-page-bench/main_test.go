package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseOptionsEnforcesProductionBounds(t *testing.T) {
	t.Parallel()

	_, err := parseOptions([]string{"-base-url=https://api.example", "-concurrency=11"})
	require.ErrorContains(t, err, "concurrency")

	_, err = parseOptions([]string{
		"-base-url=https://api.example",
		"-requests=200",
		"-warmups-per-page=1",
	})
	require.ErrorContains(t, err, "must not exceed 200")

	_, err = parseOptions([]string{"-base-url=https://api.example", "-max-duration", "61s"})
	require.ErrorContains(t, err, "max-duration")
}

func TestRunMeasuresAndValidatesReaderContract(t *testing.T) {
	t.Parallel()

	server := newBenchmarkServer(t, "dev-abc1234", "permitted")
	defer server.Close()

	opts := testOptions(server.URL)
	result, err := run(context.Background(), http.DefaultTransport, &opts)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "dev-abc1234", result.Version)
	assert.Equal(t, 4, result.Requests)
	assert.Equal(t, 0, result.Errors)
	assert.Equal(t, 4, result.Overall.Samples)
	assert.Positive(t, result.Overall.P95MS)
	assert.Equal(t, 4, result.CacheStatuses["BYPASS/DYNAMIC"])
	assert.Len(t, result.ContentHashes["1"], 1)
	assert.Len(t, result.ContentHashes["48"], 1)
}

func TestRunRejectsNonPermittedPresentation(t *testing.T) {
	t.Parallel()

	server := newBenchmarkServer(t, "dev-abc1234", "restricted")
	defer server.Close()

	opts := testOptions(server.URL)
	result, err := run(context.Background(), http.DefaultTransport, &opts)
	require.Error(t, err)
	require.NotNil(t, result)
	assert.Positive(t, result.Errors)
	assert.Contains(t, result.Samples[0].Error, "license_status")
}

func TestRunRejectsVersionMismatchBeforeLoad(t *testing.T) {
	t.Parallel()

	server := newBenchmarkServer(t, "dev-wrong", "permitted")
	defer server.Close()

	opts := testOptions(server.URL)
	result, err := run(context.Background(), http.DefaultTransport, &opts)
	require.ErrorContains(t, err, "version mismatch")
	assert.Nil(t, result)
}

func TestRunMeasuresCompleteResponseBody(t *testing.T) {
	t.Parallel()

	const bodyDelay = 50 * time.Millisecond

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/version" {
			writeJSON(t, writer, map[string]string{"version": "dev-abc1234"})

			return
		}

		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("Cache-Control", revalidateCacheHeader)
		writer.Header().Set("CF-Cache-Status", "DYNAMIC")
		writer.Header().Set("X-Surau-Cache", "BYPASS")
		writer.WriteHeader(http.StatusOK)
		flusher, ok := writer.(http.Flusher)
		require.True(t, ok)
		flusher.Flush()
		time.Sleep(bodyDelay)
		require.NoError(t, json.NewEncoder(writer).Encode(validEnvelope(48, 1, "permitted")))
	}))
	defer server.Close()

	opts := testOptions(server.URL)
	opts.pages = []int{48}
	opts.requests = 1
	opts.concurrency = 1
	result, err := run(context.Background(), http.DefaultTransport, &opts)
	require.NoError(t, err)
	require.Len(t, result.Samples, 1)
	assert.GreaterOrEqual(t, result.Samples[0].DurationMS, milliseconds(bodyDelay))
	assert.Less(t, result.Samples[0].TTFBMS, result.Samples[0].DurationMS)
}

func TestValidatePageBodyRejectsMissingCitableIdentity(t *testing.T) {
	t.Parallel()

	body, err := json.Marshal(pageEnvelope{
		Items: []pageItem{{AyahKey: "48:1", PageNumber: new(48)}},
		Total: 1,
	})
	require.NoError(t, err)
	require.ErrorContains(t, validatePageBody(48, body), "Citable Unit identity")
}

func TestValidateCachePolicyRejectsUnexpectedCloudflareCaching(t *testing.T) {
	t.Parallel()

	headers := http.Header{
		"Cache-Control":   []string{revalidateCacheHeader},
		"X-Surau-Cache":   []string{"L1-HIT"},
		"Cf-Cache-Status": []string{"DYNAMIC"},
	}
	require.ErrorContains(t, validateCachePolicy(cachePolicyCloudflare, headers), "L1-HIT")
}

func TestValidatePageBodyRejectsMismatchedFootnoteParent(t *testing.T) {
	t.Parallel()

	payload := validEnvelope(48, 1, "permitted")
	payload.Items[0].Translation.FootnoteUnits = []pageFootnoteUnit{{
		UnitID:       "00000000-0000-0000-0002-000000004801",
		Anchor:       "quran/48:1/u/3",
		ParentUnitID: "00000000-0000-0000-0009-000000004801",
	}}
	body, err := json.Marshal(payload)
	require.NoError(t, err)
	require.ErrorContains(t, validatePageBody(48, body), "parent_unit_id")
}

func newBenchmarkServer(t *testing.T, version, licenseStatus string) *httptest.Server {
	t.Helper()

	return httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/version":
			writeJSON(t, writer, map[string]string{"version": version})
		case "/v1/quran/pages/1/ayahs":
			writer.Header().Set("Cache-Control", revalidateCacheHeader)
			writer.Header().Set("CF-Cache-Status", "DYNAMIC")
			writer.Header().Set("X-Surau-Cache", "BYPASS")
			writeJSON(t, writer, validEnvelope(1, 7, licenseStatus))
		case "/v1/quran/pages/48/ayahs":
			writer.Header().Set("Cache-Control", revalidateCacheHeader)
			writer.Header().Set("CF-Cache-Status", "DYNAMIC")
			writer.Header().Set("X-Surau-Cache", "BYPASS")
			writeJSON(t, writer, validEnvelope(48, 1, licenseStatus))
		default:
			http.NotFound(writer, request)
		}
	}))
}

func validEnvelope(page, count int, licenseStatus string) pageEnvelope {
	items := make([]pageItem, 0, count)
	for index := 1; index <= count; index++ {
		ayahKey := fmt.Sprintf("%d:%d", page, index)
		items = append(items, pageItem{
			AyahKey:           ayahKey,
			PageNumber:        new(page),
			PrimaryUnitID:     fmt.Sprintf("00000000-0000-0000-0000-%012d", page*100+index),
			PrimaryUnitAnchor: "quran/" + ayahKey + "/u/1",
			Translation: &pagePresentation{
				UnitID:        fmt.Sprintf("00000000-0000-0000-0001-%012d", page*100+index),
				Anchor:        "quran/" + ayahKey + "/u/2",
				LicenseStatus: licenseStatus,
			},
		})
	}

	return pageEnvelope{Items: items, Total: len(items)}
}

func writeJSON(t *testing.T, writer http.ResponseWriter, value any) {
	t.Helper()
	writer.Header().Set("Content-Type", "application/json")
	require.NoError(t, json.NewEncoder(writer).Encode(value))
}

func testOptions(baseURL string) options {
	return options{
		baseURL:         baseURL,
		pages:           []int{1, 48},
		requests:        4,
		concurrency:     2,
		requestTimeout:  time.Second,
		maxDuration:     5 * time.Second,
		expectedVersion: "dev-abc1234",
		cachePolicy:     cachePolicyCloudflare,
		query:           map[string][]string{"view": {"reader_minimal"}},
	}
}
