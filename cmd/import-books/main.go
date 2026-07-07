package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/alfariesh/surau-backend/internal/importer"
)

func main() {
	var opts importer.Options
	var bookIDs string

	flag.StringVar(&opts.SourceDir, "source-dir", "/Users/macmini/Downloads/database", "raw database root directory")
	flag.StringVar(&opts.PostgresURL, "pg-url", os.Getenv("PG_URL"), "PostgreSQL URL")
	flag.StringVar(&opts.ReleaseKey, "release-key", "", "source release key; defaults to current UTC timestamp")
	flag.StringVar(&bookIDs, "book-ids", "", "comma-separated book IDs for sample import")
	flag.IntVar(&opts.Limit, "limit", 0, "limit imported content books for sample import")
	flag.BoolVar(&opts.SkipDiskCheck, "skip-disk-check", false, "skip full import disk preflight")
	flag.Uint64Var(&opts.MinFreeGB, "min-free-gb", 30, "minimum free GiB required for full import")
	flag.StringVar(&opts.ApproveRemovalsRun, "approve-removals", "",
		"apply the removals staged by the given run id as soft tombstones; default (empty) only stages removals for review")
	flag.Parse()

	ids, err := parseBookIDs(bookIDs)
	if err != nil {
		log.Fatalf("invalid --book-ids: %v", err)
	}
	opts.BookIDs = ids

	stats, err := importer.Run(context.Background(), opts)
	if err != nil {
		log.Fatalf("import failed: %v", err)
	}

	fmt.Printf(
		"run_id=%s release=%s books=%d pages=%d headings=%d skipped=%d checksum=%s\n",
		stats.RunID,
		stats.ReleaseKey,
		stats.ImportedBooks,
		stats.ImportedPages,
		stats.ImportedHeadings,
		stats.SkippedFiles,
		stats.MasterChecksum,
	)

	if stats.StagedRemovalPages > 0 || stats.StagedRemovalHeadings > 0 {
		fmt.Printf(
			"STAGED removals: pages=%d headings=%d — NOTHING was deleted or hidden.\nReview them (book_import_removal_stages, run_id=%s), then re-run with -approve-removals=%s to apply soft tombstones.\n",
			stats.StagedRemovalPages,
			stats.StagedRemovalHeadings,
			stats.RunID,
			stats.RunID,
		)
	}

	if stats.TombstonedPages > 0 || stats.TombstonedHeadings > 0 {
		fmt.Printf(
			"tombstoned: pages=%d headings=%d (soft — reversible by a source that restores them; approved stage run %s)\n",
			stats.TombstonedPages,
			stats.TombstonedHeadings,
			stats.ApprovedStageRun,
		)
	}

	for _, errMsg := range stats.Errors {
		fmt.Printf("warning=%s\n", errMsg)
	}
}

func parseBookIDs(value string) ([]int, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}

	parts := strings.Split(value, ",")
	ids := make([]int, 0, len(parts))
	for _, part := range parts {
		id, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil {
			return nil, err
		}

		if id <= 0 {
			return nil, fmt.Errorf("book id must be positive: %d", id)
		}

		ids = append(ids, id)
	}

	return ids, nil
}
