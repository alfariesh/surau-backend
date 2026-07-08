package diffcover

import (
	"fmt"
	"strconv"
	"strings"
)

// RenderText is the terse job-log form of a result.
func RenderText(result Result, threshold float64) string {
	var b strings.Builder

	fmt.Fprintf(&b, "diff coverage: %.1f%% (%d/%d changed statements covered; threshold %.0f%%)\n",
		result.DiffPercent(), result.CoveredStmts, result.ChangedStmts, threshold)
	fmt.Fprintf(&b, "total coverage (profile union): %.1f%% (%d/%d statements)\n",
		result.TotalPercent(), result.TotalCovered, result.TotalStmts)

	for _, file := range result.Files {
		fmt.Fprintf(&b, "  %s: %d/%d covered", file.Path, file.CoveredStmts, file.ChangedStmts)

		if len(file.UncoveredLines) > 0 {
			fmt.Fprintf(&b, " — uncovered lines: %s", joinLineRanges(file.UncoveredLines))
		}

		if !file.InProfiles {
			b.WriteString(" (file absent from all coverage profiles)")
		}

		b.WriteString("\n")
	}

	return b.String()
}

// RenderMarkdown is the GITHUB_STEP_SUMMARY / PR-comment form of a result.
func RenderMarkdown(result Result, threshold float64) string {
	var b strings.Builder

	verdict := "✅ PASS"
	if !result.Pass(threshold) {
		verdict = "❌ FAIL"
	}

	b.WriteString("<!-- diffcover -->\n")
	b.WriteString("### Coverage gate (F1-E)\n\n")
	fmt.Fprintf(&b, "%s — new code %.1f%% covered (threshold %.0f%%), repo total %.1f%%\n\n",
		verdict, result.DiffPercent(), threshold, result.TotalPercent())
	fmt.Fprintf(&b, "Changed statements covered: **%d/%d** · Repo statements covered: %d/%d (union of unit+live profiles)\n\n",
		result.CoveredStmts, result.ChangedStmts, result.TotalCovered, result.TotalStmts)

	if len(result.Files) == 0 {
		b.WriteString("No measurable Go statements changed under `internal/`+`pkg/` — gate passes by definition.\n")

		return b.String()
	}

	b.WriteString("| File | Covered | Uncovered lines |\n|---|---|---|\n")

	for _, file := range result.Files {
		note := joinLineRanges(file.UncoveredLines)
		if note == "" {
			note = "—"
		}

		if !file.InProfiles {
			note += " (no profile: package has no linked tests)"
		}

		fmt.Fprintf(&b, "| `%s` | %d/%d | %s |\n", file.Path, file.CoveredStmts, file.ChangedStmts, note)
	}

	return b.String()
}

// joinLineRanges compresses sorted line numbers into "12-15, 20, 31-32".
func joinLineRanges(lines []int) string {
	if len(lines) == 0 {
		return ""
	}

	var parts []string

	start, prev := lines[0], lines[0]

	flush := func() {
		if start == prev {
			parts = append(parts, strconv.Itoa(start))

			return
		}

		parts = append(parts, fmt.Sprintf("%d-%d", start, prev))
	}

	for _, line := range lines[1:] {
		if line == prev+1 {
			prev = line

			continue
		}

		flush()

		start, prev = line, line
	}

	flush()

	return strings.Join(parts, ", ")
}
