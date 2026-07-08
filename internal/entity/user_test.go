package entity

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeUserRole(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		role string
		want string
	}{
		{name: "user", role: " user ", want: UserRoleUser},
		{name: "editor", role: "EDITOR", want: UserRoleEditor},
		{name: "admin", role: "Admin", want: UserRoleAdmin},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := NormalizeUserRole(tt.role)

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNormalizeUserRoleRejectsInvalidRole(t *testing.T) {
	t.Parallel()

	_, err := NormalizeUserRole("owner")

	require.ErrorIs(t, err, ErrInvalidRole)
}

func TestNormalizeProductionLang(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		lang string
		want string
	}{
		{name: "indonesian", lang: " ID ", want: "id"},
		{name: "english region", lang: "en-US", want: "en"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := NormalizeProductionLang(tt.lang)

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNormalizeProductionLangRejectsArabic(t *testing.T) {
	t.Parallel()

	_, err := NormalizeProductionLang("ar")

	require.ErrorIs(t, err, ErrUnsupportedLanguage)
}

func TestProductionHelpers(t *testing.T) {
	t.Parallel()

	status, err := NormalizeProductionWorkflowStatus(" READY ")
	require.NoError(t, err)
	assert.Equal(t, ProductionWorkflowReady, status)

	decision, err := NormalizeProductionReviewDecision(" approve ")
	require.NoError(t, err)
	assert.Equal(t, ProductionReviewDecisionApprove, decision)

	assetType, err := NormalizeProductionAssetType(" SECTION_AUDIO ")
	require.NoError(t, err)
	assert.Equal(t, ProductionAssetSectionAudio, assetType)
	assert.True(t, IsHeadingProductionAsset(assetType))
	assert.True(t, IsProductionEventType(ProductionEventDraftSave))
	assert.True(t, IsProductionEventType(ProductionEventDraftRestore))
	assert.False(t, IsProductionEventType("production_asset.unknown"))

	_, err = NormalizeProductionReviewDecision("ship")
	require.ErrorIs(t, err, ErrInvalidReviewDecision)

	blocking := BookProductionBlocking{
		Code:      "missing_required_asset",
		AssetType: ProductionAssetSectionTranslation,
		Message:   "section translation draft is missing",
	}
	assert.Equal(t, "missing_required_asset", blocking.Code)
	assert.Equal(t, ProductionAssetSectionTranslation, blocking.AssetType)
}

func TestNormalizeProductionDraftTarget(t *testing.T) {
	t.Parallel()

	headingID := 10
	assetType, gotHeadingID, err := NormalizeProductionDraftTarget(" SECTION_TRANSLATION ", &headingID)
	require.NoError(t, err)
	assert.Equal(t, ProductionAssetSectionTranslation, assetType)
	assert.Equal(t, &headingID, gotHeadingID)

	assetType, gotHeadingID, err = NormalizeProductionDraftTarget(" book_metadata ", nil)
	require.NoError(t, err)
	assert.Equal(t, ProductionAssetBookMetadata, assetType)
	assert.Nil(t, gotHeadingID)

	_, _, err = NormalizeProductionDraftTarget(ProductionAssetSectionAudio, nil)
	require.ErrorIs(t, err, ErrHeadingNotFound)

	_, _, err = NormalizeProductionDraftTarget(ProductionAssetBookMetadata, &headingID)
	require.ErrorIs(t, err, ErrInvalidProductionDraft)
}

func TestBookProductionCandidateShape(t *testing.T) {
	t.Parallel()

	projectID := "project-id"
	workflow := ProductionWorkflowDrafting
	publication := ProductionPublicationHidden
	candidate := BookProductionCandidate{
		BookID:                    797,
		Name:                      "book",
		HasContent:                true,
		HeadingCount:              12,
		PageCount:                 30,
		ExistingProjectID:         &projectID,
		ExistingWorkflowStatus:    &workflow,
		ExistingPublicationStatus: &publication,
	}

	assert.Equal(t, 797, candidate.BookID)
	assert.Equal(t, 12, candidate.HeadingCount)
	assert.Equal(t, &projectID, candidate.ExistingProjectID)
	assert.Equal(t, &workflow, candidate.ExistingWorkflowStatus)
}
