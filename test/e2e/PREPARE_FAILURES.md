# Prepared E2E bundle failure inventory

Written before implementation. Subprocess proofs use a synthetic Git checkout
and fake Go executable; they never build Helm or start application servers.

- A changed HEAD, dirty tracked file, added untracked source, deleted file, or
  changed executable bit makes the source fingerprint stale.
- Changed embedded frontend contents, missing assets, or a symlink escaping the
  frontend tree invalidate preparation or reuse.
- Missing metadata, partial build failure, or an existing output directory must
  never expose a usable, overwritten, or partially prepared bundle.
- A binary with changed bytes, missing execute permission, mismatched Go build
  settings, or missing race instrumentation cannot pass verification.
- A changed Go toolchain, build flags, GitHub harness requirement, run ID, or
  run attempt cannot reuse an earlier bundle. Revision environment variables do
  not establish provenance.
- Source or assets changing during compilation abort preparation.
- Concurrent preparation or verification of the same destination fails closed;
  complete bundles publish atomically and cannot be rebuilt in place.
- Build parallelism is bounded independently of runtime server configuration.
- Caller flags cannot override output paths, targets, or the build job budget.
- An optional Go command prefix must preserve literal argv without shell
  expansion. An empty prefix or a changed prefix must not authorize reuse.
- TERM or INT during preparation must stop owned compiler/wrapper descendants,
  including processes ignoring TERM, remove partial stages, and exit with
  128 + signal. Unrelated processes must survive cancellation.
- Docker fallback cancellation must remove only its private CID from the daemon;
  client termination alone does not prove daemon cleanup. Duplicate bind mounts,
  shell expansion of Go arguments, or lost Docker exit status are regressions.

Run `python3 test/e2e/test_prepare.py` to repeat the proofs. CI may capture stdout
and stderr as the test artifact. All fixture content and metadata are synthetic.
Run `python3 test/e2e/test_go_command.py` for the daemon-free Docker wrapper proofs.
