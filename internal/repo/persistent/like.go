package persistent

import "strings"

// likeEscaper neutralizes LIKE/ILIKE metacharacters in user-supplied search
// text. All search values are bound parameters (never SQL-injected), but an
// unescaped `%`/`_` changes the PATTERN semantics: `%` alone matches every
// row (full scan on a public endpoint) and crafted patterns defeat the
// trigram index. Backslash is PostgreSQL's default LIKE escape character, so
// no explicit ESCAPE clause is needed.
//
//nolint:gochecknoglobals // immutable replacer, built once instead of per query
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// escapeLike returns the text with LIKE metacharacters escaped so it matches
// literally inside a `%...%` pattern.
func escapeLike(text string) string {
	return likeEscaper.Replace(text)
}
