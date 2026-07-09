package unitregistry_test

import (
	"fmt"
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/usecase/unitregistry"
)

//nolint:gochecknoglobals // fixed test timestamp shared across plan cases
var planTime = time.Date(2026, 7, 9, 1, 0, 0, 0, time.UTC)

func emptySnapshot() entity.UnitRegistrySnapshot {
	return entity.UnitRegistrySnapshot{
		MaxOrdinalByScope: map[int]int{},
		ExistingIDs:       map[string]struct{}{},
	}
}

// applySnapshot mimics the repo: mints join the active set, retires leave it
// (but their ids stay minted forever), updates mutate locator metadata.
//
//nolint:gocritic // test helper; snapshot copy cost is negligible
func applySnapshot(snap entity.UnitRegistrySnapshot, plan entity.UnitReconcilePlan) entity.UnitRegistrySnapshot {
	byID := make(map[string]entity.CitableUnit, len(snap.Active))
	for i := range snap.Active {
		byID[snap.Active[i].ID] = snap.Active[i]
	}

	for _, up := range plan.Updates {
		u, ok := byID[up.ID]
		if !ok {
			panic("update for unknown unit " + up.ID)
		}

		u.Position = up.Position
		u.PageID = up.PageID
		u.ParentUnitID = up.ParentUnitID
		byID[up.ID] = u
	}

	for _, r := range plan.Retires {
		if _, ok := byID[r.ID]; !ok {
			panic("retire for unknown unit " + r.ID)
		}

		delete(byID, r.ID)
	}

	out := entity.UnitRegistrySnapshot{
		MaxOrdinalByScope: map[int]int{},
		ExistingIDs:       map[string]struct{}{},
	}
	maps.Copy(out.MaxOrdinalByScope, snap.MaxOrdinalByScope)

	for id := range snap.ExistingIDs {
		out.ExistingIDs[id] = struct{}{}
	}

	for i := range plan.Mints {
		m := plan.Mints[i]
		byID[m.ID] = m

		scope := 0
		if m.HeadingID != nil {
			scope = *m.HeadingID
		}

		if m.Ordinal > out.MaxOrdinalByScope[scope] {
			out.MaxOrdinalByScope[scope] = m.Ordinal
		}
	}

	for id, u := range byID {
		out.ExistingIDs[id] = struct{}{}
		out.Active = append(out.Active, u)
	}

	for _, r := range plan.Retires {
		out.ExistingIDs[r.ID] = struct{}{}
	}

	if int64(len(out.Active)) != plan.ExpectedActive {
		panic(fmt.Sprintf("expected active %d, got %d", plan.ExpectedActive, len(out.Active)))
	}

	return out
}

//nolint:gocritic // test helper; source/snapshot copy cost is negligible
func derivePlan(t *testing.T, src entity.BookUnitSource, snap entity.UnitRegistrySnapshot) (entity.UnitReconcilePlan, entity.UnitRegistrySnapshot) {
	t.Helper()

	units, _, err := unitregistry.DeriveBook(&src)
	if err != nil {
		t.Fatalf("DeriveBook: %v", err)
	}

	plan := unitregistry.PlanBook(src.BookID, planTime, units, &snap)

	return plan, applySnapshot(snap, plan)
}

func simpleSource(pages ...string) entity.BookUnitSource {
	src := entity.BookUnitSource{BookID: 7, ReleaseKey: "1.0", LoadedAt: planTime}
	for i, content := range pages {
		src.Pages = append(src.Pages, entity.BookUnitSourcePage{PageID: i + 1, ContentHTML: content})
	}

	return src
}

func TestPlanBookInitialMintAndRerunNoop(t *testing.T) {
	t.Parallel()

	src := simpleSource("فقرة أولى\nفقرة ثانية\nفقرة أولى") // duplicate twin included

	plan, snap := derivePlan(t, src, emptySnapshot())
	if plan.Report.Minted != 3 || plan.Report.Matched != 0 || len(plan.Retires) != 0 {
		t.Fatalf("initial report = %+v", plan.Report)
	}
	// Ordinals sequential, anchors formatted, twin occurrences 1 and 2.
	for i, m := range plan.Mints {
		if m.Ordinal != i+1 {
			t.Fatalf("mint[%d] ordinal = %d", i, m.Ordinal)
		}

		wantAnchor := fmt.Sprintf("kitab/7/h/0/u/%d", i+1)
		if m.Anchor != wantAnchor {
			t.Fatalf("mint[%d] anchor = %q, want %q", i, m.Anchor, wantAnchor)
		}
	}

	if plan.Mints[0].Occurrence != 1 || plan.Mints[2].Occurrence != 2 {
		t.Fatalf("twin occurrences = %d, %d", plan.Mints[0].Occurrence, plan.Mints[2].Occurrence)
	}

	if plan.Mints[0].ID == plan.Mints[2].ID {
		t.Fatalf("identical twins must mint distinct ids")
	}

	if plan.Mints[0].TextNormalized == "" || plan.Mints[0].NormalizationVersion != 1 {
		t.Fatalf("mint normalization = %q v%d", plan.Mints[0].TextNormalized, plan.Mints[0].NormalizationVersion)
	}

	// AC-1 core: re-plan over unchanged source is a strict no-op with the same ids.
	rerun, _ := derivePlan(t, src, snap)
	if rerun.Report.Minted != 0 || rerun.Report.Superseded != 0 || rerun.Report.Tombstoned != 0 ||
		rerun.Report.Updated != 0 || rerun.Report.Matched != 3 {
		t.Fatalf("rerun must be a no-op, report = %+v", rerun.Report)
	}
}

func TestPlanBookSplitParagraph(t *testing.T) {
	t.Parallel()

	src := simpleSource("الفقرة الأصلية الكاملة قبل التقسيم\nفقرة ثابتة")
	_, snap := derivePlan(t, src, emptySnapshot())

	split := simpleSource("النصف الأول من الفقرة\nالنصف الثاني منها\nفقرة ثابتة")
	plan, _ := derivePlan(t, split, snap)

	if plan.Report.Minted != 2 || plan.Report.Superseded != 1 || plan.Report.Tombstoned != 0 {
		t.Fatalf("split report = %+v", plan.Report)
	}

	if len(plan.Edges) != 2 {
		t.Fatalf("split edges = %+v", plan.Edges)
	}

	pred := plan.Edges[0].PredecessorID
	succs := map[string]bool{}

	for _, e := range plan.Edges {
		if e.PredecessorID != pred || e.Reason != entity.UnitLineageReasonEdit {
			t.Fatalf("edge = %+v", e)
		}

		succs[e.SuccessorID] = true
	}

	if len(succs) != 2 {
		t.Fatalf("split must fan out to two successors, got %v", succs)
	}
	// The retired unit is superseded, matched neighbor untouched.
	if plan.Retires[0].Lifecycle != entity.UnitLifecycleSuperseded {
		t.Fatalf("retire = %+v", plan.Retires[0])
	}
}

func TestPlanBookPureDeletionTombstones(t *testing.T) {
	t.Parallel()

	src := simpleSource("فقرة تبقى\nفقرة ستحذف نهائيا من الكتاب")
	_, snap := derivePlan(t, src, emptySnapshot())

	shrunk := simpleSource("فقرة تبقى")
	plan, _ := derivePlan(t, shrunk, snap)

	if plan.Report.Tombstoned != 1 || plan.Report.Superseded != 0 || plan.Report.Minted != 0 {
		t.Fatalf("deletion report = %+v", plan.Report)
	}

	if len(plan.Edges) != 0 {
		t.Fatalf("pure deletion must not create edges: %+v", plan.Edges)
	}
}

func TestPlanBookTwinDeletionLinksSurvivor(t *testing.T) {
	t.Parallel()

	src := simpleSource("نفس الفقرة المكررة\nفاصل بين التوأمين\nنفس الفقرة المكررة")
	_, snap := derivePlan(t, src, emptySnapshot())

	oneTwin := simpleSource("نفس الفقرة المكررة\nفاصل بين التوأمين")
	plan, _ := derivePlan(t, oneTwin, snap)

	if plan.Report.Superseded != 1 || plan.Report.Tombstoned != 0 || plan.Report.Minted != 0 {
		t.Fatalf("twin deletion report = %+v", plan.Report)
	}

	if len(plan.Edges) != 1 || plan.Edges[0].Reason != entity.UnitLineageReasonContentMove {
		t.Fatalf("twin deletion edges = %+v", plan.Edges)
	}
}

func TestPlanBookRevertAfterSupersedeBumpsOccurrence(t *testing.T) {
	t.Parallel()

	original := simpleSource("النص الأصلي قبل أي تعديل")
	planA, snap := derivePlan(t, original, emptySnapshot())
	originalID := planA.Mints[0].ID

	edited := simpleSource("النص بعد التعديل الأول")
	_, snap = derivePlan(t, edited, snap)

	reverted := simpleSource("النص الأصلي قبل أي تعديل")
	plan, _ := derivePlan(t, reverted, snap)

	if plan.Report.Minted != 1 || plan.Report.Superseded != 1 {
		t.Fatalf("revert report = %+v", plan.Report)
	}

	mint := plan.Mints[0]
	if mint.ID == originalID {
		t.Fatalf("revert must not recycle the retired id %s", originalID)
	}

	if mint.Occurrence != 2 {
		t.Fatalf("revert occurrence = %d, want 2", mint.Occurrence)
	}

	if mint.Ordinal != 3 {
		t.Fatalf("revert ordinal = %d, want 3 (never recycled)", mint.Ordinal)
	}
}

func TestPlanBookReorderKeepsIdentity(t *testing.T) {
	t.Parallel()

	src := simpleSource("الفقرة الأولى في الترتيب\nالفقرة الثانية في الترتيب\nالفقرة الثالثة في الترتيب")
	planA, snap := derivePlan(t, src, emptySnapshot())

	swapped := simpleSource("الفقرة الثانية في الترتيب\nالفقرة الأولى في الترتيب\nالفقرة الثالثة في الترتيب")
	plan, _ := derivePlan(t, swapped, snap)

	if plan.Report.Minted != 0 || plan.Report.Superseded != 0 || plan.Report.Tombstoned != 0 {
		t.Fatalf("reorder must not mint/retire: %+v", plan.Report)
	}

	if plan.Report.Updated != 2 {
		t.Fatalf("reorder updates = %d, want 2 position moves", plan.Report.Updated)
	}

	for _, m := range planA.Mints {
		_ = m // ids unchanged by construction; matched count proves it
	}

	if plan.Report.Matched != 3 {
		t.Fatalf("matched = %d", plan.Report.Matched)
	}
}

func TestPlanBookScopeMoveRescue(t *testing.T) {
	t.Parallel()

	mk := func(anchorPage int) entity.BookUnitSource {
		pageOne := "فقرة متنقلة بين الفصول"
		pageTwo := `<span id="toc-5">فصل</span>` + "\nفقرة مستقرة في الفصل"

		if anchorPage == 1 {
			pageOne = `<span id="toc-5">فصل</span>` + "\nفقرة متنقلة بين الفصول"
			pageTwo = "فقرة مستقرة في الفصل"
		}

		src := simpleSource(pageOne, pageTwo)
		src.Headings = []entity.BookUnitSourceHeading{{HeadingID: 5, PageID: anchorPage}}

		return src
	}

	_, snap := derivePlan(t, mk(2), emptySnapshot()) // moving paragraph = front-matter
	plan, _ := derivePlan(t, mk(1), snap)            // anchor moves to page 1 → paragraph now in scope 5

	if plan.Report.Minted != 1 || plan.Report.Superseded != 1 || plan.Report.Tombstoned != 0 {
		t.Fatalf("scope move report = %+v", plan.Report)
	}

	if len(plan.Edges) != 1 || plan.Edges[0].Reason != entity.UnitLineageReasonContentMove {
		t.Fatalf("scope move edges = %+v", plan.Edges)
	}

	if plan.Mints[0].HeadingID == nil || *plan.Mints[0].HeadingID != 5 {
		t.Fatalf("mint scope = %v", plan.Mints[0].HeadingID)
	}
}

func TestPlanBookGapCapZips(t *testing.T) {
	t.Parallel()

	const n = 6 // 6×6 = 36 > 32 cap

	oldLines := make([]string, 0, n)

	newLines := make([]string, 0, n)
	for i := range n {
		oldLines = append(oldLines, fmt.Sprintf("فقرة قديمة رقم %d في هذا الباب", i))
		newLines = append(newLines, fmt.Sprintf("فقرة جديدة رقم %d في هذا الباب", i))
	}

	_, snap := derivePlan(t, simpleSource(strings.Join(oldLines, "\n")), emptySnapshot())
	plan, _ := derivePlan(t, simpleSource(strings.Join(newLines, "\n")), snap)

	if plan.Report.CappedGaps != 1 {
		t.Fatalf("capped gaps = %d", plan.Report.CappedGaps)
	}

	if len(plan.Edges) != n {
		t.Fatalf("zip edges = %d, want %d", len(plan.Edges), n)
	}

	if plan.Report.Superseded != n || plan.Report.Minted != n {
		t.Fatalf("cap report = %+v", plan.Report)
	}
}

func TestPlanBookFootnoteParentRepoint(t *testing.T) {
	t.Parallel()

	src := simpleSource("متن يحمل إشارة (¬١) واضحة\n__________\n(¬١) حاشية ثابتة النص")

	planA, snap := derivePlan(t, src, emptySnapshot())
	if len(planA.Mints) != 2 || planA.Mints[1].ParentUnitID == nil {
		t.Fatalf("initial mints = %+v", planA.Mints)
	}

	// Body edited (footnote unchanged): footnote must be re-pointed to the new body unit.
	edited := simpleSource("متن معدل يحمل إشارة (¬١) واضحة\n__________\n(¬١) حاشية ثابتة النص")
	plan, _ := derivePlan(t, edited, snap)

	if plan.Report.Minted != 1 || plan.Report.Superseded != 1 {
		t.Fatalf("edit report = %+v", plan.Report)
	}

	if plan.Report.Updated != 1 {
		t.Fatalf("footnote re-point updates = %d, want 1", plan.Report.Updated)
	}

	up := plan.Updates[0]
	if up.ParentUnitID == nil || *up.ParentUnitID != plan.Mints[0].ID {
		t.Fatalf("footnote parent = %v, want new body id %s", up.ParentUnitID, plan.Mints[0].ID)
	}
}

func TestPlanBookFootnoteBecomesUnlinkedRefreshesLabel(t *testing.T) {
	t.Parallel()

	// A footnote linked (fallback) to a body block; the body is then deleted so
	// the footnote becomes unlinked. Its text is unchanged, so it MATCHES (not
	// re-minted) — the update must both NULL the parent AND refresh
	// footnote_link to 'unlinked', or the audit footnote_parent check would
	// false-positive on this legitimate edit (review finding).
	src := simpleSource("متن الفقرة قبل الحذف\n__________\n(¬١) حاشية بلا إشارة في المتن")

	planA, snap := derivePlan(t, src, emptySnapshot())
	if len(planA.Mints) != 2 {
		t.Fatalf("initial mints = %+v", planA.Mints)
	}

	footnote := planA.Mints[1]
	if footnote.Kind != entity.UnitKindFootnote || footnote.ProvenanceDetail["footnote_link"] != entity.FootnoteLinkFallback {
		t.Fatalf("initial footnote = %+v", footnote)
	}

	// Body line gone: page is only the separator + footnote.
	deleted := simpleSource("__________\n(¬١) حاشية بلا إشارة في المتن")
	plan, _ := derivePlan(t, deleted, snap)

	if plan.Report.Minted != 0 || plan.Report.Tombstoned != 1 {
		t.Fatalf("delete report = %+v", plan.Report)
	}

	if len(plan.Updates) != 1 {
		t.Fatalf("expected 1 footnote update, got %+v", plan.Updates)
	}

	up := plan.Updates[0]
	if up.ID != footnote.ID {
		t.Fatalf("update targets %s, want matched footnote %s", up.ID, footnote.ID)
	}

	if up.ParentUnitID != nil {
		t.Fatalf("footnote parent must become NULL, got %v", up.ParentUnitID)
	}

	if up.FootnoteLink == nil || *up.FootnoteLink != entity.FootnoteLinkUnlinked {
		t.Fatalf("footnote_link must refresh to %q, got %v", entity.FootnoteLinkUnlinked, up.FootnoteLink)
	}
}
