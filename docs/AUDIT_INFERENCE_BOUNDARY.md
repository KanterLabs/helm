# Evidence-only inference boundary

## Failure inventory (written before adapter and isolation tests)

The existing `Draft`/`Run` read-only sandbox permits reading same-UID files.
It is not suitable for a GitHub evidence audit, because a malicious snapshot
could ask the model to read the PAT, actor credentials, other projects, or
environment variables. Additional failure paths are shell execution, patch
tools, image/file readers, MCP resources/tools, web tools, spawned agents,
hooks, skills, memories, inherited project instructions, oversized input or
output, malformed structured output, and continuing an earlier thread.
Never use a prompt instruction as the enforcement for any of these paths.

Validation must use a fake model endpoint and synthetic canaries, with no real
inference, account tokens, PATs, or private prompts. Inspect the model request's
actual tools and context. Return adversarial tool calls even when unadvertised;
check both the next request's result and the absence of a filesystem canary.
The protocol adapter must fail closed on configuration it cannot constrain.
The evidence route must remain separate from existing account/login and Draft.

Account-generation failure cases (recorded before fencing implementation):
a logout and login to the same identity between two polls must invalidate
in-flight work; failed login/logout network attempts must also fence existing
work; a connected account becoming disconnected must advance the fence; repeated
unchanged connected or disconnected polls must remain stable. Counters must be
actor-scoped and synchronized, and existing account response semantics must
remain intact. Restarted workers independently fence interrupted attempts, so
process-local generation counters may reset at restart.

## Accepted beta boundary

Pinned executable: `codex-cli 0.153.0`, SHA-256
`fce635028842bfe9257140e8b7d53162732945e2f356fc35225be0702b4974be`.
Its generated `v2/ThreadStartParams`
and `v2/TurnStartParams` schemas document `environments: []` as disabling
environment access. `Manager.DraftEvidence` accepts only this version and
`gpt-5.6-luna` and `gpt-5.6-sol`; other models and versions fail before inference. The deployment
must retain the validated binary; the runtime version check is a compatibility
guard, not binary attestation. A different binary or model needs another proof.

Each call creates a short-lived process using the requesting actor's existing
`CODEX_HOME`. Codex still owns login, refresh, and authentication. Helm does not
read, copy, or export auth.json. Account sessions and ordinary Draft retain their
existing behavior. Evidence process environment contains only PATH, the actor
HOME, and CODEX_HOME. Actor close and manager close also close evidence sessions.

`EvidenceGeneration(actorID)` supplies a synchronized actor-local fence to audit
workers. Login/logout attempts advance it before any network or session IO,
including attempts that fail. Observed connected-to-disconnected transitions
advance it once; identical account polls leave it stable. DraftEvidence rechecks
the generation after startup, and workers must compare it before accepting
results. Sequence tests cover failed login/logout, same-actor invalidation,
cross-actor isolation and stable repeated polls. Counters reset on process
restart; interrupted attempts require the worker's separate restart fence.

Process-start and thread overrides disable shell, apps/plugins, browser/computer
use, image generation, agent delegation, hooks, skill discovery/instructions,
memories, web search and automatic local context. Both thread and turn explicitly
use `environments: []`. Threads are fresh and ephemeral. Effective configured
MCP servers cause rejection before thread creation; an empty override alone
would merge with inherited servers rather than remove them.

Input is at most 512 KiB, with a required valid JSON schema of at most 32 KiB.
Output must complete successfully, be at most 512 KiB, and parse as JSON.
The caller must still validate the application's output shape and evidence
citations. Helm does not interpret model output as commands or tool requests.

## Real executable transport proof

The separate audit profile defaults to Sol; ordinary Luna assistance is
unchanged. The original Luna capture below is retained. Sol was checked through
the same pinned executable and adapter with a synthetic provider, including
direct and nested execution, MCP, file/image reads, network, subagents,
namespaced collaboration, and skills. Configured MCP and an unvalidated version
still stop before any model request. The probe verifies the actual transmitted
model, not only its artifact label. Compact receipts and full-capture hashes are
in [`evidence-sol-boundary-proof.json`](../internal/codexruntime/testdata/evidence-sol-boundary-proof.json).

The first Sol capture exposed collaboration metadata despite both agent feature
flags being false. The explicit `agents.enabled=false` setting overrides that
model-selected backend. With it, Sol exposes only the same inert code-mode
metadata described below. The strict external-authority check was preserved;
all injected calls are rejected and no execution canary is created. A Luna
regression capture passes with the added setting as well.

Luna's built-in catalog forces code mode, so the actual request includes
`functions.exec` and `functions.wait` metadata in `additional_tools`. **This is
not a literal zero-tool request.** There are no nested external authorities.
More strongly, process-start `features.code_mode_host=false` prevents code-mode
execution: injected custom `exec` returns `code-mode host is disabled`.
Direct injected shell, filesystem/image reader, MCP, network, subagent and
skill function calls all return unsupported-call results. The fake model then
returns valid JSON so the complete adapter path is exercised.

The fake endpoint sees only fixed audit instructions, structured-output
settings, the synthetic snapshot, and runtime metadata. Synthetic unrelated
file, AGENTS.md and environment canaries never appear. No filesystem execution
canary is created. A configured malicious MCP command is rejected with zero
model requests and never starts. An unvalidated version similarly produces
zero requests. Unknown models are refused by the adapter's isolated protocol
test. No live model or private account was used for these checks.

Captured proof (including one actual request and all attack results):
[`evidence-boundary-proof.json`](../internal/codexruntime/testdata/evidence-boundary-proof.json).

Repeat from the repository root, supplying the pinned executable path:

```sh
python3 internal/codexruntime/evidence_transport_probe.py \
  .artifacts/board-space/deploy/image/codex \
  --adapter --adversarial --attack nested --artifact /tmp/evidence-proof.json
```

Add `--model gpt-5.6-sol` to repeat the Sol proof. Other attack choices:
`direct`, `mcp`, `filesystem`, `network`, `subagent`, `collaboration`,
`skill`. Run `--adapter --configured-mcp` and `--adapter --bad-version` for
the pre-inference failure cases. Each invocation captures a complete synthetic
HTTP request/response artifact at the requested path. The probe's wrapper
injects only the fake provider; all isolation controls come from DraftEvidence.
Its Go E2E launcher uses the cached repository toolchain paths and makes no
external model requests. The standalone protocol probe can additionally
inspect supported config controls without the adapter.

Official references reviewed:

- https://learn.chatgpt.com/docs/app-server
- https://learn.chatgpt.com/docs/config-file/config-reference

The cached source tree is useful implementation evidence, but the executable,
captured transport, and generated schemas govern this beta's compatibility.
