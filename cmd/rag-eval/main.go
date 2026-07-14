package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/alfariesh/surau-backend/internal/rageval"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	os.Exit(run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	var opts rageval.Options

	flags := flag.NewFlagSet("rag-eval", flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.StringVar(&opts.BaseURL, "base-url", envOrDefault("RAG_EVAL_BASE_URL", rageval.DefaultBaseURL), "BookRAG API base URL")
	flags.StringVar(&opts.CasesPath, "cases", rageval.DefaultCasesPath, "JSONL golden cases file")
	flags.StringVar(&opts.Output, "output", "table", "output format: table or json")
	flags.DurationVar(&opts.Timeout, "timeout", rageval.DefaultTimeout, "HTTP timeout per case")
	flags.BoolVar(&opts.FailFast, "fail-fast", false, "stop after first failed case")
	flags.IntVar(&opts.Limit, "limit", 0, "limit number of cases")
	flags.IntVar(&opts.Retries, "retries", 1, "retry failed cases")
	flags.BoolVar(&opts.StrictAnswer, "strict-answer", false, "treat answer_must_contain misses as failures")
	flags.StringVar(&opts.ExpectedCitationMode, "expected-citation-mode", "", "require this citation_mode in every response trace")
	flags.BoolVar(&opts.ForbidLegacyFallback, "forbid-legacy-fallback", false, "fail when any response trace reports legacy fallback")
	verbose := flags.Bool("verbose", false, "print per-case progress to stderr")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if *verbose {
		opts.ProgressWriter = stderr
	}

	start := time.Now()
	summary, err := rageval.Run(ctx, opts)
	if err != nil {
		fmt.Fprintf(stderr, "rag-eval: %v\n", err)
		return 1
	}
	if err = rageval.WriteSummary(stdout, summary, opts.Output); err != nil {
		fmt.Fprintf(stderr, "rag-eval: %v\n", err)
		return 1
	}

	if summary.Failed > 0 {
		fmt.Fprintf(stderr, "rag-eval: %d/%d failed in %s\n", summary.Failed, summary.Total, time.Since(start).Round(time.Millisecond))
		return 1
	}

	return 0
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}

	return fallback
}
