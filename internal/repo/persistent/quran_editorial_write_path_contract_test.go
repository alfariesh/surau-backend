package persistent

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestQuranEditorialRuntimeWritesUseWorkflow is the source-level half of Q-1's
// single-write-path proof. Runtime SQL (including TRUNCATE and protected
// quran_surahs inserts) may mutate Quran editorial state only in the persistent
// workflow implementation named below. The transaction marker itself is also
// allowlisted to that file, so another package cannot imitate the writer.
//
// Migration SQL is excluded because it owns schema expansion/grandfathering,
// while *_test.go is excluded because self-contained fixtures and negative DB
// tests intentionally issue raw DML. Neither exclusion is compiled into the
// production binary. Importers are runtime code and are deliberately NOT
// excluded: they must call the workflow instead of carrying private SQL.
func TestQuranEditorialRuntimeWritesUseWorkflow(t *testing.T) {
	t.Parallel()

	const allowedWriter = "internal/repo/persistent/quran_editorial_postgres.go"

	quranEditorialDMLPattern := regexp.MustCompile(
		`(?is)\b(?:INSERT\s+INTO|UPDATE|DELETE\s+FROM|TRUNCATE(?:\s+TABLE)?)\s+` +
			`(?:(?:"?public"?)\s*\.\s*)?"?` +
			`(?:quran_surah_editorial|quran_ayah_editorial|quran_editorial_revisions)\b"?`,
	)
	quranSurahEditorialFieldsDMLPattern := regexp.MustCompile(
		`(?is)\bUPDATE\s+(?:(?:"?public"?)\s*\.\s*)?"?quran_surahs\b"?\s+SET\b[^;]*` +
			`\b(?:slug|chronological_order|ruku_count)\s*=`,
	)
	quranSurahEditorialFieldsInsertPattern := regexp.MustCompile(
		`(?is)\bINSERT\s+INTO\s+(?:(?:"?public"?)\s*\.\s*)?"?quran_surahs\b"?\s*\([^)]*` +
			`\b(?:slug|chronological_order|ruku_count)\b`,
	)
	quranEditorialParentTruncatePattern := regexp.MustCompile(
		`(?is)\bTRUNCATE(?:\s+TABLE)?\s+(?:ONLY\s+)?` +
			`(?:(?:"?public"?)\s*\.\s*)?"?(?:quran_surahs|quran_ayahs)\b"?`,
	)
	quranEditorialParentDeletePattern := regexp.MustCompile(
		`(?is)\bDELETE\s+FROM\s+(?:(?:"?public"?)\s*\.\s*)?"?` +
			`(?:quran_surahs|quran_ayahs)\b"?`,
	)
	quranEditorialWriterMarkerPattern := regexp.MustCompile(
		`surau\.quran_editorial_writer|quran-editorial-service`,
	)

	repoRoot := filepath.Clean("../../..")
	fset := token.NewFileSet()
	violations := make([]string, 0)

	err := filepath.WalkDir(repoRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			return fmt.Errorf("relative path for %s: %w", path, err)
		}

		rel = filepath.ToSlash(rel)
		if entry.IsDir() {
			switch rel {
			case ".git", "migrations", "tmp", "vendor":
				return filepath.SkipDir
			default:
				return nil
			}
		}

		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") || rel == allowedWriter {
			return nil
		}

		parsed, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return fmt.Errorf("parse %s: %w", rel, err)
		}

		ast.Inspect(parsed, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}

			value, err := strconv.Unquote(literal.Value)
			if err != nil {
				return true
			}

			if !quranEditorialDMLPattern.MatchString(value) &&
				!quranSurahEditorialFieldsDMLPattern.MatchString(value) &&
				!quranSurahEditorialFieldsInsertPattern.MatchString(value) &&
				!quranEditorialParentTruncatePattern.MatchString(value) &&
				!quranEditorialParentDeletePattern.MatchString(value) &&
				!quranEditorialWriterMarkerPattern.MatchString(value) {
				return true
			}

			position := fset.Position(literal.Pos())
			violations = append(violations, fmt.Sprintf("%s:%d", rel, position.Line))

			return true
		})

		return nil
	})
	require.NoError(t, err)
	require.Empty(t, violations,
		"Q-1 write-path bypass found; move SQL into %s and call the workflow: %s",
		allowedWriter, strings.Join(violations, ", "))
}
