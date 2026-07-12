package unitregistry

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/repo"
	"github.com/alfariesh/surau-backend/internal/searchtext"
)

// reconcileAttempts bounds optimistic-conflict retries: two concurrent
// reconciles of one book serialize on the advisory lock, the loser replans.
const reconcileAttempts = 3

// UseCase is the single write service of the Citable Unit registry.
type UseCase struct {
	repo      repo.CitableUnitRepo
	quranRepo repo.QuranCitableUnitRepo
}

// New -.
func New(r repo.CitableUnitRepo) *UseCase {
	uc := &UseCase{repo: r}
	if quranRepo, ok := r.(repo.QuranCitableUnitRepo); ok {
		uc.quranRepo = quranRepo
	}

	return uc
}

// ReconcileBook derives the book from its effective source and reconciles the
// registry to match: unchanged content keeps its ids (AC-1), changed content
// mints successors and retires predecessors with lineage (AC-2).
func (uc *UseCase) ReconcileBook(ctx context.Context, bookID int) (entity.UnitReconcileReport, error) {
	var lastErr error

	for attempt := 1; attempt <= reconcileAttempts; attempt++ {
		src, err := uc.repo.LoadBookSource(ctx, bookID)
		if err != nil {
			return entity.UnitReconcileReport{}, err
		}

		derived, _, err := DeriveBook(&src)
		if err != nil {
			return entity.UnitReconcileReport{}, err
		}

		snap, err := uc.repo.Snapshot(ctx, bookID)
		if err != nil {
			return entity.UnitReconcileReport{}, err
		}

		plan := PlanBook(bookID, src.LoadedAt, derived, &snap)
		plan.Report.Attempts = attempt

		err = uc.repo.ApplyReconcile(ctx, &plan)
		if err == nil {
			return plan.Report, nil
		}

		if !errors.Is(err, entity.ErrUnitReconcileConflict) {
			return entity.UnitReconcileReport{}, err
		}

		lastErr = err
	}

	return entity.UnitReconcileReport{}, fmt.Errorf("reconcile book %d: %w", bookID, lastErr)
}

// ReconcileBookIfDerived is the editorial-publish hook entry: books that never
// went through the initial backfill (units_derived_at IS NULL) are skipped, so
// non-pilot publishes cost nothing.
func (uc *UseCase) ReconcileBookIfDerived(ctx context.Context, bookID int) (entity.UnitReconcileReport, bool, error) {
	derivedAt, err := uc.repo.BookDerivedAt(ctx, bookID)
	if err != nil {
		return entity.UnitReconcileReport{}, false, err
	}

	if derivedAt == nil {
		return entity.UnitReconcileReport{}, true, nil
	}

	report, err := uc.ReconcileBook(ctx, bookID)

	return report, false, err
}

// AuditPass runs one scheduled integrity audit: SQL invariant counts plus the
// Go-side hash/normalization recompute over every active unit — the tripwire
// that catches writes which bypassed the service (AC-3/AC-4).
func (uc *UseCase) AuditPass(ctx context.Context) (entity.CitableAuditReport, error) {
	report, err := uc.repo.AuditCounts(ctx)
	if err != nil {
		return report, err
	}

	units, err := uc.repo.ListActiveUnitsForHashCheck(ctx)
	if err != nil {
		return report, err
	}

	for i := range units {
		u := &units[i]

		marker := ""
		if u.Marker != nil {
			marker = *u.Marker
		}

		if !bytes.Equal(ContentHash(u.Kind, marker, u.Text), u.ContentHash) {
			report.Violations.HashMismatch++

			continue
		}

		if u.NormalizationVersion == searchtext.ProfileVersion &&
			u.TextNormalized != searchtext.Normalize(u.Text) {
			report.Violations.HashMismatch++
		}
	}

	report.RanAt = time.Now()

	return report, nil
}

// ResolveUnit exposes the internal lineage walk (AC-2); the public anchor
// resolution surface is built by B-2 on top of it.
func (uc *UseCase) ResolveUnit(ctx context.Context, unitID string) (entity.UnitResolution, error) {
	return uc.repo.ResolveUnit(ctx, unitID)
}
