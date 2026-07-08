package diffcover

import (
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
)

var generatedHeader = regexp.MustCompile(`(?m)^// Code generated .* DO NOT EDIT\.$`)

// isGeneratedSource reports whether the file carries the standard generated
// marker (checked against the head so a quoted marker deep in a real file
// cannot exempt it).
func isGeneratedSource(src []byte) bool {
	const headLimit = 2048
	if len(src) > headLimit {
		src = src[:headLimit]
	}

	return generatedHeader.Match(src)
}

// stmtStartLines parses a Go source file and returns the set of lines that
// begin a statement. Files absent from every profile (a package no test
// binary links) still need a statement denominator — otherwise brand-new
// untested code would slip through the gate — and this mirrors closely enough
// what `go test -cover` would instrument: function bodies, not imports, type
// declarations, or comments.
func stmtStartLines(path string, src []byte) (map[int]bool, error) {
	fset := token.NewFileSet()

	file, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}

	lines := map[int]bool{}

	ast.Inspect(file, func(node ast.Node) bool {
		stmt, ok := node.(ast.Stmt)
		if !ok {
			return true
		}

		// Braces carry no statements of their own; counting them would charge
		// the diff for pure structure.
		if _, isBlock := stmt.(*ast.BlockStmt); isBlock {
			return true
		}

		lines[fset.Position(stmt.Pos()).Line] = true

		return true
	})

	return lines, nil
}
