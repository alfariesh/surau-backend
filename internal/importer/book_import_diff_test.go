package importer

import (
	"testing"

	"github.com/alfariesh/surau-backend/internal/readerutil"
	"github.com/stretchr/testify/assert"
)

func TestDiffPages(t *testing.T) {
	t.Parallel()

	current := map[int]pageRecord{
		1: {Part: "1", ContentHTML: "<p>one</p>", ContentText: "one"},
		2: {Part: "1", ContentHTML: "<p>two</p>", ContentText: "two"},
		3: {Part: "1", ContentHTML: "<p>three</p>", ContentText: "three", IsDeleted: true},
		4: {Part: "1", ContentHTML: "<p>four</p>", ContentText: "four"},
	}
	incoming := []sourcePage{
		{ID: 1, Part: new("1"), ContentHTML: "<p>one</p>", ContentText: "one"},       // identical -> skip
		{ID: 2, Part: new("1"), ContentHTML: "<p>two v2</p>", ContentText: "two v2"}, // changed
		{ID: 3, Part: new("1"), ContentHTML: "<p>three</p>", ContentText: "three"},   // revived tombstone
		{ID: 5, Part: new("1"), ContentHTML: "<p>five</p>", ContentText: "five"},     // added
	}

	diff := diffPages(current, incoming)

	assert.Equal(t, 1, diff.Added)
	assert.Equal(t, 1, diff.Changed)
	assert.Equal(t, 1, diff.Revived)
	assert.Len(t, diff.Upserts, 3)
	assert.Equal(t, []int{4}, diff.RemovedIDs, "live row missing from source is the only removal; tombstoned rows never re-remove")
}

func TestDiffPagesIdenticalIsNoOp(t *testing.T) {
	t.Parallel()

	current := map[int]pageRecord{
		1: {Part: "1", Number: "1", ContentHTML: "<p>one</p>", ContentText: "one", ServicesJSON: `{"a": 1, "b": 2}`},
	}
	incoming := []sourcePage{
		// jsonb normalizes key order/whitespace — semantic equality must hold.
		{ID: 1, Part: new("1"), Number: new("1"), ContentHTML: "<p>one</p>", ContentText: "one", Services: `{"b":2,"a":1}`},
	}

	diff := diffPages(current, incoming)

	assert.True(t, diff.empty())
	assert.Zero(t, diff.Added+diff.Changed+diff.Revived)
	assert.Empty(t, diff.RemovedIDs)
}

func TestDiffHeadings(t *testing.T) {
	t.Parallel()

	current := map[int]headingRecord{
		1: {PageID: 1, Depth: 1, Ordinal: 1, Content: "Bab Satu"},
		2: {ParentID: 1, PageID: 3, Depth: 2, Ordinal: 2, Content: "Bab Dua"},
	}
	incoming := []readerutil.DecoratedHeading{
		{SourceHeading: readerutil.SourceHeading{ID: 1, PageID: 1, Content: "Bab Satu"}, Depth: 1, Ordinal: 1},
	}

	diff := diffHeadings(current, incoming)

	assert.Zero(t, diff.Added+diff.Changed+diff.Revived)
	assert.Equal(t, []int{2}, diff.RemovedIDs)
}

func TestSubsetOf(t *testing.T) {
	t.Parallel()

	assert.True(t, subsetOf(nil, nil))
	assert.True(t, subsetOf([]int{2}, []int{1, 2, 3}))
	assert.False(t, subsetOf([]int{4}, []int{1, 2, 3}))
	assert.False(t, subsetOf([]int{1}, nil))
}

func TestJSONEqual(t *testing.T) {
	t.Parallel()

	assert.True(t, jsonEqual("", ""))
	assert.False(t, jsonEqual("", `{}`))
	assert.True(t, jsonEqual(`{"a":1,"b":[1,2]}`, `{ "b": [1, 2], "a": 1 }`))
	assert.False(t, jsonEqual(`{"a":1}`, `{"a":2}`))
	assert.False(t, jsonEqual(`[1,2]`, `[2,1]`), "array order is semantic")
}
