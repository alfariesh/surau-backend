// Package searchtext is the single write path for persisted, normalized
// search text (charter D9 / 1B C5: one Arabic-normalization semantic,
// versioned). It wraps the canonical profile in internal/quranutil — do NOT
// fork or inline another normalization when persisting derived search
// columns; bump ProfileVersion together with any change to the canonical
// profile and re-run the affected backfills.
//
// Known, deliberate delta (decision deferred to B-5): the reader's inline
// query-time book-search folding also folds ء and ة, which the canonical
// profile does not. Persisted columns follow the canonical profile only.
package searchtext

import "github.com/alfariesh/surau-backend/internal/quranutil"

// ProfileVersion identifies the normalization profile used for persisted
// search text. Stored on backfill checkpoints so a profile change is
// re-runnable instead of silently mixed.
const ProfileVersion = 1

// Normalize returns the canonical normalized form for search/linking.
// Never use the result as display text.
func Normalize(value string) string {
	return quranutil.NormalizeKey(value)
}
