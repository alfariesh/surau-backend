package persistent

import (
	"context"
	"os"
	"testing"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/pkg/postgres"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const productionWorkflowMissingProjectID = "b5b60000-0000-4000-8000-0000000000ff"

// TestLiveProductionWorkflowWritesAreAtomic pins the production-project
// writes that pair an asset change with a project state change: each pair now
// commits in one transaction, so a failure can never leave the asset changed
// while the project still advertises the old state (e.g. published without a
// final asset). It shares the committed provenance fixture, hence serial.
//
//nolint:paralleltest // serial: shares the committed production provenance fixture
func TestLiveProductionWorkflowWritesAreAtomic(t *testing.T) {
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
		_, registerErr := registry.RegisterOrVerify(ctx, &run)
		require.NoError(t, registerErr)
	}

	require.NoError(t, seedProductionProvenanceFixture(ctx, pg))

	editorial := NewEditorialRepo(pg)
	project, err := editorial.CreateProductionProject(ctx, productionProvenanceActorID, entity.BookProductionProject{
		BookID:         productionProvenanceBookID,
		Lang:           "en",
		WorkflowStatus: entity.ProductionWorkflowCandidate,
		RequiresReview: true,
		Priority:       1,
	})
	require.NoError(t, err)

	heading := productionProvenanceHeading

	saveProductionMachineDrafts(ctx, t, editorial, project.ID, "first")

	t.Run("review submit moves the project to in_review", func(t *testing.T) {
		require.NoError(t, editorial.ReviewProductionAsset(
			ctx, productionProvenanceActorID, project.ID,
			entity.ProductionAssetSectionTranslation, &heading, entity.ProductionReviewDecisionSubmit, nil,
		))
		assertProductionProjectState(ctx, t, editorial, project.ID,
			entity.ProductionWorkflowInReview, entity.ProductionPublicationHidden)
	})

	t.Run("draft writes on a missing project still answer draft not found", func(t *testing.T) {
		err := editorial.ReviewProductionAsset(
			ctx, productionProvenanceActorID, productionWorkflowMissingProjectID,
			entity.ProductionAssetSectionTranslation, &heading, entity.ProductionReviewDecisionApprove, nil,
		)
		require.ErrorIs(t, err, entity.ErrDraftNotFound)

		err = editorial.DeleteHeadingSummaryDraft(
			ctx, productionProvenanceActorID, productionWorkflowMissingProjectID, heading, nil,
		)
		require.ErrorIs(t, err, entity.ErrDraftNotFound)
	})

	t.Run("draft delete returns the project to drafting", func(t *testing.T) {
		require.NoError(t, editorial.DeleteHeadingSummaryDraft(ctx, productionProvenanceActorID, project.ID, heading, nil))
		assertProductionProjectState(ctx, t, editorial, project.ID,
			entity.ProductionWorkflowDrafting, entity.ProductionPublicationHidden)

		err := editorial.DeleteHeadingSummaryDraft(ctx, productionProvenanceActorID, project.ID, heading, nil)
		require.ErrorIs(t, err, entity.ErrDraftNotFound)
	})

	saveProductionMachineDrafts(ctx, t, editorial, project.ID, "second")

	for _, target := range productionProvenanceTargets() {
		require.NoError(t, editorial.ReviewProductionAsset(
			ctx, productionProvenanceActorID, project.ID, target.assetType, target.headingID,
			entity.ProductionReviewDecisionApprove, nil,
		))
	}

	_, err = editorial.PublishProductionProject(ctx, productionProvenanceActorID, project.ID, nil)
	require.NoError(t, err)

	t.Run("a missing final asset rolls back and leaves the project published", func(t *testing.T) {
		err := editorial.DeleteFinalProductionAsset(
			ctx, productionProvenanceActorID, project.ID, entity.ProductionAssetSectionAudio, &heading, nil,
		)
		require.ErrorIs(t, err, entity.ErrTranslationNotFound)
		assertProductionProjectState(ctx, t, editorial, project.ID,
			entity.ProductionWorkflowPublished, entity.ProductionPublicationPublished)
	})

	t.Run("final asset delete hides the project and records its trail", func(t *testing.T) {
		reason := "takedown drill"
		require.NoError(t, editorial.DeleteFinalProductionAsset(
			ctx, productionProvenanceActorID, project.ID, entity.ProductionAssetSectionTranslation, &heading, &reason,
		))
		assertProductionProjectState(ctx, t, editorial, project.ID,
			entity.ProductionWorkflowDrafting, entity.ProductionPublicationHidden)

		var deleted bool
		require.NoError(t, pg.Pool.QueryRow(ctx, `
SELECT is_deleted FROM section_translations
WHERE book_id = $1 AND heading_id = $2 AND lang = 'en'`,
			productionProvenanceBookID, heading).Scan(&deleted))
		assert.True(t, deleted)

		var audits, events int
		require.NoError(t, pg.Pool.QueryRow(ctx, `
SELECT count(*) FROM admin_audit_logs
WHERE actor_id = $1 AND action = 'production_asset.final_delete'`,
			productionProvenanceActorID).Scan(&audits))
		require.NoError(t, pg.Pool.QueryRow(ctx, `
SELECT count(*) FROM book_production_events
WHERE project_id = $1 AND event_type = $2`,
			project.ID, entity.ProductionEventFinalDelete).Scan(&events))
		assert.Equal(t, 1, audits, "exactly one audit row for the committed delete")
		assert.Equal(t, 1, events, "exactly one timeline event for the committed delete")
	})

	t.Run("final asset delete on a missing project", func(t *testing.T) {
		err := editorial.DeleteFinalProductionAsset(
			ctx, productionProvenanceActorID, productionWorkflowMissingProjectID,
			entity.ProductionAssetSectionTranslation, &heading, nil,
		)
		require.ErrorIs(t, err, entity.ErrProductionProjectNotFound)
	})
}

func assertProductionProjectState(
	ctx context.Context,
	t *testing.T,
	editorial *EditorialRepo,
	projectID, workflowStatus, publicationStatus string,
) {
	t.Helper()

	project, err := editorial.GetProductionProject(ctx, projectID)
	require.NoError(t, err)
	assert.Equal(t, workflowStatus, project.WorkflowStatus)
	assert.Equal(t, publicationStatus, project.PublicationStatus)
}
