package unitregistry

import (
	"bytes"
	"maps"
	"sort"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
)

// attachRawHistoricalUnits augments an effective reconcile plan for a pre-K-1
// pilot. Active IDs and lifecycle decisions remain exactly those of the
// effective plan; raw-only units with a planner-proven alignment are inserted
// superseded and point at the final active set. Orphans are tombstoned rather
// than redirected heuristically. The repo applies both sets atomically under
// the C2 writer guard, so a crash cannot leave duplicate raw snapshots.
//
//nolint:funlen,gocognit,gocyclo,cyclop,nlreturn,wsl_v5 // deterministic lineage filtering stays in one pass
func attachRawHistoricalUnits(
	bookID int,
	loadedAt time.Time,
	rawDerived []DerivedUnit,
	snap *entity.UnitRegistrySnapshot,
	effective *entity.UnitReconcilePlan,
) {
	final := snapshotAfterPlan(snap, effective)
	rawPlan := PlanBook(bookID, loadedAt, rawDerived, &final)
	if len(rawPlan.Mints) == 0 {
		return
	}

	rawIDs := make(map[string]struct{}, len(rawPlan.Mints))
	for i := range rawPlan.Mints {
		unit := rawPlan.Mints[i]
		// The ordinary gap planner must prove a successor below. An orphan
		// remains tombstoned, so its Anchor cannot redirect to unrelated text.
		unit.Lifecycle = entity.UnitLifecycleTombstoned
		if unit.ProvenanceDetail == nil {
			unit.ProvenanceDetail = map[string]any{}
		}
		unit.ProvenanceDetail["raw_snapshot"] = true
		effective.HistoricalMints = append(effective.HistoricalMints, unit)
		rawIDs[unit.ID] = struct{}{}
	}

	edged := make(map[string]struct{}, len(rawPlan.Mints))
	seenEdge := make(map[string]struct{})
	historicalByID := make(map[string]*entity.CitableUnit, len(effective.HistoricalMints))
	for i := range effective.HistoricalMints {
		historicalByID[effective.HistoricalMints[i].ID] = &effective.HistoricalMints[i]
	}
	activeByID := make(map[string]*entity.CitableUnit, len(final.Active))
	for i := range final.Active {
		activeByID[final.Active[i].ID] = &final.Active[i]
	}
	edgesByActive := make(map[string][]entity.CitableUnitLineage)
	activeOrder := make([]string, 0)
	for _, edge := range rawPlan.Edges {
		if _, historical := rawIDs[edge.SuccessorID]; historical {
			if _, seen := edgesByActive[edge.PredecessorID]; !seen {
				activeOrder = append(activeOrder, edge.PredecessorID)
			}
			edgesByActive[edge.PredecessorID] = append(edgesByActive[edge.PredecessorID], edge)
		}
	}
	for _, activeID := range activeOrder {
		candidates := edgesByActive[activeID]
		active := activeByID[activeID]
		if active == nil {
			continue
		}
		hasExact := false
		for _, edge := range candidates {
			historical := historicalByID[edge.SuccessorID]
			if historicalEdgeSameSlot(historical, active) && historicalContentEqual(historical, active) {
				hasExact = true
				break
			}
		}
		for _, edge := range candidates {
			historical := historicalByID[edge.SuccessorID]
			if !historicalEdgeSameSlot(historical, active) ||
				(hasExact && !historicalContentEqual(historical, active)) {
				continue
			}
			addHistoricalEdge(effective, seenEdge, edge.SuccessorID, activeID)
			edged[edge.SuccessorID] = struct{}{}
		}
	}

	for i := range effective.HistoricalMints {
		historical := &effective.HistoricalMints[i]
		if _, ok := edged[historical.ID]; ok {
			historical.Lifecycle = entity.UnitLifecycleSuperseded
		}
	}

	effective.Report.Minted += len(effective.HistoricalMints)
	for i := range effective.HistoricalMints {
		if effective.HistoricalMints[i].Lifecycle == entity.UnitLifecycleSuperseded {
			effective.Report.Superseded++
		} else {
			effective.Report.Tombstoned++
		}
	}
	recountPlanEdges(&effective.Report, effective.Edges)
}

func historicalEdgeSameSlot(historical, active *entity.CitableUnit) bool {
	return historical != nil && active != nil &&
		scopeKeyOf(historical.HeadingID) == scopeKeyOf(active.HeadingID) &&
		sameOptionalInt(historical.PageID, active.PageID) &&
		normalizedContentRole(historical.ContentRole) == normalizedContentRole(active.ContentRole) &&
		normalizedLanguage(historical.Language) == normalizedLanguage(active.Language)
}

func historicalContentEqual(historical, active *entity.CitableUnit) bool {
	return historical.Kind == active.Kind && bytes.Equal(historical.ContentHash, active.ContentHash)
}

//nolint:gocyclo,cyclop,wsl_v5 // snapshot projection applies each plan phase in order
func snapshotAfterPlan(
	snap *entity.UnitRegistrySnapshot,
	plan *entity.UnitReconcilePlan,
) entity.UnitRegistrySnapshot {
	final := entity.UnitRegistrySnapshot{
		MaxOrdinalByScope: make(map[int]int, len(snap.MaxOrdinalByScope)),
		ExistingIDs:       make(map[string]struct{}, len(snap.ExistingIDs)+len(plan.Mints)),
	}
	maps.Copy(final.MaxOrdinalByScope, snap.MaxOrdinalByScope)
	for id := range snap.ExistingIDs {
		final.ExistingIDs[id] = struct{}{}
	}

	active := make(map[string]entity.CitableUnit, len(snap.Active)+len(plan.Mints))
	for i := range snap.Active {
		active[snap.Active[i].ID] = snap.Active[i]
	}
	for _, update := range plan.Updates {
		unit, ok := active[update.ID]
		if !ok {
			continue
		}
		unit.Position = update.Position
		unit.PageID = update.PageID
		unit.ParentUnitID = update.ParentUnitID
		unit.HTML = update.HTML
		unit.ReviewStatus = update.ReviewStatus
		unit.ProvenanceDetail = update.ProvenanceDetail
		unit.SourceDocumentHash = update.SourceDocumentHash
		unit.SourceCharStart = update.SourceCharStart
		unit.SourceCharEnd = update.SourceCharEnd
		active[update.ID] = unit
	}
	for _, retire := range plan.Retires {
		delete(active, retire.ID)
	}
	for i := range plan.Mints {
		unit := plan.Mints[i]
		active[unit.ID] = unit
		final.ExistingIDs[unit.ID] = struct{}{}
		scope := scopeKeyOf(unit.HeadingID)
		if unit.Ordinal > final.MaxOrdinalByScope[scope] {
			final.MaxOrdinalByScope[scope] = unit.Ordinal
		}
	}

	for id := range active {
		final.Active = append(final.Active, active[id])
	}
	sort.Slice(final.Active, func(i, j int) bool {
		left, right := &final.Active[i], &final.Active[j]
		if scopeKeyOf(left.HeadingID) != scopeKeyOf(right.HeadingID) {
			return scopeKeyOf(left.HeadingID) < scopeKeyOf(right.HeadingID)
		}
		if left.Position != right.Position {
			return left.Position < right.Position
		}

		return left.Ordinal < right.Ordinal
	})

	return final
}

//nolint:wsl_v5 // dedupe marker and append are one atomic planning step
func addHistoricalEdge(plan *entity.UnitReconcilePlan, seen map[string]struct{}, predecessor, successor string) {
	key := predecessor + "\x00" + successor
	if _, exists := seen[key]; exists {
		return
	}

	seen[key] = struct{}{}
	plan.Edges = append(plan.Edges, entity.CitableUnitLineage{
		PredecessorID: predecessor,
		SuccessorID:   successor,
		Reason:        entity.UnitLineageReasonEdit,
	})
}

//nolint:wsl_v5 // counters are initialized immediately before the edge pass
func recountPlanEdges(report *entity.UnitReconcileReport, edges []entity.CitableUnitLineage) {
	report.EditEdges = 0
	report.MoveEdges = 0
	for _, edge := range edges {
		if edge.Reason == entity.UnitLineageReasonEdit {
			report.EditEdges++
		} else {
			report.MoveEdges++
		}
	}
}
