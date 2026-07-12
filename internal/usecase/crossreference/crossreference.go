// Package crossreference owns every write into the B-3 Cross-Reference
// registry and exposes its public/editorial read policies.
package crossreference

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	anchorgrammar "github.com/alfariesh/surau-backend/internal/anchor"
	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/repo"
	"github.com/alfariesh/surau-backend/internal/searchtext"
	"github.com/google/uuid"
)

const (
	defaultListLimit uint64 = 50
	maxListLimit     uint64 = 200
	maxListOffset    uint64 = 10000
)

// UseCase is the single application write service for Cross-References.
type UseCase struct {
	repo repo.CrossReferenceRepo
}

// New constructs the B-3 service.
func New(r repo.CrossReferenceRepo) *UseCase {
	return &UseCase{repo: r}
}

// CreateHuman creates a pending human-authored Cross-Reference. Actor
// attribution comes exclusively from the authenticated session argument.
//
//nolint:gocritic // value input is the public contract and is normalized into an owned entity
func (uc *UseCase) CreateHuman(
	ctx context.Context,
	input entity.CrossReferenceCreateInput,
	actorID string,
) (entity.CrossReference, error) {
	if !validUUID(actorID) {
		return entity.CrossReference{}, invalid("actor_id must be a UUID")
	}

	id := uuid.NewString()
	confidence := input.Confidence
	actor := actorID
	ref := entity.CrossReference{
		ID:                   id,
		SourceAnchor:         input.SourceAnchor,
		TargetAnchor:         input.TargetAnchor,
		Kind:                 input.Kind,
		Method:               entity.CrossReferenceMethodHuman,
		MethodDetail:         entity.CrossReferenceMethodDetail{ActorID: actorID},
		Confidence:           &confidence,
		ReviewStatus:         entity.CrossReferenceStatusPending,
		EvidenceText:         input.EvidenceText,
		EvidenceNormalized:   searchtext.Normalize(input.EvidenceText),
		NormalizationVersion: searchtext.ProfileVersion,
		Origin:               entity.CrossReferenceOriginHuman,
		OriginKey:            id,
		CreatedBy:            &actor,
		Metadata:             input.Metadata,
		CreatedAt:            time.Now().UTC(),
		UpdatedAt:            time.Now().UTC(),
	}

	if err := prepareAndValidate(&ref, false); err != nil {
		return entity.CrossReference{}, err
	}

	return uc.repo.Create(ctx, ref)
}

// UpsertDerived writes an attributed resolver or machine link. It is
// idempotent by (origin, origin_key) and never accepts human method claims.
//
//nolint:gocritic // value input prevents callers retaining a pointer to the normalized copy
func (uc *UseCase) UpsertDerived(
	ctx context.Context,
	ref entity.CrossReference,
) (entity.CrossReference, error) {
	if ref.Method == entity.CrossReferenceMethodHuman {
		return entity.CrossReference{}, invalid("human links must use CreateHuman")
	}

	if err := prepareAndValidate(&ref, false); err != nil {
		return entity.CrossReference{}, err
	}

	return uc.repo.UpsertDerived(ctx, ref, nil)
}

// BridgeLegacy atomically upserts the old quran_book_references projection,
// the generic registry row, and its typed compatibility bridge. Their UUID is
// deliberately identical for parity and rollback simplicity.
//
//nolint:gocritic // value inputs are normalized without mutating importer-owned records
func (uc *UseCase) BridgeLegacy(
	ctx context.Context,
	ref entity.CrossReference,
	bridge entity.QuranCrossReferenceBridge,
) (entity.CrossReference, error) {
	if ref.ID == "" {
		ref.ID = bridge.ID
	}

	if ref.ID != bridge.ID || !validUUID(bridge.ID) {
		return entity.CrossReference{}, invalid("bridge and Cross-Reference ids must be the same UUID")
	}

	if (ref.Origin != entity.CrossReferenceOriginLegacyQuran && ref.Origin != entity.CrossReferenceOriginResolver) ||
		ref.Method != entity.CrossReferenceMethodResolver {
		return entity.CrossReference{}, invalid("Quran bridge must use legacy/resolver origin and resolver method")
	}

	if err := prepareAndValidate(&ref, true); err != nil {
		return entity.CrossReference{}, err
	}

	if err := validateBridge(&ref, &bridge); err != nil {
		return entity.CrossReference{}, err
	}

	return uc.repo.UpsertDerived(ctx, ref, &bridge)
}

// Get returns one editorial Cross-Reference by UUID.
func (uc *UseCase) Get(ctx context.Context, id string) (entity.CrossReference, error) {
	if !validUUID(id) {
		return entity.CrossReference{}, entity.ErrCrossReferenceNotFound
	}

	return uc.repo.Get(ctx, id)
}

// Review applies one of the existing five review states. A nil expected time
// represents the explicit If-Match wildcard; the transport remains responsible
// for rejecting a missing header before calling this method.
func (uc *UseCase) Review(
	ctx context.Context,
	id, status, reviewerID string,
	expectedUpdatedAt *time.Time,
) (entity.CrossReference, error) {
	if !validUUID(id) {
		return entity.CrossReference{}, entity.ErrCrossReferenceNotFound
	}

	if !validUUID(reviewerID) || !validReviewStatus(status) {
		return entity.CrossReference{}, invalid("invalid reviewer or review status")
	}

	return uc.repo.Review(ctx, id, status, reviewerID, expectedUpdatedAt)
}

// ListPublic is hard-wired to approved, visibility-filtered rows. Callers can
// narrow by kind but cannot request another review status or method.
func (uc *UseCase) ListPublic(
	ctx context.Context,
	anchor, direction, kind string,
	limit, offset uint64,
) (entity.CrossReferenceList, error) {
	canonical, err := canonicalAnchor(anchor)
	if err != nil {
		return entity.CrossReferenceList{}, err
	}

	if !validDirection(direction) || (kind != "" && !validKind(kind)) {
		return entity.CrossReferenceList{}, invalid("invalid direction or kind")
	}

	limit, offset = clampPagination(limit, offset)

	return uc.repo.List(ctx, repo.CrossReferenceFilter{
		Anchor:       canonical,
		Direction:    direction,
		Kind:         kind,
		ReviewStatus: entity.CrossReferenceStatusApproved,
		PublicOnly:   true,
		Limit:        limit,
		Offset:       offset,
	})
}

// ListEditorial lists the review queue. Empty Anchor and ReviewStatus mean all;
// unlike the public surface this method may filter method and all five states.
//
//nolint:cyclop,gocyclo // closed matrix keeps every externally accepted value explicit
func (uc *UseCase) ListEditorial(
	ctx context.Context,
	filter repo.CrossReferenceFilter, //nolint:gocritic // interface contract deliberately uses an owned value
) (entity.CrossReferenceList, error) {
	filter.PublicOnly = false

	if filter.Anchor != "" {
		canonical, err := canonicalAnchor(filter.Anchor)
		if err != nil {
			return entity.CrossReferenceList{}, err
		}

		filter.Anchor = canonical
		if !validDirection(filter.Direction) {
			return entity.CrossReferenceList{}, invalid("direction is required with anchor")
		}
	} else if filter.Direction != "" && !validDirection(filter.Direction) {
		return entity.CrossReferenceList{}, invalid("invalid direction")
	}

	if filter.Kind != "" && !validKind(filter.Kind) {
		return entity.CrossReferenceList{}, invalid("invalid kind")
	}

	if filter.Method != "" && !validMethod(filter.Method) {
		return entity.CrossReferenceList{}, invalid("invalid method")
	}

	if filter.ReviewStatus != "" && !validReviewStatus(filter.ReviewStatus) {
		return entity.CrossReferenceList{}, invalid("invalid review status")
	}

	filter.Limit, filter.Offset = clampPagination(filter.Limit, filter.Offset)

	return uc.repo.List(ctx, filter)
}

// FreezeLegacyQuranWrites closes direct writes to quran_book_references after
// the backfill's in-transaction parity preflight succeeds. Service writes keep
// working through the guard GUC.
func (uc *UseCase) FreezeLegacyQuranWrites(ctx context.Context) error {
	return uc.repo.FreezeLegacyQuranWrites(ctx)
}

// UnfreezeLegacyQuranWrites re-opens the compatibility writer for an explicit
// old-binary rollback. It is never called automatically by the bridge job.
func (uc *UseCase) UnfreezeLegacyQuranWrites(ctx context.Context) error {
	return uc.repo.UnfreezeLegacyQuranWrites(ctx)
}

//nolint:cyclop,gocognit,gocyclo,wsl_v5 // one linear validation pipeline mirrors database constraints
func prepareAndValidate(ref *entity.CrossReference, legacy bool) error {
	if !validUUID(ref.ID) {
		return invalid("id must be a UUID")
	}

	source, err := anchorgrammar.Parse(ref.SourceAnchor)
	if err != nil {
		return invalidWithCause("invalid source Anchor", err)
	}

	target, err := anchorgrammar.Parse(ref.TargetAnchor)
	if err != nil {
		return invalidWithCause("invalid target Anchor", err)
	}

	ref.SourceAnchor = source.String()
	ref.TargetAnchor = target.String()
	if ref.SourceAnchor == ref.TargetAnchor {
		return invalid("source and target Anchors must differ")
	}

	applyProjection(ref, &source, true)
	applyProjection(ref, &target, false)

	if !validKind(ref.Kind) || !validMethod(ref.Method) || !validReviewStatus(ref.ReviewStatus) {
		return invalid("invalid kind, method, or review status")
	}

	if ref.Confidence == nil && (!legacy || ref.Origin != entity.CrossReferenceOriginLegacyQuran) {
		return invalid("confidence is required for new Cross-References")
	}

	if ref.Confidence != nil && (*ref.Confidence < 0 || *ref.Confidence > 1) {
		return invalid("confidence must be between zero and one")
	}

	if err := validateMethodDetail(ref); err != nil {
		return err
	}

	if strings.TrimSpace(ref.EvidenceText) == "" || strings.TrimSpace(ref.EvidenceNormalized) == "" {
		return invalid("original and normalized evidence are required")
	}

	if ref.NormalizationVersion != searchtext.ProfileVersion ||
		ref.EvidenceNormalized != searchtext.Normalize(ref.EvidenceText) {
		return invalid("evidence must use the current canonical normalization profile")
	}

	if strings.TrimSpace(ref.Origin) == "" || strings.TrimSpace(ref.OriginKey) == "" {
		return invalid("origin and origin_key are required")
	}

	metadata, err := metadataObject(ref.Metadata)
	if err != nil {
		return err
	}

	ref.Metadata = metadata

	return nil
}

//nolint:wsl_v5 // projection resets are intentionally grouped before corpus-specific assignments
func applyProjection(ref *entity.CrossReference, value *anchorgrammar.Value, source bool) {
	point := value.Start()
	corpus := string(point.Corpus())

	if source {
		ref.SourceCorpus = corpus
		ref.SourceWorkID = nil
		if point.Corpus() == anchorgrammar.CorpusKitab {
			ref.SourceWorkID = new(point.BookID())
		}

		return
	}

	ref.TargetCorpus = corpus
	ref.TargetWorkID = nil
	ref.TargetQuranSurahID = nil
	ref.TargetQuranFromAyah = nil
	ref.TargetQuranToAyah = nil

	switch point.Corpus() {
	case anchorgrammar.CorpusKitab:
		ref.TargetWorkID = new(point.BookID())
	case anchorgrammar.CorpusQuran:
		ref.TargetQuranSurahID = new(point.Surah())
		if point.Kind() == anchorgrammar.PointKindQuranAyah ||
			point.Kind() == anchorgrammar.PointKindQuranUnit {
			ref.TargetQuranFromAyah = new(point.Ayah())
			to := point.Ayah()
			if end, ok := value.End(); ok {
				to = end.Ayah()
			}
			ref.TargetQuranToAyah = new(to)
		}
	case anchorgrammar.CorpusHadith, anchorgrammar.CorpusWiki, anchorgrammar.CorpusEntity:
		// Reserved corpora are currently rejected by the Anchor parser. Keeping
		// this case explicit documents the projection's forward-compatible shape.
	}
}

//nolint:cyclop,gocyclo // conditional method attribution is intentionally enumerated
func validateMethodDetail(ref *entity.CrossReference) error {
	detail := &ref.MethodDetail

	switch ref.Method {
	case entity.CrossReferenceMethodResolver:
		if ref.GenerationRunID != nil {
			return invalid("resolver method must not carry a generation run")
		}
		if strings.TrimSpace(detail.Strategy) == "" {
			return invalid("resolver method requires strategy")
		}

		ref.Generation = nil
	case entity.CrossReferenceMethodMachine:
		if strings.TrimSpace(detail.ModelID) == "" || strings.TrimSpace(detail.PromptVersion) == "" ||
			!validUUID(detail.RunID) {
			return invalid("machine method requires model_id, prompt_version, and run_id")
		}

		if ref.GenerationRunID != nil && *ref.GenerationRunID != detail.RunID {
			return invalid("machine generation_run_id must match method_detail.run_id")
		}

		runID := detail.RunID
		ref.GenerationRunID = &runID
		ref.Generation = &entity.GenerationIdentity{
			RunID:         detail.RunID,
			ModelID:       detail.ModelID,
			PromptVersion: detail.PromptVersion,
		}
	case entity.CrossReferenceMethodHuman:
		if ref.GenerationRunID != nil {
			return invalid("human method must not carry a generation run")
		}
		if ref.CreatedBy == nil || !validUUID(*ref.CreatedBy) || detail.ActorID != *ref.CreatedBy {
			return invalid("human method actor must come from created_by")
		}

		ref.Generation = nil
	default:
		return invalid("unknown method")
	}

	return nil
}

//nolint:cyclop,gocyclo,wsl_v5 // bridge parity checks are a flat translation of the legacy typed contract
func validateBridge(ref *entity.CrossReference, bridge *entity.QuranCrossReferenceBridge) error {
	if bridge.BookID <= 0 || bridge.PageID <= 0 || strings.TrimSpace(bridge.MatchStrategy) == "" ||
		strings.TrimSpace(bridge.SourceText) == "" || strings.TrimSpace(bridge.NormalizedText) == "" {
		return invalid("legacy bridge locator, strategy, and evidence are required")
	}

	if ref.SourceWorkID == nil || *ref.SourceWorkID != bridge.BookID ||
		ref.TargetQuranSurahID == nil || bridge.SurahID == nil ||
		*ref.TargetQuranSurahID != *bridge.SurahID {
		return invalid("legacy bridge projections do not match its Anchors")
	}

	workSource := fmt.Sprintf("kitab/%d", bridge.BookID)
	expectedSource := workSource
	if bridge.HeadingID != nil {
		expectedSource = fmt.Sprintf("%s/h/%d", expectedSource, *bridge.HeadingID)
	}
	// A soft-deleted legacy heading is retained in the bridge for wire parity,
	// while the generic source correctly falls back to the still-active Work.
	if ref.SourceAnchor != expectedSource && ref.SourceAnchor != workSource {
		return invalid("legacy bridge source Anchor does not match its locator")
	}

	if bridge.HeadingID != nil && *bridge.HeadingID <= 0 {
		return invalid("legacy bridge heading must be positive")
	}

	if !validLegacyKind(bridge.ReferenceKind) {
		return invalid("invalid legacy Quran reference kind")
	}

	if bridge.SourceText != ref.EvidenceText || bridge.NormalizedText != ref.EvidenceNormalized ||
		bridge.MatchStrategy != ref.MethodDetail.Strategy {
		return invalid("legacy bridge evidence or strategy differs from its registry row")
	}

	if err := validateLegacyMapping(ref, bridge); err != nil {
		return err
	}

	metadata, err := metadataObject(bridge.Metadata)
	if err != nil {
		return err
	}

	bridge.Metadata = metadata

	return nil
}

//nolint:cyclop,gocognit,gocyclo // four legacy kinds have deliberately explicit point/range mappings
func validateLegacyMapping(ref *entity.CrossReference, bridge *entity.QuranCrossReferenceBridge) error {
	switch bridge.ReferenceKind {
	case "surah":
		if ref.Kind != entity.CrossReferenceKindCites || bridge.FromAyahNumber != nil || bridge.ToAyahNumber != nil {
			return invalid("legacy surah must map to a cites surah Anchor")
		}
	case "surah_ayah", "quote":
		expectedKind := entity.CrossReferenceKindCites
		if bridge.ReferenceKind == "quote" {
			expectedKind = entity.CrossReferenceKindQuotes
		}

		if ref.Kind != expectedKind || bridge.FromAyahNumber == nil || bridge.ToAyahNumber == nil ||
			ref.TargetQuranFromAyah == nil || ref.TargetQuranToAyah == nil ||
			*bridge.FromAyahNumber != *ref.TargetQuranFromAyah ||
			*bridge.ToAyahNumber != *ref.TargetQuranToAyah {
			return invalid("legacy Quran range mapping is inconsistent")
		}

	case "ambiguous":
		if ref.Kind != entity.CrossReferenceKindCites ||
			ref.ReviewStatus != entity.CrossReferenceStatusAmbiguous {
			return invalid("legacy ambiguous links must remain review_status=ambiguous")
		}

		if bridge.FromAyahNumber == nil && bridge.ToAyahNumber == nil {
			if ref.TargetQuranFromAyah != nil || ref.TargetQuranToAyah != nil {
				return invalid("legacy ambiguous surah mapping is inconsistent")
			}

			break
		}

		if bridge.FromAyahNumber == nil || bridge.ToAyahNumber == nil ||
			ref.TargetQuranFromAyah == nil || ref.TargetQuranToAyah == nil ||
			*bridge.FromAyahNumber != *ref.TargetQuranFromAyah ||
			*bridge.ToAyahNumber != *ref.TargetQuranToAyah {
			return invalid("legacy ambiguous Quran range mapping is inconsistent")
		}
	default:
		return invalid("invalid legacy Quran reference kind")
	}

	return nil
}

func metadataObject(raw entity.RawJSON) (entity.RawJSON, error) {
	if len(raw) == 0 {
		return entity.RawJSON(`{}`), nil
	}

	trimmed := bytes.TrimSpace(raw)
	if !json.Valid(trimmed) || !bytes.HasPrefix(trimmed, []byte("{")) {
		return nil, invalid("metadata must be a JSON object")
	}

	return entity.RawJSON(trimmed), nil
}

func canonicalAnchor(raw string) (string, error) {
	value, err := anchorgrammar.Parse(raw)
	if err != nil {
		return "", invalidWithCause("invalid Anchor", err)
	}

	return value.String(), nil
}

func clampPagination(limit, offset uint64) (clampedLimit, clampedOffset uint64) {
	if limit == 0 {
		limit = defaultListLimit
	} else if limit > maxListLimit {
		limit = maxListLimit
	}

	if offset > maxListOffset {
		offset = maxListOffset
	}

	return limit, offset
}

func validKind(value string) bool {
	switch value {
	case entity.CrossReferenceKindCites, entity.CrossReferenceKindQuotes,
		entity.CrossReferenceKindExplains, entity.CrossReferenceKindParallel:
		return true
	default:
		return false
	}
}

func validMethod(value string) bool {
	switch value {
	case entity.CrossReferenceMethodResolver, entity.CrossReferenceMethodMachine, entity.CrossReferenceMethodHuman:
		return true
	default:
		return false
	}
}

func validReviewStatus(value string) bool {
	switch value {
	case entity.CrossReferenceStatusPending, entity.CrossReferenceStatusApproved,
		entity.CrossReferenceStatusRejected, entity.CrossReferenceStatusAmbiguous,
		entity.CrossReferenceStatusNeedsReview:
		return true
	default:
		return false
	}
}

func validDirection(value string) bool {
	return value == entity.CrossReferenceDirectionIncoming || value == entity.CrossReferenceDirectionOutgoing
}

func validLegacyKind(value string) bool {
	switch value {
	case "surah_ayah", "surah", "quote", "ambiguous":
		return true
	default:
		return false
	}
}

func validUUID(value string) bool {
	_, err := uuid.Parse(value)

	return err == nil
}

func invalid(message string) error {
	return fmt.Errorf("%w: %s", entity.ErrInvalidCrossReference, message)
}

func invalidWithCause(message string, cause error) error {
	return fmt.Errorf("%w: %s: %w", entity.ErrInvalidCrossReference, message, cause)
}

// IsInvalid is a small transport/helper seam which avoids exposing validation
// internals while preserving errors.Is behavior.
func IsInvalid(err error) bool {
	return errors.Is(err, entity.ErrInvalidCrossReference)
}
