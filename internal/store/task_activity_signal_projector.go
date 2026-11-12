package store

import (
	"context"
	"sync"
	"time"
)

// TaskActivitySignalProjector keeps task_activity_signal current from the
// event cursor (docs/FOCUS_NEGLECT_SIGNAL_PLAN.md D4; failure modes in
// test/neglect/PROJECTOR.md). One is attached per process (per Store); a
// second process running its own is wasteful but safe, because every rebuild
// is an idempotent upsert and the processed cursor only moves forward.
//
//   - Seed: on first run the current MAX(events.cursor) is recorded as the
//     processed cursor, then every task without a row at the current
//     classification version is rebuilt in id-keyset batches. Each batch
//     commits on its own, so a restart resumes and repeats at most the one
//     batch that was not committed. The seed pass runs at every start, so a
//     raised classification version reclassifies older rows.
//   - Tail: events in (processed, high] are read in bounded cursor windows;
//     their tasks are rebuilt and the cursor moves to the window end in the
//     same transaction as the rows. Events committed while a window is being
//     rebuilt are past its end and belong to the next window.
//   - Histories are classified before the write transaction opens, so the
//     writer lock is held only for the upserts.
type TaskActivitySignalProjector struct {
	store    *Store
	options  TaskActivitySignalProjectorOptions
	wake     chan struct{}
	stepMu   sync.Mutex // one step at a time between Run and Sync
	mu       sync.Mutex // guards the fields below
	advanced chan struct{}
	progress int64 // highest processed cursor this process has observed
	stats    TaskActivitySignalProjectorStats

	// Step state, guarded by stepMu.
	initialized  bool
	seedStarted  bool
	seedDone     bool
	seedAfter    string
	lastPassTail bool

	// Test hooks; nil in production.
	classify    func(ctx context.Context, taskID string) (TaskActivitySignal, error)
	beforeWrite func(pass string, taskIDs []string)
}

// TaskActivitySignalProjectorOptions bound the projector's work units. Zero
// values take the defaults.
type TaskActivitySignalProjectorOptions struct {
	// SeedBatch is the number of tasks rebuilt per seed transaction.
	SeedBatch int
	// TailWindow is the number of event cursors read per tail window.
	TailWindow int64
	// Interval is the ticker fallback that finds writes made by another
	// process or binary; same-process commits wake the projector at once.
	Interval time.Duration
	// OnPassError, if set, is called from Run with each failed pass's error
	// (never with ctx's own cancellation). It must not block.
	OnPassError func(error)
}

const (
	defaultTaskActivitySignalSeedBatch  = 100
	defaultTaskActivitySignalTailWindow = 1000
	defaultTaskActivitySignalInterval   = 2 * time.Second
)

// TaskActivitySignalProjectorStats are process-local counters for metrics.
// They carry no task ids or text.
type TaskActivitySignalProjectorStats struct {
	SeedComplete         bool
	SeedRemaining        int64
	SeedBatch            int
	SeedRebuilds         uint64
	TailRebuilds         uint64
	SeedRebuildSeconds   float64
	TailRebuildSeconds   float64
	Writes               uint64
	WriteSeconds         float64
	WriteMaxSeconds      float64
	ClassificationErrors uint64
	PassErrors           uint64
}

// AttachTaskActivitySignalProjector creates the store's projector. It
// returns a conflict if one is already attached; Detach releases the slot.
// The projector does nothing until Run or Sync is called.
func (s *Store) AttachTaskActivitySignalProjector(options TaskActivitySignalProjectorOptions) (*TaskActivitySignalProjector, error) {
	if options.SeedBatch <= 0 {
		options.SeedBatch = defaultTaskActivitySignalSeedBatch
	}
	if options.TailWindow <= 0 {
		options.TailWindow = defaultTaskActivitySignalTailWindow
	}
	if options.Interval <= 0 {
		options.Interval = defaultTaskActivitySignalInterval
	}
	projector := &TaskActivitySignalProjector{store: s, options: options, wake: make(chan struct{}, 1), advanced: make(chan struct{})}
	projector.stats.SeedBatch = options.SeedBatch
	projector.stats.SeedRemaining = -1
	if !s.activitySignal.CompareAndSwap(nil, projector) {
		return nil, conflict("a task activity signal projector is already attached", nil)
	}
	return projector, nil
}

// Detach removes the projector from its store; commits stop waking it.
// It is idempotent and never detaches a different projector.
func (p *TaskActivitySignalProjector) Detach() {
	p.store.activitySignal.CompareAndSwap(p, nil)
}

// Wake asks the projector to look for new events. It never blocks.
func (p *TaskActivitySignalProjector) Wake() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// Run seeds and tails until ctx is canceled, sleeping between passes until
// a commit wakes it or the ticker fires. A failed pass writes nothing and is
// retried on the next wake or tick. It returns ctx.Err().
func (p *TaskActivitySignalProjector) Run(ctx context.Context) error {
	ticker := time.NewTicker(p.options.Interval)
	defer ticker.Stop()
	for {
		more, err := p.step(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err == nil && more {
			continue
		}
		if err != nil && p.options.OnPassError != nil {
			p.options.OnPassError(err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-p.wake:
		case <-ticker.C:
		}
	}
}

// Sync runs passes until the seed is complete and the processed cursor has
// reached the newest event it observed, or a pass fails.
func (p *TaskActivitySignalProjector) Sync(ctx context.Context) error {
	for {
		more, err := p.step(ctx)
		if err != nil || !more {
			return err
		}
	}
}

// Stats returns a copy of the process-local counters.
func (p *TaskActivitySignalProjector) Stats() TaskActivitySignalProjectorStats {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stats
}

// step performs one unit of work: a tail window or a seed batch, alternating
// while both are pending so neither starves. more reports that work remains.
func (p *TaskActivitySignalProjector) step(ctx context.Context) (more bool, err error) {
	p.stepMu.Lock()
	defer p.stepMu.Unlock()
	defer func() {
		if err != nil {
			p.mu.Lock()
			p.stats.PassErrors++
			p.mu.Unlock()
		}
	}()
	if !p.initialized {
		if err := p.store.ensureTaskActivitySignalProjection(ctx); err != nil {
			return false, err
		}
		p.initialized = true
	}
	progress, err := p.store.TaskActivitySignalProgress(ctx)
	if err != nil {
		return false, err
	}
	p.observe(progress.Processed)
	tailPending := progress.Processed < progress.High
	switch {
	case tailPending && (p.seedDone || !p.lastPassTail):
		p.lastPassTail = true
		return true, p.tailWindow(ctx, progress.Processed, progress.High)
	case !p.seedDone:
		p.lastPassTail = false
		return true, p.seedBatch(ctx)
	}
	return false, nil
}

// observe records a processed cursor and releases waiters when it advances.
func (p *TaskActivitySignalProjector) observe(processed int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if processed > p.progress {
		p.progress = processed
		close(p.advanced)
		p.advanced = make(chan struct{})
	}
}

// advancedSignal is closed the next time the processed cursor advances.
func (p *TaskActivitySignalProjector) advancedSignal() <-chan struct{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.advanced
}
