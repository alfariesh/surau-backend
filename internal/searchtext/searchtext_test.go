package searchtext_test

import (
	"testing"

	"github.com/alfariesh/surau-backend/internal/searchtext"
	"github.com/stretchr/testify/assert"
)

func TestNormalizeFoldsArabicVariants(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "hamza alif variants fold to bare alif", in: "أحكام إحكام آحكام ٱحكام", want: "احكام احكام احكام احكام"},
		{name: "alif maqsura folds to ya", in: "مصطفى", want: "مصطفي"},
		{name: "hamza on waw and ya", in: "مؤمن سائل", want: "مومن سايل"},
		{name: "harakat stripped", in: "مُحَمَّد", want: "محمد"},
		{name: "punctuation becomes space and collapses", in: "ابن-تيمية  (الحراني)", want: "ابن تيمية الحراني"},
		{name: "latin and digits kept", in: "Sahih 123", want: "Sahih 123"},
		{name: "empty stays empty", in: "", want: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, searchtext.Normalize(tc.in))
		})
	}
}

func TestProfileVersionIsStable(t *testing.T) {
	t.Parallel()

	// Bumping this is a DELIBERATE act: it must come with re-running every
	// backfill that persists normalized text (see package doc).
	assert.Equal(t, 1, searchtext.ProfileVersion)
}
