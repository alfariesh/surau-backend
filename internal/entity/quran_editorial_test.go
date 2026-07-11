package entity

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestQuranEditorialWorkspaceCurrentUpdatedAt(t *testing.T) {
	t.Parallel()

	publishedAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	draftAt := publishedAt.Add(time.Hour)
	tests := []struct {
		name string
		got  time.Time
		want time.Time
	}{
		{
			name: "surah draft takes precedence",
			got: QuranSurahEditorialWorkspace{
				Draft:     &QuranSurahEditorialEdit{UpdatedAt: draftAt},
				Published: &QuranSurahEditorialEdit{UpdatedAt: publishedAt},
			}.CurrentUpdatedAt(),
			want: draftAt,
		},
		{
			name: "surah published fallback",
			got: QuranSurahEditorialWorkspace{
				Published: &QuranSurahEditorialEdit{UpdatedAt: publishedAt},
			}.CurrentUpdatedAt(),
			want: publishedAt,
		},
		{
			name: "surah empty workspace",
			got:  QuranSurahEditorialWorkspace{}.CurrentUpdatedAt(),
			want: time.Time{},
		},
		{
			name: "ayah draft takes precedence",
			got: QuranAyahEditorialWorkspace{
				Draft:     &QuranAyahEditorialEdit{UpdatedAt: draftAt},
				Published: &QuranAyahEditorialEdit{UpdatedAt: publishedAt},
			}.CurrentUpdatedAt(),
			want: draftAt,
		},
		{
			name: "ayah published fallback",
			got: QuranAyahEditorialWorkspace{
				Published: &QuranAyahEditorialEdit{UpdatedAt: publishedAt},
			}.CurrentUpdatedAt(),
			want: publishedAt,
		},
		{
			name: "ayah empty workspace",
			got:  QuranAyahEditorialWorkspace{}.CurrentUpdatedAt(),
			want: time.Time{},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, test.want, test.got)
		})
	}
}
