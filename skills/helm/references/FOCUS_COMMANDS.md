# Focus CLI commands

Every command requires `--project PROJECT` (ID, key, slug, or unambiguous name).
`--focus FOCUS` accepts an opaque ID or case-insensitive name inside that project.
Names are resolved before writes; mutation routes use opaque IDs. Focus uses the
existing release API and its `planned` / `released` statuses.

```sh
python3 scripts/helm.py focus list --project TC --status planned
python3 scripts/helm.py focus get --project TC --focus "Onboarding"
python3 scripts/helm.py focus work --project TC --focus "Onboarding" --all
python3 scripts/helm.py focus review --project TC --inactive-days 14
python3 scripts/helm.py focus create --project TC --name "Onboarding" \
  --description "Finish the first-run setup flow" --target-date 2026-12-01
python3 scripts/helm.py focus edit --project TC --focus "Onboarding" \
  --clear-description --clear-target-date
python3 scripts/helm.py focus assign --project TC --focus "Onboarding" \
  --task TC-101 --task TC-102
python3 scripts/helm.py focus complete --project TC --focus "Onboarding"
python3 scripts/helm.py focus reopen --project TC --focus "Onboarding" \
  --reason "Follow-up scope was approved"
python3 scripts/helm.py focus unassign --project TC --focus "Onboarding" --task TC-101
python3 scripts/helm.py focus delete --project TC --focus "Onboarding"
```

`list` supports `--status planned|released`, `--target-from`, `--target-to`,
`--limit` (1–200), `--cursor`, and `--all`. `work` supports the same pagination
flags. `get` and `work` require `--focus`. They preserve the existing release
response fields. `releases list|get`, `release-work`, and the task-claim command
`release --task` remain available.

`review` accepts optional `--focus`, positive `--inactive-days` (default 14),
`--focus-limit` (20), `--task-limit` (200), `--history-limit` (50),
`--timeline-limit` (100), `--request-limit` (100), `--time-limit` seconds (30),
and `--cursor` for explicit continuation. Discovery and review are read-only.
A partial review describes its coverage; recommendations authorize no mutation.

Review budgets cap logical reads, with a process timer bounding in-flight HTTP
retries on Linux/macOS. Limits may be reduced; the command help and output show
their ceilings. Queue entries are compact references, so task details and
timelines are read only for the bounded inspection slice. Due/blocker ordering
uses available fields; findings are ordered over inspected evidence, not a
claim that every project task was ranked.

`next_cursor` continues inventory/history slices and is bound to the project
and Focus. A changed inventory requires a fresh review. If the budget cannot
even finish inventory, `resume.restart_required` explains how to increase the
budget or narrow the Focus instead of returning a cursor that cannot advance.
`history_continuations` identifies older task timeline pages to inspect with
`timeline --task TASK --before CURSOR`; capped/ambiguous history remains unknown.
Queue continuation can repeat current Backlog evidence; deduplicate by task ID
when combining reports. Reports are current evidence, not durable execution plans.

Activity classification version 1 compares actual task-scope/state fields,
human comments, dependency/checklist changes, and changing progress/evidence.
New `task.updated` events include additive `changed_fields` containing actual
`title`, `description`, or `column_id` changes; an empty array means no change
to those work fields. Legacy events without that evidence and comments whose
actor was deleted remain unknown. No data/schema migration is needed. A new
attachment counts as evidence; identical progress, lease churn, metadata and
heartbeats do not reset inactivity. Recorded activity does not prove delivery.

Run `make focus-test` from the repository to build the isolated server, run skill
regressions and retain synthetic real-API receipts under `.artifacts/focus-*`.

`create` requires `--name`, with optional `--description` and `--target-date`
(`YYYY-MM-DD`). `edit` accepts those fields; omission preserves existing values.
Use `--clear-description` or `--clear-target-date` to send explicit null. Each
clear flag is mutually exclusive with the corresponding value flag. `edit`
requires at least one change. Description is limited to 10,000 characters.

Mutations use the current strong resource ETag. `edit`, `complete`, `reopen`,
`delete`, `assign`, and `unassign` accept positive `--version` to explicitly use
an expected version instead. For membership this is the task version and applies
only to a single task. A stale version surfaces the server conflict; the CLI
never overwrites it by automatically reading and retrying with a newer version.

Every mutation accepts `--operation-id UUIDv4`; otherwise one is generated and
returned. Preserve that ID while reconciling or resuming one logical operation.
Bulk membership derives a stable, distinct key for each opaque task ID and
intended membership, so a safe resume does not change a task's operation identity.
Do not reuse an operation ID for a new action. After a lost response, re-read
current records and reconcile before retrying with that same operation ID.

Mutation success returns `ok: true` and `operation_id`, plus the Focus response
(or `deleted: true`). A server write error returns `ok: false`, its structured
`error`, and the operation ID on stdout with exit status 1. Input and preflight
errors use the normal stderr error envelope. No conflict is automatically retried.

`assign` / `unassign` accept repeated `--task` keys or IDs. They change only
`release_id`: they never claim tasks or move board columns. `unassign` refuses to
clear a task belonging to a different Focus. Duplicate references or IDs are
processed once; already matching membership is reported with `changed: false`.
The result has `atomic: false`, `succeeded`, `failed`, and `pending`. Processing
stops at the first failure, preserves earlier successes, and returns `resume`
with project/Focus IDs, remaining tasks, action, and the same operation ID.
Re-read the failed/current memberships, resolve the error, then resume only the
remaining authorized tasks. A partial batch exits 1; it is never rolled back or
presented as atomic.

Completion is always explicit. The API requires at least one direct member and
all required prerequisite work complete before completion succeeds. A completed
Focus and its membership are frozen until `reopen` succeeds with a non-empty
reason. Deletion succeeds only for an empty, active (`planned`) Focus. Readiness
alone never completes a Focus.
