package importer

import (
	"testing"

	"github.com/alfariesh/surau-backend/internal/repo/persistent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMergeSurahMetadataUpdate(t *testing.T) {
	t.Parallel()

	t.Run("no metadata leaves batch empty", func(t *testing.T) {
		t.Parallel()

		updates := make(map[int]persistent.QuranSurahMetadataUpdate)
		rec := quranSurahEditorialRecord{SurahID: 2}

		err := mergeSurahMetadataUpdate(updates, &rec)
		require.NoError(t, err)
		assert.Empty(t, updates)
	})

	t.Run("partial records merge consistently", func(t *testing.T) {
		t.Parallel()

		updates := make(map[int]persistent.QuranSurahMetadataUpdate)
		slug := "al-baqarah"
		chronologicalOrder := 87
		rukuCount := 40

		require.NoError(t, mergeSurahMetadataUpdate(updates, &quranSurahEditorialRecord{
			SurahID: 2,
			Slug:    &slug,
		}))
		require.NoError(t, mergeSurahMetadataUpdate(updates, &quranSurahEditorialRecord{
			SurahID:            2,
			ChronologicalOrder: &chronologicalOrder,
			RukuCount:          &rukuCount,
		}))
		require.NoError(t, mergeSurahMetadataUpdate(updates, &quranSurahEditorialRecord{
			SurahID: 2,
			Slug:    &slug,
		}))

		update := updates[2]
		require.NotNil(t, update.Slug)
		require.NotNil(t, update.ChronologicalOrder)
		require.NotNil(t, update.RukuCount)
		assert.Equal(t, slug, *update.Slug)
		assert.Equal(t, chronologicalOrder, *update.ChronologicalOrder)
		assert.Equal(t, rukuCount, *update.RukuCount)
	})

	tests := []struct {
		name   string
		first  quranSurahEditorialRecord
		second quranSurahEditorialRecord
		field  string
	}{
		{
			name: "conflicting slug",
			first: quranSurahEditorialRecord{
				SurahID: 2,
				Slug:    new("al-baqarah"),
			},
			second: quranSurahEditorialRecord{
				SurahID: 2,
				Slug:    new("baqarah"),
			},
			field: "slug",
		},
		{
			name: "conflicting chronological order",
			first: quranSurahEditorialRecord{
				SurahID:            2,
				ChronologicalOrder: new(87),
			},
			second: quranSurahEditorialRecord{
				SurahID:            2,
				ChronologicalOrder: new(88),
			},
			field: "chronological_order",
		},
		{
			name: "conflicting ruku count",
			first: quranSurahEditorialRecord{
				SurahID:   2,
				RukuCount: new(40),
			},
			second: quranSurahEditorialRecord{
				SurahID:   2,
				RukuCount: new(41),
			},
			field: "ruku_count",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			updates := make(map[int]persistent.QuranSurahMetadataUpdate)
			require.NoError(t, mergeSurahMetadataUpdate(updates, &test.first))

			err := mergeSurahMetadataUpdate(updates, &test.second)
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.field)
		})
	}
}

func TestQuranAyahEditorialFAQs(t *testing.T) {
	t.Parallel()

	result := quranAyahEditorialFAQs([]quranAyahEditorialFAQ{
		{Question: "Apa maknanya?", AnswerHTML: "<p>Jawaban.</p>"},
	})

	require.Len(t, result, 1)
	assert.Equal(t, "Apa maknanya?", result[0].Question)
	assert.Equal(t, "<p>Jawaban.</p>", result[0].AnswerHTML)
	assert.Empty(t, quranAyahEditorialFAQs(nil))
}
