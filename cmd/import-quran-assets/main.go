package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

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
	var opts importer.QuranAssetOptions
	var surahInfoPaths repeatedStringFlag
	var recitationPaths repeatedStringFlag
	var transliterationPaths repeatedStringFlag

	flag.StringVar(&opts.PostgresURL, "pg-url", os.Getenv("PG_URL"), "PostgreSQL URL")
	flag.StringVar(&opts.SurahNamesPath, "surah-names-json", "", "QUL surah names JSON export")
	flag.Var(&surahInfoPaths, "surah-info-json", "optional QUL surah-info JSON export; repeat for multiple languages")
	flag.StringVar(&opts.ScriptQPCHafsPath, "script-qpc-hafs-json", "", "QUL QPC Hafs script JSON export")
	flag.StringVar(&opts.ScriptImlaeiSimplePath, "script-imlaei-simple-json", "", "QUL Imlaei/simple script JSON export")
	flag.StringVar(&opts.TranslationSimplePath, "translation-simple-json", "", "translation JSON export; supports ayah-key maps and normalized row arrays")
	flag.StringVar(&opts.TranslationFootnoteTagsPath, "translation-footnote-tags-json", "", "optional translation row JSON with structured footnotes")
	flag.Var(&recitationPaths, "recitation-json", "optional QUL recitation/timestamp JSON export; repeat for multiple reciters")
	flag.StringVar(&opts.TranslationLang, "translation-lang", "", "translation language: ar, id, or en (default id)")
	flag.StringVar(&opts.SurahInfoLang, "surah-info-lang", "", "optional surah info language override: ar, id, or en")
	flag.StringVar(&opts.TranslationSourceID, "translation-source-id", "", "translation source id")
	flag.StringVar(&opts.TranslationSourceName, "translation-source-name", "", "translation source display name")
	flag.StringVar(&opts.TranslationSourceURL, "translation-source-url", "", "translation source URL")
	flag.StringVar(&opts.TranslationResourceID, "translation-resource-id", "", "QUL translation resource id")
	flag.StringVar(&opts.TranslationFormat, "translation-format", "", "translation simple export format")
	flag.StringVar(&opts.TranslationFootnoteFormat, "translation-footnote-format", "", "translation footnote export format")
	flag.StringVar(&opts.LicenseStatus, "license-status", "", "license review status")
	flag.Var(&transliterationPaths, "transliteration-json", "optional transliteration JSON in lang=path form; repeat for multiple languages")
	flag.BoolVar(&opts.DryRun, "dry-run", false, "parse files and print counts without writing")
	flag.BoolVar(&opts.ResolveReferences, "resolve-references", false, "resolve existing knowledge_mentions with extraction_class=quran_reference")
	flag.Parse()
	opts.SurahInfoPaths = []string(surahInfoPaths)
	opts.RecitationPaths = []string(recitationPaths)
	opts.TransliterationPaths = parseTransliterationFlags(transliterationPaths)

	stats, err := importer.RunQuranAssetImport(context.Background(), opts)
	if err != nil {
		log.Fatalf("quran asset import failed: %v", err)
	}

	fmt.Printf(
		"surahs=%d surah_infos=%d ayahs=%d translations=%d transliterations=%d recitations=%d audio_tracks=%d audio_segments=%d book_references=%d skipped_references=%d dry_run=%t\n",
		stats.Surahs,
		stats.SurahInfos,
		stats.Ayahs,
		stats.Translations,
		stats.Transliterations,
		stats.Recitations,
		stats.AudioTracks,
		stats.AudioSegments,
		stats.BookReferences,
		stats.SkippedReferences,
		stats.DryRun,
	)
}

func parseTransliterationFlags(values []string) []importer.QuranTransliterationPath {
	specs := make([]importer.QuranTransliterationPath, 0, len(values))
	for _, value := range values {
		lang, path, ok := splitLangPath(value)
		if !ok {
			log.Fatalf("invalid -transliteration-json %q, expected lang=path", value)
		}
		specs = append(specs, importer.QuranTransliterationPath{Lang: lang, Path: path})
	}

	return specs
}

func splitLangPath(value string) (string, string, bool) {
	value = strings.TrimSpace(value)
	for i, char := range value {
		if char == '=' {
			lang := strings.TrimSpace(value[:i])
			path := strings.TrimSpace(value[i+1:])
			return lang, path, lang != "" && path != ""
		}
	}

	return "", "", false
}
