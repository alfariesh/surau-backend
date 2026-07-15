package importer

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLiveImporterRoleConsumerSmoke proves the real LOGIN role, not the schema
// owner, can run the three A-2 importer footprints. Destructive cleanup remains
// owner-only and the Quran fixture is rolled back.
//
//nolint:paralleltest // one cluster role and three serial consumer smokes
func TestLiveImporterRoleConsumerSmoke(t *testing.T) {
	databaseURL := os.Getenv("SURAU_LIVE_PG")
	if databaseURL == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	owner, err := pgxpool.New(t.Context(), databaseURL)
	require.NoError(t, err)
	t.Cleanup(owner.Close)

	roleName := "surau_importer_test_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	password := uuid.NewString()
	quotedRole := pgx.Identifier{roleName}.Sanitize()
	validUntil := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	_, err = owner.Exec(t.Context(), fmt.Sprintf(
		"CREATE ROLE %s LOGIN PASSWORD '%s' NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS VALID UNTIL '%s'",
		quotedRole, password, validUntil,
	))
	require.NoError(t, err)
	_, err = owner.Exec(t.Context(), fmt.Sprintf("GRANT surau_importer TO %s", quotedRole))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, cleanupErr := owner.Exec(context.Background(), fmt.Sprintf("DROP ROLE IF EXISTS %s", quotedRole))
		assert.NoError(t, cleanupErr)
	})

	importerURL := databaseURLForRole(t, databaseURL, roleName, password)
	assertImporterLoginShape(t, importerURL, roleName)
	smokeShamelaAsImporter(t, owner, importerURL)
	smokeReaderAssetAsImporter(t, owner, importerURL)
	smokeQuranFixtureAsImporter(t, importerURL)
}

func databaseURLForRole(t *testing.T, databaseURL, roleName, password string) string {
	t.Helper()

	parsed, err := url.Parse(databaseURL)
	require.NoError(t, err)

	parsed.User = url.UserPassword(roleName, password)

	return parsed.String()
}

func assertImporterLoginShape(t *testing.T, importerURL, roleName string) {
	t.Helper()
	pool, err := pgxpool.New(t.Context(), importerURL)
	require.NoError(t, err)

	defer pool.Close()

	var currentUser string

	var canLogin, superuser, createRole, bypassRLS bool
	require.NoError(t, pool.QueryRow(t.Context(), `
SELECT current_user, rolcanlogin, rolsuper, rolcreaterole, rolbypassrls
FROM pg_roles WHERE rolname = current_user`).Scan(
		&currentUser, &canLogin, &superuser, &createRole, &bypassRLS,
	))
	assert.Equal(t, roleName, currentUser)
	assert.True(t, canLogin)
	assert.False(t, superuser)
	assert.False(t, createRole)
	assert.False(t, bypassRLS)
}

func smokeShamelaAsImporter(t *testing.T, owner *pgxpool.Pool, importerURL string) {
	t.Helper()

	const bookID = 9012
	resetBookImportState(t, owner, bookID, "a2-role-book")
	sourceDir := t.TempDir()
	writeBookSource(t, sourceDir, fixtureV1(bookID))

	first, err := Run(t.Context(), Options{
		SourceDir: sourceDir, PostgresURL: importerURL,
		ReleaseKey: "a2-role-book-v1", BookIDs: []int{bookID},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, first.ImportedBooks)
	second, err := Run(t.Context(), Options{
		SourceDir: sourceDir, PostgresURL: importerURL,
		ReleaseKey: "a2-role-book-v2", BookIDs: []int{bookID},
	})
	require.NoError(t, err)
	assert.Zero(t, second.StagedRemovalPages)
	assert.Zero(t, second.StagedRemovalHeadings)
}

func smokeReaderAssetAsImporter(t *testing.T, owner *pgxpool.Pool, importerURL string) {
	t.Helper()

	categoryID := int(time.Now().UnixNano()%100_000_000) + 1_800_000_000
	runID := uuid.NewString()
	_, err := owner.Exec(t.Context(),
		`INSERT INTO categories (id, name) VALUES ($1, 'a2 importer asset parent')`, categoryID)
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx := context.Background()

		cleanupTx, cleanupErr := owner.Begin(ctx)
		if !assert.NoError(t, cleanupErr) {
			return
		}
		defer func() { _ = cleanupTx.Rollback(ctx) }()

		_, cleanupErr = cleanupTx.Exec(ctx, `SET LOCAL session_replication_role = 'replica'`)
		assert.NoError(t, cleanupErr)
		_, cleanupErr = cleanupTx.Exec(ctx,
			`DELETE FROM category_translations WHERE category_id = $1`, categoryID)
		assert.NoError(t, cleanupErr)
		_, cleanupErr = cleanupTx.Exec(ctx, `DELETE FROM categories WHERE id = $1`, categoryID)
		assert.NoError(t, cleanupErr)
		_, cleanupErr = cleanupTx.Exec(ctx, `DELETE FROM generation_runs WHERE id = $1::uuid`, runID)
		assert.NoError(t, cleanupErr)
		assert.NoError(t, cleanupTx.Commit(ctx))
	})

	assetPath := filepath.Join(t.TempDir(), "asset.jsonl")
	asset := fmt.Sprintf(
		`{"kind":"category_translation","category_id":%d,"lang":"id","name":"Kategori A-2","provenance_class":"machine","generation":{"run_id":%q,"model_id":"a2-test","prompt_version":"catalog-translation-v1"}}`+"\n",
		categoryID, runID,
	)
	require.NoError(t, os.WriteFile(assetPath, []byte(asset), 0o600))
	stats, err := RunAssetImport(t.Context(), AssetOptions{PostgresURL: importerURL, Path: assetPath})
	require.NoError(t, err)
	assert.Equal(t, 1, stats.CategoryTranslations)
}

func smokeQuranFixtureAsImporter(t *testing.T, importerURL string) {
	t.Helper()
	pool, err := pgxpool.New(t.Context(), importerURL)
	require.NoError(t, err)

	defer pool.Close()

	tx, err := pool.Begin(t.Context())
	require.NoError(t, err)

	defer func() { _ = tx.Rollback(context.Background()) }()

	const surahID = 109

	_, err = tx.Exec(t.Context(), `
INSERT INTO quran_surahs (surah_id, name_latin, ayah_count, metadata)
VALUES ($1, 'A-2 role fixture', 1, '{"a2_role_fixture":true}'::jsonb)
ON CONFLICT (surah_id) DO UPDATE SET updated_at = now()`, surahID)
	require.NoError(t, err)
	_, err = tx.Exec(t.Context(), `
INSERT INTO quran_ayahs (surah_id, ayah_number, ayah_key, text_imlaei_simple, metadata)
VALUES ($1, 999, '109:999', 'نص اختبار', '{"a2_role_fixture":true}'::jsonb)
ON CONFLICT (surah_id, ayah_number) DO UPDATE SET updated_at = now()`, surahID)
	require.NoError(t, err)
	require.NoError(t, tx.Rollback(t.Context()))
}
