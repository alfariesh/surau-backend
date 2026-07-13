package unitregistry

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/repo"
)

var (
	errCatalogTransactionUnsupported = errors.New("citable catalog transaction is unsupported")
	errCatalogBookHasNoUnits         = errors.New("published book produced zero citable units")
	errCatalogSourceDrift            = errors.New("published source changed during catalog reconcile")
	errCatalogDeterminism            = errors.New("citable catalog determinism mismatch")
)

// ReconcileCatalogBook is K-1's atomic one-book operation. Unlike the
// editorial hook's ReconcileBook, this method includes raw history, effective
// registry state, exact mention binding, verification, and durable queue
// completion in one repo-owned transaction.
//
//nolint:funlen,gocognit,gocyclo,cyclop,wsl_v5 // transaction boundary keeps every one-book stage visibly linear
func (uc *UseCase) ReconcileCatalogBook(
	ctx context.Context,
	request entity.CitableCatalogReconcileRequest,
) (entity.CitableCatalogReconcileResult, error) {
	catalogRepo, ok := uc.repo.(repo.CitableUnitCatalogRepo)
	if !ok {
		return entity.CitableCatalogReconcileResult{}, errCatalogTransactionUnsupported
	}

	var result entity.CitableCatalogReconcileResult
	err := catalogRepo.WithCatalogTransaction(ctx, request.BookID, func(tx repo.CitableUnitCatalogTx) error {
		beforeSource, err := tx.SourceFingerprint(ctx, request.BookID)
		if err != nil {
			return err
		}
		beforeRegistry, err := tx.RegistryChecksum(ctx, request.BookID)
		if err != nil {
			return err
		}

		source, err := tx.LoadBookSource(ctx, request.BookID)
		if err != nil {
			return err
		}
		snapshot, err := tx.Snapshot(ctx, request.BookID)
		if err != nil {
			return err
		}
		derived, _, err := DeriveBook(&source)
		if err != nil {
			return err
		}
		if len(derived) == 0 {
			return fmt.Errorf("%w: book %d", errCatalogBookHasNoUnits, request.BookID)
		}

		plan := PlanBook(request.BookID, source.LoadedAt, derived, &snapshot)
		plan.Report.Attempts = 1
		if sourceHasPublishedPageEdit(&source) &&
			(len(snapshot.Active) == 0 || snapshotNeedsRawHistory(&snapshot)) {
			raw := RawBookSourceSnapshot(&source)
			rawDerived, _, err := DeriveBook(&raw)
			if err != nil {
				return err
			}
			attachRawHistoricalUnits(request.BookID, source.LoadedAt, rawDerived, &snapshot, &plan)
		}

		if err := tx.ApplyReconcile(ctx, &plan); err != nil {
			return err
		}
		if err := tx.BindKnowledgeMentions(ctx, request.BookID); err != nil {
			return err
		}

		afterSource, err := tx.SourceFingerprint(ctx, request.BookID)
		if err != nil {
			return err
		}
		if !bytes.Equal(beforeSource[:], afterSource[:]) {
			return fmt.Errorf("%w: book %d", errCatalogSourceDrift, request.BookID)
		}
		afterRegistry, err := tx.RegistryChecksum(ctx, request.BookID)
		if err != nil {
			return err
		}
		if request.Rederive && (plan.Report.Minted != 0 || plan.Report.Updated != 0 ||
			plan.Report.Superseded != 0 || plan.Report.Tombstoned != 0 || beforeRegistry != afterRegistry) {
			return fmt.Errorf(
				"%w: book %d minted=%d updated=%d superseded=%d tombstoned=%d checksum_equal=%t",
				errCatalogDeterminism, request.BookID, plan.Report.Minted, plan.Report.Updated,
				plan.Report.Superseded, plan.Report.Tombstoned, beforeRegistry == afterRegistry,
			)
		}
		if err := tx.CompleteQueueItem(ctx, request.JobName, request.BookID, afterSource, afterRegistry); err != nil {
			return err
		}

		result = entity.CitableCatalogReconcileResult{
			Report:            plan.Report,
			SourceFingerprint: afterSource,
			RegistryChecksum:  afterRegistry,
		}

		return nil
	})
	if err != nil {
		return entity.CitableCatalogReconcileResult{}, err
	}

	return result, nil
}
