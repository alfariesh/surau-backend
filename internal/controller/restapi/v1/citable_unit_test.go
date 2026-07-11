package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/controller/restapi/v1/response"
	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/pkg/logger"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeCitableUnitRegistry struct {
	result entity.UnitResolution
	err    error
}

func (f *fakeCitableUnitRegistry) ReconcileBook(context.Context, int) (entity.UnitReconcileReport, error) {
	return entity.UnitReconcileReport{}, nil
}

func (f *fakeCitableUnitRegistry) ReconcileBookIfDerived(context.Context, int) (entity.UnitReconcileReport, bool, error) {
	return entity.UnitReconcileReport{}, true, nil
}

func (f *fakeCitableUnitRegistry) AuditPass(context.Context) (entity.CitableAuditReport, error) {
	return entity.CitableAuditReport{}, nil
}

func (f *fakeCitableUnitRegistry) ResolveUnit(context.Context, string) (entity.UnitResolution, error) {
	return f.result, f.err
}

func TestEditorialGetCitableUnitExposesMachineGeneration(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 11, 12, 0, 0, 0, time.UTC)
	registry := &fakeCitableUnitRegistry{result: entity.UnitResolution{Unit: entity.CitableUnit{
		ID:                   "550e8400-e29b-41d4-a716-446655440000",
		Corpus:               entity.UnitCorpusKitab,
		BookID:               797,
		Kind:                 entity.UnitKindParagraph,
		Ordinal:              1,
		Position:             0,
		Anchor:               "kitab/797/h/0/u/1",
		Text:                 "نص مولد",
		TextNormalized:       "نص مولد",
		NormalizationVersion: 1,
		Occurrence:           1,
		Language:             "ar",
		ProvenanceClass:      entity.ProvenanceClassMachine,
		Generation: &entity.GenerationIdentity{
			RunID:         "550e8400-e29b-41d4-a716-446655440001",
			ModelID:       "model-v1",
			PromptVersion: "unit-enrichment-v1",
		},
		Lifecycle: entity.UnitLifecycleActive,
		CreatedAt: now,
		UpdatedAt: now,
	}}}

	app := fiber.New()
	controller := &V1{unitRegistry: registry, l: logger.New("error")}
	app.Get("/v1/editorial/citable-units/:id", controller.editorialGetCitableUnit)

	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/v1/editorial/citable-units/550e8400-e29b-41d4-a716-446655440000", http.NoBody)
	result, err := app.Test(request)
	require.NoError(t, err)

	defer result.Body.Close()

	assert.Equal(t, http.StatusOK, result.StatusCode)

	var body response.EditorialCitableUnitResolution
	require.NoError(t, json.NewDecoder(result.Body).Decode(&body))
	require.NotNil(t, body.Unit.Generation)
	assert.Equal(t, "model-v1", body.Unit.Generation.ModelID)
	assert.Equal(t, 1, body.Unit.NormalizationVersion)
	assert.Empty(t, body.Successors)
}

func TestEditorialGetCitableUnitNotFound(t *testing.T) {
	t.Parallel()

	app := fiber.New()
	controller := &V1{
		unitRegistry: &fakeCitableUnitRegistry{err: entity.ErrUnitNotFound},
		l:            logger.New("error"),
	}
	app.Get("/v1/editorial/citable-units/:id", controller.editorialGetCitableUnit)

	result, err := app.Test(httptest.NewRequestWithContext(t.Context(), http.MethodGet,
		"/v1/editorial/citable-units/550e8400-e29b-41d4-a716-446655440099", http.NoBody))
	require.NoError(t, err)

	defer result.Body.Close()

	assert.Equal(t, http.StatusNotFound, result.StatusCode)
}
