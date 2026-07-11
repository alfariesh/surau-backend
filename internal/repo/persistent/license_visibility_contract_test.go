package persistent

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPublicKitabReadsUseLicenseVisibilityGate freezes the B-4 query boundary.
// A direct join to book_publications would bypass restricted takedowns and the
// explicit grandfather policy, so every public kitab consumer must enter via
// public_book_publications instead.
func TestPublicKitabReadsUseLicenseVisibilityGate(t *testing.T) {
	t.Parallel()

	publicReaders := []string{
		"anchor_postgres.go",
		"bookrag_postgres.go",
		"cross_reference_postgres.go",
		"personal_postgres.go",
		"quran_postgres.go",
		"reader_postgres.go",
	}
	basePublicationRead := regexp.MustCompile(`(?i)\b(?:from|join)\s+book_publications\b`)

	for _, filename := range publicReaders {
		t.Run(filename, func(t *testing.T) {
			t.Parallel()

			source, err := os.ReadFile(filename)
			require.NoError(t, err)
			require.Contains(t, strings.ToLower(string(source)), "public_book_publications")
			require.Falsef(
				t,
				basePublicationRead.Match(source),
				"%s reads book_publications directly and can bypass the B-4 license gate",
				filename,
			)
		})
	}
}

// TestPublicSectionAssetQueriesUseStatementLocalLicenseGate prevents a
// check-then-read race: every statement that returns TOC/section asset data
// must independently join the canonical public visibility view.
func TestPublicSectionAssetQueriesUseStatementLocalLicenseGate(t *testing.T) {
	t.Parallel()

	sourceBytes, err := os.ReadFile("reader_postgres.go")
	require.NoError(t, err)

	source := string(sourceBytes)

	guardedFunctions := []string{
		"func (r *ReaderRepo) ListTOCEntries(",
		"func (r *ReaderRepo) GetSection(",
		"func (r *ReaderRepo) pageSelectBuilder()",
		"func (r *ReaderRepo) getSectionTranslation(",
		"func (r *ReaderRepo) getSectionAudio(",
		"func (r *ReaderRepo) getBookLanguageCoverage(",
	}
	for _, signature := range guardedFunctions {
		fragment := sourceFunction(t, source, signature)
		require.Containsf(
			t,
			fragment,
			"public_book_publications",
			"%s can return section assets without the statement-local B-4 visibility gate",
			signature,
		)
	}
}

// TestBookQuranReferenceQueriesGateCountAndData keeps both independent
// statements behind the public view. Guarding only a preliminary existence
// check would still allow a visibility change between that check and a read.
func TestBookQuranReferenceQueriesGateCountAndData(t *testing.T) {
	t.Parallel()

	sourceBytes, err := os.ReadFile("quran_postgres.go")
	require.NoError(t, err)
	fragment := sourceFunction(t, string(sourceBytes), "func (r *QuranRepo) ListBookQuranReferences(")

	require.Equal(
		t,
		2,
		strings.Count(fragment, "Join(\"public_book_publications"),
		"both count and data builders must join public_book_publications",
	)
}

// TestPublicAnchorUnitLicenseGate freezes the unit-level half of B-4. A
// visible Work may contain mixed-license units: eligible siblings remain
// addressable, while a derived locator must never fall back to the coarse
// source row after every unit was filtered out.
func TestPublicAnchorUnitLicenseGate(t *testing.T) {
	t.Parallel()

	sourceBytes, err := os.ReadFile("anchor_postgres.go")
	require.NoError(t, err)

	source := string(sourceBytes)

	for _, signature := range []string{
		"func (r *AnchorRepo) ResolveHeading(",
		"func (r *AnchorRepo) ResolvePage(",
	} {
		fragment := sourceFunction(t, source, signature)
		require.Contains(t, fragment, "u.license_status IS NULL OR u.license_status = 'permitted'")
		require.Contains(t, fragment, "units_derived_at IS NOT NULL")
		require.Contains(t, fragment, "len(result.ActiveRecords) == 0 && !derived")
	}

	require.Contains(t, canonicalUnitRootSQL,
		"u.license_status IS NULL OR u.license_status = 'permitted'")
	lineageLoader := sourceFunction(t, source, "func loadLineageClosure(")
	require.Contains(t, lineageLoader,
		"successor.license_status IS NULL OR successor.license_status = 'permitted'")
}

// TestCrossReferenceHeadingLicenseGate keeps heading backlinks aligned with
// the Anchor resolver: legacy non-derived headings remain addressable, while
// derived headings need at least one eligible active sibling.
func TestCrossReferenceHeadingLicenseGate(t *testing.T) {
	t.Parallel()

	migration, err := os.ReadFile("../../../migrations/20260711000003_add_book_license_gate.up.sql")
	if os.IsNotExist(err) {
		migration, err = os.ReadFile("migrations/20260711000003_add_book_license_gate.up.sql")
	}

	require.NoError(t, err)

	functionSQL := string(migration)
	require.Contains(t, functionSQL, "RETURN NOT book_derived OR EXISTS")
	require.Contains(t, functionSQL, "u.lifecycle = 'active'")
	require.Contains(t, functionSQL,
		"u.license_status IS NULL OR u.license_status = 'permitted'")
}

func sourceFunction(t *testing.T, source, signature string) string {
	t.Helper()

	start := strings.Index(source, signature)
	require.NotEqualf(t, -1, start, "function %s not found", signature)

	remainder := source[start:]
	if next := strings.Index(remainder[len(signature):], "\nfunc ("); next >= 0 {
		return remainder[:len(signature)+next]
	}

	return remainder
}
