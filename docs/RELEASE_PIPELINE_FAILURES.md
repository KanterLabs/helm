# Release preparation failure contract

Recorded before implementation. The entry point prepares a protected-main PR;
it never merges that PR or deploys production. Publication is a separate action
after the existing main deployment has completed.

| Failure | Required outcome |
| --- | --- |
| Wrong repository/remotes or missing authentication | Stop before writes; never expose credentials. |
| Local dirty files, detached checkout, or a different local branch | Prepare from the explicitly reported, deployed remote beta SHA in an isolated worktree; never stage or discard local work. Publishing local beta requires a clean checkout. |
| Gitea and GitHub branch tips differ | Stop with the exact ref that needs reconciliation; never force-push. A matching partial release-branch/tag publication may resume. |
| Beta CI pending, failed, missing deployment evidence, or live revision differs | Stop before preparing the release. Never substitute a previous successful run. |
| Invalid, reused, or older version | Stop. Never move an existing tag or overwrite another release. |
| Main contains application changes absent from tested beta | Refuse promotion; merge main into beta, test and deploy beta first. Release metadata alone may be reconciled. |
| Merge conflict | Preserve the isolated checkout for inspection and stop without changing the caller's checkout. |
| Branch advances during preparation | Recheck both remotes before publishing; stop on drift. Protected-main checks remain authoritative at merge time. |
| Release branch already exists | Validate its manifest, candidate ancestry, version, and allowed metadata-only diff; reuse it or stop. Never reset it. |
| Mirror push or PR creation fails after canonical push | Retain the branch and report a resumable failure. Rerunning must not duplicate PRs or commits. |
| Missing or oversized notes | Stop. Never invent feature descriptions or claim unperformed validation. Keep every authored file at most 20,000 bytes. |
| Schema or migration-engine changes | Identify them explicitly in the PR. Require populated-data upgrade and retained-binary compatibility evidence before merge; do not claim they have been verified by preparation. |
| Production backup is absent or stale | The PR's pre-merge checklist requires a fresh verified backup. Preparation itself never accesses or replaces production data. |
| Release PR unmerged, main CI pending/failed, or production not on its merge SHA | Refuse version publication. Never tag whatever happens to be current main. |
| Unexpected merge tree or mismatched release manifest | Refuse publication. |
| Tag/release exists at a different SHA or with different notes | Refuse to overwrite. Matching partial publication resumes safely. |
| Production deploy fails | Existing Coolify deployment restores only the previous image. Never restore a database automatically. |

The repeatable command test uses actual isolated Git repositories and a local
HTTP fixture for GitHub/Gitea/readiness responses. It captures the resulting
refs, release manifests, command output, and call history without credentials.
It must prove dry-run/preparation cannot mutate main or create version tags,
that uncommitted caller files survive, and that successful publication uses the
verified production merge commit. Network fixtures cannot prove real host
backup or migration behavior; those remain explicit pre-merge evidence gates.
