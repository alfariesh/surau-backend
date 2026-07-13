package unitregistry

import (
	"bytes"
	"reflect"
	"sort"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/searchtext"
)

// gapEdgeCap bounds M×N lineage edges per (gap, kindClass); beyond it a full
// page rewrite degrades to an order-preserving zip (counted in the report as a
// parser-quality signal, not an error).
const gapEdgeCap = 32

type matchPair struct {
	derivedIdx int // global index into the derived slice
	active     *entity.CitableUnit
}

type retireRef struct {
	unit     *entity.CitableUnit
	hasEdge  bool
	scopeKey int
}

type mintRef struct {
	planIdx    int // index into plan.Mints
	derivedIdx int
}

// planBuilder threads the shared reconcile state so PlanBook decomposes into
// per-phase methods instead of one monolith. It is single-use per book.
type planBuilder struct {
	bookID  int
	derived []DerivedUnit
	snap    *entity.UnitRegistrySnapshot
	plan    *entity.UnitReconcilePlan

	unitIDByDerivedIdx []string
	plannedIDs         map[string]struct{}
	mintRefs           []mintRef
	retireRefs         []*retireRef
	retireByID         map[string]*retireRef
	matchedPairs       []matchPair
	// Matched active units kept active, for the rescue-pass twin lookup:
	// scopeKey → rescueKey → units ordered by ordinal.
	activeTwins map[int]map[string][]*entity.CitableUnit
}

// PlanBook reconciles the derived unit list against the registry snapshot and
// returns the atomic write set. Pure function: given the same derived slice
// and snapshot it always returns the same plan, and for an unchanged source it
// returns a plan with zero mints/retires/updates (AC-1).
func PlanBook(bookID int, loadedAt time.Time, derived []DerivedUnit, snap *entity.UnitRegistrySnapshot) entity.UnitReconcilePlan {
	plan := entity.UnitReconcilePlan{
		BookID:   bookID,
		LoadedAt: loadedAt,
		BasedOn:  snap.Fingerprint,
		Report:   entity.UnitReconcileReport{BookID: bookID, Derived: len(derived)},
	}
	b := &planBuilder{
		bookID:             bookID,
		derived:            derived,
		snap:               snap,
		plan:               &plan,
		unitIDByDerivedIdx: make([]string, len(derived)),
		plannedIDs:         make(map[string]struct{}),
		mintRefs:           make([]mintRef, 0),
		retireRefs:         make([]*retireRef, 0),
		retireByID:         make(map[string]*retireRef),
		matchedPairs:       make([]matchPair, 0, len(derived)),
		activeTwins:        make(map[int]map[string][]*entity.CitableUnit),
	}

	derivedByScope, activeByScope, scopeOrder := b.groupScopes()

	plan.Report.Scopes = len(scopeOrder)
	for _, scopeKey := range scopeOrder {
		b.reconcileScope(scopeKey, derivedByScope[scopeKey], activeByScope[scopeKey])
	}

	b.resolveFootnoteParents()
	b.stageUpdates()
	b.rescuePass()
	b.finalizeRetires()
	b.fillReport()

	return plan
}

// groupScopes buckets derived and active units by scope, preserving derived
// first-appearance order and appending fully-retired (active-only) scopes in
// sorted order for determinism.
func (b *planBuilder) groupScopes() (derivedByScope map[int][]int, activeByScope map[int][]*entity.CitableUnit, scopeOrder []int) {
	derivedByScope = make(map[int][]int)
	scopeOrder = make([]int, 0)

	for i := range b.derived {
		key := scopeKeyOf(b.derived[i].HeadingID)
		if _, seen := derivedByScope[key]; !seen {
			scopeOrder = append(scopeOrder, key)
		}

		derivedByScope[key] = append(derivedByScope[key], i)
	}

	activeByScope = make(map[int][]*entity.CitableUnit)

	for i := range b.snap.Active {
		u := &b.snap.Active[i]
		key := scopeKeyOf(u.HeadingID)
		activeByScope[key] = append(activeByScope[key], u)
	}

	activeOnly := make([]int, 0)

	for key := range activeByScope {
		if _, ok := derivedByScope[key]; !ok {
			activeOnly = append(activeOnly, key)
		}
	}

	sort.Ints(activeOnly)
	scopeOrder = append(scopeOrder, activeOnly...)

	return derivedByScope, activeByScope, scopeOrder
}

// reconcileScope runs match → LIS → gap-lineage → mint for one scope.
func (b *planBuilder) reconcileScope(scopeKey int, dIdxs []int, actives []*entity.CitableUnit) {
	sort.SliceStable(actives, func(i, j int) bool { return actives[i].Position < actives[j].Position })

	scopePairs, unmatchedDerived := b.matchScope(scopeKey, dIdxs, actives)
	b.matchedPairs = append(b.matchedPairs, scopePairs...)

	unmatchedActive := make([]*entity.CitableUnit, 0)

	for _, u := range actives {
		if !unitMatched(scopePairs, u.ID) {
			unmatchedActive = append(unmatchedActive, u)
		}
	}

	// LIS over matched pairs (walked in derived order, strictly increasing
	// active position) anchors gap boundaries; off-LIS matches keep identity
	// but do not bound gaps.
	anchors := longestIncreasingByActivePos(scopePairs)
	removedByGap, addedByGap, gapKeys := b.assignGaps(anchors, unmatchedActive, unmatchedDerived)

	b.mintScope(scopeKey, dIdxs)
	b.lineageGaps(scopeKey, gapKeys, removedByGap, addedByGap)
}

// matchScope pairs the k-th derived instance of (kind, hash) with the k-th
// active instance, recording twins for the rescue pass.
func (b *planBuilder) matchScope(scopeKey int, dIdxs []int, actives []*entity.CitableUnit) (pairs []matchPair, unmatchedDerived []int) {
	queues := make(map[string][]*entity.CitableUnit)

	for _, u := range actives {
		key := matchKey(normalizedContentRole(u.ContentRole), normalizedLanguage(u.Language), u.Kind,
			u.ContentHash, u.ProvenanceClass, u.GenerationRunID)
		queues[key] = append(queues[key], u)
	}

	pairs = make([]matchPair, 0, len(dIdxs))
	unmatchedDerived = make([]int, 0)

	for _, di := range dIdxs {
		d := &b.derived[di]
		key := matchKey(d.ContentRole, d.Language, d.Kind, d.ContentHash, d.ProvenanceClass, d.GenerationRunID)

		q := queues[key]
		if len(q) == 0 {
			unmatchedDerived = append(unmatchedDerived, di)

			continue
		}

		u := q[0]
		queues[key] = q[1:]

		pairs = append(pairs, matchPair{derivedIdx: di, active: u})
		b.unitIDByDerivedIdx[di] = u.ID

		twinKey := rescueKey(normalizedContentRole(u.ContentRole), normalizedLanguage(u.Language),
			u.Kind, u.Marker != nil, u.ContentHash)

		if b.activeTwins[scopeKey] == nil {
			b.activeTwins[scopeKey] = make(map[string][]*entity.CitableUnit)
		}

		b.activeTwins[scopeKey][twinKey] = append(b.activeTwins[scopeKey][twinKey], u)
	}

	return pairs, unmatchedDerived
}

type gapKey struct {
	gap   int
	class string
}

// assignGaps places each unmatched unit into the gap between the LIS anchors
// bounding its position, bucketed by kindClass.
func (b *planBuilder) assignGaps(
	anchors []matchPair,
	unmatchedActive []*entity.CitableUnit,
	unmatchedDerived []int,
) (removedByGap map[gapKey][]*entity.CitableUnit, addedByGap map[gapKey][]int, gapKeys []gapKey) {
	gapOfPos := func(pos int) int {
		n := 0

		for _, a := range anchors {
			if a.active.Position < pos {
				n++
			}
		}

		return n
	}
	gapOfDerived := func(di int) int {
		pos := b.derived[di].ScopePosition
		n := 0

		for _, a := range anchors {
			if b.derived[a.derivedIdx].ScopePosition < pos {
				n++
			}
		}

		return n
	}

	removedByGap = make(map[gapKey][]*entity.CitableUnit)
	addedByGap = make(map[gapKey][]int)
	gapKeys = make([]gapKey, 0)
	seen := make(map[gapKey]bool)
	note := func(k gapKey) {
		if !seen[k] {
			seen[k] = true
			gapKeys = append(gapKeys, k)
		}
	}

	for _, u := range unmatchedActive {
		k := gapKey{gap: gapOfPos(u.Position), class: normalizedContentRole(u.ContentRole) + "\x00" +
			normalizedLanguage(u.Language) + "\x00" + kindClass(u.Kind, u.Marker != nil)}
		note(k)
		removedByGap[k] = append(removedByGap[k], u)
	}

	for _, di := range unmatchedDerived {
		d := &b.derived[di]
		k := gapKey{gap: gapOfDerived(di), class: d.ContentRole + "\x00" + d.Language + "\x00" +
			kindClass(d.Kind, d.Marker != "")}
		note(k)
		addedByGap[k] = append(addedByGap[k], di)
	}

	return removedByGap, addedByGap, gapKeys
}

// mintScope mints unmatched derived units in document order, minting an ordinal
// past the scope high-water mark and probing occurrence for an unused id (so a
// revert after supersede never recycles a retired id).
func (b *planBuilder) mintScope(scopeKey int, dIdxs []int) {
	nextOrdinal := b.snap.MaxOrdinalByScope[scopeKey]
	dupSoFar := make(map[string]int)

	for _, di := range dIdxs {
		d := &b.derived[di]
		key := matchKey(d.ContentRole, d.Language, d.Kind, d.ContentHash, d.ProvenanceClass, d.GenerationRunID)
		dupSoFar[key]++

		if b.unitIDByDerivedIdx[di] != "" {
			continue // matched
		}

		occurrence := dupSoFar[key]

		id := derivedUnitID(b.bookID, scopeKey, d, occurrence)
		for {
			_, existed := b.snap.ExistingIDs[id]

			_, planned := b.plannedIDs[id]
			if !existed && !planned {
				break
			}

			occurrence++
			id = derivedUnitID(b.bookID, scopeKey, d, occurrence)
		}

		b.plannedIDs[id] = struct{}{}
		nextOrdinal++
		b.unitIDByDerivedIdx[di] = id

		unit := mintUnit(b.bookID, scopeKey, id, nextOrdinal, occurrence, d)
		b.plan.Mints = append(b.plan.Mints, unit)
		b.mintRefs = append(b.mintRefs, mintRef{planIdx: len(b.plan.Mints) - 1, derivedIdx: di})
	}
}

// lineageGaps draws supersede edges inside each gap (full M:N within the cap,
// order-preserving zip beyond it) and records the removed units for retirement.
//
//nolint:gocognit // three flat cases per gap (no additions / within cap M:N / over-cap zip)
func (b *planBuilder) lineageGaps(
	scopeKey int,
	gapKeys []gapKey,
	removedByGap map[gapKey][]*entity.CitableUnit,
	addedByGap map[gapKey][]int,
) {
	for _, k := range gapKeys {
		removed := removedByGap[k]
		if len(removed) == 0 {
			continue
		}

		for _, u := range removed {
			ref := &retireRef{unit: u, scopeKey: scopeKey}
			b.retireRefs = append(b.retireRefs, ref)
			b.retireByID[u.ID] = ref
		}

		added := addedByGap[k]
		if len(added) == 0 {
			continue
		}

		if len(removed)*len(added) <= gapEdgeCap {
			for _, u := range removed {
				for _, di := range added {
					b.addEdge(u.ID, b.unitIDByDerivedIdx[di], entity.UnitLineageReasonEdit)
					b.retireByID[u.ID].hasEdge = true
				}
			}

			continue
		}

		b.plan.Report.CappedGaps++

		for i, u := range removed {
			target := added[len(added)-1]
			if i < len(added) {
				target = added[i]
			}

			b.addEdge(u.ID, b.unitIDByDerivedIdx[target], entity.UnitLineageReasonEdit)
			b.retireByID[u.ID].hasEdge = true
		}
	}
}

// resolveFootnoteParents points minted footnotes at their owning body unit now
// that every derived index has an id.
func (b *planBuilder) resolveFootnoteParents() {
	for _, ref := range b.mintRefs {
		d := &b.derived[ref.derivedIdx]
		if d.ParentIdx >= 0 {
			parentID := b.unitIDByDerivedIdx[d.ParentIdx]
			b.plan.Mints[ref.planIdx].ParentUnitID = &parentID
		}
	}
}

// stageUpdates records locator changes for matched units (position/page/parent
// re-point); identity fields never change (B-D11).
func (b *planBuilder) stageUpdates() {
	for _, pair := range b.matchedPairs {
		d := &b.derived[pair.derivedIdx]

		var wantParent *string

		if d.ParentIdx >= 0 {
			id := b.unitIDByDerivedIdx[d.ParentIdx]
			wantParent = &id
		}

		if pair.active.Position == d.ScopePosition &&
			samePage(pair.active.PageID, d.PageID) &&
			sameParent(pair.active.ParentUnitID, wantParent) &&
			sameOptionalString(pair.active.HTML, optionalString(d.HTML)) &&
			pair.active.ReviewStatus == d.ReviewStatus &&
			reflect.DeepEqual(pair.active.ProvenanceDetail, derivedProvenanceDetail(d)) &&
			bytes.Equal(pair.active.SourceDocumentHash, d.SourceDocumentHash) &&
			sameRuneOffset(pair.active.SourceCharStart, d.SourceCharStart) &&
			sameRuneOffset(pair.active.SourceCharEnd, d.SourceCharEnd) {
			continue
		}

		pageID := d.PageID
		update := entity.UnitPlanUpdate{
			ID:                 pair.active.ID,
			Position:           d.ScopePosition,
			PageID:             &pageID,
			ParentUnitID:       wantParent,
			HTML:               optionalString(d.HTML),
			ReviewStatus:       d.ReviewStatus,
			ProvenanceDetail:   derivedProvenanceDetail(d),
			SourceDocumentHash: d.SourceDocumentHash,
			SourceCharStart:    new(d.SourceCharStart),
			SourceCharEnd:      new(d.SourceCharEnd),
		}
		// A footnote's parent linkage changed (the update fires only when
		// position/page/parent moved), so refresh its footnote_link label —
		// otherwise a footnote that just became unlinked keeps 'marker'/
		// 'fallback' and the audit footnote_parent check false-positives.
		if isDerivedFootnote(d) {
			link := d.FootnoteLink
			update.FootnoteLink = &link
		}

		b.plan.Updates = append(b.plan.Updates, update)
	}
}

// rescuePass links retired-without-successor content that reappears elsewhere
// this run (scope move, twin deletion) to a minted unit or a surviving active
// twin — a content_move edge instead of a dead tombstone.
//
//nolint:gocognit // one pass over retire refs: skip-if-edged, mint-target, else twin-target
func (b *planBuilder) rescuePass() {
	rescueTargets := make(map[string][]string) // rescueKey → minted ids in doc order

	for _, ref := range b.mintRefs {
		d := &b.derived[ref.derivedIdx]
		k := rescueKey(d.ContentRole, d.Language, d.Kind, d.Marker != "", d.ContentHash)
		rescueTargets[k] = append(rescueTargets[k], b.plan.Mints[ref.planIdx].ID)
	}

	sort.SliceStable(b.retireRefs, func(i, j int) bool {
		if b.retireRefs[i].scopeKey != b.retireRefs[j].scopeKey {
			return b.retireRefs[i].scopeKey < b.retireRefs[j].scopeKey
		}

		return b.retireRefs[i].unit.Ordinal < b.retireRefs[j].unit.Ordinal
	})

	for _, ref := range b.retireRefs {
		if ref.hasEdge {
			continue
		}

		k := rescueKey(normalizedContentRole(ref.unit.ContentRole), normalizedLanguage(ref.unit.Language),
			ref.unit.Kind, ref.unit.Marker != nil, ref.unit.ContentHash)
		if targets := rescueTargets[k]; len(targets) > 0 {
			b.addEdge(ref.unit.ID, targets[0], entity.UnitLineageReasonContentMove)
			rescueTargets[k] = targets[1:]
			ref.hasEdge = true

			continue
		}

		if twins := b.activeTwins[ref.scopeKey][k]; len(twins) > 0 {
			best := twins[0]
			for _, t := range twins[1:] {
				if t.Ordinal < best.Ordinal {
					best = t
				}
			}

			b.addEdge(ref.unit.ID, best.ID, entity.UnitLineageReasonContentMove)
			ref.hasEdge = true
		}
	}
}

// finalizeRetires stamps each retired unit superseded (has a successor) or
// tombstoned (none).
func (b *planBuilder) finalizeRetires() {
	for _, ref := range b.retireRefs {
		lifecycle := entity.UnitLifecycleTombstoned
		if ref.hasEdge {
			lifecycle = entity.UnitLifecycleSuperseded
			b.plan.Report.Superseded++
		} else {
			b.plan.Report.Tombstoned++
		}

		b.plan.Retires = append(b.plan.Retires, entity.UnitPlanRetire{ID: ref.unit.ID, Lifecycle: lifecycle})
	}
}

func (b *planBuilder) fillReport() {
	b.plan.Report.Matched = len(b.matchedPairs)
	b.plan.Report.Minted = len(b.plan.Mints)

	b.plan.Report.Updated = len(b.plan.Updates)
	for _, e := range b.plan.Edges {
		if e.Reason == entity.UnitLineageReasonEdit {
			b.plan.Report.EditEdges++
		} else {
			b.plan.Report.MoveEdges++
		}
	}

	for i := range b.derived {
		if b.derived[i].Kind == entity.UnitKindHTML {
			b.plan.Report.HTMLUnits++
		}
	}

	b.plan.ExpectedActive = int64(len(b.matchedPairs) + len(b.plan.Mints))
}

func (b *planBuilder) addEdge(pred, succID, reason string) {
	b.plan.Edges = append(b.plan.Edges, entity.CitableUnitLineage{
		PredecessorID: pred,
		SuccessorID:   succID,
		Reason:        reason,
	})
}

func mintUnit(bookID, scopeKey int, id string, ordinal, occurrence int, d *DerivedUnit) entity.CitableUnit {
	var headingID *int

	if scopeKey != frontMatterHeadingID {
		h := scopeKey
		headingID = &h
	}

	pageID := d.PageID

	var marker *string

	if d.Marker != "" {
		m := d.Marker
		marker = &m
	}

	var html *string

	if d.HTML != "" {
		h := d.HTML
		html = &h
	}

	detail := derivedProvenanceDetail(d)

	bookIDValue := bookID

	return entity.CitableUnit{
		ID:                   id,
		Corpus:               entity.UnitCorpusKitab,
		BookID:               &bookIDValue,
		HeadingID:            headingID,
		PageID:               &pageID,
		Kind:                 d.Kind,
		Ordinal:              ordinal,
		Position:             d.ScopePosition,
		Anchor:               AnchorFor(bookID, scopeKey, ordinal),
		Marker:               marker,
		Text:                 d.Text,
		HTML:                 html,
		TextNormalized:       searchtext.Normalize(d.Text),
		NormalizationVersion: searchtext.ProfileVersion,
		ContentHash:          d.ContentHash,
		Occurrence:           occurrence,
		Language:             d.Language,
		ContentRole:          d.ContentRole,
		ReviewStatus:         d.ReviewStatus,
		SourceDocumentHash:   d.SourceDocumentHash,
		SourceCharStart:      new(d.SourceCharStart),
		SourceCharEnd:        new(d.SourceCharEnd),
		ProvenanceClass:      d.ProvenanceClass,
		GenerationRunID:      d.GenerationRunID,
		ProvenanceDetail:     detail,
		Lifecycle:            entity.UnitLifecycleActive,
	}
}

//nolint:wsl_v5 // provenance fields are deliberately assembled in contract order
func derivedProvenanceDetail(d *DerivedUnit) map[string]any {
	detail := map[string]any{}
	if d.ProvenanceClass == entity.ProvenanceClassEditorial && d.EditActorID != "" {
		detail["edit_actor_id"] = d.EditActorID
	}
	if d.ProvenanceClass == entity.ProvenanceClassSource && d.ReleaseKey != "" {
		detail["release"] = d.ReleaseKey
	}
	if d.FormattingEditActorID != "" {
		detail["format_edit_actor_id"] = d.FormattingEditActorID
	}
	if isDerivedFootnote(d) {
		detail["footnote_link"] = d.FootnoteLink
	}

	return detail
}

func isDerivedFootnote(d *DerivedUnit) bool {
	return d.Kind == entity.UnitKindFootnote || d.Marker != ""
}

func derivedUnitID(bookID, scopeKey int, d *DerivedUnit, occurrence int) string {
	if d.ContentRole == entity.UnitContentRoleBookPage && d.Language == "ar" {
		return UnitID(entity.UnitCorpusKitab, bookID, scopeKey, d.Kind, d.ContentHash, occurrence)
	}

	return UnitIDV2(entity.UnitCorpusKitab, bookID, scopeKey, d.ContentRole, d.Language,
		d.Kind, d.ContentHash, occurrence)
}

func unitMatched(pairs []matchPair, id string) bool {
	for _, p := range pairs {
		if p.active.ID == id {
			return true
		}
	}

	return false
}

func samePage(a *int, b int) bool {
	return a != nil && *a == b
}

func sameParent(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}

	return *a == *b
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}

	return &value
}

func sameRuneOffset(value *int, want int) bool { return value != nil && *value == want }

func sameOptionalString(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}

	return *a == *b
}

// longestIncreasingByActivePos returns the LIS of pairs (walked in derived
// order) strictly increasing in active position — the stable anchors that
// bound lineage gaps when blocks reorder.
func longestIncreasingByActivePos(pairs []matchPair) []matchPair {
	if len(pairs) == 0 {
		return nil
	}

	tails := make([]int, 0, len(pairs)) // indices into pairs

	parents := make([]int, len(pairs))
	for i := range pairs {
		pos := pairs[i].active.Position

		lo, hi := 0, len(tails)
		for lo < hi {
			mid := lo + (hi-lo)/2 //nolint:mnd // binary-search midpoint
			if pairs[tails[mid]].active.Position < pos {
				lo = mid + 1
			} else {
				hi = mid
			}
		}

		parents[i] = -1
		if lo > 0 {
			parents[i] = tails[lo-1]
		}

		if lo == len(tails) {
			tails = append(tails, i)
		} else {
			tails[lo] = i
		}
	}

	out := make([]matchPair, 0, len(tails))
	for idx := tails[len(tails)-1]; idx >= 0; idx = parents[idx] {
		out = append(out, pairs[idx])
	}

	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}

	return out
}
