package anchor

import (
	"errors"
	"strings"
	"testing"
)

func TestParseCanonicalPoints(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		raw       string
		kind      PointKind
		corpus    Corpus
		bookID    int
		headingID int
		ordinal   int
		surah     int
		ayah      int
	}{
		{name: "Quran surah", raw: "quran/73", kind: PointKindQuranSurah, corpus: CorpusQuran, surah: 73},
		{name: "Quran ayah", raw: "quran/73:4", kind: PointKindQuranAyah, corpus: CorpusQuran, surah: 73, ayah: 4},
		{name: "kitab Work", raw: "kitab/797", kind: PointKindKitabWork, corpus: CorpusKitab, bookID: 797},
		{name: "kitab heading", raw: "kitab/797/h/11", kind: PointKindKitabHeading, corpus: CorpusKitab, bookID: 797, headingID: 11},
		{name: "kitab unit", raw: "kitab/797/h/11/u/42", kind: PointKindKitabUnit, corpus: CorpusKitab, bookID: 797, headingID: 11, ordinal: 42},
		{name: "front matter unit", raw: "kitab/797/h/0/u/1", kind: PointKindKitabUnit, corpus: CorpusKitab, bookID: 797, ordinal: 1},
		{name: "largest PostgreSQL integers", raw: "kitab/2147483647/h/2147483647/u/2147483647", kind: PointKindKitabUnit, corpus: CorpusKitab, bookID: 2147483647, headingID: 2147483647, ordinal: 2147483647},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			value, err := Parse(test.raw)
			if err != nil {
				t.Fatalf("Parse(%q): %v", test.raw, err)
			}

			if value.IsRange() {
				t.Fatalf("Parse(%q) unexpectedly returned a range", test.raw)
			}

			point := value.Start()
			assertPoint(t, point, test.kind, test.corpus, test.bookID, test.headingID, test.ordinal, test.surah, test.ayah)

			if got := value.String(); got != test.raw {
				t.Errorf("String() = %q, want %q", got, test.raw)
			}
		})
	}
}

func TestParseCanonicalRanges(t *testing.T) {
	t.Parallel()

	tests := []string{
		"quran/73:1..quran/73:4",
		"kitab/797/h/11/u/1..kitab/797/h/11/u/42",
		"kitab/797/h/0/u/1..kitab/797/h/11/u/42",
		"kitab/797..kitab/797/h/11/u/42",
	}

	for _, raw := range tests {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()

			value, err := Parse(raw)
			if err != nil {
				t.Fatalf("Parse(%q): %v", raw, err)
			}

			if !value.IsRange() {
				t.Fatalf("Parse(%q) did not return a range", raw)
			}

			if _, ok := value.End(); !ok {
				t.Fatal("End() did not expose the second boundary")
			}

			if got := value.String(); got != raw {
				t.Errorf("String() = %q, want %q", got, raw)
			}
		})
	}
}

func TestParseRejectsInvalidCanonicalValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
	}{
		{name: "empty", raw: ""},
		{name: "whitespace", raw: " quran/73:4"},
		{name: "control", raw: "quran/73:\x004"},
		{name: "non ASCII", raw: "quran/٧٣:٤"},
		{name: "uppercase", raw: "Quran/73:4"},
		{name: "over max length", raw: strings.Repeat("a", MaxLength+1)},
		{name: "legacy is not canonical", raw: "73:4"},
		{name: "unknown corpus", raw: "book/797"},
		{name: "reserved hadith", raw: "hadith/1"},
		{name: "reserved wiki", raw: "wiki/article"},
		{name: "reserved entity", raw: "entity/person/1"},
		{name: "Quran leading zero surah", raw: "quran/073:4"},
		{name: "Quran surah point leading zero", raw: "quran/073"},
		{name: "Quran leading zero ayah", raw: "quran/73:04"},
		{name: "Quran zero surah", raw: "quran/0:4"},
		{name: "Quran zero surah point", raw: "quran/0"},
		{name: "Quran surah trailing colon", raw: "quran/73:"},
		{name: "Quran extra colon", raw: "quran/73:4:5"},
		{name: "Quran overflow", raw: "quran/2147483648:1"},
		{name: "Quran surah point overflow", raw: "quran/2147483648"},
		{name: "kitab zero Work", raw: "kitab/0"},
		{name: "kitab Work leading zero", raw: "kitab/0797"},
		{name: "kitab overflow", raw: "kitab/2147483648"},
		{name: "heading zero is not a point", raw: "kitab/797/h/0"},
		{name: "heading leading zero", raw: "kitab/797/h/011"},
		{name: "unit zero ordinal", raw: "kitab/797/h/11/u/0"},
		{name: "unit wrong hierarchy", raw: "kitab/797/heading/11/unit/1"},
		{name: "unit extra segment", raw: "kitab/797/h/11/u/1/x"},
		{name: "range shorthand", raw: "quran/73:1..73:4"},
		{name: "range missing start", raw: "..quran/73:4"},
		{name: "range missing end", raw: "quran/73:1.."},
		{name: "range multiple separators", raw: "quran/73:1..quran/73:2..quran/73:3"},
		{name: "range mixed corpus", raw: "quran/73:4..kitab/797"},
		{name: "range mixed Quran surah", raw: "quran/72:4..quran/73:4"},
		{name: "range reversed Quran ayahs", raw: "quran/73:4..quran/73:1"},
		{name: "range between Quran surahs", raw: "quran/73..quran/73"},
		{name: "range from Quran surah to ayah", raw: "quran/73..quran/73:4"},
		{name: "range from Quran ayah to surah", raw: "quran/73:4..quran/73"},
		{name: "range mixed Work", raw: "kitab/797/h/11/u/1..kitab/798/h/11/u/2"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := Parse(test.raw)
			if !errors.Is(err, ErrInvalid) {
				t.Errorf("Parse(%q) error = %v, want ErrInvalid", test.raw, err)
			}
		})
	}
}

func TestParsePointRejectsRange(t *testing.T) {
	t.Parallel()

	_, err := ParsePoint("quran/73:1..quran/73:4")
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("ParsePoint(range) error = %v, want ErrInvalid", err)
	}
}

func TestLegacyParsers(t *testing.T) {
	t.Parallel()

	ayah, err := ParseLegacyAyahKey("73:4")
	if err != nil {
		t.Fatalf("ParseLegacyAyahKey(): %v", err)
	}

	if got := ayah.String(); got != "quran/73:4" {
		t.Errorf("legacy ayah canonical point = %q", got)
	}

	headingID, err := ParseLegacyTOC("toc-11")
	if err != nil {
		t.Fatalf("ParseLegacyTOC(): %v", err)
	}

	if headingID != 11 {
		t.Errorf("legacy toc heading id = %d, want 11", headingID)
	}
}

func TestLegacyParsersRejectInvalidValues(t *testing.T) {
	t.Parallel()

	ayahKeys := []string{"quran/73:4", "073:4", "73:04", "0:4", "73", "73:4 ", "2147483648:1"}
	for _, raw := range ayahKeys {
		if _, err := ParseLegacyAyahKey(raw); !errors.Is(err, ErrInvalid) {
			t.Errorf("ParseLegacyAyahKey(%q) error = %v, want ErrInvalid", raw, err)
		}
	}

	tocAnchors := []string{"toc-0", "toc-011", "toc-", "TOC-11", "toc--1", "toc-2147483648"}
	for _, raw := range tocAnchors {
		if _, err := ParseLegacyTOC(raw); !errors.Is(err, ErrInvalid) {
			t.Errorf("ParseLegacyTOC(%q) error = %v, want ErrInvalid", raw, err)
		}
	}
}

func TestConstructorsAndRangeValidation(t *testing.T) {
	t.Parallel()

	start, err := NewKitabUnit(797, 0, 1)
	if err != nil {
		t.Fatalf("NewKitabUnit(): %v", err)
	}

	end, err := NewKitabHeading(797, 11)
	if err != nil {
		t.Fatalf("NewKitabHeading(): %v", err)
	}

	value, err := NewRange(start, end)
	if err != nil {
		t.Fatalf("NewRange(): %v", err)
	}

	if got := value.String(); got != "kitab/797/h/0/u/1..kitab/797/h/11" {
		t.Errorf("NewRange().String() = %q", got)
	}

	_, quranSurahErr := NewQuranSurah(0)
	_, quranAyahErr := NewQuranAyah(0, 1)
	_, workErr := NewKitabWork(-1)
	_, headingErr := NewKitabHeading(1, 0)
	_, unitErr := NewKitabUnit(1, -1, 1)
	_, pointErr := FromPoint(Point{})

	invalidConstructors := []error{
		quranSurahErr,
		quranAyahErr,
		workErr,
		headingErr,
		unitErr,
		pointErr,
	}
	for index, constructorErr := range invalidConstructors {
		if !errors.Is(constructorErr, ErrInvalid) {
			t.Errorf("invalid constructor %d error = %v, want ErrInvalid", index, constructorErr)
		}
	}
}

func FuzzParse(f *testing.F) {
	seeds := []string{
		"quran/73",
		"quran/73:4",
		"kitab/797/h/11/u/42",
		"quran/73:1..quran/73:4",
		"kitab/797/h/1/u/1..kitab/798/h/1/u/2",
		"quran/2147483648:1",
		"quran/73:\x004",
		"kitab/0797",
		strings.Repeat("x", MaxLength+1),
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, raw string) {
		value, err := Parse(raw)
		if err != nil {
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("Parse(%q) returned an unclassified error: %v", raw, err)
			}

			return
		}

		if got := value.String(); got != raw {
			t.Fatalf("successful Parse(%q) formatted as %q", raw, got)
		}

		if _, roundTripErr := Parse(value.String()); roundTripErr != nil {
			t.Fatalf("Parse(String()) failed: %v", roundTripErr)
		}
	})
}

func assertPoint(
	t *testing.T,
	point Point,
	kind PointKind,
	corpus Corpus,
	bookID, headingID, ordinal, surah, ayah int,
) {
	t.Helper()

	if point.Kind() != kind || point.Corpus() != corpus || point.BookID() != bookID ||
		point.HeadingID() != headingID || point.Ordinal() != ordinal || point.Surah() != surah || point.Ayah() != ayah {
		t.Errorf(
			"point = kind:%s corpus:%s book:%d heading:%d ordinal:%d surah:%d ayah:%d",
			point.Kind(), point.Corpus(), point.BookID(), point.HeadingID(), point.Ordinal(), point.Surah(), point.Ayah(),
		)
	}
}
