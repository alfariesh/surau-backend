package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/evrone/go-clean-template/internal/importer"
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

	flag.StringVar(&opts.PostgresURL, "pg-url", os.Getenv("PG_URL"), "PostgreSQL URL")
	flag.Var(&paths, "editorial-json", "surah editorial JSON file; repeat for multiple files")
	flag.BoolVar(&opts.DryRun, "dry-run", false, "parse files and print counts without writing")
	flag.Parse()
	opts.Paths = []string(paths)

	stats, err := importer.RunQuranSurahEditorialImport(context.Background(), opts)
	if err != nil {
		log.Fatalf("quran surah editorial import failed: %v", err)
	}

	fmt.Printf(
		"files=%d surah_rows=%d editorial_rows=%d dry_run=%t\n",
		stats.Files,
		stats.SurahRows,
		stats.EditorialRows,
		stats.DryRun,
	)
}
