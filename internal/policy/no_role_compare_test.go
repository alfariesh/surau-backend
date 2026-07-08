package policy_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A-1 AC: no access decision compares a role string outside the policy module.
// This test walks the access-decision layers (controllers + usecases) and
// fails if any file compares against a role VALUE — a string literal that is a
// role name, or an entity.UserRole* selector — via ==, !=, switch-case, or
// strings.EqualFold. Role logic must go through policy.Can / policy.Role*.
//
// Scope note: entity (role validation + constants), repo/persistent (the
// last-admin lifecycle guard), and cmd (ops CLI) legitimately reference role
// constants and are NOT access-decision layers, so they are out of scope. The
// policy package itself is out of scope (it OWNS the mapping). Empty-string
// role checks (role == "") are ignored because "" is not a role value.
func TestNoRoleComparisonOutsidePolicy(t *testing.T) {
	t.Parallel()

	roots := []string{"../controller", "../usecase"}

	var (
		violations []string
		fileCount  int
	)

	for _, root := range roots {
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}

			fileCount++

			fset := token.NewFileSet()

			file, parseErr := parser.ParseFile(fset, path, nil, 0)
			if parseErr != nil {
				return parseErr
			}

			ast.Inspect(file, func(n ast.Node) bool {
				if msg := roleComparisonViolation(n); msg != "" {
					pos := fset.Position(n.Pos())
					violations = append(violations, fmt.Sprintf("%s:%d: %s", path, pos.Line, msg))
				}

				return true
			})

			return nil
		})
		require.NoError(t, err, "walking %s", root)
	}

	// Floor guard: a broken walker (wrong CWD, bad root) must not vacuously
	// pass. The controller+usecase trees are well over 100 .go files.
	require.GreaterOrEqual(t, fileCount, 50,
		"walked only %d files — the AST scan is not covering the access-decision layers", fileCount)

	assert.Empty(t, violations,
		"role-value comparison found outside internal/policy — route it through policy.Can/policy.Role*:\n%s",
		strings.Join(violations, "\n"))
}

// roleComparisonViolation returns a description if the node compares against a
// role value, else "".
func roleComparisonViolation(n ast.Node) string {
	switch node := n.(type) {
	case *ast.BinaryExpr:
		if node.Op != token.EQL && node.Op != token.NEQ {
			return ""
		}

		if isRoleExpr(node.X) || isRoleExpr(node.Y) {
			return "role-value comparison via " + node.Op.String()
		}
	case *ast.CaseClause:
		for _, expr := range node.List {
			if isRoleExpr(expr) {
				return "role-value switch/case"
			}
		}
	case *ast.CallExpr:
		if calleeName(node) != "EqualFold" {
			return ""
		}

		for _, arg := range node.Args {
			if isRoleExpr(arg) {
				return "role-value strings.EqualFold"
			}
		}
	}

	return ""
}

// roleValues are the account-role strings; a literal equal to one of these is
// a role comparison (but "" and arbitrary strings are not).
var roleValues = map[string]bool{
	"user":             true,
	"editor":           true,
	"curator":          true,
	"scholar_reviewer": true,
	"admin":            true,
}

// isRoleExpr reports whether expr denotes a role value: a matching string
// literal, or an entity.UserRole* selector / UserRole* identifier.
func isRoleExpr(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return false
		}

		s, err := strconv.Unquote(e.Value)

		return err == nil && roleValues[strings.ToLower(s)]
	case *ast.SelectorExpr:
		return strings.HasPrefix(e.Sel.Name, "UserRole")
	case *ast.Ident:
		return strings.HasPrefix(e.Name, "UserRole")
	}

	return false
}

func calleeName(call *ast.CallExpr) string {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return fun.Name
	case *ast.SelectorExpr:
		return fun.Sel.Name
	}

	return ""
}
