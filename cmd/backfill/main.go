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
		jobName   = flag.String("job", "", "backfill job name (see -list)")
		chunkSize = flag.Int("chunk-size", backfill.DefaultChunkSize, "rows per chunk")
		sleep     = flag.Duration("sleep", backfill.DefaultSleep, "throttle between chunks")
		restart   = flag.Bool("restart", false, "restart a completed job from row zero")
		list      = flag.Bool("list", false, "list registered jobs and exit")
		pgURL     = flag.String("pg-url", os.Getenv("PG_URL"), "PostgreSQL connection URL")
	)

	flag.Parse()

	if *list {
		for _, job := range backfill.Jobs() {
			fmt.Printf("%s (profile_version=%d)\n", job.Name(), job.ProfileVersion())
		}

		return
	}

	if *jobName == "" {
		fatalf("-job is required (or -list to see registered jobs)")
	}

	if *pgURL == "" {
		fatalf("-pg-url is required or PG_URL must be set")
	}

	job, err := backfill.ByName(*jobName)
	if err != nil {
		fatalf("%v", err)
	}

	run(job, *pgURL, *chunkSize, *sleep, *restart)
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
