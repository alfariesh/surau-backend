package crossreference

import (
	"context"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/repo"
	"github.com/alfariesh/surau-backend/internal/searchtext"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeCrossReferenceRepo struct {
	created     []entity.CrossReference
	derived     []entity.CrossReference
	bridges     []*entity.QuranCrossReferenceBridge
	reviewed    []string
	listFilter  repo.CrossReferenceFilter
	listResult  entity.CrossReferenceList
	getResult   entity.CrossReference
	err         error
	freezeCalls int
}

func (f *fakeCrossReferenceRepo) Create(
	_ context.Context,
	ref entity.CrossReference, //nolint:gocritic // interface contract
) (entity.CrossReference, error) {
	f.created = append(f.created, ref)

	return ref, f.err
}

func (f *fakeCrossReferenceRepo) UpsertDerived(
	_ context.Context,
	ref entity.CrossReference, //nolint:gocritic // interface contract
	bridge *entity.QuranCrossReferenceBridge,
) (entity.CrossReference, error) {
	f.derived = append(f.derived, ref)
	f.bridges = append(f.bridges, bridge)

	return ref, f.err
}

func (f *fakeCrossReferenceRepo) Get(_ context.Context, _ string) (entity.CrossReference, error) {
	return f.getResult, f.err
}

func (f *fakeCrossReferenceRepo) Review(
	_ context.Context,
	_, status, _ string,
	_ *time.Time,
) (entity.CrossReference, error) {
	f.reviewed = append(f.reviewed, status)

	return entity.CrossReference{ReviewStatus: status}, f.err
}

func (f *fakeCrossReferenceRepo) List(
	_ context.Context,
	filter repo.CrossReferenceFilter, //nolint:gocritic // interface contract
) (entity.CrossReferenceList, error) {
	f.listFilter = filter

	return f.listResult, f.err
}

func (f *fakeCrossReferenceRepo) FreezeLegacyQuranWrites(context.Context) error {
	f.freezeCalls++

	return f.err
}

func (f *fakeCrossReferenceRepo) UnfreezeLegacyQuranWrites(context.Context) error {
	return f.err
}

func TestCreateHumanSetsServerOwnedFieldsForEveryKind(t *testing.T) {
	t.Parallel()

	actorID := uuid.NewString()
	evidence := "  سُورَةُ المُزَّمِّل  "

	for _, kind := range []string{
		entity.CrossReferenceKindCites,
		entity.CrossReferenceKindQuotes,
		entity.CrossReferenceKindExplains,
		entity.CrossReferenceKindParallel,
	} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()

			fake := &fakeCrossReferenceRepo{}
			uc := New(fake)
			got, err := uc.CreateHuman(context.Background(), entity.CrossReferenceCreateInput{
				SourceAnchor: "kitab/797/h/11",
				TargetAnchor: "quran/73:4..quran/73:10",
				Kind:         kind,
				Confidence:   0.75,
				EvidenceText: evidence,
			}, actorID)
			require.NoError(t, err)

			assert.Equal(t, entity.CrossReferenceMethodHuman, got.Method)
			assert.Equal(t, entity.CrossReferenceStatusPending, got.ReviewStatus)
			assert.Equal(t, actorID, got.MethodDetail.ActorID)
			require.NotNil(t, got.CreatedBy)
			assert.Equal(t, actorID, *got.CreatedBy)
			assert.Equal(t, searchtext.Normalize(evidence), got.EvidenceNormalized)
			assert.Equal(t, searchtext.ProfileVersion, got.NormalizationVersion)
			assert.Equal(t, entity.CrossReferenceOriginHuman, got.Origin)
			assert.Equal(t, got.ID, got.OriginKey)
			assert.Equal(t, entity.UnitCorpusKitab, got.SourceCorpus)
			require.NotNil(t, got.SourceWorkID)
			assert.Equal(t, 797, *got.SourceWorkID)
			assert.Equal(t, entity.UnitCorpusQuran, got.TargetCorpus)
			assert.Equal(t, 73, *got.TargetQuranSurahID)
			assert.Equal(t, 4, *got.TargetQuranFromAyah)
			assert.Equal(t, 10, *got.TargetQuranToAyah)
			assert.JSONEq(t, `{}`, string(got.Metadata))
		})
	}
}

func TestCreateHumanProjectsQuranCitableUnitToAyahCompatibilityColumns(t *testing.T) {
	t.Parallel()

	fake := &fakeCrossReferenceRepo{}
	got, err := New(fake).CreateHuman(t.Context(), entity.CrossReferenceCreateInput{
		SourceAnchor: "kitab/797/h/11/u/42",
		TargetAnchor: "quran/73:4/u/7",
		Kind:         entity.CrossReferenceKindCites,
		Confidence:   1,
		EvidenceText: "سورة المزمل",
	}, uuid.NewString())
	require.NoError(t, err)
	assert.Equal(t, entity.UnitCorpusQuran, got.TargetCorpus)
	require.NotNil(t, got.TargetQuranSurahID)
	require.NotNil(t, got.TargetQuranFromAyah)
	require.NotNil(t, got.TargetQuranToAyah)
	assert.Equal(t, []int{73, 4, 4}, []int{
		*got.TargetQuranSurahID, *got.TargetQuranFromAyah, *got.TargetQuranToAyah,
	})
}

func TestCreateHumanRejectsUntrustedOrMalformedInput(t *testing.T) {
	t.Parallel()

	valid := entity.CrossReferenceCreateInput{
		SourceAnchor: "kitab/797",
		TargetAnchor: "quran/73",
		Kind:         entity.CrossReferenceKindCites,
		Confidence:   1,
		EvidenceText: "سورة المزمل",
	}

	tests := []struct {
		name  string
		actor string
		edit  func(*entity.CrossReferenceCreateInput)
	}{
		{name: "actor", actor: "not-uuid", edit: func(*entity.CrossReferenceCreateInput) {}},
		{name: "anchor", actor: uuid.NewString(), edit: func(v *entity.CrossReferenceCreateInput) { v.TargetAnchor = "quran/no" }},
		{name: "same anchor", actor: uuid.NewString(), edit: func(v *entity.CrossReferenceCreateInput) { v.TargetAnchor = v.SourceAnchor }},
		{name: "kind", actor: uuid.NewString(), edit: func(v *entity.CrossReferenceCreateInput) { v.Kind = "mentions" }},
		{name: "confidence", actor: uuid.NewString(), edit: func(v *entity.CrossReferenceCreateInput) { v.Confidence = 1.1 }},
		{name: "evidence", actor: uuid.NewString(), edit: func(v *entity.CrossReferenceCreateInput) { v.EvidenceText = "" }},
		{name: "metadata", actor: uuid.NewString(), edit: func(v *entity.CrossReferenceCreateInput) { v.Metadata = entity.RawJSON(`[]`) }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			input := valid
			test.edit(&input)
			_, err := New(&fakeCrossReferenceRepo{}).CreateHuman(context.Background(), input, test.actor)
			require.ErrorIs(t, err, entity.ErrInvalidCrossReference)
			assert.True(t, IsInvalid(err))
		})
	}
}

func TestUpsertDerivedEnforcesResolverAndMachineAttribution(t *testing.T) {
	t.Parallel()

	confidence := 0.9
	base := entity.CrossReference{
		ID:                   uuid.NewString(),
		SourceAnchor:         "kitab/797",
		TargetAnchor:         "quran/73:4",
		Kind:                 entity.CrossReferenceKindCites,
		Confidence:           &confidence,
		ReviewStatus:         entity.CrossReferenceStatusNeedsReview,
		EvidenceText:         "سورة المزمل",
		EvidenceNormalized:   searchtext.Normalize("سورة المزمل"),
		NormalizationVersion: searchtext.ProfileVersion,
		Origin:               entity.CrossReferenceOriginResolver,
		OriginKey:            "mention-1",
		Metadata:             entity.RawJSON(`{"source":"test"}`),
	}

	t.Run("resolver", func(t *testing.T) {
		t.Parallel()

		fake := &fakeCrossReferenceRepo{}
		ref := base
		ref.Method = entity.CrossReferenceMethodResolver
		ref.MethodDetail.Strategy = "explicit_surah"

		got, err := New(fake).UpsertDerived(context.Background(), ref)
		require.NoError(t, err)
		assert.Equal(t, "explicit_surah", got.MethodDetail.Strategy)
	})

	t.Run("machine", func(t *testing.T) {
		t.Parallel()

		fake := &fakeCrossReferenceRepo{}
		ref := base
		ref.ID = uuid.NewString()
		ref.Method = entity.CrossReferenceMethodMachine
		ref.MethodDetail = entity.CrossReferenceMethodDetail{
			ModelID: "model-v1", PromptVersion: "prompt-v2", RunID: uuid.NewString(),
		}
		ref.Origin = entity.CrossReferenceOriginMachine

		got, err := New(fake).UpsertDerived(context.Background(), ref)
		require.NoError(t, err)
		assert.Equal(t, "model-v1", got.MethodDetail.ModelID)
	})

	t.Run("missing conditional detail", func(t *testing.T) {
		t.Parallel()

		for _, method := range []string{entity.CrossReferenceMethodResolver, entity.CrossReferenceMethodMachine} {
			ref := base
			ref.ID = uuid.NewString()
			ref.Method = method
			ref.MethodDetail = entity.CrossReferenceMethodDetail{}
			_, err := New(&fakeCrossReferenceRepo{}).UpsertDerived(context.Background(), ref)
			require.ErrorIs(t, err, entity.ErrInvalidCrossReference)
		}
	})

	t.Run("human bypass", func(t *testing.T) {
		t.Parallel()

		ref := base
		ref.Method = entity.CrossReferenceMethodHuman
		_, err := New(&fakeCrossReferenceRepo{}).UpsertDerived(context.Background(), ref)
		require.ErrorIs(t, err, entity.ErrInvalidCrossReference)
	})

	t.Run("normalization drift", func(t *testing.T) {
		t.Parallel()

		ref := base
		ref.Method = entity.CrossReferenceMethodResolver
		ref.MethodDetail.Strategy = "explicit"
		ref.EvidenceNormalized = "drift"
		_, err := New(&fakeCrossReferenceRepo{}).UpsertDerived(context.Background(), ref)
		require.ErrorIs(t, err, entity.ErrInvalidCrossReference)
	})
}

func TestBridgeLegacyValidatesTypedMappingAndAllowsLegacyNullConfidence(t *testing.T) {
	t.Parallel()

	id := uuid.NewString()
	headingID := 11
	surahID, from, to := 73, 4, 10
	fromKey, toKey := "73:4", "73:10"
	evidence := "سورة المزمل"
	ref := entity.CrossReference{
		ID:                   id,
		SourceAnchor:         "kitab/797/h/11",
		TargetAnchor:         "quran/73:4..quran/73:10",
		Kind:                 entity.CrossReferenceKindCites,
		Method:               entity.CrossReferenceMethodResolver,
		MethodDetail:         entity.CrossReferenceMethodDetail{Strategy: "explicit_surah_ayah"},
		Confidence:           nil,
		ReviewStatus:         entity.CrossReferenceStatusApproved,
		EvidenceText:         evidence,
		EvidenceNormalized:   searchtext.Normalize(evidence),
		NormalizationVersion: searchtext.ProfileVersion,
		Origin:               entity.CrossReferenceOriginLegacyQuran,
		OriginKey:            id,
	}
	bridge := entity.QuranCrossReferenceBridge{
		ID: id, BookID: 797, PageID: 12, HeadingID: &headingID,
		SourceText: evidence, NormalizedText: searchtext.Normalize(evidence),
		ReferenceKind: "surah_ayah", SurahID: &surahID, FromAyahNumber: &from,
		ToAyahNumber: &to, FromAyahKey: &fromKey, ToAyahKey: &toKey,
		MatchStrategy: "explicit_surah_ayah",
	}

	fake := &fakeCrossReferenceRepo{}
	got, err := New(fake).BridgeLegacy(context.Background(), ref, bridge)
	require.NoError(t, err)
	assert.Equal(t, id, got.ID)
	require.Len(t, fake.bridges, 1)
	require.NotNil(t, fake.bridges[0])

	ambiguousID := uuid.NewString()
	ambiguousRef := ref
	ambiguousRef.ID = ambiguousID
	ambiguousRef.OriginKey = ambiguousID
	ambiguousRef.TargetAnchor = "quran/73"
	ambiguousRef.ReviewStatus = entity.CrossReferenceStatusAmbiguous
	ambiguousBridge := bridge
	ambiguousBridge.ID = ambiguousID
	ambiguousBridge.ReferenceKind = "ambiguous"
	ambiguousBridge.FromAyahNumber = nil
	ambiguousBridge.ToAyahNumber = nil
	ambiguousBridge.FromAyahKey = nil
	ambiguousBridge.ToAyahKey = nil
	_, err = New(&fakeCrossReferenceRepo{}).BridgeLegacy(context.Background(), ambiguousRef, ambiguousBridge)
	require.NoError(t, err)

	bad := bridge
	bad.ReferenceKind = "quote"
	_, err = New(&fakeCrossReferenceRepo{}).BridgeLegacy(context.Background(), ref, bad)
	require.ErrorIs(t, err, entity.ErrInvalidCrossReference)

	bad = bridge
	bad.ID = uuid.NewString()
	_, err = New(&fakeCrossReferenceRepo{}).BridgeLegacy(context.Background(), ref, bad)
	require.ErrorIs(t, err, entity.ErrInvalidCrossReference)
}

func TestReviewAcceptsFiveStatesAndPropagatesOptimisticConflict(t *testing.T) {
	t.Parallel()

	id, reviewer := uuid.NewString(), uuid.NewString()
	fake := &fakeCrossReferenceRepo{}
	uc := New(fake)

	for _, status := range []string{
		entity.CrossReferenceStatusPending,
		entity.CrossReferenceStatusApproved,
		entity.CrossReferenceStatusRejected,
		entity.CrossReferenceStatusAmbiguous,
		entity.CrossReferenceStatusNeedsReview,
	} {
		at := time.Now()
		got, err := uc.Review(context.Background(), id, status, reviewer, &at)
		require.NoError(t, err)
		assert.Equal(t, status, got.ReviewStatus)
	}

	assert.Len(t, fake.reviewed, 5)

	fake.err = entity.ErrPreconditionFailed
	_, err := uc.Review(context.Background(), id, entity.CrossReferenceStatusApproved, reviewer, nil)
	require.ErrorIs(t, err, entity.ErrPreconditionFailed)

	_, err = uc.Review(context.Background(), id, "all", reviewer, nil)
	require.ErrorIs(t, err, entity.ErrInvalidCrossReference)
	_, err = uc.Review(context.Background(), "bad", entity.CrossReferenceStatusApproved, reviewer, nil)
	require.ErrorIs(t, err, entity.ErrCrossReferenceNotFound)
}

func TestListPoliciesClampAndLockPublicVisibility(t *testing.T) {
	t.Parallel()

	fake := &fakeCrossReferenceRepo{listResult: entity.CrossReferenceList{Items: []entity.CrossReference{}}}
	uc := New(fake)

	_, err := uc.ListPublic(context.Background(), "quran/73:4", entity.CrossReferenceDirectionIncoming, "", 999, 99999)
	require.NoError(t, err)
	assert.True(t, fake.listFilter.PublicOnly)
	assert.Equal(t, entity.CrossReferenceStatusApproved, fake.listFilter.ReviewStatus)
	assert.Equal(t, uint64(200), fake.listFilter.Limit)
	assert.Equal(t, uint64(10000), fake.listFilter.Offset)
	assert.Empty(t, fake.listFilter.Method)

	_, err = uc.ListPublic(context.Background(), "quran/73", "both", "", 1, 0)
	require.ErrorIs(t, err, entity.ErrInvalidCrossReference)

	_, err = uc.ListEditorial(context.Background(), repo.CrossReferenceFilter{
		Anchor:       "kitab/797",
		Direction:    entity.CrossReferenceDirectionOutgoing,
		Kind:         entity.CrossReferenceKindQuotes,
		Method:       entity.CrossReferenceMethodHuman,
		ReviewStatus: entity.CrossReferenceStatusPending,
	})
	require.NoError(t, err)
	assert.False(t, fake.listFilter.PublicOnly)
	assert.Equal(t, uint64(50), fake.listFilter.Limit)

	_, err = uc.ListEditorial(context.Background(), repo.CrossReferenceFilter{Method: "oracle"})
	require.ErrorIs(t, err, entity.ErrInvalidCrossReference)
}

func TestGetFreezeAndRepositoryErrors(t *testing.T) {
	t.Parallel()

	id := uuid.NewString()
	want := entity.CrossReference{ID: id}
	fake := &fakeCrossReferenceRepo{getResult: want}
	uc := New(fake)

	got, err := uc.Get(context.Background(), id)
	require.NoError(t, err)
	assert.Equal(t, want, got)

	_, err = uc.Get(context.Background(), "bad")
	require.ErrorIs(t, err, entity.ErrCrossReferenceNotFound)

	require.NoError(t, uc.FreezeLegacyQuranWrites(context.Background()))
	assert.Equal(t, 1, fake.freezeCalls)
	require.NoError(t, uc.UnfreezeLegacyQuranWrites(context.Background()))

	fake.err = entity.ErrForbidden

	require.Error(t, uc.FreezeLegacyQuranWrites(context.Background()))
}
