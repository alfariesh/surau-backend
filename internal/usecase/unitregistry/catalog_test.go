package unitregistry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/repo"
)

var errCatalogAtomicTest = errors.New("catalog atomic test failure")

type catalogAtomicRepo struct {
	source    entity.BookUnitSource
	snapshot  entity.UnitRegistrySnapshot
	failStage string
	committed *catalogAtomicTx
}

type catalogAtomicTx struct {
	source        entity.BookUnitSource
	snapshot      entity.UnitRegistrySnapshot
	failStage     string
	plan          *entity.UnitReconcilePlan
	mentionBound  bool
	queueComplete bool
	checksumCalls int
}

func (r *catalogAtomicRepo) WithCatalogTransaction(
	_ context.Context,
	_ int,
	fn func(repo.CitableUnitCatalogTx) error,
) error {
	staged := &catalogAtomicTx{source: r.source, snapshot: r.snapshot, failStage: r.failStage}
	if err := fn(staged); err != nil {
		return err
	}

	r.committed = staged

	return nil
}

func (r *catalogAtomicRepo) LoadBookSource(context.Context, int) (entity.BookUnitSource, error) {
	return r.source, nil
}

func (r *catalogAtomicRepo) Snapshot(context.Context, int) (entity.UnitRegistrySnapshot, error) {
	return r.snapshot, nil
}

func (*catalogAtomicRepo) ApplyReconcile(context.Context, *entity.UnitReconcilePlan) error {
	return nil
}

func (*catalogAtomicRepo) BookDerivedAt(context.Context, int) (*time.Time, error) { return nil, nil }

func (*catalogAtomicRepo) ResolveUnit(context.Context, string) (entity.UnitResolution, error) {
	return entity.UnitResolution{}, nil
}

func (*catalogAtomicRepo) AuditCounts(context.Context) (entity.CitableAuditReport, error) {
	return entity.CitableAuditReport{}, nil
}

func (*catalogAtomicRepo) ListActiveUnitsForHashCheck(context.Context) ([]entity.CitableUnit, error) {
	return nil, nil
}

func (t *catalogAtomicTx) LoadBookSource(context.Context, int) (entity.BookUnitSource, error) {
	return t.source, nil
}

func (t *catalogAtomicTx) Snapshot(context.Context, int) (entity.UnitRegistrySnapshot, error) {
	return t.snapshot, nil
}

//nolint:wsl_v5 // test double records each atomic stage compactly
func (t *catalogAtomicTx) ApplyReconcile(_ context.Context, plan *entity.UnitReconcilePlan) error {
	copyPlan := *plan
	t.plan = &copyPlan
	if t.failStage == "apply" {
		return errCatalogAtomicTest
	}

	return nil
}

func (t *catalogAtomicTx) BindKnowledgeMentions(context.Context, int) error {
	t.mentionBound = true
	if t.failStage == "mention" {
		return errCatalogAtomicTest
	}

	return nil
}

func (*catalogAtomicTx) SourceFingerprint(context.Context, int) ([32]byte, error) {
	return [32]byte{1}, nil
}

func (t *catalogAtomicTx) RegistryChecksum(context.Context, int) ([32]byte, error) {
	t.checksumCalls++
	if t.checksumCalls == 1 {
		return [32]byte{1}, nil
	}

	return [32]byte{2}, nil
}

func (t *catalogAtomicTx) CompleteQueueItem(context.Context, string, int, [32]byte, [32]byte) error {
	t.queueComplete = true
	if t.failStage == "queue" {
		return errCatalogAtomicTest
	}

	return nil
}

func catalogAtomicSource() entity.BookUnitSource {
	return entity.BookUnitSource{
		BookID:     7,
		ReleaseKey: "1.0",
		LoadedAt:   time.Date(2026, 7, 13, 1, 0, 0, 0, time.UTC),
		Pages: []entity.BookUnitSourcePage{{
			PageID: 1, RawContentHTML: "النص الخام", RawContentText: "النص الخام",
			ContentHTML: "النص المحرر", ContentText: "النص المحرر", HasPublishedEdit: true,
		}},
	}
}

func emptyCatalogSnapshot() entity.UnitRegistrySnapshot {
	return entity.UnitRegistrySnapshot{
		MaxOrdinalByScope: map[int]int{},
		ExistingIDs:       map[string]struct{}{},
	}
}

func TestReconcileCatalogBookRollsBackEveryStageOnFailure(t *testing.T) {
	t.Parallel()

	for _, stage := range []string{"apply", "mention", "queue"} {
		t.Run(stage, func(t *testing.T) {
			t.Parallel()

			r := &catalogAtomicRepo{
				source: catalogAtomicSource(), snapshot: emptyCatalogSnapshot(), failStage: stage,
			}
			uc := New(r)

			_, err := uc.ReconcileCatalogBook(context.Background(), entity.CitableCatalogReconcileRequest{
				BookID: 7, JobName: "catalog-test",
			})
			if !errors.Is(err, errCatalogAtomicTest) {
				t.Fatalf("error = %v, want injected failure", err)
			}

			if r.committed != nil {
				t.Fatal("failed one-book transaction committed partial registry/mention/queue state")
			}
		})
	}
}

//nolint:wsl_v5 // atomic-stage assertions intentionally follow commit order
func TestReconcileCatalogBookCommitsRawEffectiveMentionAndQueueTogether(t *testing.T) {
	t.Parallel()

	r := &catalogAtomicRepo{source: catalogAtomicSource(), snapshot: emptyCatalogSnapshot()}
	uc := New(r)

	result, err := uc.ReconcileCatalogBook(context.Background(), entity.CitableCatalogReconcileRequest{
		BookID: 7, JobName: "catalog-test",
	})
	if err != nil {
		t.Fatalf("ReconcileCatalogBook: %v", err)
	}

	if r.committed == nil || r.committed.plan == nil || !r.committed.mentionBound || !r.committed.queueComplete {
		t.Fatalf("atomic commit missing stage: %+v", r.committed)
	}
	if len(r.committed.plan.Mints) == 0 || len(r.committed.plan.HistoricalMints) == 0 {
		t.Fatalf("raw/effective plan = %+v", r.committed.plan)
	}
	if result.RegistryChecksum != [32]byte{2} || result.Report.Derived == 0 {
		t.Fatalf("result = %+v", result)
	}
}
