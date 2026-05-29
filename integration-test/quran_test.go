package integration_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"
)

const (
	fixtureQuranSurahID      = 114
	fixtureQuranAyahNumber   = 1
	fixtureQuranAyahKey      = "114:1"
	fixtureQuranSourceID     = "integration-id-source"
	fixtureQuranRecitationID = "integration-recitation"
	fixtureQuranReferenceID  = "00000000-0000-0000-0000-000000000991"
)

func TestQuranMultilingualContract(t *testing.T) {
	seedMultilingualQuranFixture(t)

	resp := doJSON(t, http.MethodGet, baseURL()+"/v1/quran/ayahs/114:1?lang=fr", nil, "")
	var errorBody struct {
		Error string `json:"error"`
	}
	decodeAndClose(t, resp, &errorBody)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unsupported language expected 400, got %d", resp.StatusCode)
	}
	if errorBody.Error != "unsupported language" {
		t.Fatalf("unsupported language error = %q", errorBody.Error)
	}

	idSurah := getQuranSurah(t, "id")
	if idSurah.Info == nil || idSurah.Info.Lang != "id" {
		t.Fatalf("id surah info = %+v", idSurah.Info)
	}
	assertLocalization(t, idSurah.Localization, "id", "id", false)

	enSurah := getQuranSurah(t, "en")
	if enSurah.Info != nil {
		t.Fatalf("en missing surah info should be null, got %+v", enSurah.Info)
	}
	assertLocalization(t, enSurah.Localization, "en", "ar", true)
	assertHasLang(t, enSurah.Localization.AvailableLangs, "id")
	assertAvailability(t, enSurah.Localization.Availability, "offer_available_lang", "en", "ar", true)

	idSources := getQuranTranslationSources(t, "id")
	if len(idSources) != 1 {
		t.Fatalf("id translation sources = %+v", idSources)
	}
	if idSources[0].ID != fixtureQuranSourceID || idSources[0].Coverage.TranslatedAyahs != 1 || idSources[0].Coverage.TotalAyahs != 1 {
		t.Fatalf("id translation source = %+v", idSources[0])
	}
	if !idSources[0].IsDefault {
		t.Fatalf("id translation source should be default: %+v", idSources[0])
	}

	enSources := getQuranTranslationSources(t, "en")
	if len(enSources) != 0 {
		t.Fatalf("en translation sources should be empty, got %+v", enSources)
	}

	idAyah := getQuranAyah(t, "id")
	if idAyah.Translation == nil || idAyah.Translation.Text != "Terjemah fixture Indonesia" {
		t.Fatalf("id ayah translation = %+v", idAyah.Translation)
	}
	if idAyah.TranslationMissing {
		t.Fatal("id ayah exact translation should not be missing")
	}
	assertAvailability(t, idAyah.Availability.Translation, "show_requested", "id", "id", false)

	enAyah := getQuranAyah(t, "en")
	if enAyah.Translation != nil {
		t.Fatalf("en missing translation should be null, got %+v", enAyah.Translation)
	}
	if !enAyah.TranslationMissing {
		t.Fatal("en missing translation should set translation_missing=true")
	}
	assertHasLang(t, enAyah.AvailableTranslationLangs, "id")
	assertAvailability(t, enAyah.Availability.Translation, "offer_available_lang", "en", "ar", true)

	arAyah := getQuranAyah(t, "ar")
	if arAyah.Translation != nil {
		t.Fatalf("ar ayah should suppress translation, got %+v", arAyah.Translation)
	}
	if arAyah.TranslationMissing {
		t.Fatal("ar ayah should not be marked translation_missing")
	}
	assertAvailability(t, arAyah.Availability.Translation, "hide_translation_tab", "ar", "ar", false)

	resp = doJSON(t, http.MethodGet, baseURL()+"/v1/quran/ayahs/114:1?lang=id&translation_source=missing-source", nil, "")
	decodeAndClose(t, resp, &errorBody)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing translation source expected 404, got %d", resp.StatusCode)
	}
	if errorBody.Error != "quran translation source not found" {
		t.Fatalf("missing translation source error = %q", errorBody.Error)
	}

	search := searchQuran(t, "Terjemah fixture", "en")
	if search.Total != 1 || len(search.Results) != 1 {
		t.Fatalf("quran search result = %+v", search)
	}
	result := search.Results[0]
	if result.MatchedLang != "id" || result.MatchedSourceID != fixtureQuranSourceID || result.MatchedField != "translation" {
		t.Fatalf("quran search match metadata = %+v", result)
	}
	if result.Ayah.Translation != nil {
		t.Fatalf("quran search lang=en display translation should be null, got %+v", result.Ayah.Translation)
	}
	assertAvailability(t, result.Ayah.Availability.Translation, "offer_available_lang", "en", "ar", true)

	refs := getBookQuranReferences(t, "en")
	if refs.Total != 1 || len(refs.References) != 1 || len(refs.References[0].Ayahs) != 1 {
		t.Fatalf("book quran references = %+v", refs)
	}
	refAyah := refs.References[0].Ayahs[0]
	if refAyah.Translation != nil || !refAyah.TranslationMissing {
		t.Fatalf("book quran reference ayah lang=en = %+v", refAyah)
	}
	assertAvailability(t, refAyah.Availability.Translation, "offer_available_lang", "en", "ar", true)

	resp = doJSON(t, http.MethodGet, baseURL()+"/v1/admin/quran/missing-assets?target_lang=en", nil, "")
	decodeAndClose(t, resp, &errorBody)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("admin quran missing queue without auth expected 401, got %d", resp.StatusCode)
	}

	adminToken := adminJWT(t)
	resp = doJSON(t, http.MethodGet, baseURL()+"/v1/admin/quran/missing-assets?target_lang=en", nil, adminToken)
	var allMissing missingQuranAssetsResponse
	decodeAndClose(t, resp, &allMissing)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin quran missing assets expected 200, got %d", resp.StatusCode)
	}
	if allMissing.Total != 4 {
		t.Fatalf("admin quran missing total = %d items %+v", allMissing.Total, allMissing.Items)
	}
	for _, assetType := range []string{"audio_public", "ayah_translation", "surah_info", "translation_source"} {
		assertMissingCount(t, allMissing.Counts, assetType, "en", 1)
	}

	resp = doJSON(t, http.MethodGet, baseURL()+"/v1/admin/quran/missing-assets?target_lang=en&asset_type=ayah_translation&surah_id=114", nil, adminToken)
	var missingTranslations missingQuranAssetsResponse
	decodeAndClose(t, resp, &missingTranslations)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("admin quran missing ayah translations expected 200, got %d", resp.StatusCode)
	}
	if missingTranslations.Total != 1 || len(missingTranslations.Items) != 1 {
		t.Fatalf("admin quran missing ayah translations = %+v", missingTranslations)
	}
	missingItem := missingTranslations.Items[0]
	if missingItem.AssetType != "ayah_translation" || missingItem.TargetLang != "en" {
		t.Fatalf("admin quran missing item type/lang = %+v", missingItem)
	}
	if missingItem.SurahID == nil || *missingItem.SurahID != fixtureQuranSurahID ||
		missingItem.AyahKey == nil || *missingItem.AyahKey != fixtureQuranAyahKey {
		t.Fatalf("admin quran missing item ids = %+v", missingItem)
	}
	assertHasLang(t, missingItem.AvailableLangs, "id")

	resp = doJSON(t, http.MethodGet, baseURL()+"/v1/admin/quran/missing-assets?target_lang=ar", nil, adminToken)
	decodeAndClose(t, resp, &errorBody)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("admin quran missing target_lang=ar expected 400, got %d", resp.StatusCode)
	}
	if errorBody.Error != "unsupported language" {
		t.Fatalf("admin quran missing target_lang=ar error = %q", errorBody.Error)
	}

	resp = doJSON(t, http.MethodGet, baseURL()+"/v1/admin/quran/missing-assets?target_lang=en&asset_type=metadata", nil, adminToken)
	decodeAndClose(t, resp, &errorBody)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("admin quran missing invalid asset_type expected 400, got %d", resp.StatusCode)
	}
	if errorBody.Error != "invalid asset type" {
		t.Fatalf("admin quran missing invalid asset_type error = %q", errorBody.Error)
	}
}

func seedMultilingualQuranFixture(t *testing.T) {
	t.Helper()

	seedMultilingualKitabFixture(t)

	pool := integrationDB(t)
	defer pool.Close()

	ctx, cancel := context.WithTimeout(t.Context(), requestTimeout)
	defer cancel()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin quran fixture tx: %v", err)
	}
	defer tx.Rollback(ctx)

	execFixtureSQL(t, ctx, tx, `DELETE FROM quran_book_references WHERE id = $1`, fixtureQuranReferenceID)
	execFixtureSQL(t, ctx, tx, `DELETE FROM quran_audio_segments WHERE recitation_id = $1`, fixtureQuranRecitationID)
	execFixtureSQL(t, ctx, tx, `DELETE FROM quran_audio_tracks WHERE recitation_id = $1`, fixtureQuranRecitationID)
	execFixtureSQL(t, ctx, tx, `DELETE FROM quran_recitations WHERE id = $1`, fixtureQuranRecitationID)
	execFixtureSQL(t, ctx, tx, `DELETE FROM quran_ayah_translations WHERE source_id = $1`, fixtureQuranSourceID)
	execFixtureSQL(t, ctx, tx, `DELETE FROM quran_translation_sources WHERE id = $1`, fixtureQuranSourceID)
	execFixtureSQL(t, ctx, tx, `DELETE FROM quran_surah_infos WHERE surah_id = $1`, fixtureQuranSurahID)
	execFixtureSQL(t, ctx, tx, `DELETE FROM quran_ayahs WHERE surah_id = $1`, fixtureQuranSurahID)
	execFixtureSQL(t, ctx, tx, `DELETE FROM quran_surahs WHERE surah_id = $1`, fixtureQuranSurahID)

	execFixtureSQL(t, ctx, tx, `
INSERT INTO quran_surahs (surah_id, name_arabic, name_latin, name_translation, revelation_type, ayah_count, metadata)
VALUES ($1, 'الناس', 'An-Nas Fixture', 'Manusia Fixture', 'makkiyah', 1, '{}'::jsonb)`,
		fixtureQuranSurahID,
	)
	execFixtureSQL(t, ctx, tx, `
INSERT INTO quran_surah_infos (
    surah_id, lang, surah_name, text_html, short_text, source_name, source_url,
    qul_resource_id, format, license_status, checksum, metadata, imported_at
)
VALUES ($1, 'id', 'An-Nas Fixture', '<p>Info fixture Indonesia</p>', 'Info pendek fixture',
        'integration-test', 'https://example.test/quran-info', 'fixture-info',
        'html', 'permitted', 'fixture-info-checksum', '{}'::jsonb, now())`,
		fixtureQuranSurahID,
	)
	execFixtureSQL(t, ctx, tx, `
INSERT INTO quran_ayahs (
    surah_id, ayah_number, ayah_key, text_qpc_hafs, text_imlaei_simple, search_text,
    script_type, font_family, page_number, juz_number, hizb_number, metadata
)
VALUES ($1, $2, $3, 'قُلْ أَعُوذُ بِرَبِّ النَّاسِ', 'قل أعوذ برب الناس',
        'قل اعوذ برب الناس', 'qpc_hafs', 'qpc', 604, 30, 60, '{}'::jsonb)`,
		fixtureQuranSurahID,
		fixtureQuranAyahNumber,
		fixtureQuranAyahKey,
	)
	execFixtureSQL(t, ctx, tx, `
INSERT INTO quran_translation_sources (
    id, lang, name, translator, source_url, qul_resource_id, format,
    license_status, checksum, metadata, imported_at
)
VALUES ($1, 'id', 'Fixture Indonesian Source', 'Translator Fixture',
        'https://example.test/quran-translation', 'fixture-source', 'json',
        'permitted', 'fixture-source-checksum', '{}'::jsonb, now())`,
		fixtureQuranSourceID,
	)
	execFixtureSQL(t, ctx, tx, `
INSERT INTO quran_ayah_translations (
    source_id, surah_id, ayah_number, ayah_key, lang, text, footnotes, chunks, metadata
)
VALUES ($1, $2, $3, $4, 'id', 'Terjemah fixture Indonesia', '[]'::jsonb, '[]'::jsonb, '{}'::jsonb)`,
		fixtureQuranSourceID,
		fixtureQuranSurahID,
		fixtureQuranAyahNumber,
		fixtureQuranAyahKey,
	)
	execFixtureSQL(t, ctx, tx, `
INSERT INTO quran_recitations (
    id, name, reciter_name, style, mode, source_url, qul_resource_id,
    format, license_status, checksum, metadata, imported_at
)
VALUES ($1, 'Fixture Recitation', 'Reciter Fixture', 'murattal', 'ayah',
        'https://example.test/quran-audio', 'fixture-recitation', 'json',
        'permitted', 'fixture-recitation-checksum', '{}'::jsonb, now())`,
		fixtureQuranRecitationID,
	)
	execFixtureSQL(t, ctx, tx, `
INSERT INTO quran_audio_tracks (
    recitation_id, track_type, track_key, surah_id, ayah_number, audio_url,
    r2_key, public_url, duration_ms, duration_seconds, mime_type, metadata
)
VALUES ($1, 'ayah', $2, $3, $4, 'https://example.test/source-audio.mp3',
        'quran/fixture.mp3', NULL, 3000, 3, 'audio/mpeg', '{}'::jsonb)`,
		fixtureQuranRecitationID,
		fixtureQuranAyahKey,
		fixtureQuranSurahID,
		fixtureQuranAyahNumber,
	)
	execFixtureSQL(t, ctx, tx, `
INSERT INTO quran_book_references (
    id, book_id, page_id, heading_id, source_text, normalized_text, reference_kind,
    surah_id, from_ayah_number, to_ayah_number, from_ayah_key, to_ayah_key,
    match_strategy, confidence, review_status, metadata
)
VALUES ($1, $2, 1, $3, 'QS. An-Nas:1', 'qs an nas 1', 'surah_ayah',
        $4, $5, $5, $6, $6, 'integration_fixture', 1.0, 'approved', '{}'::jsonb)`,
		fixtureQuranReferenceID,
		fixtureBookID,
		fixtureHeadingID,
		fixtureQuranSurahID,
		fixtureQuranAyahNumber,
		fixtureQuranAyahKey,
	)

	if err = tx.Commit(ctx); err != nil {
		t.Fatalf("commit quran fixture tx: %v", err)
	}
}

type quranSurahResponse struct {
	SurahID      int             `json:"surah_id"`
	Info         *quranSurahInfo `json:"info"`
	Localization localization    `json:"localization"`
}

type quranSurahInfo struct {
	Lang     string `json:"lang"`
	TextHTML string `json:"text_html"`
}

type quranTranslationSourceResponse struct {
	ID        string                   `json:"id"`
	Lang      string                   `json:"lang"`
	Coverage  quranTranslationCoverage `json:"coverage"`
	IsDefault bool                     `json:"is_default"`
}

type quranTranslationCoverage struct {
	TranslatedAyahs int     `json:"translated_ayahs"`
	TotalAyahs      int     `json:"total_ayahs"`
	Percent         float64 `json:"percent"`
}

type quranAyahResponse struct {
	SurahID                   int                       `json:"surah_id"`
	AyahNumber                int                       `json:"ayah_number"`
	AyahKey                   string                    `json:"ayah_key"`
	Translation               *quranTranslationResponse `json:"translation"`
	RequestedLang             string                    `json:"requested_lang"`
	AvailableTranslationLangs []string                  `json:"available_translation_langs"`
	TranslationMissing        bool                      `json:"translation_missing"`
	Availability              quranAyahAvailability     `json:"availability"`
}

type quranTranslationResponse struct {
	SourceID string `json:"source_id"`
	Lang     string `json:"lang"`
	Text     string `json:"text"`
}

type quranAyahAvailability struct {
	Translation availabilityDecision `json:"translation"`
	Audio       availabilityDecision `json:"audio"`
}

type quranSearchResponse struct {
	Results []quranSearchResult `json:"results"`
	Total   int                 `json:"total"`
}

type quranSearchResult struct {
	Ayah            quranAyahResponse `json:"ayah"`
	MatchedLang     string            `json:"matched_lang"`
	MatchedSourceID string            `json:"matched_source_id"`
	MatchedField    string            `json:"matched_field"`
}

type bookQuranReferencesResponse struct {
	References []bookQuranReferenceResponse `json:"references"`
	Total      int                          `json:"total"`
}

type bookQuranReferenceResponse struct {
	ID    string              `json:"id"`
	Ayahs []quranAyahResponse `json:"ayahs"`
}

type missingQuranAssetsResponse struct {
	Items  []missingQuranAssetItem `json:"items"`
	Total  int                     `json:"total"`
	Counts []missingAssetCount     `json:"counts"`
}

type missingQuranAssetItem struct {
	AssetType       string    `json:"asset_type"`
	TargetLang      string    `json:"target_lang"`
	SurahID         *int      `json:"surah_id"`
	SurahName       *string   `json:"surah_name"`
	AyahNumber      *int      `json:"ayah_number"`
	AyahKey         *string   `json:"ayah_key"`
	AvailableLangs  []string  `json:"available_langs"`
	SourceUpdatedAt time.Time `json:"source_updated_at"`
}

func getQuranSurah(t *testing.T, lang string) quranSurahResponse {
	t.Helper()

	resp := doJSON(t, http.MethodGet, fmt.Sprintf("%s/v1/quran/surahs/%d?lang=%s", baseURL(), fixtureQuranSurahID, lang), nil, "")
	var surah quranSurahResponse
	decodeAndClose(t, resp, &surah)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get quran surah %s expected 200, got %d", lang, resp.StatusCode)
	}

	return surah
}

func getQuranTranslationSources(t *testing.T, lang string) []quranTranslationSourceResponse {
	t.Helper()

	resp := doJSON(t, http.MethodGet, fmt.Sprintf("%s/v1/quran/translation-sources?lang=%s", baseURL(), lang), nil, "")
	var sources []quranTranslationSourceResponse
	decodeAndClose(t, resp, &sources)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get quran translation sources %s expected 200, got %d", lang, resp.StatusCode)
	}

	return sources
}

func getQuranAyah(t *testing.T, lang string) quranAyahResponse {
	t.Helper()

	resp := doJSON(t, http.MethodGet, fmt.Sprintf("%s/v1/quran/ayahs/%s?lang=%s", baseURL(), fixtureQuranAyahKey, lang), nil, "")
	var ayah quranAyahResponse
	decodeAndClose(t, resp, &ayah)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get quran ayah %s expected 200, got %d", lang, resp.StatusCode)
	}

	return ayah
}

func searchQuran(t *testing.T, query, lang string) quranSearchResponse {
	t.Helper()

	resp := doJSON(t, http.MethodGet, fmt.Sprintf(
		"%s/v1/quran/search?q=%s&lang=%s",
		baseURL(),
		url.QueryEscape(query),
		lang,
	), nil, "")
	var results quranSearchResponse
	decodeAndClose(t, resp, &results)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("search quran expected 200, got %d", resp.StatusCode)
	}

	return results
}

func getBookQuranReferences(t *testing.T, lang string) bookQuranReferencesResponse {
	t.Helper()

	resp := doJSON(t, http.MethodGet, fmt.Sprintf("%s/v1/books/%d/quran-references?lang=%s", baseURL(), fixtureBookID, lang), nil, "")
	var refs bookQuranReferencesResponse
	decodeAndClose(t, resp, &refs)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("book quran references expected 200, got %d", resp.StatusCode)
	}

	return refs
}
