package main

import (
	"context"
	"flag"
	"fmt"
	"log"

	"github.com/alfariesh/surau-backend/internal/dbcredential"
	"github.com/alfariesh/surau-backend/internal/importer"
)

func main() {
	var opts importer.AssetOptions

	flag.StringVar(&opts.PostgresURL, "pg-url", dbcredential.ImporterURL(), "PostgreSQL URL (defaults to IMPORTER_PG_URL)")
	flag.StringVar(&opts.Path, "file", "", "translation/audio JSONL file")
	flag.Parse()

	stats, err := importer.RunAssetImport(context.Background(), opts)
	if err != nil {
		log.Fatalf("asset import failed: %v", err)
	}

	fmt.Printf(
		"translations=%d summaries=%d audio=%d book_metadata=%d authors=%d categories=%d skipped=%d\n",
		stats.Translations,
		stats.Summaries,
		stats.Audio,
		stats.BookMetadataTranslations,
		stats.AuthorTranslations,
		stats.CategoryTranslations,
		stats.Skipped,
	)
}
