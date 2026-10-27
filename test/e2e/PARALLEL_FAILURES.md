# Isolated browser workers: failure inventory

Recorded before implementing concurrent browser workers.

- Workers race on embedded assets, migrations, build output or shared files.
  Build once in the coordinator; workers only execute the immutable binary.
- Shared databases, account homes, provider state, hook secrets or browser
  origins make otherwise independent tests interfere. Each worker owns all
  runtime state and a separate loopback port block.
- An occupied port causes tests to hit an unrelated service. Check the complete
  port block before starting fixtures and fail before running browser requests.
- Shards omit or duplicate files, especially focused runs with fewer files
  than workers. Preserve file-level sharding and prove list coverage exactly.
- Legacy URL environment variables point requests at another worker. Set both
  canonical and legacy base URLs to the worker's own origin.
- One worker's error is hidden by another worker's success. Wait for every
  child, aggregate all exit codes and retain every worker's results.
- Cancellation leaves test servers, fake providers or browser processes alive.
  Terminate owned process groups and run worker cleanup; never kill by port or
  broad process name. Never disturb another invocation's processes.
- Logs, traces or summaries collide, lose failed-worker evidence, or contain
  credentials. Separate artifact directories, retain race/secret checks and
  publish only sanitized worker identity, timing and result metadata.
- A requested focused suite, local-auth onboarding or stalled-account run
  accidentally executes the entire suite again. Keep focused arguments intact
  and choose one worker unless concurrency is explicitly requested.
- A failed build leaves embedded assets replaced. Preserve the coordinator's
  existing restoration trap, with exactly one owner of generated assets.

Validation: capture shard inventories and compare their disjoint union with
the original suite; run a real multi-worker fixture-isolation smoke before the
full suite; exercise injected child failure/cancellation in a disposable
orchestrator check; retain per-worker timings, browser traces/screenshots and
the existing artifact checksums. Compare measured serial and concurrent browser
durations separately from dependency installation and builds.

`python3 test/e2e/test_run_browsers.py` exercises actual child processes with
synthetic fixtures. It proves overlapping execution, distinct state and ports,
focused argument preservation, failure aggregation, cancellation isolation and
forced cleanup of a crashed worker's uncooperative descendant. The descendant
case reproduced an unbounded wait before the cleanup deadline was added.

The first four-worker browser run also exposed a hidden ticket-queue setup
dependency: the Coolify test's bootstrap used an idempotency header forbidden
for secret-returning webhook creation. A pre-existing queue had skipped that
branch in serial runs. Each file must establish its own valid prerequisites.
The UI shortcut suite now creates its own board as well. The drawer's foreign
claim fixture responds only for that captured task, avoiding upstream fetches
that could finish after the browser canceled a poll.

A rapid rerun also reproduced a false occupied-port error from TCP TIME_WAIT
after all fixtures had exited. The port probe uses SO_REUSEADDR, like the real
servers, while bind plus listen still rejects an active listener.
