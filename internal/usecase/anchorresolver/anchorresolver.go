// Package anchorresolver implements the public B-2 Anchor resolution
// capability across canonical and grandfathered legacy locator forms.
package anchorresolver

import (
	"context"
	"errors"
	"fmt"

	anchorgrammar "github.com/alfariesh/surau-backend/internal/anchor"
	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/repo"
	"golang.org/x/sync/errgroup"
)

// UseCase resolves one point or the two bounded endpoints of a range.
type UseCase struct {
	repo repo.AnchorRepo
}

const (
	maxPostgresInteger       = 1<<31 - 1
	anchorRangeBoundaryCount = 2
)

var errInvalidResolutionRecord = errors.New("invalid anchor resolution record")

// New constructs the read-only resolver.
func New(r repo.AnchorRepo) *UseCase {
	return &UseCase{repo: r}
}

// Resolve accepts exactly one public input shape: a canonical/legacy anchor,
// or the physical legacy page tuple. Legacy toc anchors additionally require
// a book scope because heading ids are only unique within one Work.
func (uc *UseCase) Resolve(
	ctx context.Context,
	rawAnchor string,
	bookID, pageID *int,
) (entity.AnchorResolution, error) {
	if rawAnchor == "" {
		return uc.resolveLegacyPage(ctx, bookID, pageID)
	}

	if pageID != nil {
		return entity.AnchorResolution{}, invalidAnchor("page_id cannot accompany anchor", nil)
	}

	value, canonicalErr := anchorgrammar.Parse(rawAnchor)
	if canonicalErr == nil {
		if bookID != nil {
			return entity.AnchorResolution{}, invalidAnchor("book_id is encoded in canonical anchors", nil)
		}

		return uc.resolveCanonical(ctx, rawAnchor, &value)
	}

	if ayah, err := anchorgrammar.ParseLegacyAyahKey(rawAnchor); err == nil {
		if bookID != nil {
			return entity.AnchorResolution{}, invalidAnchor("book_id cannot scope ayah_key", nil)
		}

		return uc.resolveLegacyAyah(ctx, rawAnchor, ayah)
	}

	headingID, tocErr := anchorgrammar.ParseLegacyTOC(rawAnchor)
	if tocErr == nil {
		if bookID == nil {
			return entity.AnchorResolution{}, invalidAnchor("legacy toc anchor requires book_id", nil)
		}

		return uc.resolveLegacyTOC(ctx, rawAnchor, *bookID, headingID)
	}

	return entity.AnchorResolution{}, invalidAnchor("unsupported anchor form", errors.Join(canonicalErr, tocErr))
}

func (uc *UseCase) resolveCanonical(
	ctx context.Context,
	raw string,
	value *anchorgrammar.Value,
) (entity.AnchorResolution, error) {
	canonical := value.String()
	result := entity.AnchorResolution{
		Requested:       entity.AnchorRequested{Form: entity.AnchorFormCanonical, Anchor: raw},
		CanonicalAnchor: new(canonical),
		Boundaries:      make([]entity.AnchorBoundary, 1, anchorRangeBoundaryCount),
	}

	if !value.IsRange() {
		lookup, err := uc.resolvePoint(ctx, value.Start())
		if err != nil {
			return entity.AnchorResolution{}, err
		}

		boundary, err := boundaryFromLookup(entity.AnchorBoundaryPoint, &lookup)
		if err != nil {
			return entity.AnchorResolution{}, err
		}

		result.Boundaries[0] = boundary

		return result, nil
	}

	end, _ := value.End()
	lookups := make([]entity.AnchorLookupResult, anchorRangeBoundaryCount)
	group, groupCtx := errgroup.WithContext(ctx)
	group.Go(func() error {
		lookup, err := uc.resolvePoint(groupCtx, value.Start())
		lookups[0] = lookup

		return err
	})
	group.Go(func() error {
		lookup, err := uc.resolvePoint(groupCtx, end)
		lookups[1] = lookup

		return err
	})

	if err := group.Wait(); err != nil {
		return entity.AnchorResolution{}, err
	}

	startBoundary, err := boundaryFromLookup(entity.AnchorBoundaryStart, &lookups[0])
	if err != nil {
		return entity.AnchorResolution{}, err
	}

	endBoundary, err := boundaryFromLookup(entity.AnchorBoundaryEnd, &lookups[1])
	if err != nil {
		return entity.AnchorResolution{}, err
	}

	result.Boundaries[0] = startBoundary
	result.Boundaries = append(result.Boundaries, endBoundary)

	return result, nil
}

func (uc *UseCase) resolveLegacyAyah(
	ctx context.Context,
	raw string,
	point anchorgrammar.Point,
) (entity.AnchorResolution, error) {
	lookup, err := uc.repo.ResolveQuran(ctx, fmt.Sprintf("%d:%d", point.Surah(), point.Ayah()))
	if err != nil {
		return entity.AnchorResolution{}, err
	}

	canonical := point.String()
	prependRedirect(&lookup, entity.AnchorRedirect{
		From: raw, To: canonical, Reason: entity.AnchorRedirectLegacyAlias, Depth: 1,
	})

	boundary, err := boundaryFromLookup(entity.AnchorBoundaryPoint, &lookup)
	if err != nil {
		return entity.AnchorResolution{}, err
	}

	return entity.AnchorResolution{
		Requested:       entity.AnchorRequested{Form: entity.AnchorFormLegacyAyahKey, Anchor: raw},
		CanonicalAnchor: new(canonical),
		Boundaries:      []entity.AnchorBoundary{boundary},
	}, nil
}

func (uc *UseCase) resolveLegacyTOC(
	ctx context.Context,
	raw string,
	bookID, headingID int,
) (entity.AnchorResolution, error) {
	canonicalPoint, err := anchorgrammar.NewKitabHeading(bookID, headingID)
	if err != nil {
		return entity.AnchorResolution{}, invalidAnchor("invalid legacy toc scope", err)
	}

	lookup, err := uc.repo.ResolveHeading(ctx, bookID, headingID)
	if err != nil {
		return entity.AnchorResolution{}, err
	}

	canonical := canonicalPoint.String()
	prependRedirect(&lookup, entity.AnchorRedirect{
		From: raw, To: canonical, Reason: entity.AnchorRedirectLegacyAlias, Depth: 1,
	})

	boundary, err := boundaryFromLookup(entity.AnchorBoundaryPoint, &lookup)
	if err != nil {
		return entity.AnchorResolution{}, err
	}

	return entity.AnchorResolution{
		Requested: entity.AnchorRequested{
			Form: entity.AnchorFormLegacyTOC, Anchor: raw, BookID: new(bookID),
		},
		CanonicalAnchor: new(canonical),
		Boundaries:      []entity.AnchorBoundary{boundary},
	}, nil
}

//nolint:cyclop,gocyclo // explicit validation plus deterministic synthetic redirects keep the contract visible
func (uc *UseCase) resolveLegacyPage(
	ctx context.Context,
	bookID, pageID *int,
) (entity.AnchorResolution, error) {
	if bookID == nil || pageID == nil {
		return entity.AnchorResolution{}, invalidAnchor("legacy page requires book_id and page_id", nil)
	}

	if *bookID <= 0 || *pageID <= 0 || *bookID > maxPostgresInteger || *pageID > maxPostgresInteger {
		return entity.AnchorResolution{}, invalidAnchor("legacy page scope exceeds a PostgreSQL integer", nil)
	}

	lookup, err := uc.repo.ResolvePage(ctx, *bookID, *pageID)
	if err != nil {
		return entity.AnchorResolution{}, err
	}

	from := fmt.Sprintf("page:%d:%d", *bookID, *pageID)

	redirects := make([]entity.AnchorRedirect, 0, len(lookup.ActiveRecords))
	for index := range lookup.ActiveRecords {
		record := &lookup.ActiveRecords[index]
		if record.CanonicalAnchor == nil {
			continue
		}

		redirects = append(redirects, entity.AnchorRedirect{
			From: from, To: *record.CanonicalAnchor, Reason: entity.AnchorRedirectLegacyPage, Depth: 1,
		})
	}

	prependRedirects(&lookup, redirects)

	boundary, err := boundaryFromLookup(entity.AnchorBoundaryPoint, &lookup)
	if err != nil {
		return entity.AnchorResolution{}, err
	}

	return entity.AnchorResolution{
		Requested: entity.AnchorRequested{
			Form: entity.AnchorFormLegacyPage, BookID: new(*bookID), PageID: new(*pageID),
		},
		CanonicalAnchor: nil,
		Boundaries:      []entity.AnchorBoundary{boundary},
	}, nil
}

func (uc *UseCase) resolvePoint(ctx context.Context, point anchorgrammar.Point) (entity.AnchorLookupResult, error) {
	switch point.Kind() {
	case anchorgrammar.PointKindQuranSurah:
		return uc.repo.ResolveQuranSurah(ctx, point.Surah())
	case anchorgrammar.PointKindQuranAyah:
		return uc.repo.ResolveQuran(ctx, fmt.Sprintf("%d:%d", point.Surah(), point.Ayah()))
	case anchorgrammar.PointKindKitabWork:
		return uc.repo.ResolveWork(ctx, point.BookID())
	case anchorgrammar.PointKindKitabHeading:
		return uc.repo.ResolveHeading(ctx, point.BookID(), point.HeadingID())
	case anchorgrammar.PointKindKitabUnit:
		return uc.repo.ResolveCanonicalUnit(ctx, point.String())
	default:
		return entity.AnchorLookupResult{}, invalidAnchor("unsupported canonical point", nil)
	}
}

func boundaryFromLookup(role string, lookup *entity.AnchorLookupResult) (entity.AnchorBoundary, error) {
	if lookup.CycleDetected {
		return entity.AnchorBoundary{}, entity.ErrAnchorLineageCycle
	}

	targets := make([]entity.AnchorTarget, 0, len(lookup.ActiveRecords))
	for index := range lookup.ActiveRecords {
		target, err := targetFromRecord(&lookup.ActiveRecords[index])
		if err != nil {
			return entity.AnchorBoundary{}, err
		}

		targets = append(targets, target)
	}

	redirects := append([]entity.AnchorRedirect(nil), lookup.RedirectChain...)
	if redirects == nil {
		redirects = make([]entity.AnchorRedirect, 0)
	}

	return entity.AnchorBoundary{
		Role:            role,
		CanonicalAnchor: lookup.CanonicalAnchor,
		Status:          lookup.Status,
		ActiveTargets:   targets,
		RedirectChain:   redirects,
	}, nil
}

func targetFromRecord(record *entity.AnchorRecord) (entity.AnchorTarget, error) {
	navigationURL, err := navigationURLForRecord(record)
	if err != nil {
		return entity.AnchorTarget{}, err
	}

	target := entity.AnchorTarget{
		TargetType:      record.TargetType,
		Corpus:          record.Corpus,
		CanonicalAnchor: record.CanonicalAnchor,
		UnitID:          record.UnitID,
		BookID:          record.BookID,
		HeadingID:       record.HeadingID,
		PageID:          record.PageID,
		SurahID:         record.SurahID,
		AyahKey:         record.AyahKey,
		NavigationURL:   navigationURL,
		UpdatedAt:       record.UpdatedAt,
	}

	return target, nil
}

//nolint:cyclop,gocognit,gocyclo // target variants are intentionally enumerated as one wire-contract switch
func navigationURLForRecord(record *entity.AnchorRecord) (string, error) {
	switch record.TargetType {
	case entity.AnchorTargetQuranSurah:
		if record.SurahID == nil {
			return "", invalidResolutionRecord("Quran surah target is missing surah_id")
		}

		return fmt.Sprintf("/v1/quran/surahs/%d", *record.SurahID), nil
	case entity.AnchorTargetQuranAyah:
		if record.AyahKey == nil {
			return "", invalidResolutionRecord("Quran target is missing ayah_key")
		}

		return "/v1/quran/ayahs/" + *record.AyahKey, nil
	case entity.AnchorTargetBook:
		if record.BookID == nil {
			return "", invalidResolutionRecord("book target is missing book_id")
		}

		return fmt.Sprintf("/v1/books/%d", *record.BookID), nil
	case entity.AnchorTargetBookHeading:
		if record.BookID == nil || record.HeadingID == nil {
			return "", invalidResolutionRecord("heading target is missing scope")
		}

		return fmt.Sprintf("/v1/books/%d/toc/%d/read", *record.BookID, *record.HeadingID), nil
	case entity.AnchorTargetBookPage:
		if record.BookID == nil || record.PageID == nil {
			return "", invalidResolutionRecord("page target is missing scope")
		}

		return fmt.Sprintf("/v1/books/%d/pages/%d", *record.BookID, *record.PageID), nil
	case entity.AnchorTargetCitableUnit:
		if record.BookID == nil {
			return "", invalidResolutionRecord("unit target is missing book_id")
		}

		switch {
		case record.HeadingID != nil && *record.HeadingID > 0:
			return fmt.Sprintf("/v1/books/%d/toc/%d/read", *record.BookID, *record.HeadingID), nil
		case record.PageID != nil:
			return fmt.Sprintf("/v1/books/%d/pages/%d", *record.BookID, *record.PageID), nil
		default:
			return fmt.Sprintf("/v1/books/%d", *record.BookID), nil
		}
	default:
		return "", fmt.Errorf("%w: unsupported target type %q", errInvalidResolutionRecord, record.TargetType)
	}
}

func invalidResolutionRecord(detail string) error {
	return fmt.Errorf("%w: %s", errInvalidResolutionRecord, detail)
}

func prependRedirect(lookup *entity.AnchorLookupResult, redirect entity.AnchorRedirect) {
	prependRedirects(lookup, []entity.AnchorRedirect{redirect})
}

func prependRedirects(lookup *entity.AnchorLookupResult, prefixes []entity.AnchorRedirect) {
	if len(prefixes) == 0 {
		return
	}

	for index := range lookup.RedirectChain {
		lookup.RedirectChain[index].Depth++
	}

	lookup.RedirectChain = append(prefixes, lookup.RedirectChain...)
}

func invalidAnchor(detail string, cause error) error {
	if cause == nil {
		return fmt.Errorf("%w: %s", entity.ErrInvalidAnchor, detail)
	}

	return fmt.Errorf("%w: %s: %w", entity.ErrInvalidAnchor, detail, cause)
}
