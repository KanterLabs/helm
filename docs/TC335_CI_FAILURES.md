# PR58 browser supervisor failure inventory

Before changes: browser job124193200105 on f6689ca failed in the synthetic
supervisor cancellation test, before the web build or application tests.
Race job124194150308 is an aggregate and failed because browser failed.

- Counting four `started.json` paths does not establish that their JSON is
  complete. A writer creates/truncates the file before writing its payload.
- Publishing the marker before installing the cancellation handler allows the
  supervisor to signal a worker that cannot yet produce its cancellation
  receipt, even if the JSON write has completed.
- Failure before receipt parsing must not leave the proof's synthetic worker
  process groups running. Cleanup must retain the unrelated-process assertion.

First add a deterministic regression that slows startup receipt writes while
the existing real supervisor cancellation proof observes them. It must expose
the incomplete JSON on the old fixture, then pass with complete publication
after cancellation readiness. Keep all PID/process activity synthetic and
capture test logs in gitignored artifacts. No application or deployment change
is needed for this failure.

The delayed-write regression reproduced the same JSONDecodeError in both
ordinary and timing-enabled supervisor proofs before the fixture change.
The worker now installs cancellation handling first, writes a sibling temporary
receipt, then atomically renames it to `started.json`. Assertion-failure cleanup
also gives the real supervisor time to stop its owned process groups.

Validation: both new delayed-publication cases pass, and all21 tests in
`test_run_browsers.py` pass in15.7seconds. Compilation, diff and size checks
pass. No application build or browser journey was rerun locally because this
change only affects the synthetic supervisor fixture. Red/green logs are
retained under `.artifacts/tc335/ci-startup-publication-{red,green}.log` and
`.artifacts/tc335/ci-supervisor-green.log`.

Repeat with:

```sh
python3 -m unittest discover -s test/e2e -p test_run_browsers.py
```
