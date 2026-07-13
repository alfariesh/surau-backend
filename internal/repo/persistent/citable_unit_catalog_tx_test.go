package persistent

import (
	"errors"
	"fmt"
	"testing"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestMapCatalogTransactionErrorSerializationIsRetryableConflict(t *testing.T) {
	t.Parallel()

	err := mapCatalogTransactionError(fmt.Errorf("apply: %w", &pgconn.PgError{Code: "40001"}))
	if !errors.Is(err, entity.ErrUnitReconcileConflict) {
		t.Fatalf("error = %v, want ErrUnitReconcileConflict", err)
	}
}

func TestMapCatalogTransactionErrorPreservesNonSerializationFailure(t *testing.T) {
	t.Parallel()

	want := &pgconn.PgError{Code: "23514"}
	if got := mapCatalogTransactionError(want); !errors.Is(got, want) {
		t.Fatalf("error = %v, want original %v", got, want)
	}
}
