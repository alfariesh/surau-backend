package unitregistry

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPlanQuranSurahDeterministicWithLinkedFootnotes(t *testing.T) {
	t.Parallel()

	loadedAt := time.Date(2026, time.July, 12, 1, 2, 3, 0, time.UTC)
	page := 574
	source := entity.QuranUnitSource{
		SurahID:  73,
		LoadedAt: loadedAt,
		Ayahs: []entity.QuranUnitSourceAyah{{
			AyahNumber:       1,
			PageNumber:       &page,
			PrimaryText:      "يَا أَيُّهَا الْمُزَّمِّلُ",
			PrimaryUpdatedAt: loadedAt,
			Translations: []entity.QuranUnitSourceTranslation{{
				SourceID:  "kemenag-id-translation",
				Language:  "id",
				Text:      "Wahai orang yang berselimut!",
				Footnotes: json.RawMessage(`[{"number":2,"marker":"2)","text":"Catatan dua"},{"n":1,"t":"Catatan satu"}]`),
				UpdatedAt: loadedAt,
			}},
			Transliterations: []entity.QuranUnitSourceTransliteration{{
				SourceID: "kemenag-id-latin", Language: "id",
				Text: "Yā ayyuhal-muzzammil(u).", UpdatedAt: loadedAt,
			}},
		}},
	}

	derived, err := deriveQuranSurah(&source)
	require.NoError(t, err)
	require.Len(t, derived, 5)
	assert.Equal(t, []string{
		entity.QuranUnitRolePrimaryText,
		entity.QuranUnitRoleTranslation,
		entity.QuranUnitRoleFootnote,
		entity.QuranUnitRoleFootnote,
		entity.QuranUnitRoleTransliteration,
	}, []string{derived[0].role, derived[1].role, derived[2].role, derived[3].role, derived[4].role})
	assert.Equal(t, "1", derived[2].footnoteKey, "footnotes sort by stable key, not raw order")

	empty := entity.QuranUnitRegistrySnapshot{
		MaxOrdinalByAyah: map[int]int{}, ExistingIDs: map[string]struct{}{},
	}
	first, err := planQuranSurah(&source, derived, &empty)
	require.NoError(t, err)
	require.Len(t, first.Mints, 5)
	assert.Equal(t, 1, first.Mints[0].Unit.Ordinal)
	assert.Equal(t, "quran/73:1/u/1", first.Mints[0].Unit.Anchor)
	assert.False(t, first.Mints[0].Unit.BookID != nil)
	assert.Nil(t, first.Mints[1].Unit.ParentUnitID)
	require.NotNil(t, first.Mints[2].Unit.ParentUnitID)
	assert.Equal(t, first.Mints[1].Unit.ID, *first.Mints[2].Unit.ParentUnitID)
	assert.Equal(t, first.Mints[1].Unit.ID, *first.Mints[3].Unit.ParentUnitID)

	snapshot := snapshotFromQuranMints(first.Mints)
	second, err := planQuranSurah(&source, derived, &snapshot)
	require.NoError(t, err)
	assert.Empty(t, second.Mints)
	assert.Empty(t, second.Updates)
	assert.Empty(t, second.Retires)
	assert.Empty(t, second.Edges)
	assert.Equal(t, 5, second.Report.Matched)
}

func TestPlanQuranSurahPrimaryTextDriftFailsClosed(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	source := entity.QuranUnitSource{SurahID: 1, LoadedAt: now, Ayahs: []entity.QuranUnitSourceAyah{{
		AyahNumber: 1, PrimaryText: "نص جديد", PrimaryUpdatedAt: now,
	}}}
	derived, err := deriveQuranSurah(&source)
	require.NoError(t, err)

	oldHash := ContentHash(entity.UnitKindPrimaryText, "", "نص قديم")
	oldID := QuranUnitID(1, 1, entity.QuranUnitRolePrimaryText, "qpc-hafs", "", oldHash, 1)
	snapshot := entity.QuranUnitRegistrySnapshot{
		Active: []entity.QuranCitableUnitRecord{{
			Unit: entity.CitableUnit{ID: oldID, ContentHash: oldHash, Occurrence: 1},
			Binding: entity.QuranCitableUnitBinding{
				UnitID: oldID, SurahID: 1, AyahNumber: 1, Ordinal: 1,
				Role: entity.QuranUnitRolePrimaryText, SourceUpdatedAt: now.Add(-time.Hour),
			},
		}},
		MaxOrdinalByAyah: map[int]int{1: 1}, ExistingIDs: map[string]struct{}{oldID: {}},
	}

	_, err = planQuranSurah(&source, derived, &snapshot)
	assert.ErrorIs(t, err, entity.ErrQuranPrimaryTextDrift)

	missingPrimary := source
	missingPrimary.Ayahs[0].PrimaryText = ""
	derived, err = deriveQuranSurah(&missingPrimary)
	require.NoError(t, err)
	require.Empty(t, derived)
	_, err = planQuranSurah(&missingPrimary, derived, &snapshot)
	assert.ErrorIs(t, err, entity.ErrQuranPrimaryTextDrift)
}

func TestDeriveQuranSurahSkipsLegacyAyahWithoutPrimaryText(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	source := entity.QuranUnitSource{SurahID: 2, LoadedAt: now, Ayahs: []entity.QuranUnitSourceAyah{
		{
			AyahNumber: 255,
			Translations: []entity.QuranUnitSourceTranslation{{
				SourceID: "legacy-source", Language: "id", Text: "Dependent legacy text", UpdatedAt: now,
			}},
		},
		{AyahNumber: 256, PrimaryText: "لَا إِكْرَاهَ فِي الدِّينِ", PrimaryUpdatedAt: now},
	}}

	derived, err := deriveQuranSurah(&source)
	require.NoError(t, err)
	require.Len(t, derived, 1)
	assert.Equal(t, 256, derived[0].ayahNumber)
	assert.Equal(t, entity.QuranUnitRolePrimaryText, derived[0].role)
}

func TestDeriveQuranSurahRejectsMalformedNonNullFootnotes(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	source := entity.QuranUnitSource{SurahID: 1, LoadedAt: now, Ayahs: []entity.QuranUnitSourceAyah{{
		AyahNumber: 1, PrimaryText: "نص", PrimaryUpdatedAt: now,
		Translations: []entity.QuranUnitSourceTranslation{{
			SourceID: "source", Language: "id", Text: "Terjemahan",
			Footnotes: json.RawMessage(`{"n":1,"t":"not-an-array"}`), UpdatedAt: now,
		}},
	}}}

	_, err := deriveQuranSurah(&source)
	assert.ErrorIs(t, err, entity.ErrInvalidQuranFootnotes)
}

func TestParseQuranFootnotesSupportsQULObjectMap(t *testing.T) {
	t.Parallel()

	footnotes, err := parseQuranFootnotes(
		json.RawMessage(`{"77647":"Catatan kedua","77646":"Catatan pertama"}`),
		`Teks <sup foot_note="77646">1</sup> dan <sup class="note" foot_note='77647'>2&amp;</sup>`,
	)
	require.NoError(t, err)
	assert.Equal(t, []derivedQuranFootnote{
		{key: "77646", marker: "1", text: "Catatan pertama"},
		{key: "77647", marker: "2&", text: "Catatan kedua"},
	}, footnotes)

	empty, err := parseQuranFootnotes(json.RawMessage(`{}`), "Terjemahan tanpa catatan")
	require.NoError(t, err)
	assert.Empty(t, empty)
}

func TestQuranFootnoteMarkersUseTaggedSourceMetadata(t *testing.T) {
	t.Parallel()

	translation := entity.QuranUnitSourceTranslation{
		Text: "Teks tampilan tanpa tag",
		Metadata: json.RawMessage(
			`{"verse_key":"88:17","t":"Teks <sup foot_note=\"77646\">1</sup>"}`,
		),
	}
	footnotes, err := parseQuranFootnotes(
		json.RawMessage(`{"77646":"Catatan sumber"}`),
		quranFootnoteMarkerText(&translation),
	)
	require.NoError(t, err)
	require.Len(t, footnotes, 1)
	assert.Equal(t, "1", footnotes[0].marker)
}

func TestParseQuranFootnotesRejectsMalformedQULObjectMap(t *testing.T) {
	t.Parallel()

	tests := []json.RawMessage{
		json.RawMessage(`{"note":"Teks"}`),
		json.RawMessage(`{"1":7}`),
		json.RawMessage(`{"1":""}`),
		json.RawMessage(`{"01":"Satu","1":"Duplikat kanonik"}`),
	}
	for _, raw := range tests {
		_, err := parseQuranFootnotes(raw, "")
		assert.ErrorIs(t, err, entity.ErrInvalidQuranFootnotes, string(raw))
	}
}

func snapshotFromQuranMints(mints []entity.QuranUnitMint) entity.QuranUnitRegistrySnapshot {
	snapshot := entity.QuranUnitRegistrySnapshot{
		MaxOrdinalByAyah: map[int]int{}, ExistingIDs: map[string]struct{}{},
	}

	for i := range mints {
		mint := mints[i]
		snapshot.Active = append(snapshot.Active, entity.QuranCitableUnitRecord(mint))

		snapshot.ExistingIDs[mint.Unit.ID] = struct{}{}
		if mint.Binding.Ordinal > snapshot.MaxOrdinalByAyah[mint.Binding.AyahNumber] {
			snapshot.MaxOrdinalByAyah[mint.Binding.AyahNumber] = mint.Binding.Ordinal
		}
	}

	return snapshot
}
