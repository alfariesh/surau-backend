package persistent

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alfariesh/surau-backend/pkg/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLiveExtractionRoleGrantBoundary runs as a real LOGIN role that owns only
// surau_extraction_writer membership. It freezes both ACL shape and the
// pending-only defense in depth required by A-2.
//
//nolint:paralleltest // creates one short-lived cluster role
func TestLiveExtractionRoleGrantBoundary(t *testing.T) {
	databaseURL := os.Getenv("SURAU_LIVE_PG")
	if databaseURL == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pg, err := postgres.New(databaseURL)
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	ctx := t.Context()
	loginRole := "surau_extraction_test_" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	password := uuid.NewString()
	quotedRole := pgx.Identifier{loginRole}.Sanitize()
	validUntil := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	_, err = pg.Pool.Exec(ctx, fmt.Sprintf(
		"CREATE ROLE %s LOGIN PASSWORD '%s' NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS VALID UNTIL '%s'",
		quotedRole, password, validUntil,
	))
	require.NoError(t, err)
	_, err = pg.Pool.Exec(ctx, fmt.Sprintf("GRANT surau_extraction_writer TO %s", quotedRole))
	require.NoError(t, err)

	entityID := uuid.NewString()

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		_, cleanupErr := pg.Pool.Exec(cleanupCtx, `DELETE FROM knowledge_entities WHERE id = $1::uuid`, entityID)
		assert.NoError(t, cleanupErr)
		_, cleanupErr = pg.Pool.Exec(cleanupCtx, fmt.Sprintf("DROP ROLE IF EXISTS %s", quotedRole))
		assert.NoError(t, cleanupErr)
	})

	assertExtractionGrantGolden(t, pg)

	parsedURL, err := url.Parse(databaseURL)
	require.NoError(t, err)

	parsedURL.User = url.UserPassword(loginRole, password)
	rolePG, err := postgres.New(parsedURL.String())
	require.NoError(t, err)
	t.Cleanup(rolePG.Close)
	connection, err := rolePG.Pool.Acquire(ctx)
	require.NoError(t, err)

	defer connection.Release()

	var currentUser string

	var canLogin bool
	require.NoError(t, connection.QueryRow(ctx, `
SELECT current_user, rolcanlogin FROM pg_roles WHERE rolname = current_user`).Scan(&currentUser, &canLogin))
	assert.Equal(t, loginRole, currentUser)
	assert.True(t, canLogin)

	_, err = connection.Exec(ctx, `
INSERT INTO knowledge_entities (id, entity_type, canonical_name_ar)
VALUES ($1::uuid, 'concept', 'pending-only fixture')`, entityID)
	require.NoError(t, err)

	var status string
	require.NoError(t, connection.QueryRow(ctx,
		`SELECT review_status FROM knowledge_entities WHERE id = $1::uuid`, entityID).Scan(&status))
	assert.Equal(t, "pending", status)

	_, err = connection.Exec(ctx, `
INSERT INTO knowledge_entities (id, entity_type, canonical_name_ar, review_status)
VALUES ($1::uuid, 'concept', 'forbidden explicit approval', 'approved')`, uuid.NewString())
	requireInsufficientPrivilege(t, err)
	_, err = connection.Exec(ctx,
		`UPDATE knowledge_entities SET review_status = 'approved' WHERE id = $1::uuid`, entityID)
	requireInsufficientPrivilege(t, err)

	_, err = pg.Pool.Exec(ctx,
		`UPDATE knowledge_entities SET review_status = 'approved' WHERE id = $1::uuid`, entityID)
	require.NoError(t, err)
	_, err = connection.Exec(ctx,
		`UPDATE knowledge_entities SET description_short = 'rewrite reviewed row' WHERE id = $1::uuid`, entityID)
	requireInsufficientPrivilege(t, err)
	_, err = connection.Exec(ctx, `DELETE FROM knowledge_entities WHERE id = $1::uuid`, entityID)
	requireInsufficientPrivilege(t, err)
	_, err = connection.Exec(ctx, `CREATE TABLE a2_forbidden_ddl (id integer)`)
	requireInsufficientPrivilege(t, err)
	_, err = connection.Exec(ctx, `SELECT id FROM users LIMIT 1`)
	requireInsufficientPrivilege(t, err)
	_, err = connection.Exec(ctx, `SELECT id FROM auth_sessions LIMIT 1`)
	requireInsufficientPrivilege(t, err)
}

// TestLiveImporterAndCollabRoleBoundaries is the least-privilege smoke for the
// other two A-2 group roles. It exercises their intended write footprint and
// negative auth/personal/destructive boundaries as real LOGIN roles.
//
//nolint:paralleltest // creates short-lived cluster roles and fixture rows
func TestLiveImporterAndCollabRoleBoundaries(t *testing.T) {
	databaseURL := os.Getenv("SURAU_LIVE_PG")
	if databaseURL == "" {
		t.Skip("SURAU_LIVE_PG not set")
	}

	pg, err := postgres.New(databaseURL)
	require.NoError(t, err)
	t.Cleanup(pg.Close)

	categoryID := -int(time.Now().Unix()%1_000_000) - 10_000
	documentName := "a2-grant-test-" + uuid.NewString()
	loginRoles := []string{
		"surau_importer_test_" + uuid.NewString()[:8],
		"surau_collab_test_" + uuid.NewString()[:8],
	}
	groups := []string{"surau_importer", "surau_collab_store"}

	for index, role := range loginRoles {
		quoted := pgx.Identifier{role}.Sanitize()
		_, err = pg.Pool.Exec(t.Context(), fmt.Sprintf(
			"CREATE ROLE %s LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS VALID UNTIL '%s'",
			quoted, time.Now().UTC().Add(time.Hour).Format(time.RFC3339),
		))
		require.NoError(t, err)
		_, err = pg.Pool.Exec(t.Context(), fmt.Sprintf("GRANT %s TO %s", groups[index], quoted))
		require.NoError(t, err)
	}

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		_, cleanupErr := pg.Pool.Exec(cleanupCtx, `DELETE FROM collab_documents WHERE name = $1`, documentName)
		assert.NoError(t, cleanupErr)
		_, cleanupErr = pg.Pool.Exec(cleanupCtx, `DELETE FROM categories WHERE id = $1`, categoryID)
		assert.NoError(t, cleanupErr)

		for _, role := range loginRoles {
			_, cleanupErr = pg.Pool.Exec(cleanupCtx, "DROP ROLE IF EXISTS "+pgx.Identifier{role}.Sanitize())
			assert.NoError(t, cleanupErr)
		}
	})

	connection, err := pg.Pool.Acquire(t.Context())
	require.NoError(t, err)

	_, err = connection.Exec(t.Context(), "SET ROLE "+pgx.Identifier{loginRoles[0]}.Sanitize())
	require.NoError(t, err)
	_, err = connection.Exec(t.Context(),
		`INSERT INTO categories (id, name) VALUES ($1, 'A-2 importer smoke')`, categoryID)
	require.NoError(t, err)
	_, err = connection.Exec(t.Context(),
		`UPDATE categories SET name = 'A-2 importer updated' WHERE id = $1`, categoryID)
	require.NoError(t, err)

	var categoryName string
	require.NoError(t, connection.QueryRow(t.Context(),
		`SELECT name FROM categories WHERE id = $1`, categoryID).Scan(&categoryName))
	assert.Equal(t, "A-2 importer updated", categoryName)
	_, err = connection.Exec(t.Context(), `DELETE FROM categories WHERE id = $1`, categoryID)
	requireInsufficientPrivilege(t, err)
	_, err = connection.Exec(t.Context(), `SELECT id FROM users LIMIT 1`)
	requireInsufficientPrivilege(t, err)
	_, err = connection.Exec(t.Context(), `SELECT user_id FROM reading_progress LIMIT 1`)
	requireInsufficientPrivilege(t, err)

	_, err = connection.Exec(t.Context(), "RESET ROLE")
	require.NoError(t, err)
	_, err = connection.Exec(t.Context(), "SET ROLE "+pgx.Identifier{loginRoles[1]}.Sanitize())
	require.NoError(t, err)
	_, err = connection.Exec(t.Context(), `
INSERT INTO collab_documents (name, state, updated_at)
VALUES ($1, $2, now())
ON CONFLICT (name) DO UPDATE SET state = EXCLUDED.state, updated_at = now()`, documentName, []byte{1, 2, 3})
	require.NoError(t, err)
	_, err = connection.Exec(t.Context(),
		`UPDATE collab_documents SET state = $2, updated_at = now() WHERE name = $1`, documentName, []byte{4, 5})
	require.NoError(t, err)

	var state []byte
	require.NoError(t, connection.QueryRow(t.Context(),
		`SELECT state FROM collab_documents WHERE name = $1`, documentName).Scan(&state))
	assert.Equal(t, []byte{4, 5}, state)
	_, err = connection.Exec(t.Context(), `SELECT book_id FROM book_pages LIMIT 1`)
	requireInsufficientPrivilege(t, err)
	_, err = connection.Exec(t.Context(), `SELECT id FROM users LIMIT 1`)
	requireInsufficientPrivilege(t, err)
	_, err = connection.Exec(t.Context(), `DELETE FROM collab_documents WHERE name = $1`, documentName)
	requireInsufficientPrivilege(t, err)
	_, err = connection.Exec(t.Context(), "RESET ROLE")
	require.NoError(t, err)
	connection.Release()

	assertNoBroadRoleGrants(t, pg, "surau_importer")
	assertNoBroadRoleGrants(t, pg, "surau_collab_store")
}

func requireInsufficientPrivilege(t *testing.T, err error) {
	t.Helper()
	require.Error(t, err)

	var pgErr *pgconn.PgError
	require.True(t, errors.As(err, &pgErr), "expected PostgreSQL error, got %T: %v", err, err)
	assert.Equal(t, "42501", pgErr.Code)
}

func assertNoBroadRoleGrants(t *testing.T, pg *postgres.Postgres, role string) {
	t.Helper()
	rows, err := pg.Pool.Query(t.Context(), `
SELECT table_name, privilege_type
FROM information_schema.role_table_grants
WHERE grantee = $1`, role)
	require.NoError(t, err)

	defer rows.Close()

	for rows.Next() {
		var table, privilege string
		require.NoError(t, rows.Scan(&table, &privilege))
		assert.NotContains(t, []string{"DELETE", "TRUNCATE", "REFERENCES", "TRIGGER"}, privilege)
		assert.False(
			t,
			table == "users" || strings.HasPrefix(table, "auth_") ||
				table == "reading_progress" || table == "bookmarks" || table == "saved_items",
			"%s must not receive %s on %s", role, privilege, table,
		)
	}

	require.NoError(t, rows.Err())
}

func assertExtractionGrantGolden(t *testing.T, pg *postgres.Postgres) {
	t.Helper()

	rows, err := pg.Pool.Query(t.Context(), `
SELECT table_name || ':' || privilege_type
FROM information_schema.role_table_grants
WHERE grantee = 'surau_extraction_writer'
ORDER BY table_name, privilege_type`)
	require.NoError(t, err)

	var tableGrants []string

	for rows.Next() {
		var grant string
		require.NoError(t, rows.Scan(&grant))
		tableGrants = append(tableGrants, grant)
	}

	rows.Close()
	require.NoError(t, rows.Err())
	assert.Equal(t, extractionTableGrantGolden, tableGrants)

	rows, err = pg.Pool.Query(t.Context(), `
SELECT cp.table_name, cp.privilege_type,
       string_agg(cp.column_name, ',' ORDER BY columns.ordinal_position)
FROM information_schema.column_privileges cp
JOIN information_schema.columns columns
  USING (table_catalog, table_schema, table_name, column_name)
WHERE cp.grantee = 'surau_extraction_writer'
  AND cp.privilege_type IN ('INSERT', 'UPDATE')
  AND cp.table_name = ANY($1::text[])
GROUP BY cp.table_name, cp.privilege_type
ORDER BY cp.table_name, cp.privilege_type`, extractionPendingTables)
	require.NoError(t, err)

	var columnGrants []string

	for rows.Next() {
		var table, privilege, columns string
		require.NoError(t, rows.Scan(&table, &privilege, &columns))
		columnGrants = append(columnGrants, table+":"+privilege+":"+columns)
	}

	rows.Close()
	require.NoError(t, rows.Err())
	assert.Equal(t, extractionColumnGrantGolden, columnGrants)

	for _, grant := range columnGrants {
		assert.NotContains(t, grant, "review_status")
		assert.NotContains(t, grant, "decision_status")

		if strings.HasPrefix(grant, "knowledge_claims:") {
			assert.NotContains(t, grant, ":status")
		}
	}
}

var extractionPendingTables = []string{ //nolint:gochecknoglobals // ACL golden input
	"knowledge_claims", "knowledge_entities", "knowledge_entity_aliases",
	"knowledge_entity_candidates", "knowledge_entity_labels", "knowledge_entity_links",
	"knowledge_entity_taxonomy_links", "knowledge_extraction_rejections",
	"knowledge_mentions", "knowledge_relations",
}

var extractionTableGrantGolden = []string{ //nolint:gochecknoglobals // exact migration ACL
	"book_heading_ranges:SELECT", "book_headings:SELECT", "book_pages:SELECT", "citable_units:SELECT",
	"generation_runs:INSERT", "generation_runs:SELECT", "knowledge_claims:SELECT",
	"knowledge_entities:SELECT", "knowledge_entity_aliases:SELECT", "knowledge_entity_candidates:SELECT",
	"knowledge_entity_labels:SELECT", "knowledge_entity_links:SELECT", "knowledge_entity_taxonomy_links:SELECT",
	"knowledge_extraction_chunks:INSERT", "knowledge_extraction_chunks:SELECT", "knowledge_extraction_chunks:UPDATE",
	"knowledge_extraction_documents:INSERT", "knowledge_extraction_documents:SELECT", "knowledge_extraction_documents:UPDATE",
	"knowledge_extraction_rejections:SELECT", "knowledge_extraction_runs:INSERT", "knowledge_extraction_runs:SELECT",
	"knowledge_extraction_runs:UPDATE", "knowledge_mentions:SELECT", "knowledge_prompt_versions:INSERT",
	"knowledge_prompt_versions:SELECT", "knowledge_prompt_versions:UPDATE", "knowledge_relations:SELECT",
	"knowledge_source_spans:INSERT", "knowledge_source_spans:SELECT", "knowledge_source_spans:UPDATE",
	"knowledge_taxonomies:SELECT",
}

var extractionColumnGrantGolden = []string{ //nolint:gochecknoglobals // exact content-column ACL
	"knowledge_claims:INSERT:id,run_id,subject_entity_id,claim_type,claim_text_ar,claim_text_id,evidence_mention_id,evidence_quote,attributes,created_at,source_span_id,subject_text,object_text,predicate,risk_level,certainty,requires_scholar_review",
	"knowledge_claims:UPDATE:subject_entity_id,claim_type,claim_text_ar,claim_text_id,evidence_mention_id,evidence_quote,attributes,source_span_id,subject_text,object_text,predicate,risk_level,certainty,requires_scholar_review",
	"knowledge_entities:INSERT:id,entity_type,canonical_name_ar,canonical_name_latin,normalized_name_ar,description_short,authority_refs,created_from_mention_id,created_at,updated_at,normalization_version",
	"knowledge_entities:UPDATE:canonical_name_ar,canonical_name_latin,normalized_name_ar,description_short,authority_refs,created_from_mention_id,updated_at,normalization_version",
	"knowledge_entity_aliases:INSERT:id,entity_id,alias_text,normalized_alias,language,alias_type,source_mention_id,created_at,normalization_version",
	"knowledge_entity_aliases:UPDATE:alias_text,normalized_alias,source_mention_id,normalization_version",
	"knowledge_entity_candidates:INSERT:mention_id,entity_id,score,strategy,reasons,created_at",
	"knowledge_entity_candidates:UPDATE:score,reasons",
	"knowledge_entity_labels:INSERT:id,entity_id,lang,label,label_kind,source,created_at",
	"knowledge_entity_labels:UPDATE:label,source",
	"knowledge_entity_links:INSERT:id,source_entity_id,target_entity_id,link_type,score,source,reason,reviewer_notes,created_at",
	"knowledge_entity_links:UPDATE:score,source,reason",
	"knowledge_entity_taxonomy_links:INSERT:entity_id,taxonomy_id,source_mention_id,created_at",
	"knowledge_entity_taxonomy_links:UPDATE:source_mention_id",
	"knowledge_extraction_rejections:INSERT:id,run_id,chunk_id,book_id,page_id,heading_id,document_id,extraction_class,extraction_text,exact_quote,char_start,char_end,alignment_status,code,message,attributes,source_hash,raw_output_path,created_at",
	"knowledge_mentions:INSERT:id,run_id,book_id,page_id,heading_id,document_id,extraction_class,extraction_text,exact_quote,char_start,char_end,alignment_status,attributes,normalized_text,grounded,confidence,source_hash,created_at,source_span_id,token_start,token_end,extraction_index,group_index,pass_index,normalization_version,unit_id,unit_char_start,unit_char_end,unit_binding_status,unit_binding_version,unit_source_hash",
	"knowledge_mentions:UPDATE:extraction_text,exact_quote,alignment_status,attributes,normalized_text,grounded,confidence,source_hash,source_span_id,token_start,token_end,extraction_index,group_index,pass_index,normalization_version,unit_id,unit_char_start,unit_char_end,unit_binding_status,unit_binding_version,unit_source_hash",
	"knowledge_relations:INSERT:id,run_id,subject_entity_id,predicate,object_entity_id,object_literal,evidence_mention_id,evidence_quote,certainty,attributes,created_at,source_span_id,subject_text,object_text,risk_level,requires_scholar_review",
	"knowledge_relations:UPDATE:subject_entity_id,predicate,object_entity_id,object_literal,evidence_mention_id,evidence_quote,certainty,attributes,source_span_id,subject_text,object_text,risk_level,requires_scholar_review",
}
