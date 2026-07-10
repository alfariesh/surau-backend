package anchor

import (
	"fmt"
	"strconv"
	"strings"
)

const maxPostgresInteger = 1<<31 - 1

const (
	quranPointSegments       = 2
	kitabWorkSegments        = 2
	kitabHeadingSegments     = 4
	kitabCitableUnitSegments = 6
)

// Parse validates one canonical point or full-point range.
func Parse(raw string) (Value, error) {
	if err := validateLexicalForm(raw); err != nil {
		return Value{}, err
	}

	separatorCount := strings.Count(raw, "..")
	if separatorCount == 0 {
		point, err := parsePoint(raw)
		if err != nil {
			return Value{}, err
		}

		return FromPoint(point)
	}

	if separatorCount != 1 {
		return Value{}, fmt.Errorf("%w: range must contain exactly two full points", ErrInvalid)
	}

	startRaw, endRaw, _ := strings.Cut(raw, "..")

	start, err := parsePoint(startRaw)
	if err != nil {
		return Value{}, err
	}

	end, err := parsePoint(endRaw)
	if err != nil {
		return Value{}, err
	}

	return NewRange(start, end)
}

// ParsePoint validates one canonical point. Ranges are rejected.
func ParsePoint(raw string) (Point, error) {
	if err := validateLexicalForm(raw); err != nil {
		return Point{}, err
	}

	if strings.Contains(raw, "..") {
		return Point{}, fmt.Errorf("%w: ParsePoint does not accept ranges", ErrInvalid)
	}

	return parsePoint(raw)
}

// ParseLegacyAyahKey validates a legacy {surah}:{ayah} key and maps it to its
// canonical quran/{surah}:{ayah} point.
func ParseLegacyAyahKey(raw string) (Point, error) {
	if err := validateLexicalForm(raw); err != nil {
		return Point{}, err
	}

	surah, ayah, err := parseAyahLocator(raw)
	if err != nil {
		return Point{}, err
	}

	return NewQuranAyah(surah, ayah)
}

// ParseLegacyTOC validates toc-{heading_id} and returns its positive heading
// id. The caller supplies the book scope separately.
func ParseLegacyTOC(raw string) (int, error) {
	if err := validateLexicalForm(raw); err != nil {
		return 0, err
	}

	const prefix = "toc-"
	if !strings.HasPrefix(raw, prefix) {
		return 0, fmt.Errorf("%w: legacy heading must use toc-{heading_id}", ErrInvalid)
	}

	headingID, err := parseDecimal(raw[len(prefix):], false)
	if err != nil {
		return 0, err
	}

	return headingID, nil
}

func parsePoint(raw string) (Point, error) {
	parts := strings.Split(raw, "/")
	if len(parts) == 0 {
		return Point{}, fmt.Errorf("%w: point is empty", ErrInvalid)
	}

	switch parts[0] {
	case string(CorpusQuran):
		return parseQuranPoint(parts)
	case string(CorpusKitab):
		return parseKitabPoint(parts)
	case string(CorpusHadith), string(CorpusWiki), string(CorpusEntity):
		return Point{}, fmt.Errorf("%w: corpus %q is reserved and not resolvable", ErrInvalid, parts[0])
	default:
		return Point{}, fmt.Errorf("%w: unsupported corpus", ErrInvalid)
	}
}

func parseQuranPoint(parts []string) (Point, error) {
	if len(parts) != quranPointSegments {
		return Point{}, fmt.Errorf("%w: Quran point must use quran/{surah} or quran/{surah}:{ayah}", ErrInvalid)
	}

	if !strings.Contains(parts[1], ":") {
		surah, err := parseDecimal(parts[1], false)
		if err != nil {
			return Point{}, err
		}

		return NewQuranSurah(surah)
	}

	surah, ayah, err := parseAyahLocator(parts[1])
	if err != nil {
		return Point{}, err
	}

	return NewQuranAyah(surah, ayah)
}

func parseAyahLocator(raw string) (surah, ayah int, err error) {
	if strings.Count(raw, ":") != 1 {
		return 0, 0, fmt.Errorf("%w: ayah locator must use {surah}:{ayah}", ErrInvalid)
	}

	surahRaw, ayahRaw, _ := strings.Cut(raw, ":")

	surah, err = parseDecimal(surahRaw, false)
	if err != nil {
		return 0, 0, err
	}

	ayah, err = parseDecimal(ayahRaw, false)
	if err != nil {
		return 0, 0, err
	}

	return surah, ayah, nil
}

func parseKitabPoint(parts []string) (Point, error) {
	switch len(parts) {
	case kitabWorkSegments:
		return parseKitabWork(parts)
	case kitabHeadingSegments:
		return parseKitabHeading(parts)
	case kitabCitableUnitSegments:
		return parseKitabUnit(parts)
	default:
		return Point{}, fmt.Errorf("%w: unsupported kitab point profile", ErrInvalid)
	}
}

func parseKitabWork(parts []string) (Point, error) {
	bookID, err := parseDecimal(parts[1], false)
	if err != nil {
		return Point{}, err
	}

	return NewKitabWork(bookID)
}

func parseKitabHeading(parts []string) (Point, error) {
	if parts[2] != "h" {
		return Point{}, fmt.Errorf("%w: kitab hierarchy must use h", ErrInvalid)
	}

	bookID, err := parseDecimal(parts[1], false)
	if err != nil {
		return Point{}, err
	}

	headingID, err := parseDecimal(parts[3], false)
	if err != nil {
		return Point{}, err
	}

	return NewKitabHeading(bookID, headingID)
}

func parseKitabUnit(parts []string) (Point, error) {
	if parts[2] != "h" {
		return Point{}, fmt.Errorf("%w: kitab hierarchy must use h", ErrInvalid)
	}

	if parts[4] != "u" {
		return Point{}, fmt.Errorf("%w: kitab unit hierarchy must use u", ErrInvalid)
	}

	bookID, err := parseDecimal(parts[1], false)
	if err != nil {
		return Point{}, err
	}

	headingID, err := parseDecimal(parts[3], true)
	if err != nil {
		return Point{}, err
	}

	ordinal, err := parseDecimal(parts[5], false)
	if err != nil {
		return Point{}, err
	}

	return NewKitabUnit(bookID, headingID, ordinal)
}

func parseDecimal(raw string, allowZero bool) (int, error) {
	if raw == "" {
		return 0, fmt.Errorf("%w: decimal component is empty", ErrInvalid)
	}

	if len(raw) > 1 && raw[0] == '0' {
		return 0, fmt.Errorf("%w: decimal component has a leading zero", ErrInvalid)
	}

	for index := range len(raw) {
		if raw[index] < '0' || raw[index] > '9' {
			return 0, fmt.Errorf("%w: decimal component contains a non-ASCII digit", ErrInvalid)
		}
	}

	value, err := strconv.ParseInt(raw, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("%w: decimal component exceeds a PostgreSQL integer", ErrInvalid)
	}

	if value == 0 && !allowZero {
		return 0, fmt.Errorf("%w: decimal component must be positive", ErrInvalid)
	}

	return int(value), nil
}

func validateLexicalForm(raw string) error {
	if raw == "" {
		return fmt.Errorf("%w: value is empty", ErrInvalid)
	}

	if len(raw) > MaxLength {
		return fmt.Errorf("%w: value exceeds %d bytes", ErrInvalid, MaxLength)
	}

	for index := range len(raw) {
		value := raw[index]
		if value < '!' || value > '~' {
			return fmt.Errorf("%w: value must contain visible ASCII only", ErrInvalid)
		}

		if value >= 'A' && value <= 'Z' {
			return fmt.Errorf("%w: ASCII letters must be lowercase", ErrInvalid)
		}
	}

	return nil
}
