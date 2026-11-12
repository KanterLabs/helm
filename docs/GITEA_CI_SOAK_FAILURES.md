# Gitea shadow soak failure contract

Recorded before the soak ledger, its tests and its tool were written (Helm
card H4, tracker card AR-47). It covers the evidence for the shadow gate in
the [migration plan](GITEA_CI_MIGRATION_PLAN.md#phases-and-gates):

> 20 consecutive commits where both pipelines agree; Gitea p95 wall clock no
> worse than hosted p95 plus 20 percent, including queue time; no secret
> present in the Gitea repository.

The ledger pairs GitHub's result with Gitea's shadow result per commit. Its
whole job is to say no unless the evidence supports yes, so every row below is
a way it could say yes, or say something misleading, when it should not. The
workflow itself is covered by the [shadow failure
contract](GITEA_CI_SHADOW_FAILURES.md). "Proof" means an offline test with
recorded API responses that must fail for the stated reason.

## Definitions the ledger commits to

- **GitHub run.** A run of `.github/workflows/ci.yml` on GitHub. Other GitHub
  workflows (`beta-candidate.yml`, `warm-runner-canary.yml`) are ignored.
- **Shadow run.** A Gitea run whose workflow file is `ci.yml`, whose jobs are
  all among `checks`, `browser`, `race` and `container`, and whose every job
  asks only for `ar-helm-canary-short`, `ar-helm-canary-heavy`, `homelab` or
  `homelab-heavy`. Gitea reports the path as `ci.yml@<ref>` for both
  `.gitea/workflows` and `.github/workflows`, so the path alone cannot tell
  them apart; the jobs and labels can.
- **Candidate run.** A GitHub or shadow run for a `push` or `pull_request`
  event. `workflow_dispatch` and every other event are excluded, with the
  reason recorded.
- **Compared jobs.** `checks`, `browser` and `race`, plus `container` once the
  Gitea run has it. A job present on GitHub only is listed as "not compared",
  never as agreeing. `beta_deploy` and `deploy` are never ported (plan D6) and
  are ignored.
- **Classes.** Each commit, keyed by full 40-character SHA, is exactly one of
  `agree`, `disagree`, `unpaired` (one side has no candidate run), `incomplete`
  (a candidate run is queued or running) or `excluded` (the reason is
  recorded: only cancelled, dispatched or pre-shadow runs on a side).
- **Agreement.** Every compared job has the same conclusion on both sides.
  Both sides failing the same job is agreement: the gate asks whether the
  pipelines agree, not whether the commit is good. The ledger reports how
  many agreeing commits were green so the two are never confused.
- **Streak.** Walk commits from newest to oldest. `agree` adds one;
  `disagree` ends the streak; `unpaired`, `incomplete` and `excluded` neither
  count nor reset. The output states this rule and names the commit that
  ended the streak.
- **Order.** Commits are ordered by GitHub's head-commit timestamp, falling
  back to the earliest run creation time, then by SHA.
- **Timing.** Whole-run wall clock is the last compared job's completion minus
  the run's creation time, on both sides, so it includes queue time and the
  same job set. Per job: queue is start minus ready time (run creation, or the
  completion of the jobs it needs: `race` needs `browser`, `container` needs
  `checks`), and run time is completion minus start. Samples come from
  `agree` commits whose runs were first attempts, one run per side.
  Percentiles use the nearest-rank method, so with fewer than 20 samples p95
  is the slowest sample. The timing gate is met only if Gitea's whole-run p95
  is at most 1.2 times GitHub's and at least one sample exists.

## Pairing and classification

| Failure | Detection |
| --- | --- |
| A commit counts as agreement although one side never ran. | Proof: the commit is `unpaired` and the streak neither grows nor resets. |
| A commit counts while a candidate run on either side is queued or in progress. | Proof: `incomplete`. Gitea's zero time (`1970-01-01T00:00:00Z`) is read as "not yet", never as a time. |
| A commit counts when a side's only candidate run was cancelled or superseded. | Proof: `excluded` with reason `cancelled`. A cancelled run beside a completed one on the same side is ignored and listed. |
| A `workflow_dispatch` run stands in for a push or pull-request result (dispatch runs post no commit status on Gitea). | Proof: dispatch runs are never candidates; a commit with only dispatch runs on a side is `excluded` with reason `dispatch-only`. |
| A pre-shadow Gitea run from `.github/workflows` (jobs on `ubuntu-24.04`, or `beta_deploy` and `deploy`) is paired as a shadow run. | Proof: such a run is never a candidate; a commit with only those on Gitea is `excluded` with reason `pre-shadow`. |
| A Gitea run from another workflow (`isolation.yml`, `generic.yml`) is paired. | Proof: only `ci.yml` runs are considered. |
| A rerun hides a flake or a disagreement: one attempt failed, a later one passed. | Proof: any run with `run_attempt` above 1 makes the commit `disagree` with reason `rerun`, whatever the final result. Gitea's API keeps no earlier attempt, so a rerun cannot be shown to be harmless. GitHub's earlier attempt conclusions are recorded as evidence. |
| Two candidate runs on one side for one commit (a SHA pushed to two branches, or GitHub's push and pull-request runs) disagree with each other, and only one is shown. | Proof: `disagree` with reason `side-inconsistent`, both runs listed. |
| GitHub tested a pull request's merge ref while Gitea tested the pushed head, and the ledger presents them as the same thing. | Proof: each commit records both sides' events; a GitHub `pull_request` result paired with a Gitea `push` result carries the basis `github-merge-ref`, shown in the summary, and the streak reports how many of its commits rest on that basis. |
| `container` is reported as agreeing although Gitea never ran it. | Proof: it is listed under "not compared" for the commit and in the summary. |
| A compared job is missing from one side's completed run. | Proof: `disagree` with reason `job-missing`. |
| A disagreement with a recorded, legitimate cause (run 391's `setup-go` bug) disappears or is excused. | Proof: annotations from `deploy/gitea-shadow-soak-annotations.json` are shown beside the commit in both outputs and never change its class or the streak. An annotation must name a full SHA; one that matches no commit is listed as unused. |
| Commits with no relation to the shadow are counted (pushed before shadow began). | `--since` (a date or a commit) is required; a commit is in range when one of its candidate or excluded runs was created at or after it. |
| A Gitea-only commit is called `unpaired` because GitHub's run was created before `--since`. | For each such commit the GitHub runs are looked up by full SHA before classifying. |

## Timing

| Failure | Detection |
| --- | --- |
| Queue time is left out on one side. | Proof: both sides measure from run creation; a sample with no creation time is dropped and counted. |
| Job time on one side is compared with whole-run time on the other, or GitHub's `container` and deploy jobs stretch its wall clock. | Proof: wall clock uses the compared job set on both sides. |
| A dependent job's waiting time is counted as queue. Gitea creates `race` with the run; GitHub creates it when `browser` ends. | Proof: per-job queue is measured from ready time, not job creation. |
| Failed, rerun or unpaired runs skew the percentiles. | Only `agree` commits on first attempts are sampled; the sample count is printed. |
| A p95 from three samples is read as a stable p95. | The sample count and the nearest-rank rule are printed beside every percentile. |
| The timing gate passes with no samples. | Proof: zero samples means not met. |

## Secrets and variables

| Failure | Detection |
| --- | --- |
| "No secret present" is reported true when the token could not read the secrets or variables endpoints (Gitea answers 403 to a read-only token). | Proof: each of repository secrets, repository variables, organization secrets and organization variables is `absent`, `present` (names and count only) or `not checked` with the HTTP status and Gitea's message. The claim is true only if all four were read and are empty; otherwise it is false or `not checked`, and the gate is not met. |
| A secret value is printed. | Gitea never returns values; the ledger records names and counts only. |

## Collection and integrity

| Failure | Detection |
| --- | --- |
| Pagination drops runs or jobs. | Proof: every list is paged until a short page, deduplicated by id, and its count must equal the total the API reports (`total_count` or `X-Total-Count`); otherwise the tool stops with exit status 2 and writes no ledger. Gitea's run list is newest first and is read only until a run created before `--since`, so its total cannot be compared; instead the run numbers read must be contiguous, and a run with no creation time never ends the read. |
| A transient error (timeout, 5xx) silently drops commits. | Retried three times, then the tool stops with exit status 2 and writes no ledger. Any other unexpected HTTP status stops it at once. |
| The token leaks into output, the ledger or an error message. | The token is read from `~/.config/actionrunners/gitea-token-helm` at run time and sent only as a header. Errors name the request path, never headers. Proof: both outputs and stderr are scanned for the token, and the tool refuses to write if it appears. |
| The token file is missing or empty, and the tool quietly reads Gitea anonymously. | Proof: the tool stops with exit status 2. |
| Two runs of the tool on the same data give different ledgers, so evidence cannot be diffed. | Proof: the ledger has sorted keys and no wall-clock fields; replaying the same fixture twice gives identical bytes. |
| `--gate` exits 0 when any part of the gate is unmet or unchecked. | Proof: exit status 1 unless the streak is at least 20, the timing gate is met and the secrets claim is true. Without `--gate` the exit status is 0 on success. |
