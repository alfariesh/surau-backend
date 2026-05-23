package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"github.com/evrone/go-clean-template/internal/importer"
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
	flag.IntVar(&opts.MinFreeGB, "min-free-gb", 30, "minimum free GiB required for full import")
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

	fmt.Printf("run_id=%s release=%s books=%d pages=%d headings=%d skipped=%d checksum=%s\n",
		stats.RunID,
		stats.ReleaseKey,
		stats.ImportedBooks,
		stats.ImportedPages,
		stats.ImportedHeadings,
		stats.SkippedFiles,
		stats.MasterChecksum,
	)

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
