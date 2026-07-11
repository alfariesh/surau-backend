package persistent

import (
	"context"
	"os"
	"testing"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/repo"
	"github.com/alfariesh/surau-backend/pkg/postgres"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	productionProvenanceActorID       = "b5b60000-0000-4000-8000-000000000002"
	productionProvenanceCatalogRunID  = "b5b60000-0000-4000-8000-000000000011"
	productionProvenanceSectionRunID  = "b5b60000-0000-4000-8000-000000000012"
	productionProvenanceSummaryRunID  = "b5b60000-0000-4000-8000-000000000013"
	productionProvenanceBookID        = -9_906_111
	productionProvenanceAuthor        = -9_906_112
	productionProvenanceCategory      = -9_906_113
	productionProvenanceHeading       = 9_906_114
	productionProvenancePage          = 9_906_115
	productionProvenanceCatalogModel  = "production-live-catalog-model"
	productionProvenanceSectionModel  = "production-live-section-model"
	productionProvenanceSummaryModel  = "production-live-summary-model"
	productionProvenanceCatalogPrompt = "catalog-translation-v1"
	productionProvenanceSectionPrompt = "reader-translation-v1"
	productionProvenanceSummaryPrompt = "reader-summary-translation-v1"
)

//nolint:maintidx,paralleltest // serial: committed fixture exercises the complete production lifecycle
func TestLiveProductionMachineGenerationSurvivesEditorialLifecycle(t *testing.T) {
	url := os.Getenv("SURAU_LIVE_PG")
	if url == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pg, err := postgres.New(url)
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	ctx := context.Background()
	require.NoError(t, cleanupProductionProvenanceFixture(ctx, pg))
	t.Cleanup(func() {
		if cleanupErr := cleanupProductionProvenanceFixture(context.Background(), pg); cleanupErr != nil {
			t.Errorf("cleanup production provenance fixture: %v", cleanupErr)
		}
	})

	registry := NewGenerationRunRepo(pg)
	for _, run := range productionProvenanceRuns() {
		registered, registerErr := registry.RegisterOrVerify(ctx, &run)
		require.NoError(t, registerErr)
		assert.Equal(t, run.Identity(), registered.Identity())
	}

	require.NoError(t, seedProductionProvenanceFixture(ctx, pg))

	editorial := NewEditorialRepo(pg)
	project, err := editorial.CreateProductionProject(ctx, productionProvenanceActorID, entity.BookProductionProject{
		BookID:         productionProvenanceBookID,
		Lang:           "en",
		WorkflowStatus: entity.ProductionWorkflowCandidate,
		RequiresReview: true,
		RequiresAudio:  false,
		Priority:       1,
	})
	require.NoError(t, err)

	first := saveProductionMachineDrafts(ctx, t, editorial, project.ID, "first")
	assertProductionMachineDrafts(ctx, t, editorial, project.ID)

	second := saveProductionMachineDrafts(ctx, t, editorial, project.ID, "second")
	assert.NotEqual(t, first.metadata.DisplayTitle, second.metadata.DisplayTitle)
	assert.NotEqual(t, first.author.Name, second.author.Name)
	assert.NotEqual(t, first.category.Name, second.category.Name)
	assert.NotEqual(t, first.section.Content, second.section.Content)
	assert.NotEqual(t, first.summary.Summary, second.summary.Summary)
	assertProductionMachineDrafts(ctx, t, editorial, project.ID)

	targets := productionProvenanceTargets()
	for _, target := range targets {
		revisions, total, listErr := editorial.ListProductionDraftRevisions(ctx, repo.ProductionDraftRevisionFilter{
			ProjectID: project.ID,
			AssetType: target.assetType,
			HeadingID: target.headingID,
			Limit:     10,
		})
		require.NoError(t, listErr)
		require.Equal(t, 2, total)
		require.Len(t, revisions, 2)
		assert.Equal(t, 2, revisions[0].Version)
		assert.Equal(t, 1, revisions[1].Version)

		restored, restoreErr := editorial.RestoreProductionDraftRevision(
			ctx,
			productionProvenanceActorID,
			project.ID,
			revisions[1].ID,
		)
		require.NoError(t, restoreErr)
		assert.Equal(t, 3, restored.Version)
	}

	assertProductionMachineDrafts(ctx, t, editorial, project.ID)

	for _, target := range targets {
		require.NoError(t, editorial.ReviewProductionAsset(
			ctx,
			productionProvenanceActorID,
			project.ID,
			target.assetType,
			target.headingID,
			entity.ProductionReviewDecisionApprove,
			nil,
		))
	}

	assertProductionMachineDrafts(ctx, t, editorial, project.ID)
	assertProductionProvenanceRows(ctx, t, pg, project.ID, false)

	published, err := editorial.PublishProductionProject(
		ctx,
		productionProvenanceActorID,
		project.ID,
		nil,
	)
	require.NoError(t, err)
	assert.Equal(t, entity.ProductionWorkflowPublished, published.WorkflowStatus)
	assert.Equal(t, entity.ProductionPublicationPublished, published.PublicationStatus)
	assertProductionProvenanceRows(ctx, t, pg, project.ID, true)
}

type productionMachineDraftSet struct {
	metadata entity.BookMetadataTranslationEdit
	author   entity.AuthorTranslationEdit
	category entity.CategoryTranslationEdit
	section  entity.SectionTranslationEdit
	summary  entity.HeadingSummaryEdit
}

type productionProvenanceTarget struct {
	assetType  string
	headingID  *int
	generation productionGenerationExpectation
}

type productionGenerationExpectation struct {
	runID         string
	modelID       string
	promptVersion string
}

func productionProvenanceRuns() []entity.GenerationRun {
	provider := "openai"

	return []entity.GenerationRun{
		{
			ID:            productionProvenanceCatalogRunID,
			TaskName:      "catalog-translation",
			ModelID:       productionProvenanceCatalogModel,
			PromptVersion: productionProvenanceCatalogPrompt,
			Provider:      &provider,
			Metadata:      entity.RawJSON(`{"fixture":"editorial-production","asset_family":"catalog"}`),
		},
		{
			ID:            productionProvenanceSectionRunID,
			TaskName:      "reader-translation",
			ModelID:       productionProvenanceSectionModel,
			PromptVersion: productionProvenanceSectionPrompt,
			Provider:      &provider,
			Metadata:      entity.RawJSON(`{"fixture":"editorial-production","asset_family":"section"}`),
		},
		{
			ID:            productionProvenanceSummaryRunID,
			TaskName:      "reader-summary-translation",
			ModelID:       productionProvenanceSummaryModel,
			PromptVersion: productionProvenanceSummaryPrompt,
			Provider:      &provider,
			Metadata:      entity.RawJSON(`{"fixture":"editorial-production","asset_family":"summary"}`),
		},
	}
}

func productionCatalogGeneration() productionGenerationExpectation {
	return productionGenerationExpectation{
		runID:         productionProvenanceCatalogRunID,
		modelID:       productionProvenanceCatalogModel,
		promptVersion: productionProvenanceCatalogPrompt,
	}
}

func productionSectionGeneration() productionGenerationExpectation {
	return productionGenerationExpectation{
		runID:         productionProvenanceSectionRunID,
		modelID:       productionProvenanceSectionModel,
		promptVersion: productionProvenanceSectionPrompt,
	}
}

func productionSummaryGeneration() productionGenerationExpectation {
	return productionGenerationExpectation{
		runID:         productionProvenanceSummaryRunID,
		modelID:       productionProvenanceSummaryModel,
		promptVersion: productionProvenanceSummaryPrompt,
	}
}

func productionProvenanceTargets() []productionProvenanceTarget {
	headingID := productionProvenanceHeading
	catalogGeneration := productionCatalogGeneration()

	return []productionProvenanceTarget{
		{assetType: entity.ProductionAssetBookMetadata, generation: catalogGeneration},
		{assetType: entity.ProductionAssetAuthorMetadata, generation: catalogGeneration},
		{assetType: entity.ProductionAssetCategoryMetadata, generation: catalogGeneration},
		{
			assetType:  entity.ProductionAssetSectionTranslation,
			headingID:  &headingID,
			generation: productionSectionGeneration(),
		},
		{
			assetType:  entity.ProductionAssetHeadingSummary,
			headingID:  &headingID,
			generation: productionSummaryGeneration(),
		},
	}
}

func saveProductionMachineDrafts(
	ctx context.Context,
	t *testing.T,
	editorial *EditorialRepo,
	projectID,
	version string,
) productionMachineDraftSet {
	t.Helper()

	bibliography := "bibliography " + version
	biography := "biography " + version
	title := "section title " + version

	metadata, err := editorial.SaveMetadataTranslationDraft(ctx, productionProvenanceActorID, projectID, entity.BookMetadataTranslationEdit{
		DisplayTitle: "book metadata " + version,
		Bibliography: &bibliography,
	}, nil)
	require.NoError(t, err)
	assertProductionMachineGeneration(t, metadata.ProvenanceClass, metadata.Generation, productionCatalogGeneration())

	author, err := editorial.SaveAuthorTranslationDraft(ctx, productionProvenanceActorID, projectID, entity.AuthorTranslationEdit{
		Name:      "author " + version,
		Biography: &biography,
	}, nil)
	require.NoError(t, err)
	assertProductionMachineGeneration(t, author.ProvenanceClass, author.Generation, productionCatalogGeneration())

	category, err := editorial.SaveCategoryTranslationDraft(ctx, productionProvenanceActorID, projectID, entity.CategoryTranslationEdit{
		Name: "category " + version,
	}, nil)
	require.NoError(t, err)
	assertProductionMachineGeneration(t, category.ProvenanceClass, category.Generation, productionCatalogGeneration())

	section, err := editorial.SaveSectionTranslationDraft(ctx, productionProvenanceActorID, projectID, entity.SectionTranslationEdit{
		HeadingID: productionProvenanceHeading,
		Title:     &title,
		Content:   "section content " + version,
	}, nil)
	require.NoError(t, err)
	assertProductionMachineGeneration(t, section.ProvenanceClass, section.Generation, productionSectionGeneration())

	summary, err := editorial.SaveHeadingSummaryDraft(ctx, productionProvenanceActorID, projectID, entity.HeadingSummaryEdit{
		HeadingID: productionProvenanceHeading,
		Summary:   "heading summary " + version,
	}, nil)
	require.NoError(t, err)
	assertProductionMachineGeneration(t, summary.ProvenanceClass, summary.Generation, productionSummaryGeneration())

	return productionMachineDraftSet{
		metadata: metadata,
		author:   author,
		category: category,
		section:  section,
		summary:  summary,
	}
}

func assertProductionMachineDrafts(
	ctx context.Context,
	t *testing.T,
	editorial *EditorialRepo,
	projectID string,
) {
	t.Helper()

	metadata, err := editorial.GetMetadataTranslationDraft(ctx, projectID)
	require.NoError(t, err)
	assertProductionMachineGeneration(t, metadata.ProvenanceClass, metadata.Generation, productionCatalogGeneration())

	author, err := editorial.GetAuthorTranslationDraft(ctx, projectID)
	require.NoError(t, err)
	assertProductionMachineGeneration(t, author.ProvenanceClass, author.Generation, productionCatalogGeneration())

	category, err := editorial.GetCategoryTranslationDraft(ctx, projectID)
	require.NoError(t, err)
	assertProductionMachineGeneration(t, category.ProvenanceClass, category.Generation, productionCatalogGeneration())

	section, err := editorial.GetSectionTranslationDraft(ctx, projectID, productionProvenanceHeading)
	require.NoError(t, err)
	assertProductionMachineGeneration(t, section.ProvenanceClass, section.Generation, productionSectionGeneration())

	summary, err := editorial.GetHeadingSummaryDraft(ctx, projectID, productionProvenanceHeading)
	require.NoError(t, err)
	assertProductionMachineGeneration(t, summary.ProvenanceClass, summary.Generation, productionSummaryGeneration())
}

func assertProductionMachineGeneration(
	t *testing.T,
	provenanceClass string,
	generation *entity.GenerationIdentity,
	expected productionGenerationExpectation,
) {
	t.Helper()

	assert.Equal(t, entity.ProvenanceClassMachine, provenanceClass)

	if assert.NotNil(t, generation) {
		assert.Equal(t, expected.runID, generation.RunID)
		assert.Equal(t, expected.modelID, generation.ModelID)
		assert.Equal(t, expected.promptVersion, generation.PromptVersion)
	}
}

func assertProductionProvenanceRows(
	ctx context.Context,
	t *testing.T,
	pg *postgres.Postgres,
	projectID string,
	final bool,
) {
	t.Helper()

	query := `
SELECT assets.asset_type, assets.provenance_class, assets.generation_run_id::text,
       generation.model_id, generation.prompt_version
FROM (
    SELECT 'book_metadata'::text AS asset_type, provenance_class, generation_run_id
    FROM book_metadata_translation_edits WHERE project_id = $1
    UNION ALL
    SELECT 'author_metadata', provenance_class, generation_run_id
    FROM author_translation_edits WHERE project_id = $1
    UNION ALL
    SELECT 'category_metadata', provenance_class, generation_run_id
    FROM category_translation_edits WHERE project_id = $1
    UNION ALL
    SELECT 'section_translation', provenance_class, generation_run_id
    FROM section_translation_edits WHERE project_id = $1
    UNION ALL
    SELECT 'heading_summary', provenance_class, generation_run_id
    FROM heading_summary_edits WHERE project_id = $1
) assets
JOIN generation_runs generation ON generation.id = assets.generation_run_id
ORDER BY assets.asset_type`
	args := []any{projectID}

	if final {
		query = `
SELECT assets.asset_type, assets.provenance_class, assets.generation_run_id::text,
       generation.model_id, generation.prompt_version
FROM (
    SELECT 'book_metadata'::text AS asset_type, provenance_class, generation_run_id
    FROM book_metadata_translations
    WHERE book_id = $1 AND lang = 'en'
    UNION ALL
    SELECT 'author_metadata', provenance_class, generation_run_id
    FROM author_translations
    WHERE author_id = $2 AND lang = 'en'
    UNION ALL
    SELECT 'category_metadata', provenance_class, generation_run_id
    FROM category_translations
    WHERE category_id = $3 AND lang = 'en'
    UNION ALL
    SELECT 'section_translation', provenance_class, generation_run_id
    FROM section_translations
    WHERE book_id = $1 AND lang = 'en'
    UNION ALL
    SELECT 'heading_summary', provenance_class, generation_run_id
    FROM book_heading_summaries
    WHERE book_id = $1 AND lang = 'en'
) assets
JOIN generation_runs generation ON generation.id = assets.generation_run_id
ORDER BY assets.asset_type`
		args = []any{
			productionProvenanceBookID,
			productionProvenanceAuthor,
			productionProvenanceCategory,
		}
	}

	rows, err := pg.Pool.Query(ctx, query, args...)
	require.NoError(t, err)

	defer rows.Close()

	expectedByAsset := make(map[string]productionGenerationExpectation)

	for _, target := range productionProvenanceTargets() {
		expectedByAsset[target.assetType] = target.generation
	}

	seen := make(map[string]bool)

	for rows.Next() {
		var assetType, provenanceClass, runID, modelID, promptVersion string
		require.NoError(t, rows.Scan(&assetType, &provenanceClass, &runID, &modelID, &promptVersion))

		expected, found := expectedByAsset[assetType]
		require.True(t, found, "unexpected production asset %q", assetType)
		assert.False(t, seen[assetType], "duplicate production asset %q", assetType)
		seen[assetType] = true

		assertProductionMachineGeneration(t, provenanceClass, &entity.GenerationIdentity{
			RunID:         runID,
			ModelID:       modelID,
			PromptVersion: promptVersion,
		}, expected)
	}

	require.NoError(t, rows.Err())
	assert.Len(t, seen, len(expectedByAsset))
}

func seedProductionProvenanceFixture(ctx context.Context, pg *postgres.Postgres) error {
	tx, err := pg.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackTx(ctx, tx)

	statements := []struct {
		query string
		args  []any
	}{
		{
			query: `
INSERT INTO users (id, username, email, password_hash, role, email_verified)
VALUES ($1, 'production-provenance-live', 'production-provenance-live@example.test', 'not-used', 'editor', true)`,
			args: []any{productionProvenanceActorID},
		},
		{
			query: `INSERT INTO categories (id, name) VALUES ($1, 'production provenance category')`,
			args:  []any{productionProvenanceCategory},
		},
		{
			query: `INSERT INTO authors (id, name) VALUES ($1, 'production provenance author')`,
			args:  []any{productionProvenanceAuthor},
		},
		{
			query: `
INSERT INTO books (id, name, category_id, author_id, has_content)
VALUES ($1, 'production provenance book', $2, $3, true)`,
			args: []any{productionProvenanceBookID, productionProvenanceCategory, productionProvenanceAuthor},
		},
		{
			query: `
INSERT INTO book_pages (book_id, page_id, content_html, content_text)
VALUES ($1, $2, '<p>source</p>', 'source')`,
			args: []any{productionProvenanceBookID, productionProvenancePage},
		},
		{
			query: `
INSERT INTO book_headings (book_id, heading_id, page_id, ordinal, content)
VALUES ($1, $2, $3, 1, 'source heading')`,
			args: []any{productionProvenanceBookID, productionProvenanceHeading, productionProvenancePage},
		},
		{
			query: `
INSERT INTO book_metadata_translations (
    book_id, lang, display_title, provenance_class, generation_run_id
)
VALUES ($1, 'en', 'machine book metadata', 'machine', $2)`,
			args: []any{productionProvenanceBookID, productionProvenanceCatalogRunID},
		},
		{
			query: `
INSERT INTO author_translations (
    author_id, lang, name, provenance_class, generation_run_id
)
VALUES ($1, 'en', 'machine author', 'machine', $2)`,
			args: []any{productionProvenanceAuthor, productionProvenanceCatalogRunID},
		},
		{
			query: `
INSERT INTO category_translations (
    category_id, lang, name, provenance_class, generation_run_id
)
VALUES ($1, 'en', 'machine category', 'machine', $2)`,
			args: []any{productionProvenanceCategory, productionProvenanceCatalogRunID},
		},
		{
			query: `
INSERT INTO section_translations (
    book_id, heading_id, lang, content, provenance_class, generation_run_id
)
VALUES ($1, $2, 'en', 'machine section', 'machine', $3)`,
			args: []any{productionProvenanceBookID, productionProvenanceHeading, productionProvenanceSectionRunID},
		},
		{
			query: `
INSERT INTO book_heading_summaries (
    book_id, heading_id, lang, summary, provenance_class, generation_run_id
)
VALUES ($1, $2, 'en', 'machine summary', 'machine', $3)`,
			args: []any{productionProvenanceBookID, productionProvenanceHeading, productionProvenanceSummaryRunID},
		},
	}

	for _, statement := range statements {
		if _, err = tx.Exec(ctx, statement.query, statement.args...); err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}

func cleanupProductionProvenanceFixture(ctx context.Context, pg *postgres.Postgres) error {
	tx, err := pg.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackTx(ctx, tx)

	statements := []struct {
		query string
		args  []any
	}{
		{query: `DELETE FROM admin_audit_logs WHERE actor_id = $1`, args: []any{productionProvenanceActorID}},
		{query: `DELETE FROM book_production_projects WHERE book_id = $1`, args: []any{productionProvenanceBookID}},
		{query: `DELETE FROM book_metadata_translations WHERE book_id = $1`, args: []any{productionProvenanceBookID}},
		{query: `DELETE FROM section_translations WHERE book_id = $1`, args: []any{productionProvenanceBookID}},
		{query: `DELETE FROM book_heading_summaries WHERE book_id = $1`, args: []any{productionProvenanceBookID}},
		{query: `DELETE FROM author_translations WHERE author_id = $1`, args: []any{productionProvenanceAuthor}},
		{query: `DELETE FROM category_translations WHERE category_id = $1`, args: []any{productionProvenanceCategory}},
		{query: `DELETE FROM books WHERE id = $1`, args: []any{productionProvenanceBookID}},
		{query: `DELETE FROM authors WHERE id = $1`, args: []any{productionProvenanceAuthor}},
		{query: `DELETE FROM categories WHERE id = $1`, args: []any{productionProvenanceCategory}},
		{query: `DELETE FROM users WHERE id = $1`, args: []any{productionProvenanceActorID}},
	}

	for _, statement := range statements {
		if _, err = tx.Exec(ctx, statement.query, statement.args...); err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}
