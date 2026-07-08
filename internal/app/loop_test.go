package app

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alfariesh/surau-backend/pkg/logger"
)

type noopLogger struct{}

func (noopLogger) Debug(_ any, _ ...any) {}
func (noopLogger) Info(_ string, _ ...any) {}
func (noopLogger) Warn(_ string, _ ...any) {}
func (noopLogger) Error(_ any, _ ...any) {}
func (noopLogger) Fatal(_ any, _ ...any) {}

func (n noopLogger) WithField(_ string, _ any) logger.Interface { return n }

func testLogger() logger.Interface { return noopLogger{} }

func TestRunSupervisedLoopRecoversFromPanic(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})

	spec := loopSpec{
		name:        "test_panic_loop",
		interval:    2 * time.Millisecond,
		backoffBase: time.Millisecond,
		backoffMax:  2 * time.Millisecond,
		run: func(context.Context) error {
			n := calls.Add(1)
			if n <= 2 {
				panic("injected test panic")
			}

			return nil
		},
	}

	go func() {
		runSupervisedLoop(ctx, spec, testLogger())
		close(done)
	}()

	// The loop must survive two panicking passes and keep running.
	require.Eventually(t, func() bool { return calls.Load() >= 4 }, 5*time.Second, time.Millisecond,
		"loop did not recover from injected panics")

	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("supervised loop did not stop after cancel")
	}

	// Panic passes are visible on the runs metric under their own result.
	panicked := testutil.ToFloat64(loopRuns.WithLabelValues("test_panic_loop", "panic"))
	assert.GreaterOrEqual(t, panicked, 2.0)
}

func TestRunSupervisedLoopBackoffOnConsecutiveFailures(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	failUntil := int64(3)

	spec := loopSpec{
		name:        "test_backoff_loop",
		interval:    2 * time.Millisecond,
		backoffBase: time.Millisecond,
		backoffMax:  4 * time.Millisecond,
		run: func(context.Context) error {
			if calls.Add(1) <= failUntil {
				return errors.New("transient failure")
			}

			return nil
		},
	}

	go runSupervisedLoop(ctx, spec, testLogger())

	// After consecutive failures the loop must keep retrying (with backoff)
	// and eventually succeed again.
	require.Eventually(t, func() bool { return calls.Load() >= failUntil+2 }, 5*time.Second, time.Millisecond)
}

func TestRunSupervisedLoopHonorsInitialDelay(t *testing.T) {
	t.Parallel()

	var firstCall atomic.Int64

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	start := time.Now()
	initialDelay := 60 * time.Millisecond

	spec := loopSpec{
		name:         "test_initial_delay_loop",
		interval:     5 * time.Millisecond,
		initialDelay: initialDelay,
		run: func(context.Context) error {
			firstCall.CompareAndSwap(0, time.Since(start).Nanoseconds())

			return nil
		},
	}

	go runSupervisedLoop(ctx, spec, testLogger())

	require.Eventually(t, func() bool { return firstCall.Load() > 0 }, 5*time.Second, time.Millisecond)
	assert.GreaterOrEqual(t, time.Duration(firstCall.Load()), initialDelay/2,
		"first pass ran before the initial delay")
}

func TestRunSupervisedLoopStopsWhilePassInFlight(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())

	started := make(chan struct{})

	spec := loopSpec{
		name:     "test_inflight_loop",
		interval: time.Millisecond,
		run: func(passCtx context.Context) error {
			select {
			case started <- struct{}{}:
			default:
			}
			<-passCtx.Done()

			return passCtx.Err()
		},
	}

	var wg sync.WaitGroup

	wg.Add(1)

	go func() {
		defer wg.Done()

		runSupervisedLoop(ctx, spec, testLogger())
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("pass never started")
	}

	cancel()

	require.True(t, waitGroupDoneWithin(&wg, 2*time.Second),
		"loop did not drain after cancel while a pass was in flight")
}

func TestShutdownDrainTimeoutWithStuckPass(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	release := make(chan struct{})
	started := make(chan struct{})

	spec := loopSpec{
		name:     "test_stuck_loop",
		interval: time.Millisecond,
		run: func(context.Context) error {
			select {
			case started <- struct{}{}:
			default:
			}
			// Ignores ctx: simulates a stuck pass that must not block
			// shutdown beyond the drain timeout.
			<-release

			return nil
		},
	}

	var wg sync.WaitGroup

	wg.Add(1)

	go func() {
		defer wg.Done()

		runSupervisedLoop(ctx, spec, testLogger())
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("pass never started")
	}

	cancel()

	// A stuck pass holds the WaitGroup: the bounded select in
	// shutdownServers is what lets the process exit anyway.
	assert.False(t, waitGroupDoneWithin(&wg, 50*time.Millisecond),
		"stuck pass unexpectedly drained; timeout path untested")

	close(release)

	require.True(t, waitGroupDoneWithin(&wg, 2*time.Second))
}

func TestLoopBackoffDelayGrowsAndCaps(t *testing.T) {
	t.Parallel()

	base := 100 * time.Millisecond
	maxDelay := 800 * time.Millisecond

	expected := []time.Duration{
		100 * time.Millisecond,
		200 * time.Millisecond,
		400 * time.Millisecond,
		800 * time.Millisecond,
		800 * time.Millisecond, // capped
		800 * time.Millisecond, // stays capped far beyond the doubling range
	}

	for i, want := range expected {
		failures := i + 1
		if failures == len(expected) {
			failures = 40 // deep overflow guard: must stay at the cap
		}

		got := loopBackoffDelay(failures, base, maxDelay)

		lower := time.Duration(float64(want) * (1 - loopJitterFraction))
		upper := time.Duration(float64(want) * (1 + loopJitterFraction))

		assert.GreaterOrEqual(t, got, lower, "failures=%d", failures)
		assert.LessOrEqual(t, got, upper, "failures=%d", failures)
	}
}

func TestLoopBackoffDelayDefaults(t *testing.T) {
	t.Parallel()

	got := loopBackoffDelay(1, 0, 0)

	assert.GreaterOrEqual(t, got, time.Duration(float64(loopBackoffBase)*(1-loopJitterFraction)))
	assert.LessOrEqual(t, got, time.Duration(float64(loopBackoffBase)*(1+loopJitterFraction)))
}

func waitGroupDoneWithin(wg *sync.WaitGroup, timeout time.Duration) bool {
	done := make(chan struct{})

	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}
