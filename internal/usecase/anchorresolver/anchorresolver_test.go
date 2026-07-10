package anchorresolver_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/usecase/anchorresolver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errAnchorRepository = errors.New("anchor repository unavailable")

type fakeAnchorRepo struct {
	mu sync.Mutex

	quran    map[string]entity.AnchorLookupResult
	works    map[int]entity.AnchorLookupResult
	headings map[[2]int]entity.AnchorLookupResult
	pages    map[[2]int]entity.AnchorLookupResult
	units    map[string]entity.AnchorLookupResult
	err      error
	calls    []string
}

func (f *fakeAnchorRepo) ResolveQuran(_ context.Context, ayahKey string) (entity.AnchorLookupResult, error) {
	return f.lookup("quran:"+ayahKey, f.quran[ayahKey])
}

func (f *fakeAnchorRepo) ResolveWork(_ context.Context, bookID int) (entity.AnchorLookupResult, error) {
	return f.lookup("work", f.works[bookID])
}

func (f *fakeAnchorRepo) ResolveHeading(_ context.Context, bookID, headingID int) (entity.AnchorLookupResult, error) {
	return f.lookup("heading", f.headings[[2]int{bookID, headingID}])
}

func (f *fakeAnchorRepo) ResolvePage(_ context.Context, bookID, pageID int) (entity.AnchorLookupResult, error) {
	return f.lookup("page", f.pages[[2]int{bookID, pageID}])
}

func (f *fakeAnchorRepo) ResolveCanonicalUnit(_ context.Context, canonical string) (entity.AnchorLookupResult, error) {
	return f.lookup("unit:"+canonical, f.units[canonical])
}

//nolint:gocritic // the value mirrors the repository interface and keeps the fake compact
func (f *fakeAnchorRepo) lookup(call string, result entity.AnchorLookupResult) (entity.AnchorLookupResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, call)
	if f.err != nil {
		return entity.AnchorLookupResult{}, f.err
	}

	if result.Status == "" && result.CanonicalAnchor == nil && result.ActiveRecords == nil {
		return entity.AnchorLookupResult{}, entity.ErrAnchorNotFound
	}

	return result, nil
}

func (f *fakeAnchorRepo) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return len(f.calls)
}

func TestResolveCanonicalPointProfiles(t *testing.T) {
	t.Parallel()

	updatedAt := time.Date(2026, time.July, 10, 8, 30, 0, 0, time.UTC)
	repo := &fakeAnchorRepo{
		quran: map[string]entity.AnchorLookupResult{
			"73:4": activeLookup("quran/73:4", quranRecord("73:4", updatedAt)),
		},
		works: map[int]entity.AnchorLookupResult{
			797: activeLookup("kitab/797", bookRecord(797, updatedAt)),
		},
		headings: map[[2]int]entity.AnchorLookupResult{
			{797, 11}: activeLookup("kitab/797/h/11", headingRecord(797, 11, 12, updatedAt)),
		},
		units: map[string]entity.AnchorLookupResult{
			"kitab/797/h/11/u/42": activeLookup(
				"kitab/797/h/11/u/42",
				unitRecord("unit-42", "kitab/797/h/11/u/42", 797, 11, 12, updatedAt),
			),
		},
	}
	resolver := anchorresolver.New(repo)

	tests := []struct {
		name           string
		anchor         string
		wantTargetType string
		wantURL        string
	}{
		{name: "Quran ayah", anchor: "quran/73:4", wantTargetType: entity.AnchorTargetQuranAyah, wantURL: "/v1/quran/ayahs/73:4"},
		{name: "kitab Work", anchor: "kitab/797", wantTargetType: entity.AnchorTargetBook, wantURL: "/v1/books/797"},
		{name: "kitab heading", anchor: "kitab/797/h/11", wantTargetType: entity.AnchorTargetBookHeading, wantURL: "/v1/books/797/toc/11/read"},
		{name: "kitab unit", anchor: "kitab/797/h/11/u/42", wantTargetType: entity.AnchorTargetCitableUnit, wantURL: "/v1/books/797/toc/11/read"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := resolver.Resolve(t.Context(), test.anchor, nil, nil)
			require.NoError(t, err)
			require.NotNil(t, got.CanonicalAnchor)
			assert.Equal(t, test.anchor, *got.CanonicalAnchor)
			assert.Equal(t, entity.AnchorFormCanonical, got.Requested.Form)
			assert.Equal(t, test.anchor, got.Requested.Anchor)
			require.Len(t, got.Boundaries, 1)
			assert.Equal(t, entity.AnchorBoundaryPoint, got.Boundaries[0].Role)
			assert.Equal(t, entity.UnitLifecycleActive, got.Boundaries[0].Status)
			require.Len(t, got.Boundaries[0].ActiveTargets, 1)
			assert.Equal(t, test.wantTargetType, got.Boundaries[0].ActiveTargets[0].TargetType)
			assert.Equal(t, test.wantURL, got.Boundaries[0].ActiveTargets[0].NavigationURL)
			assert.Empty(t, got.Boundaries[0].RedirectChain)
		})
	}
}

func TestResolveCanonicalRangeResolvesBoundariesOnly(t *testing.T) {
	t.Parallel()

	updatedAt := time.Date(2026, time.July, 10, 9, 0, 0, 0, time.UTC)
	repo := &fakeAnchorRepo{units: map[string]entity.AnchorLookupResult{
		"kitab/797/h/11/u/1": activeLookup(
			"kitab/797/h/11/u/1",
			unitRecord("unit-1", "kitab/797/h/11/u/1", 797, 11, 12, updatedAt),
		),
		"kitab/797/h/11/u/42": activeLookup(
			"kitab/797/h/11/u/42",
			unitRecord("unit-42", "kitab/797/h/11/u/42", 797, 11, 15, updatedAt),
		),
	}}
	resolver := anchorresolver.New(repo)

	const raw = "kitab/797/h/11/u/1..kitab/797/h/11/u/42"

	got, err := resolver.Resolve(t.Context(), raw, nil, nil)
	require.NoError(t, err)
	require.NotNil(t, got.CanonicalAnchor)
	assert.Equal(t, raw, *got.CanonicalAnchor)
	require.Len(t, got.Boundaries, 2)
	assert.Equal(t, entity.AnchorBoundaryStart, got.Boundaries[0].Role)
	assert.Equal(t, "unit-1", valueOrEmpty(got.Boundaries[0].ActiveTargets[0].UnitID))
	assert.Equal(t, entity.AnchorBoundaryEnd, got.Boundaries[1].Role)
	assert.Equal(t, "unit-42", valueOrEmpty(got.Boundaries[1].ActiveTargets[0].UnitID))
	assert.Equal(t, 2, repo.callCount(), "a range must perform exactly two boundary lookups")
}

func TestResolveLegacyForms(t *testing.T) {
	t.Parallel()

	updatedAt := time.Date(2026, time.July, 10, 10, 0, 0, 0, time.UTC)
	repo := &fakeAnchorRepo{
		quran: map[string]entity.AnchorLookupResult{
			"73:4": activeLookup("quran/73:4", quranRecord("73:4", updatedAt)),
		},
		headings: map[[2]int]entity.AnchorLookupResult{
			{797, 11}: activeLookup(
				"kitab/797/h/11",
				unitRecord("unit-1", "kitab/797/h/11/u/1", 797, 11, 12, updatedAt),
				unitRecord("unit-2", "kitab/797/h/11/u/2", 797, 11, 12, updatedAt),
			),
		},
		pages: map[[2]int]entity.AnchorLookupResult{
			{797, 12}: {
				Status: entity.UnitLifecycleActive,
				ActiveRecords: []entity.AnchorRecord{
					unitRecord("unit-1", "kitab/797/h/11/u/1", 797, 11, 12, updatedAt),
					unitRecord("unit-2", "kitab/797/h/11/u/2", 797, 11, 12, updatedAt),
				},
			},
		},
	}
	resolver := anchorresolver.New(repo)

	t.Run("legacy ayah_key", func(t *testing.T) {
		t.Parallel()

		got, err := resolver.Resolve(t.Context(), "73:4", nil, nil)
		require.NoError(t, err)
		assert.Equal(t, entity.AnchorFormLegacyAyahKey, got.Requested.Form)
		require.NotNil(t, got.CanonicalAnchor)
		assert.Equal(t, "quran/73:4", *got.CanonicalAnchor)
		require.Len(t, got.Boundaries[0].RedirectChain, 1)
		assert.Equal(t, entity.AnchorRedirect{From: "73:4", To: "quran/73:4", Reason: entity.AnchorRedirectLegacyAlias, Depth: 1}, got.Boundaries[0].RedirectChain[0])
	})

	t.Run("legacy toc returns every active unit", func(t *testing.T) {
		t.Parallel()

		bookID := 797
		got, err := resolver.Resolve(t.Context(), "toc-11", &bookID, nil)
		require.NoError(t, err)
		assert.Equal(t, entity.AnchorFormLegacyTOC, got.Requested.Form)
		require.NotNil(t, got.Requested.BookID)
		assert.Equal(t, 797, *got.Requested.BookID)
		require.NotNil(t, got.CanonicalAnchor)
		assert.Equal(t, "kitab/797/h/11", *got.CanonicalAnchor)
		require.Len(t, got.Boundaries[0].ActiveTargets, 2)
		require.Len(t, got.Boundaries[0].RedirectChain, 1)
		assert.Equal(t, "toc-11", got.Boundaries[0].RedirectChain[0].From)
		assert.Equal(t, "kitab/797/h/11", got.Boundaries[0].RedirectChain[0].To)
	})

	t.Run("legacy page has no invented canonical anchor", func(t *testing.T) {
		t.Parallel()

		bookID, pageID := 797, 12
		got, err := resolver.Resolve(t.Context(), "", &bookID, &pageID)
		require.NoError(t, err)
		assert.Equal(t, entity.AnchorFormLegacyPage, got.Requested.Form)
		assert.Nil(t, got.CanonicalAnchor)
		require.Len(t, got.Boundaries[0].ActiveTargets, 2)
		require.Len(t, got.Boundaries[0].RedirectChain, 2)
		assert.Equal(t, "page:797:12", got.Boundaries[0].RedirectChain[0].From)
		assert.Equal(t, "kitab/797/h/11/u/1", got.Boundaries[0].RedirectChain[0].To)
		assert.Equal(t, "kitab/797/h/11/u/2", got.Boundaries[0].RedirectChain[1].To)
	})
}

func TestResolveLineagePreservesBranchingDepthsAndTargets(t *testing.T) {
	t.Parallel()

	updatedAt := time.Date(2026, time.July, 10, 11, 0, 0, 0, time.UTC)

	const root = "kitab/797/h/11/u/1"

	repo := &fakeAnchorRepo{units: map[string]entity.AnchorLookupResult{
		root: {
			CanonicalAnchor: new(root),
			Status:          entity.UnitLifecycleSuperseded,
			ActiveRecords: []entity.AnchorRecord{
				unitRecord("unit-3", "kitab/797/h/11/u/3", 797, 11, 12, updatedAt),
				unitRecord("unit-4", "kitab/797/h/11/u/4", 797, 11, 12, updatedAt),
			},
			RedirectChain: []entity.AnchorRedirect{
				{From: root, To: "kitab/797/h/11/u/2", Reason: "edit", Depth: 1},
				{From: "kitab/797/h/11/u/2", To: "kitab/797/h/11/u/3", Reason: "edit", Depth: 2},
				{From: "kitab/797/h/11/u/2", To: "kitab/797/h/11/u/4", Reason: "content_move", Depth: 2},
			},
		},
	}}

	got, err := anchorresolver.New(repo).Resolve(t.Context(), root, nil, nil)
	require.NoError(t, err)
	require.Len(t, got.Boundaries, 1)
	boundary := got.Boundaries[0]
	assert.Equal(t, entity.UnitLifecycleSuperseded, boundary.Status)
	require.Len(t, boundary.ActiveTargets, 2)
	assert.Equal(t, []string{"unit-3", "unit-4"}, []string{
		valueOrEmpty(boundary.ActiveTargets[0].UnitID),
		valueOrEmpty(boundary.ActiveTargets[1].UnitID),
	})
	require.Len(t, boundary.RedirectChain, 3)
	assert.Equal(t, []int{1, 2, 2}, []int{
		boundary.RedirectChain[0].Depth,
		boundary.RedirectChain[1].Depth,
		boundary.RedirectChain[2].Depth,
	})
}

func TestResolveLegacyAliasShiftsExistingLineageDepths(t *testing.T) {
	t.Parallel()

	updatedAt := time.Date(2026, time.July, 10, 12, 0, 0, 0, time.UTC)
	repo := &fakeAnchorRepo{quran: map[string]entity.AnchorLookupResult{
		"73:4": {
			CanonicalAnchor: new("quran/73:4"),
			Status:          entity.UnitLifecycleActive,
			ActiveRecords:   []entity.AnchorRecord{quranRecord("73:4", updatedAt)},
			RedirectChain: []entity.AnchorRedirect{
				{From: "quran/73:4", To: "quran/73:5", Reason: "edit", Depth: 1},
			},
		},
	}}

	got, err := anchorresolver.New(repo).Resolve(t.Context(), "73:4", nil, nil)
	require.NoError(t, err)
	require.Len(t, got.Boundaries[0].RedirectChain, 2)
	assert.Equal(t, 1, got.Boundaries[0].RedirectChain[0].Depth)
	assert.Equal(t, 2, got.Boundaries[0].RedirectChain[1].Depth)
}

func TestResolveKnownTombstoneAndCycle(t *testing.T) {
	t.Parallel()

	const tombstone = "kitab/797/h/11/u/9"

	t.Run("known tombstone is a successful empty resolution", func(t *testing.T) {
		t.Parallel()

		repo := &fakeAnchorRepo{units: map[string]entity.AnchorLookupResult{
			tombstone: {
				CanonicalAnchor: new(tombstone),
				Status:          entity.UnitLifecycleTombstoned,
				ActiveRecords:   []entity.AnchorRecord{},
			},
		}}

		got, err := anchorresolver.New(repo).Resolve(t.Context(), tombstone, nil, nil)
		require.NoError(t, err)
		require.Len(t, got.Boundaries, 1)
		assert.Equal(t, entity.UnitLifecycleTombstoned, got.Boundaries[0].Status)
		assert.Empty(t, got.Boundaries[0].ActiveTargets)
		assert.Empty(t, got.Boundaries[0].RedirectChain)
	})

	t.Run("cycle is never silently truncated", func(t *testing.T) {
		t.Parallel()

		repo := &fakeAnchorRepo{units: map[string]entity.AnchorLookupResult{
			tombstone: {
				CanonicalAnchor: new(tombstone),
				Status:          entity.UnitLifecycleSuperseded,
				CycleDetected:   true,
			},
		}}

		_, err := anchorresolver.New(repo).Resolve(t.Context(), tombstone, nil, nil)
		assert.ErrorIs(t, err, entity.ErrAnchorLineageCycle)
	})
}

func TestResolveRejectsInvalidAmbiguousAndMissingScope(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		anchor string
		bookID *int
		pageID *int
	}{
		{name: "nothing supplied"},
		{name: "page misses page id", bookID: new(797)},
		{name: "page misses book id", pageID: new(12)},
		{name: "canonical plus book scope", anchor: "kitab/797", bookID: new(797)},
		{name: "canonical plus page scope", anchor: "kitab/797", pageID: new(12)},
		{name: "legacy ayah plus book scope", anchor: "73:4", bookID: new(797)},
		{name: "toc missing book scope", anchor: "toc-11"},
		{name: "toc plus page scope", anchor: "toc-11", bookID: new(797), pageID: new(12)},
		{name: "leading zero legacy ayah", anchor: "073:4"},
		{name: "unsupported anchor", anchor: "page-12"},
		{name: "range crosses Work", anchor: "kitab/797..kitab/798"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			repo := &fakeAnchorRepo{}
			_, err := anchorresolver.New(repo).Resolve(t.Context(), test.anchor, test.bookID, test.pageID)
			assert.ErrorIs(t, err, entity.ErrInvalidAnchor)
			assert.Zero(t, repo.callCount(), "invalid input must fail before persistence")
		})
	}
}

func TestResolvePropagatesRepositoryFailuresAndNotFound(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		err  error
	}{
		{name: "not found", err: entity.ErrAnchorNotFound},
		{name: "repository failure", err: errAnchorRepository},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			repo := &fakeAnchorRepo{err: test.err}
			_, err := anchorresolver.New(repo).Resolve(t.Context(), "quran/73:4", nil, nil)
			assert.ErrorIs(t, err, test.err)
		})
	}
}

func activeLookup(canonical string, records ...entity.AnchorRecord) entity.AnchorLookupResult {
	return entity.AnchorLookupResult{
		CanonicalAnchor: new(canonical),
		Status:          entity.UnitLifecycleActive,
		ActiveRecords:   records,
	}
}

func quranRecord(ayahKey string, updatedAt time.Time) entity.AnchorRecord {
	canonical := "quran/" + ayahKey

	return entity.AnchorRecord{
		TargetType:      entity.AnchorTargetQuranAyah,
		Corpus:          entity.UnitCorpusQuran,
		CanonicalAnchor: &canonical,
		AyahKey:         new(ayahKey),
		Lifecycle:       entity.UnitLifecycleActive,
		UpdatedAt:       updatedAt,
	}
}

func bookRecord(bookID int, updatedAt time.Time) entity.AnchorRecord {
	canonical := "kitab/797"

	return entity.AnchorRecord{
		TargetType:      entity.AnchorTargetBook,
		Corpus:          entity.UnitCorpusKitab,
		CanonicalAnchor: &canonical,
		BookID:          new(bookID),
		Lifecycle:       entity.UnitLifecycleActive,
		UpdatedAt:       updatedAt,
	}
}

func headingRecord(bookID, headingID, pageID int, updatedAt time.Time) entity.AnchorRecord {
	canonical := "kitab/797/h/11"

	return entity.AnchorRecord{
		TargetType:      entity.AnchorTargetBookHeading,
		Corpus:          entity.UnitCorpusKitab,
		CanonicalAnchor: &canonical,
		BookID:          new(bookID),
		HeadingID:       new(headingID),
		PageID:          new(pageID),
		Lifecycle:       entity.UnitLifecycleActive,
		UpdatedAt:       updatedAt,
	}
}

//nolint:unparam // all fixtures intentionally stay in the one Work under test
func unitRecord(id, canonical string, bookID, headingID, pageID int, updatedAt time.Time) entity.AnchorRecord {
	return entity.AnchorRecord{
		TargetType:      entity.AnchorTargetCitableUnit,
		Corpus:          entity.UnitCorpusKitab,
		CanonicalAnchor: new(canonical),
		UnitID:          new(id),
		BookID:          new(bookID),
		HeadingID:       new(headingID),
		PageID:          new(pageID),
		Lifecycle:       entity.UnitLifecycleActive,
		UpdatedAt:       updatedAt,
	}
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}

	return *value
}
