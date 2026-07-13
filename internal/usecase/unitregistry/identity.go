// Package unitregistry is the single write service for the shared Citable Unit
// registry (roadmap/phase-1b-content-backbone.md C2): it derives units from
// corpus content, reconciles them against the registry with deterministic
// identity, and maintains lifecycle/lineage. No other code path writes to
// citable_units / citable_unit_lineage (enforced by a DB trigger guard).
package unitregistry

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

// nsCitableUnit is the fixed UUIDv5 namespace for unit identity. Changing it
// changes every unit id in existence — never touch it.
//
//nolint:gochecknoglobals // deterministic constant namespace
var nsCitableUnit = uuid.NewSHA1(uuid.NameSpaceURL, []byte("https://surau.org/citable-unit"))

// frontMatterHeadingID is the sentinel used in anchors and natural keys for
// units that precede the first heading anchor (heading_id NULL in the DB).
// The deriver rejects source books that use heading id 0 for a real heading.
const frontMatterHeadingID = 0

// scopeKeyOf collapses the nullable heading scope to its natural-key form.
func scopeKeyOf(headingID *int) int {
	if headingID == nil {
		return frontMatterHeadingID
	}

	return *headingID
}

// ContentHash is the unit identity hash: sha256(kind || 0x00 || marker || 0x00 || text).
// HTML/formatting changes deliberately do not participate — a color span is not
// a new unit. The audit recomputes this from persisted columns (foreign-write
// tripwire), so hash input must be reconstructible from the row alone.
func ContentHash(kind, marker, text string) []byte {
	h := sha256.New()
	h.Write([]byte(kind))
	h.Write([]byte{0})
	h.Write([]byte(marker))
	h.Write([]byte{0})
	h.Write([]byte(text))

	return h.Sum(nil)
}

// UnitID mints the deterministic UUIDv5 for a natural key. Because the id is
// the table PK, natural-key uniqueness across ALL lifecycles is enforced for
// free; re-deriving unchanged content always reproduces the same id (AC-1).
func UnitID(corpus string, bookID, scopeKey int, kind string, contentHash []byte, occurrence int) string {
	name := fmt.Sprintf("%s|%d|%d|%s|%x|%d", corpus, bookID, scopeKey, kind, contentHash, occurrence)

	return uuid.NewSHA1(nsCitableUnit, []byte(name)).String()
}

// UnitIDV2 extends the natural key with the published content slot. The v1
// function above remains the identity source for book_page/ar forever, so K-1
// does not remint pilot units when translations and summaries join the same
// registry.
func UnitIDV2(
	corpus string,
	bookID, scopeKey int,
	contentRole, language, kind string,
	contentHash []byte,
	occurrence int,
) string {
	name := fmt.Sprintf("v2|%s|%d|%d|%s|%s|%s|%x|%d",
		corpus, bookID, scopeKey, contentRole, language, kind, contentHash, occurrence)

	return uuid.NewSHA1(nsCitableUnit, []byte(name)).String()
}

// AnchorFor renders the provisional canonical anchor
// kitab/{book_id}/h/{heading_id|0}/u/{ordinal}. B-2 ratifies the cross-corpus
// grammar; until then the anchor is an opaque unique string, unexposed publicly.
func AnchorFor(bookID, scopeKey, ordinal int) string {
	return fmt.Sprintf("kitab/%d/h/%d/u/%d", bookID, scopeKey, ordinal)
}

// QuranUnitID uses the same fixed B-1 namespace while making the Quran source
// slot explicit. Content edits mint successors; unchanged re-runs reproduce the
// same UUID exactly.
func QuranUnitID(
	surahID, ayahNumber int,
	role, sourceID, footnoteKey string,
	contentHash []byte,
	occurrence int,
) string {
	name := fmt.Sprintf("quran|%d|%d|%s|%s|%s|%x|%d",
		surahID, ayahNumber, role, sourceID, footnoteKey, contentHash, occurrence)

	return uuid.NewSHA1(nsCitableUnit, []byte(name)).String()
}

// QuranAnchorFor is the child Citable Unit anchor under the grandfathered
// logical ayah Anchor quran/{surah}:{ayah}.
func QuranAnchorFor(surahID, ayahNumber, ordinal int) string {
	return fmt.Sprintf("quran/%d:%d/u/%d", surahID, ayahNumber, ordinal)
}

// Latin, Arabic-Indic (U+0660–0669), and Extended Arabic-Indic (U+06F0–06F9)
// digit runs; the char-range warning is expected for the Arabic blocks.
//
//nolint:gocritic // badRegexp: Arabic-Indic digit ranges are intentional
var _digitsRE = regexp.MustCompile(`[0-9٠-٩۰-۹]+`)

// markerValue extracts the numeric value of a footnote marker such as (¬٢),
// (2), or (۲); Arabic-Indic and Extended Arabic-Indic digits map to their
// values. Returns -1 when no digits are present.
func markerValue(marker string) int {
	digits := _digitsRE.FindString(marker)
	if digits == "" {
		return -1
	}

	const decimalRadix = 10

	value := 0

	for _, r := range digits {
		var d int

		switch {
		case r >= '0' && r <= '9':
			d = int(r - '0')
		case r >= '٠' && r <= '٩':
			d = int(r - '٠')
		case r >= '۰' && r <= '۹':
			d = int(r - '۰')
		default:
			return -1
		}

		value = value*decimalRadix + d
	}

	return value
}

// textReferencesMarker reports whether body text carries an inline footnote
// reference with the same numeric value as marker, e.g. body "…التنزيل. (¬١)"
// references footnote marker "(¬١)" or "(١)".
func textReferencesMarker(text, marker string) bool {
	want := markerValue(marker)
	if want < 0 {
		return false
	}

	for _, ref := range _footnoteRefsIn(text) {
		if markerValue(ref) == want {
			return true
		}
	}

	return false
}

//nolint:gocritic // badRegexp: Arabic-Indic digit ranges are intentional
var _footnoteRefInTextRE = regexp.MustCompile(`\((?:¬)?[0-9٠-٩۰-۹]+\)`)

func _footnoteRefsIn(text string) []string {
	return _footnoteRefInTextRE.FindAllString(text, -1)
}

// kindClass buckets kinds for lineage alignment. A Quran quote extracted from
// a marker-bearing footnote remains in the footnote lineage class, while an
// ordinary paragraph reclassified as a quran_quote remains body content.
func kindClass(kind string, markerPresent bool) string {
	if markerPresent && (kind == "footnote" || kind == "quran_quote") {
		return "footnote"
	}

	switch kind {
	case "footnote":
		return "footnote"
	case "html":
		return "html"
	default:
		return "body"
	}
}

// hashKey is the map key form of a content hash.
func hashKey(hash []byte) string {
	return fmt.Sprintf("%x", hash)
}

// matchKey groups units for rank matching within a scope.
func matchKey(contentRole, language, kind string, hash []byte, provenanceClass string, generationRunID *string) string {
	generation := ""
	if generationRunID != nil {
		generation = *generationRunID
	}

	return contentRole + "\x00" + language + "\x00" + kind + "\x00" + hashKey(hash) +
		"\x00" + provenanceClass + "\x00" + generation
}

// rescueKey groups retired/minted units for the book-level rescue pass.
func rescueKey(contentRole, language, kind string, markerPresent bool, hash []byte) string {
	return contentRole + "\x00" + language + "\x00" + kindClass(kind, markerPresent) + "\x00" + hashKey(hash)
}

func normalizedContentRole(role string) string {
	if role == "" {
		return "book_page"
	}

	return role
}

func normalizedLanguage(language string) string {
	if language == "" {
		return "ar"
	}

	return language
}

// trimNonEmpty reports whether text has visible content.
func trimNonEmpty(s string) bool {
	return strings.TrimSpace(s) != ""
}
