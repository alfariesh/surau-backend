package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/alfariesh/surau-backend/internal/importer"
)

func main() {
	var opts importer.QuranAudioR2SyncOptions

	flag.StringVar(&opts.PostgresURL, "pg-url", os.Getenv("PG_URL"), "PostgreSQL URL")
	flag.StringVar(&opts.ManifestPath, "manifest-jsonl", "tmp/quran-audio-r2-manifest.jsonl", "Quran audio R2 manifest JSONL")
	flag.StringVar(&opts.RecitationMetadataPath, "recitation-metadata-json", "", "optional recitation metadata JSON array/object")
	flag.StringVar(&opts.PublicBaseURL, "public-base-url", os.Getenv("QURAN_AUDIO_PUBLIC_BASE_URL"), "public R2 base URL, for example https://pub-id.r2.dev")
	flag.BoolVar(&opts.DryRun, "dry-run", false, "parse manifest and print counts without writing")
	flag.Parse()

	stats, err := importer.RunQuranAudioR2Sync(context.Background(), opts)
	if err != nil {
		log.Fatalf("quran audio r2 sync failed: %v", err)
	}

	fmt.Printf(
		"recitations=%d tracks=%d updated=%d public_urls=%d dry_run=%t\n",
		stats.Recitations,
		stats.Tracks,
		stats.Updated,
		stats.PublicURLs,
		stats.DryRun,
	)
}
