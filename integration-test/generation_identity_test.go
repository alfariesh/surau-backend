//nolint:wsl_v5 // SQL-heavy B-5/B-6 fixtures stay clearer when setup statements are grouped
package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/jackc/pgx/v5"
)

const (
	generationContractRunID       = "00000000-0000-4000-8000-00000000b601"
	generationContractUnitID      = "00000000-0000-4000-8000-00000000b602"
	generationContractReferenceID = "00000000-0000-4000-8000-00000000b603"
	generationContractLegacyRefID = "00000000-0000-4000-8000-00000000b604"
	generationContractV1RefID     = "00000000-0000-4000-8000-00000000b605"
	generationContractResolverID  = "00000000-0000-4000-8000-00000000b606"
	generationContractModelID     = "integration-model-v1"
	generationContractPrompt      = "integration-cross-reference-v1"
)

// TestGenerationIdentityEditorialContract proves the additive B-5/B-6 API
// contract against the real auth middleware, repository, and PostgreSQL
// guards. Machine output exposes one typed model+prompt+run tuple; legacy
// Quran rows remain explicit NULL instead of receiving invented provenance.
func TestGenerationIdentityEditorialContract(t *testing.T) {
	seedCrossReferenceFixture(t)
	t.Cleanup(func() { cleanupCrossReferenceFixture(t) })
	seedGenerationIdentityFixture(t)

	plainToken := roleUserToken(t, "user")
	editorToken := roleUserToken(t, "editor")
	adminToken := adminJWT(t)
	unitPath := "/v1/editorial/citable-units/" + generationContractUnitID

	t.Run("Citable Unit requires review capability", func(t *testing.T) {
		for _, actor := range []struct {
			name       string
			token      string
			wantStatus int
		}{
			{name: "anonymous", wantStatus: http.StatusUnauthorized},
			{name: "plain user", token: plainToken, wantStatus: http.StatusForbidden},
		} {
			t.Run(actor.name, func(t *testing.T) {
				resp := doJSON(t, http.MethodGet, baseURL()+unitPath, nil, actor.token)
				defer resp.Body.Close()

				if resp.StatusCode != actor.wantStatus {
					t.Fatalf("GET %s as %s = %d, want %d", unitPath, actor.name, resp.StatusCode, actor.wantStatus)
				}
			})
		}
	})

	t.Run("machine Citable Unit exposes typed generation", func(t *testing.T) {
		resp := doJSON(t, http.MethodGet, baseURL()+unitPath, nil, editorToken)
		var body struct {
			Unit struct {
				ID                   string                     `json:"id"`
				NormalizationVersion int                        `json:"normalization_version"`
				ProvenanceClass      string                     `json:"provenance_class"`
				Generation           *entity.GenerationIdentity `json:"generation"`
			} `json:"unit"`
			Successors []any `json:"successors"`
		}
		decodeAndClose(t, resp, &body)

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("editorial Citable Unit expected 200, got %d", resp.StatusCode)
		}
		if body.Unit.ID != generationContractUnitID || body.Unit.NormalizationVersion != 1 {
			t.Fatalf("Citable Unit identity/version = %+v", body.Unit)
		}
		if body.Unit.ProvenanceClass != entity.ProvenanceClassMachine {
			t.Fatalf("Citable Unit provenance = %q, want machine", body.Unit.ProvenanceClass)
		}
		assertGenerationIdentity(t, body.Unit.Generation)
		if len(body.Successors) != 0 {
			t.Fatalf("active Citable Unit successors = %d, want 0", len(body.Successors))
		}
	})

	t.Run("source Citable Unit has no generation", func(t *testing.T) {
		resp := doJSON(
			t,
			http.MethodGet,
			baseURL()+"/v1/editorial/citable-units/"+crossReferenceUnitA1ID,
			nil,
			editorToken,
		)
		var body struct {
			Unit struct {
				ProvenanceClass string                     `json:"provenance_class"`
				Generation      *entity.GenerationIdentity `json:"generation"`
			} `json:"unit"`
		}
		decodeAndClose(t, resp, &body)

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("editorial source Citable Unit expected 200, got %d", resp.StatusCode)
		}
		if body.Unit.ProvenanceClass != entity.ProvenanceClassSource || body.Unit.Generation != nil {
			t.Fatalf("source Citable Unit attribution = %+v", body.Unit)
		}
	})

	t.Run("missing Citable Unit returns typed 404", func(t *testing.T) {
		resp := doJSON(
			t,
			http.MethodGet,
			baseURL()+"/v1/editorial/citable-units/00000000-0000-4000-8000-00000000b699",
			nil,
			editorToken,
		)
		var body struct {
			Error string `json:"error"`
			Code  string `json:"code"`
		}
		decodeAndClose(t, resp, &body)

		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("missing Citable Unit expected 404, got %d", resp.StatusCode)
		}
		if body.Code != "citable_unit_not_found" {
			t.Fatalf("missing Citable Unit code = %q", body.Code)
		}
	})

	t.Run("malformed Citable Unit id returns typed 404", func(t *testing.T) {
		resp := doJSON(
			t,
			http.MethodGet,
			baseURL()+"/v1/editorial/citable-units/not-a-uuid",
			nil,
			editorToken,
		)
		var body struct {
			Code string `json:"code"`
		}
		decodeAndClose(t, resp, &body)

		if resp.StatusCode != http.StatusNotFound || body.Code != "citable_unit_not_found" {
			t.Fatalf("malformed Citable Unit response = %d/%q", resp.StatusCode, body.Code)
		}
	})

	t.Run("machine Cross-Reference get exposes typed generation", func(t *testing.T) {
		resp := doJSON(
			t,
			http.MethodGet,
			baseURL()+"/v1/editorial/cross-references/"+generationContractReferenceID,
			nil,
			adminToken,
		)
		var ref entity.CrossReference
		decodeAndClose(t, resp, &ref)

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("editorial machine Cross-Reference expected 200, got %d", resp.StatusCode)
		}
		assertMachineCrossReferenceGeneration(t, &ref)
	})

	t.Run("resolver Cross-Reference has no generation", func(t *testing.T) {
		resp := doJSON(
			t,
			http.MethodGet,
			baseURL()+"/v1/editorial/cross-references/"+generationContractResolverID,
			nil,
			adminToken,
		)
		var ref entity.CrossReference
		decodeAndClose(t, resp, &ref)

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("editorial resolver Cross-Reference expected 200, got %d", resp.StatusCode)
		}
		if ref.Method != entity.CrossReferenceMethodResolver || ref.Generation != nil {
			t.Fatalf("resolver Cross-Reference attribution = %+v", ref)
		}
		if ref.MethodDetail.Strategy != "integration_fixture" {
			t.Fatalf("resolver method_detail = %+v", ref.MethodDetail)
		}
	})

	t.Run("machine Cross-Reference list exposes typed generation", func(t *testing.T) {
		requestURL := baseURL() + "/v1/editorial/cross-references?method=machine&limit=200"
		resp := doJSON(t, http.MethodGet, requestURL, nil, adminToken)
		var result entity.CrossReferenceList
		decodeAndClose(t, resp, &result)

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("editorial machine Cross-Reference list expected 200, got %d", resp.StatusCode)
		}
		for _, ref := range result.Items {
			if ref.ID == generationContractReferenceID {
				assertMachineCrossReferenceGeneration(t, &ref)

				return
			}
		}
		t.Fatalf("machine Cross-Reference %s missing from %+v", generationContractReferenceID, result.Items)
	})

	t.Run("legacy Quran normalization version remains explicit null", func(t *testing.T) {
		path := fmt.Sprintf(
			"/v1/books/%d/quran-references?lang=ar&limit=200&offset=0",
			crossReferenceBookAID,
		)
		resp := doJSON(t, http.MethodGet, baseURL()+path, nil, "")
		var result struct {
			Items []map[string]json.RawMessage `json:"items"`
			Total int                          `json:"total"`
		}
		decodeAndClose(t, resp, &result)

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("legacy Quran reference list expected 200, got %d", resp.StatusCode)
		}
		if result.Total != 2 {
			t.Fatalf("legacy Quran reference total = %d, want 2", result.Total)
		}
		assertQuranReferenceNormalizationJSON(t, result.Items, generationContractLegacyRefID, "null")
		assertQuranReferenceNormalizationJSON(t, result.Items, generationContractV1RefID, "1")
	})
}

func seedGenerationIdentityFixture(t *testing.T) {
	t.Helper()

	pool := integrationDB(t)
	defer pool.Close()
	ctx, cancel := context.WithTimeout(t.Context(), requestTimeout)
	defer cancel()

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin generation identity fixture: %v", err)
	}
	defer tx.Rollback(ctx)

	execFixtureSQL(t, ctx, tx, `SET LOCAL surau.registry_writer = 'unit-service'`)
	execFixtureSQL(t, ctx, tx, `SET LOCAL surau.cross_reference_writer = 'cross-reference-service'`)
	execFixtureSQL(t, ctx, tx, `
INSERT INTO generation_runs (id, task_name, model_id, prompt_version, metadata)
VALUES ($1::uuid, 'integration-generation-contract', $2, $3, '{"integration_fixture":"b6"}'::jsonb)
ON CONFLICT (id) DO NOTHING`, generationContractRunID, generationContractModelID, generationContractPrompt)

	var descriptorMatches bool
	if err := tx.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1 FROM generation_runs
    WHERE id = $1::uuid AND model_id = $2 AND prompt_version = $3
)`, generationContractRunID, generationContractModelID, generationContractPrompt).Scan(&descriptorMatches); err != nil {
		t.Fatalf("verify generation descriptor: %v", err)
	}
	if !descriptorMatches {
		t.Fatal("B-6 integration generation descriptor conflict")
	}

	unitAnchor := generationContractUnitAnchor()
	execFixtureSQL(t, ctx, tx, `
INSERT INTO citable_units (
    id, corpus, book_id, heading_id, page_id, kind, ordinal, position, anchor,
    text, text_normalized, normalization_version, content_hash, occurrence,
    language, provenance_class, generation_run_id, lifecycle, updated_at
) VALUES (
    $1::uuid, 'kitab', $2, 1, 1, 'paragraph', 601, 600, $3,
    'B-6 machine Citable Unit', 'b-6 machine citable unit', 1,
    decode(md5($1::text), 'hex'), 1, 'ar', 'machine', $4::uuid, 'active',
    '2026-07-11T00:00:00Z'
)`, generationContractUnitID, crossReferenceBookAID, unitAnchor, generationContractRunID)

	execFixtureSQL(
		t, ctx, tx, `
INSERT INTO cross_references (
    id, source_anchor, target_anchor, source_corpus, target_corpus,
    source_work_id, target_work_id, kind, method, method_detail,
    generation_run_id, confidence, review_status, evidence_text,
    evidence_normalized, normalization_version, origin, origin_key, metadata,
    created_at, updated_at
) VALUES (
    $1::uuid, $2, $3, 'kitab', 'kitab', $4, $5, 'parallel', 'machine',
    jsonb_build_object('model_id', $6::text, 'prompt_version', $7::text, 'run_id', $8::text),
    $8::uuid, 0.8750, 'pending', 'B-6 machine evidence', 'b-6 machine evidence',
    1, 'machine', $1::text, '{"integration_fixture":"b6"}'::jsonb,
    '2026-07-11T00:00:01Z', '2026-07-11T00:00:01Z'
)`,
		generationContractReferenceID,
		unitAnchor,
		crossReferenceUnitAnchor(crossReferenceBookBID, 1),
		crossReferenceBookAID,
		crossReferenceBookBID,
		generationContractModelID,
		generationContractPrompt,
		generationContractRunID,
	)
	execFixtureSQL(
		t, ctx, tx, `
INSERT INTO cross_references (
    id, source_anchor, target_anchor, source_corpus, target_corpus,
    source_work_id, target_work_id, kind, method, method_detail,
    confidence, review_status, evidence_text, evidence_normalized,
    normalization_version, origin, origin_key, metadata, created_at, updated_at
) VALUES (
    $1::uuid, $2, $3, 'kitab', 'kitab', $4, $5, 'cites', 'resolver',
    '{"strategy":"integration_fixture"}'::jsonb, 1.0, 'approved',
    'B-6 deterministic resolver evidence', 'b-6 deterministic resolver evidence',
    1, 'resolver', $1::text, '{"integration_fixture":"b6-resolver"}'::jsonb,
    '2026-07-11T00:00:02Z', '2026-07-11T00:00:02Z'
)`,
		generationContractResolverID,
		crossReferenceUnitAnchor(crossReferenceBookAID, 1),
		crossReferenceUnitAnchor(crossReferenceBookBID, 2),
		crossReferenceBookAID,
		crossReferenceBookBID,
	)

	insertGenerationQuranReference(t, ctx, tx, generationContractV1RefID, true)

	// A fresh writer cannot create NULL-version text. Temporarily disabling
	// only this trigger reproduces a pre-B-5 row inside the otherwise current
	// schema, then restores enforcement before commit.
	execFixtureSQL(t, ctx, tx, `
ALTER TABLE quran_book_references
    DISABLE TRIGGER trg_quran_book_references_normalization_version`)
	insertGenerationQuranReference(t, ctx, tx, generationContractLegacyRefID, false)
	execFixtureSQL(t, ctx, tx, `
ALTER TABLE quran_book_references
    ENABLE TRIGGER trg_quran_book_references_normalization_version`)

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit generation identity fixture: %v", err)
	}
}

//nolint:revive // test helpers conventionally keep testing.T before context
func insertGenerationQuranReference(
	t *testing.T,
	ctx context.Context,
	tx pgx.Tx,
	id string,
	versioned bool,
) {
	t.Helper()

	var normalizationVersion any
	if versioned {
		normalizationVersion = 1
	}

	execFixtureSQL(t, ctx, tx, `
INSERT INTO quran_book_references (
    id, book_id, page_id, heading_id, source_text, normalized_text,
    normalization_version, reference_kind, surah_id, from_ayah_number,
    to_ayah_number, from_ayah_key, to_ayah_key, match_strategy, confidence,
    review_status, metadata, created_at, updated_at
) VALUES (
    $1::uuid, $2, 1, 1, 'B-5 normalization API ' || $1::text,
    'b-5 normalization api ' || $1::text, $3::integer, 'surah_ayah', $4::integer,
    $5::integer, $5::integer, ($4::integer)::text || ':' || ($5::integer)::text,
    ($4::integer)::text || ':' || ($5::integer)::text,
    'integration_fixture', 1.0, 'approved',
    jsonb_build_object('integration_fixture', 'b5-normalization-version'),
    '2026-07-11T00:00:02Z', '2026-07-11T00:00:02Z'
)`, id, crossReferenceBookAID, normalizationVersion, crossReferenceSurahID, crossReferenceAyahFrom)
}

func generationContractUnitAnchor() string {
	return fmt.Sprintf("kitab/%d/h/1/u/601", crossReferenceBookAID)
}

func assertGenerationIdentity(t *testing.T, identity *entity.GenerationIdentity) {
	t.Helper()

	if identity == nil {
		t.Fatal("generation identity is missing")
	}
	if identity.RunID != generationContractRunID ||
		identity.ModelID != generationContractModelID ||
		identity.PromptVersion != generationContractPrompt {
		t.Fatalf("generation identity = %+v", identity)
	}
}

func assertMachineCrossReferenceGeneration(t *testing.T, ref *entity.CrossReference) {
	t.Helper()

	if ref.Method != entity.CrossReferenceMethodMachine {
		t.Fatalf("Cross-Reference method = %q, want machine", ref.Method)
	}
	assertGenerationIdentity(t, ref.Generation)
	if ref.MethodDetail.RunID != generationContractRunID ||
		ref.MethodDetail.ModelID != generationContractModelID ||
		ref.MethodDetail.PromptVersion != generationContractPrompt {
		t.Fatalf("legacy method_detail generation tuple = %+v", ref.MethodDetail)
	}
}

func assertQuranReferenceNormalizationJSON(
	t *testing.T,
	items []map[string]json.RawMessage,
	id string,
	want string,
) {
	t.Helper()

	for _, item := range items {
		var gotID string
		if err := json.Unmarshal(item["id"], &gotID); err != nil {
			t.Fatalf("decode Quran reference id: %v", err)
		}
		if gotID != id {
			continue
		}

		raw, exists := item["normalization_version"]
		if !exists {
			t.Fatalf("Quran reference %s omitted normalization_version", id)
		}
		if string(raw) != want {
			t.Fatalf("Quran reference %s normalization_version = %s, want %s", id, raw, want)
		}

		return
	}

	t.Fatalf("Quran reference %s missing from response", id)
}
