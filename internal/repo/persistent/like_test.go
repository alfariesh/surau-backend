package persistent

import (
	"strings"
	"testing"
)

func TestEscapeLike(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"":           "",
		"salam":      "salam",
		"100%":       `100\%`,
		"a_b":        `a\_b`,
		`back\slash`: `back\\slash`,
		"%_":         `\%\_`,
		`%%\__`:      `\%\%\\\_\_`,
		"عرب%ي":      `عرب\%ي`,
		"no-op چیزی": "no-op چیزی",
	}

	for input, want := range cases {
		if got := escapeLike(input); got != want {
			t.Errorf("escapeLike(%q) = %q, want %q", input, got, want)
		}
	}
}

// FuzzEscapeLike pins the safety property: the escaped output never contains
// an unescaped LIKE metacharacter, so it always matches literally.
func FuzzEscapeLike(f *testing.F) {
	for _, seed := range []string{"", "%", "_", `\`, `%%\__`, "kitab 100%_", `\%`} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		escaped := escapeLike(input)

		// Strip every escaped pair; nothing pattern-significant may remain.
		stripped := strings.NewReplacer(`\\`, "", `\%`, "", `\_`, "").Replace(escaped)
		if strings.ContainsAny(stripped, `%_\`) {
			t.Errorf("escapeLike(%q) = %q leaves unescaped metacharacter (residue %q)", input, escaped, stripped)
		}
	})
}
