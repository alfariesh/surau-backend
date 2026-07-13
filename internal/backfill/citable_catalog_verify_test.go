package backfill

import (
	"crypto/sha256"
	"testing"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/internal/usecase/unitregistry"
	"github.com/stretchr/testify/assert"
)

func TestCanonicalCoverageDetectsTailDroppedByDeriverAndRegistry(t *testing.T) {
	t.Parallel()

	const sourceText = "kept LOST"

	source := entity.BookUnitSource{
		BookID: 1,
		Pages: []entity.BookUnitSourcePage{{
			PageID: 1, ContentText: sourceText,
		}},
	}
	required := canonicalRequiredCoverage(1, &source)
	documentHash := sha256.Sum256([]byte(sourceText))
	markCanonicalRunes(required, canonicalSpanKey(
		1, 0, 1, entity.UnitKindParagraph, "kept", "ar", entity.UnitContentRoleBookPage,
		documentHash[:], 0, 4,
	))

	covered, uncovered := countUncoveredCanonicalRunes(required)

	assert.Equal(t, int64(8), covered)
	assert.Equal(t, int64(4), uncovered, "the independently sourced LOST tail must fail coverage")
}

func TestCanonicalCoverageAcceptsCompleteArabicPage(t *testing.T) {
	t.Parallel()

	source := entity.BookUnitSource{
		BookID: 1,
		Pages: []entity.BookUnitSourcePage{{
			PageID:      1,
			ContentHTML: "<p>فقرة عربية أولى.</p><p>فقرة عربية ثانية.</p>",
			ContentText: "فقرة عربية أولى.\nفقرة عربية ثانية.",
		}},
		Headings: []entity.BookUnitSourceHeading{{HeadingID: 1, PageID: 1}},
	}
	derived, _, err := unitregistry.DeriveBook(&source)
	assert.NoError(t, err)

	required := canonicalRequiredCoverage(1, &source)

	for i := range derived {
		markCanonicalRunes(required, canonicalSpanForDerived(1, &derived[i]))
	}

	_, uncovered := countUncoveredCanonicalRunes(required)
	assert.Zero(t, uncovered)
}

func TestCanonicalCoverageBitmapHandlesWordBoundariesAndClipping(t *testing.T) {
	t.Parallel()

	const sourceText = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789abcdefghij"

	source := entity.BookUnitSource{
		BookID: 1,
		Pages: []entity.BookUnitSourcePage{{
			PageID: 1, ContentText: sourceText,
		}},
	}
	required := canonicalRequiredCoverage(1, &source)
	documentHash := sha256.Sum256([]byte(sourceText))
	markCanonicalRunes(required, canonicalSpanKey(
		1, 0, 1, entity.UnitKindParagraph, sourceText, "ar", entity.UnitContentRoleBookPage,
		documentHash[:], -10, len([]rune(sourceText))+10,
	))

	covered, uncovered := countUncoveredCanonicalRunes(required)
	assert.Equal(t, int64(len([]rune(sourceText))), covered)
	assert.Zero(t, uncovered)
}
