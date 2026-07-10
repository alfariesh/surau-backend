// Package anchor defines Surau's canonical, cross-corpus Anchor grammar.
//
// Active profiles are Quran ayahs and kitab Work, heading, and Citable Unit
// points. Hadith, wiki, and entity corpora are reserved for future profiles and
// are deliberately rejected until their locator grammar is ratified.
package anchor

import (
	"errors"
	"fmt"
)

// MaxLength is the maximum byte length of one canonical point or range.
const MaxLength = 512

// ErrInvalid identifies malformed, unsupported, or out-of-scope Anchors.
var ErrInvalid = errors.New("invalid anchor")

// Corpus is the first component of every canonical Anchor.
type Corpus string

const (
	CorpusQuran Corpus = "quran"
	CorpusKitab Corpus = "kitab"

	// These corpora are reserved, not active parser profiles.
	CorpusHadith Corpus = "hadith"
	CorpusWiki   Corpus = "wiki"
	CorpusEntity Corpus = "entity"
)

// PointKind identifies the active canonical point profile.
type PointKind string

const (
	PointKindQuranAyah    PointKind = "quran_ayah"
	PointKindKitabWork    PointKind = "kitab_work"
	PointKindKitabHeading PointKind = "kitab_heading"
	PointKindKitabUnit    PointKind = "kitab_unit"
)

// Point is one validated canonical Anchor endpoint. Its fields are private so
// callers cannot construct values that the formatter would serialize outside
// the grammar; use ParsePoint or one of the New* constructors.
type Point struct {
	corpus    Corpus
	kind      PointKind
	bookID    int
	headingID int
	ordinal   int
	surah     int
	ayah      int
}

// NewQuranAyah constructs quran/{surah}:{ayah}.
func NewQuranAyah(surah, ayah int) (Point, error) {
	if !positiveInteger(surah) || !positiveInteger(ayah) {
		return Point{}, fmt.Errorf("%w: Quran surah and ayah must be positive PostgreSQL integers", ErrInvalid)
	}

	return Point{corpus: CorpusQuran, kind: PointKindQuranAyah, surah: surah, ayah: ayah}, nil
}

// NewKitabWork constructs kitab/{book_id}.
func NewKitabWork(bookID int) (Point, error) {
	if !positiveInteger(bookID) {
		return Point{}, fmt.Errorf("%w: kitab book id must be a positive PostgreSQL integer", ErrInvalid)
	}

	return Point{corpus: CorpusKitab, kind: PointKindKitabWork, bookID: bookID}, nil
}

// NewKitabHeading constructs kitab/{book_id}/h/{heading_id}.
func NewKitabHeading(bookID, headingID int) (Point, error) {
	if !positiveInteger(bookID) || !positiveInteger(headingID) {
		return Point{}, fmt.Errorf("%w: kitab book and heading ids must be positive PostgreSQL integers", ErrInvalid)
	}

	return Point{
		corpus: CorpusKitab, kind: PointKindKitabHeading, bookID: bookID, headingID: headingID,
	}, nil
}

// NewKitabUnit constructs
// kitab/{book_id}/h/{heading_id|0}/u/{ordinal}. Heading zero is the dedicated
// front-matter scope and is valid only for a unit point.
func NewKitabUnit(bookID, headingID, ordinal int) (Point, error) {
	if !positiveInteger(bookID) || !nonNegativeInteger(headingID) || !positiveInteger(ordinal) {
		return Point{}, fmt.Errorf(
			"%w: kitab book id and ordinal must be positive PostgreSQL integers and heading id may additionally be zero",
			ErrInvalid,
		)
	}

	return Point{
		corpus: CorpusKitab, kind: PointKindKitabUnit, bookID: bookID, headingID: headingID, ordinal: ordinal,
	}, nil
}

// Corpus returns the point's corpus discriminator.
func (p Point) Corpus() Corpus {
	return p.corpus
}

// Kind returns the point's active grammar profile.
func (p Point) Kind() PointKind {
	return p.kind
}

// BookID returns the kitab Work id, or zero for a non-kitab point.
func (p Point) BookID() int {
	return p.bookID
}

// HeadingID returns the kitab heading scope, including zero for front matter.
func (p Point) HeadingID() int {
	return p.headingID
}

// Ordinal returns the Citable Unit ordinal, or zero for a non-unit point.
func (p Point) Ordinal() int {
	return p.ordinal
}

// Surah returns the Quran surah number, or zero for a non-Quran point.
func (p Point) Surah() int {
	return p.surah
}

// Ayah returns the Quran ayah number, or zero for a non-Quran point.
func (p Point) Ayah() int {
	return p.ayah
}

// String formats the validated point canonically. The zero value formats as
// an empty string and is rejected by FromPoint and NewRange.
func (p Point) String() string {
	switch p.kind {
	case PointKindQuranAyah:
		return fmt.Sprintf("quran/%d:%d", p.surah, p.ayah)
	case PointKindKitabWork:
		return fmt.Sprintf("kitab/%d", p.bookID)
	case PointKindKitabHeading:
		return fmt.Sprintf("kitab/%d/h/%d", p.bookID, p.headingID)
	case PointKindKitabUnit:
		return fmt.Sprintf("kitab/%d/h/%d/u/%d", p.bookID, p.headingID, p.ordinal)
	default:
		return ""
	}
}

// Value is either one point or a two-boundary range. Range endpoints are
// always in the same corpus and Work; Quran ranges are also in one surah.
type Value struct {
	start Point
	end   *Point
}

// FromPoint wraps one validated point as a non-range Anchor.
func FromPoint(point Point) (Value, error) {
	if !point.valid() {
		return Value{}, fmt.Errorf("%w: invalid point value", ErrInvalid)
	}

	return Value{start: point}, nil
}

// NewRange constructs a canonical range after checking its shared scope.
func NewRange(start, end Point) (Value, error) {
	if !start.valid() || !end.valid() {
		return Value{}, fmt.Errorf("%w: invalid range boundary", ErrInvalid)
	}

	if err := validateRangeScope(start, end); err != nil {
		return Value{}, err
	}

	value := Value{start: start, end: &end}
	if len(value.String()) > MaxLength {
		return Value{}, fmt.Errorf("%w: range exceeds %d bytes", ErrInvalid, MaxLength)
	}

	return value, nil
}

// Start returns the point or range's first boundary.
func (v *Value) Start() Point {
	return v.start
}

// End returns the range's second boundary and true, or a zero point and false
// for a non-range Anchor.
func (v *Value) End() (Point, bool) {
	if v.end == nil {
		return Point{}, false
	}

	return *v.end, true
}

// IsRange reports whether the Anchor has two boundaries.
func (v *Value) IsRange() bool {
	return v.end != nil
}

// String formats the validated Anchor canonically.
func (v Value) String() string {
	if v.end != nil {
		return v.start.String() + ".." + v.end.String()
	}

	return v.start.String()
}

func (p Point) valid() bool {
	switch p.kind {
	case PointKindQuranAyah:
		return p.validQuranAyah()
	case PointKindKitabWork:
		return p.validKitabWork()
	case PointKindKitabHeading:
		return p.validKitabHeading()
	case PointKindKitabUnit:
		return p.validKitabUnit()
	default:
		return false
	}
}

func (p Point) validQuranAyah() bool {
	return p.corpus == CorpusQuran && positiveInteger(p.surah) && positiveInteger(p.ayah)
}

func (p Point) validKitabWork() bool {
	return p.corpus == CorpusKitab && positiveInteger(p.bookID)
}

func (p Point) validKitabHeading() bool {
	return p.validKitabWork() && positiveInteger(p.headingID)
}

func (p Point) validKitabUnit() bool {
	return p.validKitabWork() && nonNegativeInteger(p.headingID) && positiveInteger(p.ordinal)
}

func validateRangeScope(start, end Point) error {
	if start.corpus != end.corpus {
		return fmt.Errorf("%w: range boundaries must use one corpus", ErrInvalid)
	}

	switch start.corpus {
	case CorpusQuran:
		if start.surah != end.surah {
			return fmt.Errorf("%w: Quran range boundaries must use one surah", ErrInvalid)
		}

		if end.ayah < start.ayah {
			return fmt.Errorf("%w: Quran range end must not precede its start", ErrInvalid)
		}
	case CorpusKitab:
		if start.bookID != end.bookID {
			return fmt.Errorf("%w: kitab range boundaries must use one Work", ErrInvalid)
		}
	case CorpusHadith, CorpusWiki, CorpusEntity:
		return fmt.Errorf("%w: corpus has no active range profile", ErrInvalid)
	default:
		return fmt.Errorf("%w: corpus has no active range profile", ErrInvalid)
	}

	return nil
}

func positiveInteger(value int) bool {
	return value > 0 && value <= maxPostgresInteger
}

func nonNegativeInteger(value int) bool {
	return value >= 0 && value <= maxPostgresInteger
}
