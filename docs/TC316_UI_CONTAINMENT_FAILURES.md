# TC-316 UI containment — failure inventory

Phase 1 of [the consolidation plan](PRODUCT_CONSOLIDATION_PLAN.md) parks the
audit review entry points in the browser and corrects Roadmap metric language.
Portable transfer was parked too until TC-317 passed; it is now restored.
This is explicit **UI containment** only. Audit HTTP routes, stores,
readers/writers and the API contract are unchanged; this inventory makes no backend authorization or security claim and
adds no feature-flag platform. Audit entry points return only after TC-311's
independent accuracy and human acceptance.

Written before the product change. Each item names the browser journey that
must fail on the pre-change UI and pass afterwards. Fixtures are synthetic.

## Audit history (parked review)

| # | Failure | Evidence / journey |
| --- | --- | --- |
| A1 | The audit list still offers **Run audit**, **Run first audit** or the audit-only **GitHub repositories** editor. | `audit.spec.ts`, `github-audit-ui.spec.ts`: list shows zero start/config controls. |
| A2 | A detail deep link still renders **Approve**, **Dismiss**, **Edit destination**, **Save destination** or **Preview approved finding**, or mounts the preview/apply dialogs. | Deep-link detail has zero mutation buttons and no `dialog`/`alertdialog`. |
| A3 | A server-run job panel still offers **Cancel audit** or **Retry audit**. | Seeded running, finished and cancelled jobs show progress/outcome but no job actions. |
| A4 | Viewing history issues any non-GET API request (start, retry, cancel, finding patch, preview/apply, repository save). | Every audit journey asserts zero recorded writes. |
| A5 | The paused capability is not explained, or the copy synthesizes results, claims findings were applied/verified, or claims server-side enforcement. | A visible `status` explains that new runs and review are paused here and that recorded findings are unverified history. |
| A6 | Authorized history stops being readable: runs, paginated findings, captured/current versions, citations, evidence manifest, omissions and task links disappear. | Queued, complete and partial runs still render findings and evidence read-only. |
| A7 | Copy still instructs the retired flow ("findings to review", "Approve a destination, then preview…", "Run a fresh audit", "Approval and apply are locked…"). | Detail headings and group help describe recorded history instead. |
| A8 | Revoked access no longer clears already-rendered private content (findings, manifest, captured source). | Refresh after the detail read returns 403 / `evidence_unavailable` removes findings and manifest and shows the alert. |
| A9 | A late audit list/detail response from a previous project paints into the current project. | Switch project while the old list is held; releasing it does not show the old run. |
| A10 | Read-only citation viewing regresses (focus return, unsafe link suppression). | Citation viewer opens, Escape returns focus, `javascript:` URLs stay unlinked. |
| A11 | Mobile layout overflows at 390px in list or detail. | Horizontal overflow check on list and detail. |

## Portable transfer (restored after TC-317)

TC-317's v4 lossless round trip passed parent review (commit `35f65ed`), so the
normal board controls stay available rather than parked. The journey guards
the restored entry point:

| # | Failure | Evidence / journey |
| --- | --- | --- |
| T1 | **Export project** is missing or downloads a pre-v4 archive. | `tc316-containment.spec.ts`: Actions → Export downloads `version: 4`. |
| T2 | Choosing a file imports immediately or the preview writes tasks. | Import archive issues only a `dry_run` request; destination unchanged. |
| T3 | The review dialog is skipped or hides its counts. | "Review import" dialog shows tasks to create before confirmation. |
| T4 | Explicit confirmation does not import the archived tasks. | Confirming creates the same task titles in the destination. |

## Roadmap metrics (truthful labels and scope)

| # | Failure | Evidence / journey |
| --- | --- | --- |
| R1 | A completion metric says **Completed**/**completed** with "Shipped so far" or otherwise implies deployment. | Metric reads "Tasks marked complete"; no "Shipped" text in Roadmap. |
| R2 | The hero synthesizes a completed count from the percentage when the count is absent. | Count shown equals the server's semantic completed count. |
| R3 | A project-scoped Roadmap describes workspace scope ("Workspace pulse", "Across all projects", "each project's"), or lists other projects under Project progress. | Project view shows only that project's row and project-scope copy. |
| R4 | A load failure renders zeros (0 tasks, 0%) as if they were facts, or shows a previous scope's figures under the current heading. | Failed summary shows an unavailable state, no metric cards and a Retry. |
| R5 | An empty project shows "0%" completion as if progress were measured. | Zero-task project states "No tasks yet". |
| R6 | Loading shows stale metrics instead of the loading state. | Existing skeleton retained (covered by existing Roadmap journeys). |

## Limitations recorded in advance

- The audit, job cancel/retry, finding PATCH and apply endpoints still
  accept authorized API callers. Containment is a UI decision, not enforcement.
- Cancel is hidden too because the detail page must have zero mutation actions;
  an operator can still cancel through the API.
- Repository selection for PR status remains in admin Settings; only the
  audit-page copy of the editor is parked.
- The real-backend `github-beta-integration.spec.ts` harness drives start,
  review and apply through the API and checks the UI read-only; it needs the
  dedicated synthetic upstream and is not part of the focused acceptance.
