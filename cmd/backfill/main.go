// Command backfill runs one registered resumable backfill job (F1-H).
//
// Usage:
//
//	backfill -list
//	backfill -job=authors-name-search [-chunk-size=100] [-sleep=200ms] [-restart]
//
// Ctrl-C / SIGTERM pauses gracefully after the current chunk; rerunning the
// same command resumes from the stored cursor. Only one instance per job can
// run (Postgres advisory lock). Operational guide: docs/data-change-playbook.md.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/alfariesh/surau-backend/internal/backfill"
	"github.com/alfariesh/surau-backend/pkg/logger"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	var (
		jobName    = flag.String("job", "", "backfill job name (see -list)")
		chunkSize  = flag.Int("chunk-size", backfill.DefaultChunkSize, "rows per chunk")
		sleep      = flag.Duration("sleep", backfill.DefaultSleep, "throttle between chunks")
		restart    = flag.Bool("restart", false, "restart a completed job from row zero")
		list       = flag.Bool("list", false, "list registered jobs and exit")
		verifyK1   = flag.Bool("verify-citable-catalog", false, "print K-1 catalog acceptance evidence as JSON")
		priorityK1 = flag.Bool("catalog-priority-only", false,
			"limit the K-1 catalog job to O-4-2 categories 3 and 7")
		pgURL = flag.String("pg-url", os.Getenv("PG_URL"), "PostgreSQL connection URL")
	)

	flag.Parse()

	if *list {
		for _, job := range backfill.Jobs() {
			fmt.Printf("%s (profile_version=%d)\n", job.Name(), job.ProfileVersion())
		}

		return
	}

	if *jobName == "" && !*verifyK1 {
		fatalf("-job is required (or -list to see registered jobs)")
	}

	if *pgURL == "" {
		fatalf("-pg-url is required or PG_URL must be set")
	}

	if *verifyK1 {
		if !verifyCitableCatalog(*pgURL) {
			os.Exit(1)
		}

		return
	}

	job, err := backfill.ByName(*jobName)
	if err != nil {
		fatalf("%v", err)
	}

	if *priorityK1 && *jobName != "citable-units-kitab-catalog" {
		fatalf("-catalog-priority-only is valid only with -job=citable-units-kitab-catalog")
	}

	backfill.CitableCatalogPriorityOnly = *priorityK1

	run(job, *pgURL, *chunkSize, *sleep, *restart)
}

func verifyCitableCatalog(pgURL string) bool {
	ctx := context.Background()

	pool, err := pgxpool.New(ctx, pgURL)
	if err != nil {
		fatalf("connect postgres: %v", err)
	}
	defer pool.Close()

	report, err := backfill.VerifyCitableCatalog(ctx, pool)
	if err != nil {
		fatalf("verify citable catalog: %v", err)
	}

	if err := json.NewEncoder(os.Stdout).Encode(report); err != nil {
		fatalf("encode citable catalog report: %v", err)
	}

	return report.Passed
}

func run(job backfill.Job, pgURL string, chunkSize int, sleep time.Duration, restart bool) {
	// SIGINT/SIGTERM cancel the context; the runner finishes the current
	// chunk, persists the checkpoint, and marks the job paused.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, pgURL)
	if err != nil {
		fatalf("connect postgres: %v", err)
	}
	defer pool.Close()

	runner := &backfill.Runner{
		Pool:      pool,
		Log:       logger.New("info"),
		ChunkSize: chunkSize,
		Sleep:     sleep,
		AfterChunk: func(state backfill.State) {
			fmt.Printf(
				"%s: %s rows_done=%d/%d cursor=%d\n",
				state.JobName, state.Status, state.RowsDone, state.RowsTotal, state.LastCursor,
			)
		},
	}

	switch err := runner.Run(ctx, job, restart); {
	case err == nil:
	case errors.Is(err, backfill.ErrJobCompleted):
		fmt.Println(err.Error())
	default:
		fatalf("%v", err)
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
