package importer

import (
	"encoding/json"
	"reflect"
	"sort"

	"github.com/alfariesh/surau-backend/internal/readerutil"
)

// pageRecord / headingRecord mirror the mutable columns of a stored row, used
// to decide whether an incoming source row actually differs (E4: identical
// re-import must be a true no-op).
type pageRecord struct {
	Part         string
	PrintedPage  string
	Number       string
	ContentHTML  string
	ContentText  string
	ServicesJSON string
	IsDeleted    bool
}

type headingRecord struct {
	ParentID  int
	PageID    int
	Depth     int
	Ordinal   int
	Content   string
	IsDeleted bool
}

// rowDiff is the staged-diff verdict for one table of a book: which incoming
// rows need writing (added / changed / revived-from-tombstone) and which
// stored live rows disappeared from the source (removal candidates — never
// deleted, only staged or tombstoned after approval).
type rowDiff[T any] struct {
	Upserts    []T
	Added      int
	Changed    int
	Revived    int
	RemovedIDs []int
}

func (d rowDiff[T]) empty() bool {
	return len(d.Upserts) == 0 && len(d.RemovedIDs) == 0
}

type (
	pageDiff    = rowDiff[sourcePage]
	headingDiff = rowDiff[readerutil.DecoratedHeading]
)

// diffRows compares stored rows against the incoming live source rows.
// Incoming must already exclude source-flagged deletions — those count as
// removals, exactly like rows missing from the source.
func diffRows[T, R any](
	current map[int]R,
	incoming []T,
	idOf func(T) int,
	deleted func(R) bool,
	equal func(*R, *T) bool,
) rowDiff[T] {
	var diff rowDiff[T]

	incomingIDs := make(map[int]struct{}, len(incoming))

	for _, row := range incoming {
		incomingIDs[idOf(row)] = struct{}{}

		record, exists := current[idOf(row)]
		switch {
		case !exists:
			diff.Added++
			diff.Upserts = append(diff.Upserts, row)
		case deleted(record):
			diff.Revived++
			diff.Upserts = append(diff.Upserts, row)
		case !equal(&record, &row):
			diff.Changed++
			diff.Upserts = append(diff.Upserts, row)
		}
	}

	for id, record := range current {
		if deleted(record) {
			continue
		}

		if _, ok := incomingIDs[id]; !ok {
			diff.RemovedIDs = append(diff.RemovedIDs, id)
		}
	}

	sort.Ints(diff.RemovedIDs)

	return diff
}

func diffPages(current map[int]pageRecord, incoming []sourcePage) pageDiff {
	return diffRows(
		current, incoming,
		func(p sourcePage) int { return p.ID },
		func(r pageRecord) bool { return r.IsDeleted },
		pageEqual,
	)
}

func diffHeadings(current map[int]headingRecord, incoming []readerutil.DecoratedHeading) headingDiff {
	return diffRows(
		current, incoming,
		func(h readerutil.DecoratedHeading) int { return h.ID },
		func(r headingRecord) bool { return r.IsDeleted },
		headingEqual,
	)
}

func pageEqual(record *pageRecord, page *sourcePage) bool {
	return record.Part == ptrValue(page.Part) &&
		record.PrintedPage == ptrValue(page.PrintedPage) &&
		record.Number == ptrValue(page.Number) &&
		record.ContentHTML == page.ContentHTML &&
		record.ContentText == page.ContentText &&
		jsonEqual(record.ServicesJSON, page.Services)
}

func headingEqual(record *headingRecord, heading *readerutil.DecoratedHeading) bool {
	return record.ParentID == heading.ParentID &&
		record.PageID == heading.PageID &&
		record.Depth == heading.Depth &&
		record.Ordinal == heading.Ordinal &&
		record.Content == heading.Content
}

// jsonEqual compares two JSON documents semantically: Postgres jsonb does not
// preserve whitespace or object key order, so comparing raw strings against a
// stored jsonb::text would flag unchanged rows as changed. Empty means NULL.
func jsonEqual(a, b string) bool {
	if a == "" || b == "" {
		return a == b
	}

	var av, bv any
	if err := json.Unmarshal([]byte(a), &av); err != nil {
		return a == b
	}

	if err := json.Unmarshal([]byte(b), &bv); err != nil {
		return a == b
	}

	return reflect.DeepEqual(av, bv)
}

// subsetOf reports whether every id in needles is present in haystack.
func subsetOf(needles, haystack []int) bool {
	set := make(map[int]struct{}, len(haystack))
	for _, id := range haystack {
		set[id] = struct{}{}
	}

	for _, id := range needles {
		if _, ok := set[id]; !ok {
			return false
		}
	}

	return true
}

func ptrValue(value *string) string {
	if value == nil {
		return ""
	}

	return *value
}
