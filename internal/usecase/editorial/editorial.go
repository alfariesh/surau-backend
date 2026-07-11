package editorial

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/readerlang"
	"github.com/alfariesh/surau-backend/internal/readerutil"
	"github.com/alfariesh/surau-backend/internal/repo"
	"github.com/alfariesh/surau-backend/pkg/logger"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const (
	defaultLimit = 50
	maxLimit     = 200

	// unitReconcileTimeout bounds the post-publish registry reconcile; a slow
	// or failing reconcile must never fail the publish itself (audit is the
	// safety net, phase-1b B-1).
	unitReconcileTimeout = 15 * time.Second
)

// unitReconcileFailures counts post-publish reconciles that errored; the
// citable-unit audit loop and its stale_books gauge are the recovery path.
//
//nolint:gochecknoglobals // process-wide Prometheus instrument (promauto pattern)
var unitReconcileFailures = promauto.NewCounter(prometheus.CounterOpts{
	Name: "surau_citable_reconcile_failures_total",
	Help: "Post-publish citable-unit reconciles that failed (registry may be stale until the next backfill/audit).",
})

// UnitReconciler re-derives a book's citable units after an editorial publish
// (consumer-side slice of the unitregistry usecase, phase-1b B-1).
type UnitReconciler interface {
	ReconcileBookIfDerived(ctx context.Context, bookID int) (entity.UnitReconcileReport, bool, error)
}

// UseCase provides editorial operations.
type UseCase struct {
	repo           repo.EditorialRepo
	license        repo.LicenseRepo
	quranEditorial repo.QuranEditorialRepo
	units          UnitReconciler
	log            logger.Interface
}

// New creates an editorial usecase. units and l are optional (nil-safe): when
// wired, published page edits trigger a citable-unit reconcile for books that
// already went through the pilot backfill.
func New(r repo.EditorialRepo, units UnitReconciler, l logger.Interface) *UseCase {
	uc := &UseCase{repo: r, units: units, log: l}
	if licenseRepo, ok := r.(repo.LicenseRepo); ok {
		uc.license = licenseRepo
	}

	if quranRepo, ok := r.(repo.QuranEditorialRepo); ok {
		uc.quranEditorial = quranRepo
	}

	return uc
}

// reconcileUnitsAfterPublish runs the phase-1b lineage hook: the publish is
// already committed, so failures are logged + counted, never returned — the
// scheduled audit (stale_books) catches any book left behind.
func (uc *UseCase) reconcileUnitsAfterPublish(ctx context.Context, bookID int) {
	if uc.units == nil {
		return
	}

	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), unitReconcileTimeout)
	defer cancel()

	report, skipped, err := uc.units.ReconcileBookIfDerived(rctx, bookID)
	if err != nil {
		unitReconcileFailures.Inc()

		if uc.log != nil {
			uc.log.Error(fmt.Errorf("editorial - unit reconcile after publish (book %d): %w", bookID, err))
		}

		return
	}

	if !skipped && uc.log != nil {
		uc.log.Info("editorial - units reconciled after publish: book=%d minted=%d superseded=%d tombstoned=%d updated=%d",
			bookID, report.Minted, report.Superseded, report.Tombstoned, report.Updated)
	}
}

// Books returns paginated books for admin review.
func (uc *UseCase) Books(
	ctx context.Context,
	query string,
	status *string,
	categoryID *int,
	hasContent *bool,
	limit, offset int,
) ([]entity.Book, int, error) {
	if status != nil {
		trimmed := strings.TrimSpace(*status)
		if trimmed == "" {
			status = nil
		} else if !isPublicationStatus(trimmed) {
			return nil, 0, entity.ErrInvalidStatus
		} else {
			status = &trimmed
		}
	}

	return uc.repo.ListBooks(ctx, repo.EditorialBookFilter{
		Query:      strings.TrimSpace(query),
		Status:     status,
		CategoryID: categoryID,
		HasContent: hasContent,
		Limit:      clampLimit(limit),
		Offset:     clampOffset(offset),
	})
}

// ProductionCandidates returns source books with current production state for one target language.
func (uc *UseCase) ProductionCandidates(
	ctx context.Context,
	lang,
	query string,
	categoryID,
	authorID *int,
	hasContent *bool,
	unstarted bool,
	limit,
	offset int,
) ([]entity.BookProductionCandidate, int, error) {
	normalizedLang, err := entity.NormalizeProductionLang(lang)
	if err != nil {
		return nil, 0, err
	}

	return uc.repo.ListProductionCandidates(ctx, repo.ProductionCandidateFilter{
		Lang:       normalizedLang,
		Query:      strings.TrimSpace(query),
		CategoryID: categoryID,
		AuthorID:   authorID,
		HasContent: hasContent,
		Unstarted:  unstarted,
		Limit:      clampLimit(limit),
		Offset:     clampOffset(offset),
	})
}

// UpdatePublication changes visibility and ordering.
func (uc *UseCase) UpdatePublication(
	ctx context.Context,
	actorID string,
	bookID int,
	status string,
	featured bool,
	sortOrder *int,
) (entity.BookPublication, error) {
	status = strings.TrimSpace(status)
	if !isPublicationStatus(status) {
		return entity.BookPublication{}, entity.ErrInvalidStatus
	}

	return uc.repo.UpdatePublication(ctx, actorID, entity.BookPublication{
		BookID:    bookID,
		Status:    status,
		Featured:  featured,
		SortOrder: sortOrder,
	})
}

// GetMetadataDraft returns one source metadata draft.
func (uc *UseCase) GetMetadataDraft(ctx context.Context, bookID int) (entity.BookMetadataEdit, error) {
	if bookID <= 0 {
		return entity.BookMetadataEdit{}, entity.ErrBookNotFound
	}

	return uc.repo.GetMetadataDraft(ctx, bookID)
}

// SaveMetadataDraft stores metadata override as draft. A non-nil expected
// timestamp enforces optimistic concurrency against the current draft.
func (uc *UseCase) SaveMetadataDraft(
	ctx context.Context,
	actorID string,
	edit entity.BookMetadataEdit,
	expected *time.Time,
	origin string,
) (entity.BookMetadataEdit, error) {
	edit.Status = entity.EditStatusDraft
	edit.DisplayTitle = trimStringPtr(edit.DisplayTitle)
	edit.Bibliography = trimStringPtr(edit.Bibliography)
	edit.Hint = trimStringPtr(edit.Hint)
	edit.Description = trimStringPtr(edit.Description)
	edit.CoverURL = trimStringPtr(edit.CoverURL)
	edit.Notes = trimStringPtr(edit.Notes)
	if edit.CategoryID != nil && *edit.CategoryID <= 0 {
		edit.CategoryID = nil
	}

	return uc.repo.SaveMetadataDraft(ctx, actorID, edit, expected, origin)
}

// PublishMetadataDraft promotes metadata draft to published.
func (uc *UseCase) PublishMetadataDraft(ctx context.Context, actorID string, bookID int, expected *time.Time) (entity.BookMetadataEdit, error) {
	return uc.repo.PublishMetadataDraft(ctx, actorID, bookID, expected)
}

// GetPageEdit returns raw page plus draft and published overrides.
func (uc *UseCase) GetPageEdit(ctx context.Context, bookID, pageID int) (entity.EditorialPageEdit, error) {
	return uc.repo.GetPageEdit(ctx, bookID, pageID)
}

// SavePageDraft stores page override as draft. A non-nil expected timestamp
// enforces optimistic concurrency against the current draft (or raw page when
// no draft exists yet).
//
//nolint:gocritic // value param mirrors the usecase.Editorial interface
func (uc *UseCase) SavePageDraft(
	ctx context.Context,
	actorID string,
	edit entity.BookPageEdit,
	expected *time.Time,
	origin string,
) (entity.BookPageEdit, error) {
	contentHTML, contentText := readerutil.NormalizeContent(edit.ContentHTML)
	edit.Status = entity.EditStatusDraft
	edit.ContentHTML = contentHTML
	edit.ContentText = contentText

	return uc.repo.SavePageDraft(ctx, actorID, edit, expected, origin)
}

// PageDraftRevisions returns the newest-first revision history for one page's
// draft content.
func (uc *UseCase) PageDraftRevisions(
	ctx context.Context,
	bookID, pageID int,
	limit, offset int,
) ([]entity.BookSourceEditRevision, int, error) {
	if bookID <= 0 {
		return nil, 0, entity.ErrBookNotFound
	}

	if pageID <= 0 {
		return nil, 0, entity.ErrPageNotFound
	}

	return uc.repo.ListSourceEditRevisions(ctx, repo.SourceEditRevisionFilter{
		BookID:    bookID,
		AssetType: entity.SourceEditAssetPage,
		PageID:    &pageID,
		Limit:     clampLimit(limit),
		Offset:    clampOffset(offset),
	})
}

// RestorePageDraftRevision replays one historical snapshot as the current page
// draft. The restore itself is recorded as a new revision (origin "restore"),
// so history stays append-only.
func (uc *UseCase) RestorePageDraftRevision(
	ctx context.Context,
	actorID string,
	bookID, pageID int,
	revisionID string,
) (entity.BookPageEdit, error) {
	revision, err := uc.repo.GetSourceEditRevision(ctx, strings.TrimSpace(revisionID))
	if err != nil {
		return entity.BookPageEdit{}, err
	}

	if revision.AssetType != entity.SourceEditAssetPage ||
		revision.BookID != bookID ||
		revision.PageID == nil || *revision.PageID != pageID {
		return entity.BookPageEdit{}, entity.ErrDraftNotFound
	}

	var snapshot struct {
		ContentHTML string `json:"content_html"`
	}
	if err = json.Unmarshal(revision.Snapshot, &snapshot); err != nil {
		return entity.BookPageEdit{}, fmt.Errorf("EditorialUseCase - RestorePageDraftRevision - unmarshal snapshot: %w", err)
	}

	if strings.TrimSpace(snapshot.ContentHTML) == "" {
		return entity.BookPageEdit{}, entity.ErrDraftNotFound
	}

	return uc.SavePageDraft(ctx, actorID, entity.BookPageEdit{
		BookID:      bookID,
		PageID:      pageID,
		ContentHTML: snapshot.ContentHTML,
	}, nil, entity.EditOriginRestore)
}

// PublishPageDraft promotes page draft to published. A successful publish is
// the supersede/mint moment for citable units (readers serve the published
// edit from now on), so the registry reconciles right after the commit.
func (uc *UseCase) PublishPageDraft(ctx context.Context, actorID string, bookID, pageID int, expected *time.Time) (entity.BookPageEdit, error) {
	edit, err := uc.repo.PublishPageDraft(ctx, actorID, bookID, pageID, expected)
	if err != nil {
		return edit, err
	}

	uc.reconcileUnitsAfterPublish(ctx, bookID)

	return edit, nil
}

// GetHeadingDraft returns one source heading draft.
func (uc *UseCase) GetHeadingDraft(ctx context.Context, bookID, headingID int) (entity.BookHeadingEdit, error) {
	if bookID <= 0 {
		return entity.BookHeadingEdit{}, entity.ErrBookNotFound
	}
	if headingID <= 0 {
		return entity.BookHeadingEdit{}, entity.ErrHeadingNotFound
	}

	return uc.repo.GetHeadingDraft(ctx, bookID, headingID)
}

// SaveHeadingDraft stores heading title override as draft. A non-nil expected
// timestamp enforces optimistic concurrency against the current draft.
func (uc *UseCase) SaveHeadingDraft(
	ctx context.Context,
	actorID string,
	edit entity.BookHeadingEdit,
	expected *time.Time,
	origin string,
) (entity.BookHeadingEdit, error) {
	edit.Status = entity.EditStatusDraft
	edit.Content = strings.TrimSpace(edit.Content)

	return uc.repo.SaveHeadingDraft(ctx, actorID, edit, expected, origin)
}

// PublishHeadingDraft promotes heading draft to published.
func (uc *UseCase) PublishHeadingDraft(ctx context.Context, actorID string, bookID, headingID int, expected *time.Time) (entity.BookHeadingEdit, error) {
	return uc.repo.PublishHeadingDraft(ctx, actorID, bookID, headingID, expected)
}

// AddCollectionItem adds or reorders a book in a collection.
func (uc *UseCase) AddCollectionItem(
	ctx context.Context,
	actorID, slug string,
	bookID int,
	sortOrder *int,
) (entity.BookCollectionItem, error) {
	return uc.repo.AddCollectionItem(ctx, actorID, strings.TrimSpace(slug), bookID, sortOrder)
}

// TranslationFeedbacks returns paginated reader feedback for admin review.
func (uc *UseCase) TranslationFeedbacks(
	ctx context.Context,
	bookID, headingID *int,
	lang, vote, status string,
	limit, offset int,
) ([]entity.EditorialTranslationFeedback, int, error) {
	filter, err := translationFeedbackFilter(bookID, headingID, lang, vote, status, limit, offset)
	if err != nil {
		return nil, 0, err
	}

	return uc.repo.ListTranslationFeedbacks(ctx, filter)
}

// TranslationFeedbackSummary aggregates reader feedback for admin review.
func (uc *UseCase) TranslationFeedbackSummary(
	ctx context.Context,
	bookID, headingID *int,
	lang, vote, status string,
	limit int,
) (entity.EditorialTranslationFeedbackSummary, error) {
	filter, err := translationFeedbackFilter(bookID, headingID, lang, vote, status, limit, 0)
	if err != nil {
		return entity.EditorialTranslationFeedbackSummary{}, err
	}

	return uc.repo.TranslationFeedbackSummary(ctx, filter)
}

// MissingReaderAssets returns admin queue items for missing localized reader assets.
func (uc *UseCase) MissingReaderAssets(
	ctx context.Context,
	targetLang string,
	assetType string,
	bookID *int,
	limit,
	offset int,
) (entity.EditorialMissingReaderAssets, error) {
	filter, err := missingReaderAssetFilter(targetLang, assetType, bookID, limit, offset)
	if err != nil {
		return entity.EditorialMissingReaderAssets{}, err
	}

	return uc.repo.ListMissingReaderAssets(ctx, filter)
}

// ResolveTranslationFeedback marks reader feedback as handled by an admin.
func (uc *UseCase) ResolveTranslationFeedback(
	ctx context.Context,
	actorID, feedbackID string,
	note *string,
) (entity.EditorialTranslationFeedback, error) {
	feedbackID = strings.TrimSpace(feedbackID)
	if feedbackID == "" {
		return entity.EditorialTranslationFeedback{}, entity.ErrInvalidFeedback
	}

	return uc.repo.ResolveTranslationFeedback(ctx, actorID, feedbackID, trimStringPtr(note))
}

// ReopenTranslationFeedback moves a handled feedback row back to the active queue.
func (uc *UseCase) ReopenTranslationFeedback(
	ctx context.Context,
	actorID, feedbackID string,
) (entity.EditorialTranslationFeedback, error) {
	feedbackID = strings.TrimSpace(feedbackID)
	if feedbackID == "" {
		return entity.EditorialTranslationFeedback{}, entity.ErrInvalidFeedback
	}

	return uc.repo.ReopenTranslationFeedback(ctx, actorID, feedbackID)
}

func isPublicationStatus(status string) bool {
	switch status {
	case entity.PublicationStatusHidden,
		entity.PublicationStatusDraft,
		entity.PublicationStatusPublished,
		entity.PublicationStatusArchived:
		return true
	default:
		return false
	}
}

func translationFeedbackFilter(
	bookID, headingID *int,
	lang, vote, status string,
	limit, offset int,
) (repo.TranslationFeedbackFilter, error) {
	lang = strings.ToLower(strings.TrimSpace(lang))
	vote = strings.ToLower(strings.TrimSpace(vote))
	if vote != "" && vote != "like" && vote != "dislike" {
		return repo.TranslationFeedbackFilter{}, entity.ErrInvalidFeedback
	}

	status = strings.ToLower(strings.TrimSpace(status))
	if status == "" {
		status = entity.FeedbackStatusOpen
	}
	if status == "all" {
		status = ""
	} else if status != entity.FeedbackStatusOpen && status != entity.FeedbackStatusResolved {
		return repo.TranslationFeedbackFilter{}, entity.ErrInvalidFeedback
	}

	return repo.TranslationFeedbackFilter{
		BookID:    bookID,
		HeadingID: headingID,
		Lang:      lang,
		Vote:      vote,
		Status:    status,
		Limit:     clampLimit(limit),
		Offset:    clampOffset(offset),
	}, nil
}

func missingReaderAssetFilter(
	targetLang string,
	assetType string,
	bookID *int,
	limit,
	offset int,
) (repo.MissingReaderAssetFilter, error) {
	targetLang = strings.TrimSpace(targetLang)
	targetLangs := []string{readerlang.Default, readerlang.English}
	if targetLang != "" {
		normalized, err := readerlang.Normalize(targetLang)
		if err != nil || normalized == readerlang.Arabic {
			return repo.MissingReaderAssetFilter{}, entity.ErrUnsupportedLanguage
		}

		targetLangs = []string{normalized}
	}

	assetType = strings.ToLower(strings.TrimSpace(assetType))
	if assetType != "" && !isMissingReaderAssetType(assetType) {
		return repo.MissingReaderAssetFilter{}, entity.ErrInvalidAssetType
	}

	return repo.MissingReaderAssetFilter{
		TargetLangs: targetLangs,
		AssetType:   assetType,
		BookID:      bookID,
		Limit:       clampLimit(limit),
		Offset:      clampOffset(offset),
	}, nil
}

func isMissingReaderAssetType(assetType string) bool {
	switch assetType {
	case entity.MissingAssetBookMetadata,
		entity.MissingAssetCategoryMetadata,
		entity.MissingAssetAuthorMetadata,
		entity.MissingAssetSectionTranslation,
		entity.MissingAssetHeadingSummary,
		entity.MissingAssetSectionAudio:
		return true
	default:
		return false
	}
}

func trimStringPtr(value *string) *string {
	if value == nil {
		return nil
	}

	trimmed := strings.TrimSpace(*value)
	if trimmed == "" {
		return nil
	}

	return &trimmed
}

func clampLimit(limit int) uint64 {
	if limit <= 0 {
		return defaultLimit
	}

	if limit > maxLimit {
		return maxLimit
	}

	return uint64(limit)
}

func clampOffset(offset int) uint64 {
	if offset < 0 {
		return 0
	}

	return uint64(offset)
}
