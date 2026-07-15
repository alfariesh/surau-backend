package main

import (
	"context"
	"flag"
	"fmt"
	"log"

	"github.com/alfariesh/surau-backend/internal/dbcredential"
	"github.com/alfariesh/surau-backend/internal/importer"
)

type repeatedStringFlag []string

func (f *repeatedStringFlag) String() string {
	return fmt.Sprint([]string(*f))
}

func (f *repeatedStringFlag) Set(value string) error {
	*f = append(*f, value)
	return nil
}

func main() {
	var opts importer.QuranSurahEditorialOptions
	var paths repeatedStringFlag

	flag.StringVar(&opts.PostgresURL, "pg-url", dbcredential.ImporterURL(), "PostgreSQL URL (defaults to IMPORTER_PG_URL)")
	flag.Var(&paths, "editorial-json", "surah editorial JSON file; repeat for multiple files")
	flag.BoolVar(&opts.DryRun, "dry-run", false, "parse files and print counts without writing")
	flag.BoolVar(&opts.Publish, "publish", false, "explicitly publish all imported drafts (requires permitted licenses)")
	flag.Parse()
	opts.Paths = []string(paths)

	stats, err := importer.RunQuranSurahEditorialImport(context.Background(), opts)
	if err != nil {
		log.Fatalf("quran surah editorial import failed: %v", err)
	}

	fmt.Printf(
		"files=%d surah_rows=%d editorial_rows=%d changed=%d published=%d dry_run=%t publish=%t\n",
		stats.Files,
		stats.SurahRows,
		stats.EditorialRows,
		stats.Changed,
		stats.Published,
		stats.DryRun,
		stats.Publish,
	)
}
