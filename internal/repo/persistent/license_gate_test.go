package persistent

import (
	"errors"
	"testing"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errDatabaseUnavailable = errors.New("database unavailable")

func TestMapLicensePublishError(t *testing.T) {
	t.Parallel()

	require.ErrorIs(t, mapLicensePublishError(&pgconn.PgError{
		Code:           "23514",
		ConstraintName: licensePublishConstraint,
	}), entity.ErrLicenseNotPermitted)
	require.ErrorIs(t, mapLicensePublishError(&pgconn.PgError{
		Code:    "P0001",
		Message: "license_not_permitted",
	}), entity.ErrLicenseNotPermitted)

	plain := errDatabaseUnavailable
	require.ErrorIs(t, mapLicensePublishError(plain), plain)
}

func TestProductionPublishCheckIncludesLicenseGate(t *testing.T) {
	t.Parallel()

	complete := entity.BookProductionCompleteness{
		Ready:         true,
		RequiredCount: 2,
		CompleteCount: 2,
		Missing:       []entity.BookProductionMissingAsset{},
	}

	permitted := productionPublishCheckFromCompleteness(complete, entity.LicenseStatusPermitted)
	assert.True(t, permitted.CanPublish)
	assert.Equal(t, entity.LicenseStatusPermitted, permitted.LicenseStatus)
	assert.Empty(t, permitted.BlockingErrors)

	unknown := productionPublishCheckFromCompleteness(complete, entity.LicenseStatusUnknown)
	assert.True(t, unknown.Ready, "asset readiness stays independent from the license decision")
	assert.False(t, unknown.CanPublish)
	assert.Equal(t, entity.LicenseStatusUnknown, unknown.LicenseStatus)
	require.Len(t, unknown.BlockingErrors, 1)
	assert.Equal(t, "license_not_permitted", unknown.BlockingErrors[0].Code)
}
