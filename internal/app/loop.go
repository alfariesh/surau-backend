package app

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"runtime/debug"
	"time"

	"github.com/alfariesh/surau-backend/pkg/logger"
)

// Background-loop supervision (F1-C): every loop pass recovers from panics so
// one bad pass cannot kill the process, consecutive failures back off with
// jitter instead of hammering a broken dependency, and shutdown drains
// in-flight passes with a bounded wait (see shutdownServers).
const (
	loopBackoffBase    = 30 * time.Second
	loopBackoffMax     = 15 * time.Minute
	loopJitterFraction = 0.2
	loopDrainTimeout   = 5 * time.Second
)

// errLoopPanic marks pass errors that came from a recovered panic so the
// runs-total metric can count them separately from ordinary errors.
var errLoopPanic = errors.New("loop pass panicked")

// loopSpec describes one supervised background loop. backoffBase/backoffMax
// are test seams; zero values fall back to the package constants.
type loopSpec struct {
	name         string
	interval     time.Duration
	initialDelay time.Duration
	run          func(ctx context.Context) error

	backoffBase time.Duration
	backoffMax  time.Duration
}

// startLoop launches one supervised loop goroutine registered on the drain
// WaitGroup, with the loop name stamped on every log line.
func (s *servers) startLoop(ctx context.Context, spec loopSpec, l logger.Interface) {
	s.loopWG.Add(1)

	go func() {
		defer s.loopWG.Done()

		runSupervisedLoop(ctx, spec, l.WithField("loop", spec.name))
	}()
}

// runSupervisedLoop drives spec.run until ctx is cancelled. The first pass
// waits initialDelay (or one interval when zero, matching a plain ticker);
// each subsequent wait is the interval on success or a growing backoff on
// consecutive failures.
func runSupervisedLoop(ctx context.Context, spec loopSpec, l logger.Interface) {
	wait := spec.interval
	if spec.initialDelay > 0 {
		wait = spec.initialDelay
	}

	timer := time.NewTimer(wait)
	defer timer.Stop()

	failures := 0

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}

		err := runLoopPass(ctx, spec, l)

		recordLoopRun(spec.name, err)

		if err == nil {
			failures = 0

			timer.Reset(spec.interval)

			continue
		}

		if ctx.Err() != nil {
			// Shutdown interrupted the pass; the drain is already waiting.
			return
		}

		failures++
		next := loopBackoffDelay(failures, spec.backoffBase, spec.backoffMax)

		l.Error(fmt.Errorf("app - loop pass failed (consecutive=%d, next retry in %s): %w", failures, next.Round(time.Millisecond), err))
		timer.Reset(next)
	}
}

// runLoopPass executes one pass, converting a panic into an error so the
// supervisor loop (and the process) survives it.
func runLoopPass(ctx context.Context, spec loopSpec, l logger.Interface) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%w: %v", errLoopPanic, r)

			l.Error(fmt.Errorf("app - loop panic recovered: %v\n%s", r, debug.Stack()))
		}
	}()

	return spec.run(ctx)
}

// loopBackoffDelay returns the wait before retry number `failures`
// (1-based): base doubling per consecutive failure, capped, with ±20% jitter
// so restarted loops do not synchronize against a struggling dependency.
func loopBackoffDelay(failures int, base, maxDelay time.Duration) time.Duration {
	if base <= 0 {
		base = loopBackoffBase
	}

	if maxDelay <= 0 {
		maxDelay = loopBackoffMax
	}

	delay := base
	for i := 1; i < failures && delay < maxDelay; i++ {
		delay *= 2
	}

	if delay > maxDelay {
		delay = maxDelay
	}

	jitter := 1 - loopJitterFraction + 2*loopJitterFraction*rand.Float64() //nolint:gosec // non-crypto jitter
	delay = time.Duration(float64(delay) * jitter)

	if delay <= 0 {
		delay = base
	}

	return delay
}
