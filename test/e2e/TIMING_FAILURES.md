# Duration-shard failure inventory

Written before implementation. Proofs use synthetic reports and subprocesses;
they start no Helm server, Chromium, or build.

- A profile becomes an obsolete inventory and drops newly added tests. Discover
  the exact selection using pinned Playwright on every run; profile data supplies
  weights only. New/changed files receive the median measured file duration.
- Greedy planning duplicates or omits files, splits sequential file state, or is
  nondeterministic on ties. Assign whole files once, longest first, with stable
  path/worker tie breaks; retain a safe assignment receipt.
- Replacing positional selectors widens path:line or grep filters. Keep original
  arguments and intersect them with Playwright's file-only `--test-list` filter.
- Empty assignments accidentally run everything. Skip unused fixtures entirely;
  an entirely empty selection fails before fixture startup.
- A corrupt profile contains traversals, absolute paths, symlink escapes,
  duplicate keys, NaN, infinity, negative/zero durations, or fabricated metadata.
  Reject malformed profiles before fixture startup, including unselected paths.
- Old timings are silently accepted after source changes. Bind each measurement
  to file bytes at an explicit Git revision, report mismatches as estimated, and
  never use the report's old inventory as the current selection.
- The collector leaks test names, stdout, errors, attachments, source locations,
  credentials, or report paths. Emit only relative file paths, hashes, numerical
  durations/counts, report content hashes, version, and explicit revision.
- Nested suites or retry results are missed/double counted. Walk specs once and
  sum each recorded result duration, including retries; aggregate files across
  independent reports, with per-file mean across reports that contain the file.
- Discovery loads the wrong configuration or emits non-JSON output. Use the
  repository's installed Playwright CLI and normal config; force JSON stdout,
  reject errors, wrong root, missing files and unsafe options, and fail closed.
- Discovery changes environment-dependent runtime skip behavior. `--list` only
  imports test declarations; runtime skips and fixtures execute in each normal
  worker environment. No server is required for declaration discovery.
- CLI config, alternate test lists, incremental selection, or interactive modes
  invalidate the discovery/assignment contract. Reject these in opt-in mode;
  preserve existing default shard behavior and normal focused test filters.
- CLI output/report overrides or source/snapshot updates share mutable evidence
  or source between workers. Reject these in timed mode. Interpret grep operands
  as filter text even when the operand resembles a forbidden option.
- Worker failure/cancellation loses evidence or leaks descendants. Keep the
  existing supervisor cleanup and failure aggregation, covered by its existing
  subprocess proofs with an additional timed-worker failure proof.
