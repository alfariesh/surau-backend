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

const auditHashPageSize = 1000

type pagedHashAuditRepo interface {
	ListActiveUnitsForHashCheckPage(ctx context.Context, afterID string, limit int) ([]entity.CitableUnit, error)
}

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
//
//nolint:nestif,nlreturn,wsl_v5 // raw-first lineage is an explicit resumable sub-stage of the bounded conflict loop
func (uc *UseCase) ReconcileBook(ctx context.Context, bookID int) (entity.UnitReconcileReport, error) {
	var lastErr error

	for attempt := 1; attempt <= reconcileAttempts; attempt++ {
		src, err := uc.repo.LoadBookSource(ctx, bookID)
		if err != nil {
			return entity.UnitReconcileReport{}, err
		}

		snap, err := uc.repo.Snapshot(ctx, bookID)
		if err != nil {
			return entity.UnitReconcileReport{}, err
		}

		// Existing knowledge_mentions use offsets into the imported raw page.
		// On an edited book's first materialization, persist that raw snapshot
		// first, then immediately reconcile the effective editorial snapshot.
		// The second pass retires raw units through normal lineage, leaving an
		// exact historical target for the mention backfill. A crash between the
		// passes is resumable: active raw units make the next call skip this arm.
		if len(snap.Active) == 0 && sourceHasPublishedTextEdit(&src) {
			raw := RawBookSourceSnapshot(&src)
			rawDerived, _, err := DeriveBook(&raw)
			if err != nil {
				return entity.UnitReconcileReport{}, err
			}
			rawPlan := PlanBook(bookID, src.LoadedAt, rawDerived, &snap)
			rawPlan.Report.Attempts = attempt
			rawPlan.Intermediate = true
			if err := uc.repo.ApplyReconcile(ctx, &rawPlan); err != nil {
				if errors.Is(err, entity.ErrUnitReconcileConflict) {
					lastErr = err
					continue
				}

				return entity.UnitReconcileReport{}, err
			}

			return uc.ReconcileBook(ctx, bookID)
		}

		derived, _, err := DeriveBook(&src)
		if err != nil {
			return entity.UnitReconcileReport{}, err
		}

		plan := PlanBook(bookID, src.LoadedAt, derived, &snap)
		plan.Report.Attempts = attempt
		if sourceHasPublishedPageEdit(&src) && (len(snap.Active) == 0 || snapshotNeedsRawHistory(&snap)) {
			raw := RawBookSourceSnapshot(&src)
			rawDerived, _, err := DeriveBook(&raw)
			if err != nil {
				return entity.UnitReconcileReport{}, err
			}
			attachRawHistoricalUnits(bookID, src.LoadedAt, rawDerived, &snap, &plan)
		}

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

func snapshotNeedsRawHistory(snap *entity.UnitRegistrySnapshot) bool {
	for i := range snap.Active {
		unit := &snap.Active[i]
		if normalizedContentRole(unit.ContentRole) == entity.UnitContentRoleBookPage &&
			len(unit.SourceDocumentHash) == 0 {
			return true
		}
	}

	return false
}

func sourceHasPublishedPageEdit(src *entity.BookUnitSource) bool {
	for i := range src.Pages {
		page := &src.Pages[i]
		if page.HasPublishedEdit && page.RawContentText != "" {
			return true
		}
	}

	return false
}

func sourceHasPublishedTextEdit(src *entity.BookUnitSource) bool {
	for i := range src.Pages {
		page := &src.Pages[i]
		if page.HasPublishedEdit && page.RawContentText != "" && page.RawContentText != page.ContentText {
			return true
		}
	}

	return false
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
//
//nolint:nestif,wsl_v5 // paged and legacy repo adapters deliberately share one audit path
func (uc *UseCase) AuditPass(ctx context.Context) (entity.CitableAuditReport, error) {
	report, err := uc.repo.AuditCounts(ctx)
	if err != nil {
		return report, err
	}

	auditUnits := func(units []entity.CitableUnit) {
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
	}

	if paged, ok := uc.repo.(pagedHashAuditRepo); ok {
		afterID := ""
		for {
			units, err := paged.ListActiveUnitsForHashCheckPage(ctx, afterID, auditHashPageSize)
			if err != nil {
				return report, err
			}
			auditUnits(units)
			if len(units) < auditHashPageSize {
				break
			}
			afterID = units[len(units)-1].ID
		}
	} else {
		units, err := uc.repo.ListActiveUnitsForHashCheck(ctx)
		if err != nil {
			return report, err
		}
		auditUnits(units)
	}

	report.RanAt = time.Now()

	return report, nil
}

// ResolveUnit exposes the internal lineage walk (AC-2); the public anchor
// resolution surface is built by B-2 on top of it.
func (uc *UseCase) ResolveUnit(ctx context.Context, unitID string) (entity.UnitResolution, error) {
	return uc.repo.ResolveUnit(ctx, unitID)
}
