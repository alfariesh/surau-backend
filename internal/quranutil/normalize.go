package quranutil

// ProfileName and ProfileVersion identify the frozen canonical Arabic
// normalization contract. A semantic change must select a new immutable
// implementation and corpus; NormalizeKeyV1 remains available for old rows.
const (
	ProfileName    = "search-key"
	ProfileVersion = 1
)

// NormalizeKey removes Quranic marks and normalizes Arabic variants for lookup.
// It is intentionally for search/linking only; never use it as display text.
func NormalizeKey(value string) string {
	return NormalizeKeyV1(value)
}
