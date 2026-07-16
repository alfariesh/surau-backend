package persistent

import (
	"context"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/internal/entity"
	"github.com/alfariesh/surau-backend/pkg/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
)

const (
	quranPagePlanFixtureAyahs = 6236
	quranPagePlanFixtureUnits = 39875
	quranPagePlanWorstAyahs   = 42
	quranPagePlanWorstUnits   = 259
)

// TestLiveQuranCitableHydrationGenericPlanPerformanceGate reproduces the
// production cardinalities that made PostgreSQL's cached generic plan rescan
// the complete effective-license view for every requested binding. The test
// forces that plan class on one pgx connection and gates the complete hydration
// call, including the statement-local canonical license decision.
//
//nolint:paralleltest,wsl_v5 // serial, transaction-scoped production-shape live-DB fixture
func TestLiveQuranCitableHydrationGenericPlanPerformanceGate(t *testing.T) {
	databaseURL := os.Getenv("SURAU_LIVE_PG")
	if databaseURL == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pg, err := postgres.New(databaseURL)
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	tx, err := pg.Pool.Begin(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })

	ayahKeys := seedQuranPagePlanFixture(ctx, t, tx)
	_, err = tx.Exec(ctx, `SET LOCAL plan_cache_mode = 'force_generic_plan'`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `SET LOCAL statement_timeout = '2s'`)
	require.NoError(t, err)

	const warmups = 2
	const measured = 20
	durations := make([]time.Duration, 0, measured)
	for iteration := range warmups + measured {
		ayahs := make([]entity.QuranAyah, len(ayahKeys))
		for index, ayahKey := range ayahKeys {
			ayahs[index].AyahKey = ayahKey
		}

		startedAt := time.Now()
		err = hydrateQuranCitablePresentation(ctx, tx, ayahs)
		duration := time.Since(startedAt)
		require.NoError(t, err)
		for index := range ayahs {
			require.NotNil(t, ayahs[index].PrimaryUnitID,
				"licensed primary Citable Unit missing for %s", ayahs[index].AyahKey)
		}
		if iteration >= warmups {
			durations = append(durations, duration)
		}
	}

	slices.Sort(durations)
	p95 := durations[(95*len(durations)+99)/100-1]
	t.Logf("generic-plan hydration p50=%s p95=%s max=%s samples=%d units=%d",
		durations[len(durations)/2], p95, durations[len(durations)-1], len(durations),
		quranPagePlanWorstUnits)
	// Hydration gets a strict 20 ms sub-budget so the old corpus-wide rescan
	// (about 32 ms even on the fast local fixture and seconds in production)
	// fails before HTTP overhead is added. The deployment gate separately
	// enforces the public-read origin target of 200 ms end to end.
	require.Less(t, p95, 20*time.Millisecond,
		"Quran hydration regressed under the cached generic plan")

	require.NoError(t, tx.Rollback(ctx))
}

func seedQuranPagePlanFixture(
	ctx context.Context,
	t *testing.T,
	tx pgx.Tx,
) []string {
	t.Helper()

	const surahID = 114

	var ayahOffset int

	err := tx.QueryRow(ctx, `
SELECT COALESCE(max(ayah_number), 0)
FROM quran_ayahs
WHERE surah_id = $1`, surahID).Scan(&ayahOffset)
	require.NoError(t, err)

	_, err = tx.Exec(ctx, `SET LOCAL surau.registry_writer = 'unit-service'`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `SET LOCAL session_replication_role = 'replica'`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
UPDATE quran_script_sources
SET license_status = 'permitted'
WHERE id = 'qpc-hafs'`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
INSERT INTO quran_surahs (
    surah_id, name_latin, ayah_count, units_derived_at, units_stale_at
) VALUES ($1, 'Quran page performance fixture', $2, clock_timestamp(), NULL)
ON CONFLICT (surah_id) DO UPDATE
SET ayah_count = quran_surahs.ayah_count + EXCLUDED.ayah_count,
    units_derived_at = clock_timestamp(),
    units_stale_at = NULL`, surahID, quranPagePlanFixtureAyahs)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `
INSERT INTO quran_ayahs (
    surah_id, ayah_number, ayah_key, text_qpc_hafs, text_imlaei_simple,
    search_text, page_number, juz_number, hizb_number
)
SELECT $1::integer,
       $4::integer + number,
       ($1::integer)::text || ':' || ($4::integer + number),
       'fixture-ayah-' || ($4::integer + number),
       'fixture-ayah-' || ($4::integer + number),
       'fixture ayah ' || ($4::integer + number),
       CASE WHEN number <= $3::integer THEN 585 ELSE 1 END,
       1,
       1
FROM generate_series(1, $2::integer) number`,
		surahID, quranPagePlanFixtureAyahs, quranPagePlanWorstAyahs, ayahOffset)
	require.NoError(t, err)
	_, err = tx.Exec(
		ctx, `
WITH mapped AS (
    SELECT unit_number,
           CASE
               WHEN unit_number <= $5::integer THEN $6::integer + 1 + ((unit_number - 1) % $3::integer)
               ELSE $6::integer + $3::integer + 1 + ((unit_number - $5::integer - 1) % ($2::integer - $3::integer))
           END AS ayah_number,
           CASE
               WHEN unit_number <= $5::integer THEN 1 + ((unit_number - 1) / $3::integer)
               ELSE 1 + ((unit_number - $5::integer - 1) / ($2::integer - $3::integer))
           END AS ordinal
    FROM generate_series(1, $4::integer) unit_number
), inserted_units AS (
    INSERT INTO citable_units (
        id, corpus, kind, ordinal, position, anchor, text, text_normalized,
        normalization_version, content_hash, occurrence, language,
        provenance_class, lifecycle, content_role, review_status
    )
    SELECT md5('quran-page-plan-' || ($1::integer)::text || '-' || mapped.unit_number)::uuid,
           'quran',
           'primary_text',
           mapped.ordinal,
           mapped.ordinal,
           'quran/' || ($1::integer)::text || ':' || mapped.ayah_number || '/u/' || mapped.ordinal,
           'fixture-ayah-' || mapped.ayah_number,
           'fixture-ayah-' || mapped.ayah_number,
           1,
           decode(md5('quran-page-plan-hash-' || ($1::integer)::text || '-' || mapped.unit_number), 'hex'),
           1,
           'ar',
           'source',
           'active',
           NULL::text,
           'approved'
    FROM mapped
    RETURNING id, anchor
)
INSERT INTO quran_citable_unit_bindings (
    unit_id, surah_id, ayah_number, ordinal, role, source_updated_at
)
SELECT inserted.id,
       $1::integer,
       split_part(split_part(inserted.anchor, '/', 2), ':', 2)::integer,
       split_part(inserted.anchor, '/', 4)::integer,
       'primary_text',
       ayah.updated_at
FROM inserted_units inserted
JOIN quran_ayahs ayah
  ON ayah.surah_id = $1::integer
 AND ayah.ayah_number = split_part(split_part(inserted.anchor, '/', 2), ':', 2)::integer`,
		surahID,
		quranPagePlanFixtureAyahs,
		quranPagePlanWorstAyahs,
		quranPagePlanFixtureUnits,
		quranPagePlanWorstUnits,
		ayahOffset,
	)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `SET LOCAL session_replication_role = 'origin'`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `ANALYZE quran_ayahs`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `ANALYZE citable_units`)
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `ANALYZE quran_citable_unit_bindings`)
	require.NoError(t, err)

	rows, err := tx.Query(ctx, `
SELECT ayah_key
FROM quran_ayahs
WHERE surah_id = $1 AND page_number = 585
	ORDER BY ayah_number`, surahID)
	require.NoError(t, err)

	defer rows.Close()

	keys := make([]string, 0, quranPagePlanWorstAyahs)

	for rows.Next() {
		var key string
		require.NoError(t, rows.Scan(&key))
		keys = append(keys, key)
	}

	require.NoError(t, rows.Err())
	require.Len(t, keys, quranPagePlanWorstAyahs)

	return keys
}
