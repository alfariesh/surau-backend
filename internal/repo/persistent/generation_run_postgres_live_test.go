package persistent

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/pkg/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

//nolint:paralleltest // serial: intentionally replays DDL on the shared migrated schema
func TestLiveGenerationRunMigrationReplayIsIdempotent(t *testing.T) {
	url := os.Getenv("SURAU_LIVE_PG")
	if url == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pg, err := postgres.New(url)
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	up, err := os.ReadFile("../../../migrations/20260711000002_add_generation_runs.up.sql")
	require.NoError(t, err)

	for range 2 {
		_, err = pg.Pool.Exec(context.Background(), string(up))
		require.NoError(t, err)
	}
}

//nolint:maintidx,paralleltest,wsl_v5 // serial: exercises a complete pre-B-6 upgrade inside a rolled-back transaction
func TestLiveGenerationRunMigrationUpgradesLegacyRows(t *testing.T) {
	url := os.Getenv("SURAU_LIVE_PG")
	if url == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pg, err := postgres.New(url)
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	var k1Installed bool
	require.NoError(t, pg.Pool.QueryRow(context.Background(), `
SELECT EXISTS (
    SELECT 1 FROM information_schema.columns
    WHERE table_name = 'citable_units' AND column_name = 'content_role'
)`).Scan(&k1Installed))
	if k1Installed {
		t.Skip("isolated B-6 down replay must run before newer K-1 dependent indexes/views")
	}

	down, err := os.ReadFile("../../../migrations/20260711000002_add_generation_runs.down.sql")
	require.NoError(t, err)
	up, err := os.ReadFile("../../../migrations/20260711000002_add_generation_runs.up.sql")
	require.NoError(t, err)

	ctx := context.Background()
	tx, err := pg.Pool.Begin(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })

	_, err = tx.Exec(ctx, string(down))
	require.NoError(t, err)

	legacyExtractionID := uuid.NewString()
	_, err = tx.Exec(ctx, `
INSERT INTO knowledge_extraction_runs (
    id, task_name, prompt_version, model_id, provider, parameters, source_scope
) VALUES ($1, 'mentions', 'legacy-prompt-v3', 'legacy-model', 'openai', '{"temperature":0}', '{"book_ids":[1]}')`,
		legacyExtractionID)
	require.NoError(t, err)

	const legacyV7RunID = "01890f47-6a4b-7ccd-8ef0-123456789abc"

	legacyCrossReferenceID := uuid.NewString()
	_, err = tx.Exec(ctx, `SET LOCAL surau.cross_reference_writer = 'cross-reference-service'`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
INSERT INTO cross_references (
    id, source_anchor, target_anchor, source_corpus, target_corpus, kind, method,
    method_detail, confidence, evidence_text, evidence_normalized,
    normalization_version, origin, origin_key
) VALUES (
    $1::uuid, 'entity/legacy-source', 'entity/legacy-target', 'entity', 'entity', 'cites', 'machine',
    jsonb_build_object(
        'model_id', 'legacy-cross-model',
        'prompt_version', 'legacy-cross-v1',
        'run_id', $2::text
    ),
    1, 'legacy machine evidence', 'legacy machine evidence', 1, 'machine', $1::text
)`, legacyCrossReferenceID, strings.ToUpper(legacyV7RunID))
	require.NoError(t, err)

	legacyCategoryID := -int(time.Now().UnixNano()%1_000_000_000) - 1
	_, err = tx.Exec(ctx, `INSERT INTO categories (id, name) VALUES ($1, 'legacy-category')`, legacyCategoryID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
INSERT INTO category_translations (category_id, lang, name)
VALUES ($1, 'id', 'legacy translation')`, legacyCategoryID)
	require.NoError(t, err)

	_, err = tx.Exec(ctx, string(up))
	require.NoError(t, err)

	var extractionTask, extractionModel, extractionPrompt string

	err = tx.QueryRow(ctx, `
SELECT task_name, model_id, prompt_version
FROM generation_runs WHERE id = $1`, legacyExtractionID).
		Scan(&extractionTask, &extractionModel, &extractionPrompt)
	require.NoError(t, err)
	assert.Equal(t, "mentions", extractionTask)
	assert.Equal(t, "legacy-model", extractionModel)
	assert.Equal(t, "legacy-prompt-v3", extractionPrompt)

	var (
		crossRunID    string
		crossTask     string
		crossModel    string
		crossPrompt   string
		legacyClass   string
		legacyRunNull bool
	)

	err = tx.QueryRow(ctx, `
SELECT cr.generation_run_id::text, gr.task_name, gr.model_id, gr.prompt_version
FROM cross_references cr
JOIN generation_runs gr ON gr.id = cr.generation_run_id
WHERE cr.id = $1`, legacyCrossReferenceID).
		Scan(&crossRunID, &crossTask, &crossModel, &crossPrompt)
	require.NoError(t, err)
	assert.Equal(t, legacyV7RunID, crossRunID)
	assert.Equal(t, "cross-reference", crossTask)
	assert.Equal(t, "legacy-cross-model", crossModel)
	assert.Equal(t, "legacy-cross-v1", crossPrompt)

	err = tx.QueryRow(ctx, `
SELECT provenance_class, generation_run_id IS NULL
FROM category_translations
WHERE category_id = $1 AND lang = 'id'`, legacyCategoryID).
		Scan(&legacyClass, &legacyRunNull)
	require.NoError(t, err)
	assert.Equal(t, "legacy_unknown", legacyClass)
	assert.True(t, legacyRunNull, "legacy asset must not receive a fabricated generation identity")

	var validatedForeignKeys int

	err = tx.QueryRow(ctx, `
SELECT count(*)
FROM pg_constraint
WHERE contype = 'f'
  AND convalidated
  AND conname = ANY($1::text[])`, []string{
		"knowledge_extraction_runs_generation_run_fk",
		"cross_references_generation_run_fk",
		"citable_units_generation_run_fk",
		"book_metadata_translations_generation_run_fk",
		"author_translations_generation_run_fk",
		"category_translations_generation_run_fk",
		"section_translations_generation_run_fk",
		"book_heading_summaries_generation_run_fk",
		"book_metadata_translation_edits_generation_run_fk",
		"author_translation_edits_generation_run_fk",
		"category_translation_edits_generation_run_fk",
		"section_translation_edits_generation_run_fk",
		"heading_summary_edits_generation_run_fk",
	}).Scan(&validatedForeignKeys)
	require.NoError(t, err)
	assert.Equal(t, 13, validatedForeignKeys)

	require.NoError(t, tx.Rollback(ctx))
}

// TestLiveGenerationRunRegistry proves B-6 at the database boundary: adapter
// idempotency uses JSONB equality, descriptors cannot be mutated, extraction
// shares the same UUID, and generated assets cannot lose/fabricate provenance.
//
//nolint:paralleltest,maintidx // one serial transactional invariant fixture
func TestLiveGenerationRunRegistry(t *testing.T) {
	url := os.Getenv("SURAU_LIVE_PG")
	if url == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pg, err := postgres.New(url)
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	ctx := context.Background()

	// A session-local shadow exercises the adapter without leaving permanent
	// immutable test rows behind. postgres.New defaults to one pooled session.
	_, err = pg.Pool.Exec(ctx, `
CREATE TEMP TABLE generation_runs
(LIKE public.generation_runs INCLUDING ALL)
ON COMMIT PRESERVE ROWS`)
	require.NoError(t, err)

	registry := NewGenerationRunRepo(pg)
	provider := "openai"
	run := entity.GenerationRun{
		ID:            uuid.NewString(),
		TaskName:      "reader-translation",
		ModelID:       "model-a",
		PromptVersion: "reader-translation-v1",
		Provider:      &provider,
		Metadata:      entity.RawJSON(`{"temperature":0,"batch":{"size":25}}`),
	}
	stored, err := registry.RegisterOrVerify(ctx, &run)
	require.NoError(t, err)
	assert.Equal(t, run.Identity(), stored.Identity())
	assert.False(t, stored.CreatedAt.IsZero())

	retry := run
	retry.Metadata = entity.RawJSON(`{"batch":{"size":25},"temperature":0}`)
	retried, err := registry.RegisterOrVerify(ctx, &retry)
	require.NoError(t, err, "JSON object ordering is not an identity conflict")
	assert.Equal(t, stored.CreatedAt, retried.CreatedAt)

	conflict := run
	conflict.ModelID = "model-b"
	_, err = registry.RegisterOrVerify(ctx, &conflict)
	assert.ErrorIs(t, err, entity.ErrGenerationRunConflict)

	got, err := registry.Get(ctx, run.ID)
	require.NoError(t, err)
	assert.Equal(t, stored.Identity(), got.Identity())

	_, err = registry.Get(ctx, uuid.NewString())
	assert.ErrorIs(t, err, entity.ErrGenerationRunNotFound)
	_, err = registry.Get(ctx, "bad-id")
	assert.ErrorIs(t, err, entity.ErrInvalidGenerationRun)

	_, err = pg.Pool.Exec(ctx, `DROP TABLE generation_runs`)
	require.NoError(t, err)

	tx, err := pg.Pool.Begin(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })

	machineRunID := uuid.NewString()
	secondRunID := uuid.NewString()
	extractionRunID := uuid.NewString()
	mismatchedExtractionRunID := uuid.NewString()

	const v7RunID = "01890f47-6a4b-7ccd-8ef0-123456789abc"

	for _, descriptor := range []struct {
		id, task string
	}{
		{id: machineRunID, task: "catalog-translation"},
		{id: secondRunID, task: "catalog-translation"},
		{id: extractionRunID, task: "mentions"},
		{id: mismatchedExtractionRunID, task: "mentions"},
		{id: v7RunID, task: "cross-reference"},
	} {
		_, err = tx.Exec(ctx, `
INSERT INTO generation_runs (id, task_name, model_id, prompt_version, provider)
VALUES ($1, $2, 'model-a', 'prompt-v1', 'openai')`, descriptor.id, descriptor.task)
		require.NoError(t, err)
	}

	expectGenerationTxError(ctx, t, tx,
		`UPDATE generation_runs SET model_id = 'changed' WHERE id = $1`, machineRunID)
	expectGenerationTxError(ctx, t, tx,
		`DELETE FROM generation_runs WHERE id = $1`, machineRunID)

	_, err = tx.Exec(ctx, `SET LOCAL surau.cross_reference_writer = 'cross-reference-service'`)
	require.NoError(t, err)

	crossReferenceID := uuid.NewString()
	expectGenerationTxError(ctx, t, tx, `
INSERT INTO cross_references (
    id, source_anchor, target_anchor, source_corpus, target_corpus, kind, method,
    method_detail, generation_run_id, confidence, evidence_text, evidence_normalized,
    normalization_version, origin, origin_key
) VALUES (
    $1::uuid, 'entity/source', 'entity/target', 'entity', 'entity', 'cites', 'machine',
    jsonb_build_object('model_id', 'wrong-model', 'prompt_version', 'prompt-v1', 'run_id', $2::text),
    $2::uuid, 1, 'machine evidence', 'machine evidence', 1, 'machine', $1::text
)`, crossReferenceID, machineRunID)
	expectGenerationTxError(ctx, t, tx, `
INSERT INTO cross_references (
    id, source_anchor, target_anchor, source_corpus, target_corpus, kind, method,
    method_detail, generation_run_id, confidence, evidence_text, evidence_normalized,
    normalization_version, origin, origin_key
) VALUES (
    $1::uuid, 'entity/source', 'entity/target', 'entity', 'entity', 'cites', 'machine',
    jsonb_build_object('model_id', 'model-a', 'prompt_version', 'prompt-v1', 'run_id', $2::text),
    $2::uuid, 1, 'machine evidence', 'machine evidence', 1, 'machine', $1::text
)`, uuid.NewString(), uuid.NewString())
	_, err = tx.Exec(ctx, `
INSERT INTO cross_references (
    id, source_anchor, target_anchor, source_corpus, target_corpus, kind, method,
    method_detail, generation_run_id, confidence, evidence_text, evidence_normalized,
    normalization_version, origin, origin_key
) VALUES (
    $1::uuid, 'entity/source', 'entity/target', 'entity', 'entity', 'cites', 'machine',
    jsonb_build_object('model_id', 'model-a', 'prompt_version', 'prompt-v1', 'run_id', $2::text),
    $2::uuid, 1, 'machine evidence', 'machine evidence', 1, 'machine', $1::text
)`, crossReferenceID, machineRunID)
	require.NoError(t, err)

	var canonicalV7 bool

	err = tx.QueryRow(ctx, `
SELECT generation_run_safe_uuid($1) = $2::uuid`, strings.ToUpper(v7RunID), v7RunID).
		Scan(&canonicalV7)
	require.NoError(t, err)
	assert.True(t, canonicalV7, "uppercase UUID v7 input must canonicalize to the registered UUID")

	v7CrossReferenceID := uuid.NewString()
	_, err = tx.Exec(ctx, `
INSERT INTO cross_references (
    id, source_anchor, target_anchor, source_corpus, target_corpus, kind, method,
    method_detail, generation_run_id, confidence, evidence_text, evidence_normalized,
    normalization_version, origin, origin_key
) VALUES (
    $1::uuid, 'entity/source-v7', 'entity/target-v7', 'entity', 'entity', 'cites', 'machine',
    jsonb_build_object('model_id', 'model-a', 'prompt_version', 'prompt-v1', 'run_id', $2::text),
    $3::uuid, 1, 'v7 machine evidence', 'v7 machine evidence', 1, 'machine', $1::text
)`, v7CrossReferenceID, strings.ToUpper(v7RunID), v7RunID)
	require.NoError(t, err, "legacy uppercase UUID v7 must satisfy typed generation identity")

	expectGenerationTxError(ctx, t, tx, `
UPDATE cross_references
SET generation_run_id = $2::uuid,
    method_detail = jsonb_build_object(
        'model_id', 'model-a', 'prompt_version', 'prompt-v1', 'run_id', $2::text
    )
WHERE id = $1`, crossReferenceID, secondRunID)

	_, err = tx.Exec(ctx, `SET LOCAL surau.registry_writer = 'unit-service'`)
	require.NoError(t, err)

	const insertCitableUnitSQL = `
INSERT INTO citable_units (
    id, corpus, kind, ordinal, position, anchor, text, text_normalized,
    normalization_version, content_hash, occurrence, provenance_class, generation_run_id
) VALUES (
    $1::uuid, 'quran', 'paragraph', 910006001, 0, 'quran/test-unit/' || $1::text,
    'machine unit', 'machine unit', 1, decode(repeat('ab', 32), 'hex'), 1, $2, $3::uuid
)`

	expectGenerationTxError(ctx, t, tx, insertCitableUnitSQL,
		uuid.NewString(), entity.ProvenanceClassMachine, nil)
	expectGenerationTxError(ctx, t, tx, insertCitableUnitSQL,
		uuid.NewString(), entity.ProvenanceClassEditorial, machineRunID)
	expectGenerationTxError(ctx, t, tx, insertCitableUnitSQL,
		uuid.NewString(), entity.ProvenanceClassMachine, uuid.NewString())

	machineUnitID := uuid.NewString()
	_, err = tx.Exec(ctx, insertCitableUnitSQL,
		machineUnitID, entity.ProvenanceClassMachine, machineRunID)
	require.NoError(t, err, "machine Citable Unit accepts one registered generation run")
	expectGenerationTxError(ctx, t, tx, `
UPDATE citable_units
SET provenance_class = 'editorial', generation_run_id = NULL
WHERE id = $1`, machineUnitID)
	expectGenerationTxError(ctx, t, tx, `
UPDATE citable_units
SET generation_run_id = $2::uuid
WHERE id = $1`, machineUnitID, secondRunID)

	categoryID := -int(time.Now().UnixNano()%1_000_000_000) - 1
	legacyCategoryID := categoryID - 1
	restoredLegacyCategoryID := categoryID - 2
	_, err = tx.Exec(ctx, `
INSERT INTO categories (id, name)
VALUES ($1, 'machine-test'), ($2, 'legacy-test'), ($3, 'restored-legacy-test')`,
		categoryID, legacyCategoryID, restoredLegacyCategoryID)
	require.NoError(t, err)

	expectGenerationTxError(ctx, t, tx, `
INSERT INTO category_translations (category_id, lang, name)
VALUES ($1, 'id', 'tanpa asal')`, categoryID)
	expectGenerationTxError(ctx, t, tx, `
INSERT INTO category_translations (category_id, lang, name, provenance_class)
VALUES ($1, 'id', 'tanpa run', 'machine')`, categoryID)
	expectGenerationTxError(ctx, t, tx, `
INSERT INTO category_translations (
    category_id, lang, name, provenance_class, generation_run_id
)
VALUES ($1, 'id', 'run palsu', 'editorial', $2)`, categoryID, machineRunID)

	_, err = tx.Exec(ctx, `
INSERT INTO category_translations (
    category_id, lang, name, provenance_class, generation_run_id
)
VALUES ($1, 'id', 'hasil mesin', 'machine', $2)`, categoryID, machineRunID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
UPDATE category_translations SET name = 'hasil direview', updated_at = now()
WHERE category_id = $1 AND lang = 'id'`, categoryID)
	require.NoError(t, err, "human review preserves machine class and original run")
	expectGenerationTxError(ctx, t, tx, `
UPDATE category_translations SET generation_run_id = $2
WHERE category_id = $1 AND lang = 'id'`, categoryID, secondRunID)
	expectGenerationTxError(ctx, t, tx, `
UPDATE category_translations SET provenance_class = 'editorial'
WHERE category_id = $1 AND lang = 'id'`, categoryID)

	_, err = tx.Exec(ctx, `SET LOCAL surau.production_publish = 'on'`)
	require.NoError(t, err)
	expectGenerationTxError(ctx, t, tx, `
UPDATE category_translations
SET provenance_class = 'editorial', generation_run_id = NULL
WHERE category_id = $1 AND lang = 'id'`, categoryID)
	_, err = tx.Exec(ctx, `
UPDATE category_translations
SET name = 'human replacement', provenance_class = 'editorial', generation_run_id = NULL
WHERE category_id = $1 AND lang = 'id'`, categoryID)
	require.NoError(t, err, "publish may replace machine text with a genuinely new editorial asset")
	_, err = tx.Exec(ctx, `SET LOCAL surau.production_publish = 'off'`)
	require.NoError(t, err)

	// Seed the shape that existed immediately before this migration, then prove
	// maintenance/review is allowed but rewriting requires honest provenance.
	_, err = tx.Exec(ctx, `
ALTER TABLE category_translations DISABLE TRIGGER trg_category_translations_provenance`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
INSERT INTO category_translations (category_id, lang, name, provenance_class)
VALUES ($1, 'id', 'legacy', 'legacy_unknown')`, legacyCategoryID)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
ALTER TABLE category_translations ENABLE TRIGGER trg_category_translations_provenance`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
UPDATE category_translations SET updated_at = now()
WHERE category_id = $1 AND lang = 'id'`, legacyCategoryID)
	require.NoError(t, err, "untouched legacy content remains maintainable")
	_, err = tx.Exec(ctx, `
UPDATE category_translations SET source = 'metadata-only'
WHERE category_id = $1 AND lang = 'id'`, legacyCategoryID)
	require.NoError(t, err, "source metadata does not claim authorship of legacy text")
	expectGenerationTxError(ctx, t, tx, `
UPDATE category_translations SET provenance_class = 'editorial'
WHERE category_id = $1 AND lang = 'id'`, legacyCategoryID)
	expectGenerationTxError(ctx, t, tx, `
UPDATE category_translations SET name = 'rewritten'
WHERE category_id = $1 AND lang = 'id'`, legacyCategoryID)
	_, err = tx.Exec(ctx, `
UPDATE category_translations
SET name = 'human rewrite', provenance_class = 'editorial'
WHERE category_id = $1 AND lang = 'id'`, legacyCategoryID)
	require.NoError(t, err)

	_, err = tx.Exec(ctx, `SET LOCAL surau.production_restore = 'on'`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
INSERT INTO category_translations (category_id, lang, name, provenance_class)
VALUES ($1, 'id', 'restored legacy', 'legacy_unknown')`, restoredLegacyCategoryID)
	require.NoError(t, err, "restore may preserve an honest legacy_unknown snapshot")
	_, err = tx.Exec(ctx, `SET LOCAL surau.production_restore = 'off'`)
	require.NoError(t, err)

	expectGenerationTxError(ctx, t, tx, `
INSERT INTO knowledge_extraction_runs (
    id, task_name, prompt_version, model_id
)
VALUES ($1, 'mentions', 'prompt-v1', 'model-a')`, uuid.NewString())
	_, err = tx.Exec(ctx, `
INSERT INTO knowledge_extraction_runs (
    id, task_name, prompt_version, model_id
)
VALUES ($1, 'mentions', 'prompt-v1', 'model-a')`, extractionRunID)
	require.NoError(t, err, "knowledge extraction shares the generation UUID one-to-one")
	expectGenerationTxError(ctx, t, tx, `
INSERT INTO knowledge_extraction_runs (
    id, task_name, prompt_version, model_id
)
VALUES ($1, 'mentions', 'prompt-v1', 'different-model')`, mismatchedExtractionRunID)
	expectGenerationTxError(ctx, t, tx, `
UPDATE knowledge_extraction_runs SET prompt_version = 'changed-prompt'
WHERE id = $1`, extractionRunID)

	var assetTriggerCount int

	err = tx.QueryRow(ctx, `
SELECT count(*)
FROM pg_trigger
WHERE NOT tgisinternal
  AND tgname = ANY($1::text[])`, []string{
		"trg_book_metadata_translations_provenance",
		"trg_author_translations_provenance",
		"trg_category_translations_provenance",
		"trg_section_translations_provenance",
		"trg_book_heading_summaries_provenance",
		"trg_book_metadata_translation_edits_provenance",
		"trg_author_translation_edits_provenance",
		"trg_category_translation_edits_provenance",
		"trg_section_translation_edits_provenance",
		"trg_heading_summary_edits_provenance",
	}).Scan(&assetTriggerCount)
	require.NoError(t, err)
	assert.Equal(t, 10, assetTriggerCount)

	require.NoError(t, tx.Rollback(ctx))
}

func expectGenerationTxError(
	ctx context.Context,
	t *testing.T,
	tx pgx.Tx,
	query string,
	args ...any,
) {
	t.Helper()

	_, err := tx.Exec(ctx, "SAVEPOINT generation_expected_error")
	require.NoError(t, err)
	_, err = tx.Exec(ctx, query, args...)
	require.Error(t, err)

	_, rollbackErr := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT generation_expected_error")
	require.NoError(t, rollbackErr)

	_, releaseErr := tx.Exec(ctx, "RELEASE SAVEPOINT generation_expected_error")
	require.NoError(t, releaseErr)
}
