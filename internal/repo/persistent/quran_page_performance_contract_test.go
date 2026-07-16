package persistent

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestQuranCitableHydrationKeepsTargetedCanonicalLicenseGate prevents the
// performance fix from regressing into either a corpus-wide license-view join
// or an application-side license shortcut. The canonical virtual view remains
// the only visibility decision; LATERAL + LIMIT makes the lookup unit-scoped.
func TestQuranCitableHydrationKeepsTargetedCanonicalLicenseGate(t *testing.T) {
	t.Parallel()

	sourceBytes, err := os.ReadFile("quran_citable_presentation.go")
	require.NoError(t, err)
	fragment := sourceFunction(
		t,
		string(sourceBytes),
		"func hydrateQuranCitablePresentation(",
	)

	require.Contains(t, fragment, "JOIN LATERAL (")
	require.Contains(t, fragment, "FROM citable_units_with_effective_license license")
	require.Contains(t, fragment, "license.id = u.id")
	require.Contains(t, fragment, "license.corpus = 'quran'")
	require.Contains(t, fragment, "license.effective_license_status = 'permitted'")
	require.Contains(t, fragment, "LIMIT 1")
	require.Contains(t, fragment, "u.corpus = 'quran'")
}

// TestEffectiveLicenseViewSplitPreservesPolicyShape freezes the two mutually
// exclusive corpus branches and every Quran source rule. The rewrite is a
// planner boundary only: it must not copy a license onto Citable Units or
// weaken restricted/grandfather handling.
func TestEffectiveLicenseViewSplitPreservesPolicyShape(t *testing.T) {
	t.Parallel()

	up := readRepoFile(t, "../../../migrations/20260716000001_split_citable_effective_license_view.up.sql")
	down := readRepoFile(t, "../../../migrations/20260716000001_split_citable_effective_license_view.down.sql")
	profile := readRepoFile(t, "../../../ops/perf/quran-page-profile.sql")
	diagnostics := readRepoFile(t, "../../../ops/perf/quran-page-diagnostics.sh")

	require.Contains(t, up, "CREATE OR REPLACE VIEW citable_units_with_effective_license")
	require.Contains(t, up, "WHERE u.corpus <> 'quran'")
	require.Contains(t, up, "UNION ALL")
	require.Contains(t, up, "WHERE u.corpus = 'quran'")

	for _, policy := range []string{
		"ts.license_status",
		"xs.license_status",
		"ss.license_status = 'permitted'",
		"ss.license_status <> 'restricted'",
		"ss.checksum IS NOT DISTINCT FROM ss.license_grandfathered_checksum",
		"'quran_translation_source'::TEXT",
		"'quran_transliteration_source'::TEXT",
		"'quran_script_grandfather'::TEXT",
		"'quran_script_source'::TEXT",
	} {
		require.Contains(t, up, policy)
		require.Contains(t, down, policy)
	}

	require.NotContains(t, up, "UPDATE citable_units")
	require.NotContains(t, up, "MATERIALIZED VIEW")
	require.Equal(t, 2, strings.Count(profile, "FROM citable_units_with_effective_license license"))
	require.Equal(t, 2, strings.Count(profile, "license.corpus = 'quran'"))

	for _, dataset := range []string{
		"quran_surahs",
		"quran_ayahs",
		"quran_translations",
		"quran_transliterations",
		"quran_citable_units",
		"quran_citable_bindings",
		"quran_translation_sources",
		"quran_transliteration_sources",
		"quran_script_sources",
		"quran_effective_licenses",
	} {
		require.Contains(t, diagnostics, "'"+dataset+"'")
	}
}

func readRepoFile(t *testing.T, path string) string {
	t.Helper()

	contents, err := os.ReadFile(path)
	require.NoError(t, err)

	return string(contents)
}
