/* Synthetic cards that are already correct or that couldn't be verified. */
(function () {
  const F = window.HelmAuditFixtures;
  const C = F.criterion;
  const E = F.cite;

  F.cards.push(
    {
      key: 'HB-3', title: 'Board column drag ordering', kind: 'task', column: 'Done', page: 1,
      outcome: 'correct', confidence: 0.93,
      reason: 'Drag ordering is implemented and persisted, with a passing browser test.',
      tally: '3 of 3 conditions supported',
      criteria: [
        C('supported', 'Cards can be reordered by drag within a column.', 'Pointer and keyboard reordering share one ordering function.',
          [E('Implementation', 'web/src/lib/boardOrdering.ts', '22-23', 'beta', 'export function moveWithin(col: Column, from: number, to: number) {\n  col.cards.splice(to, 0, ...col.cards.splice(from, 1));')]),
        C('supported', 'Order persists after reload.', 'The new position is saved with the card version.', [E('Implementation', 'internal/store/task_order.go', '30', 'beta', 'return s.saveOrder(ctx, tx, columnID, ordered)')]),
        C('supported', 'Covered by a browser test.', 'Required browser check passed on this commit.', [E('Check result', 'web e2e: board ordering (required)', '', 'check', 'Passed on beta @ 5f3c9a1')])
      ]
    },
    {
      key: 'HB-5', title: 'Webhook delivery retries', kind: 'task', column: 'In progress', page: 1,
      outcome: 'correct', confidence: 0.82,
      reason: 'Retries are committed, but exponential backoff is still outstanding, so In progress is right.',
      tally: '1 of 2 conditions supported · backoff outstanding',
      criteria: [
        C('supported', 'Failed deliveries are retried up to 5 times.', 'The worker requeues failed deliveries.', [E('Implementation', 'internal/webhooks/worker.go', '51', 'beta', 'if attempt < 5 { return q.Requeue(job, attempt+1) }')]),
        C('contradicted', 'Retries use exponential backoff.', 'Requeue uses a fixed 30-second delay.', [E('Implementation', 'internal/webhooks/queue.go', '19', 'beta', 'job.NotBefore = now.Add(30 * time.Second)')])
      ]
    },
    {
      key: 'HB-8', title: 'Dark theme for settings', kind: 'task', column: 'Backlog', page: 1,
      outcome: 'correct', confidence: 0.79,
      reason: 'Backlog is right: the card depends on HB-40, which is still open, and no implementation is on beta.',
      tally: 'Not started · waiting on a dependency',
      criteria: [
        C('contradicted', 'Ready requires every dependency to be complete.', 'Task facts show the dependency HB-40 is still open, so the card is not ready.', [E('Task fact', 'Dependency HB-40 (open)', '', 'task', '')]),
        C('unknown', 'Settings pages follow the dark theme.', 'No dark-theme settings code was found on beta. Absence alone does not prove the card is unstarted; the dependency does.', [])
      ]
    },
    {
      key: 'HB-27', title: 'Bulk label editing', kind: 'task', column: 'In progress', page: 1,
      outcome: 'correct', confidence: 0.8,
      reason: 'The implementation exists only on PR #52 head, not on beta, so In progress is right.',
      tally: 'Work found only on PR #52 head',
      criteria: [
        C('unknown', 'Select several cards and add or remove a label in one action.',
          'Implemented on the unmerged PR head. Not present on beta, which this audit judges.',
          [E('Implementation', 'web/src/lib/components/BulkLabels.svelte', '5-7', 'pr',
            'async function apply(labels: string[]) {\n  await api.bulkLabel(selected.map((c) => c.id), labels);\n}')]),
        C('unknown', 'The change is undoable.', 'No undo path was found on beta or on the PR head.', [])
      ],
      missing: [{ text: 'PR #52 is not merged into beta.', prevents: 'Prevents a Done recommendation for the beta baseline. Choose PR #52 head as the baseline to judge the unmerged work.' }]
    },
    {
      key: 'HB-2', title: 'Settings layout refresh', kind: 'task', column: 'Done', page: 1,
      outcome: 'correct', confidence: 0.9,
      reason: 'Both layout conditions are implemented on beta.',
      tally: '2 of 2 conditions supported',
      criteria: [
        C('supported', 'Settings sections use a two-column grid on desktop.', 'Grid layout applies from 900px.', [E('Implementation', 'web/src/styles/settings.css', '4', 'beta', '@media (min-width: 900px) { .settings-grid { grid-template-columns: 220px 1fr; } }')]),
        C('supported', 'Sections stack on mobile.', 'Default layout is a single column.', [E('Implementation', 'web/src/styles/settings.css', '2', 'beta', '.settings-grid { display: grid; gap: 24px; }')])
      ]
    },
    {
      key: 'HB-11', title: 'Release notes generator', kind: 'task', column: 'Done', page: 1,
      outcome: 'correct', confidence: 0.88,
      reason: 'The generator groups merged work by label and is covered by a passing test.',
      tally: '2 of 2 conditions supported',
      criteria: [
        C('supported', 'Groups changes by label.', 'Entries are bucketed by their first label.', [E('Implementation', 'deploy/release_notes.py', '40', 'beta', 'groups.setdefault(entry.labels[0], []).append(entry)')]),
        C('supported', 'Has a test.', 'Required check passed on this commit.', [E('Check result', 'python release tests (required)', '', 'check', 'Passed on beta @ 5f3c9a1')])
      ]
    },
    {
      key: 'HB-18', title: 'Add health endpoint', kind: 'task', column: 'In progress', page: 1,
      outcome: 'unverified', confidence: 0.2,
      reason: 'The route implementation wasn’t inspected within this run’s evidence budget.',
      recovery: ['Review inspected evidence', 'Refine source', 'Re-audit this card'],
      criteria: [
        C('unknown', 'GET /healthz returns 200 with build version.', '12 files were read; none registered /healthz. The router file list was cut off by the per-card budget.', [E('Implementation', 'internal/httpapi/routes.go', '1-3', 'beta', '// first 3 of 412 lines captured\npackage httpapi\nimport (')]),
        C('unknown', 'Covered by a smoke test.', 'No test referencing /healthz was found in the files read.', [])
      ],
      missing: [{ text: 'Route registration for /healthz was not located.', prevents: 'Prevents any column judgment. In progress is left unchanged.' }]
    },
    {
      key: 'HB-26', title: 'Improve onboarding', kind: 'task', column: 'Ready', page: 1,
      outcome: 'unverified', confidence: 0.1,
      reason: 'The card has no acceptance criteria or clear goal to check committed work against.',
      recovery: ['Add acceptance criteria', 'Re-audit this card'],
      criteria: [],
      missing: [{ text: 'No goal or acceptance criteria on the card.', prevents: 'Nothing can be judged complete or incomplete.' }]
    },
    {
      key: 'HB-29', title: 'Signed release artifacts', kind: 'task', column: 'QA hold', page: 1,
      outcome: 'unverified', confidence: 0.6,
      reason: 'QA hold has no column meaning, so the audit can’t say whether this work belongs there.',
      recovery: ['Choose column meaning'],
      criteria: [
        C('supported', 'Release artifacts are signed.', 'The release script signs each artifact.', [E('Implementation', 'deploy/release_finish.py', '77', 'beta', 'sign_artifact(path, key_ref=signing_key)')]),
        C('supported', 'Signatures are verified before publishing.', 'Verification runs before upload.', [E('Implementation', 'deploy/verify-release.sh', '18', 'beta', 'cosign verify-blob --signature "$sig" "$artifact"')])
      ],
      missing: [{ text: 'QA hold has no meaning in this project.', prevents: 'The work is supported, but it can’t be compared with an unmapped column.' }],
      variants: {
        in_progress: { outcome: 'correction', to: 'Done', op: 'complete', reason: 'Both release conditions are implemented on beta; QA hold means work in progress here.', tally: '2 of 2 conditions supported', missing: [] },
        done: { outcome: 'correct', reason: 'Both release conditions are implemented; QA hold counts as Done in this project.', tally: '2 of 2 conditions supported', missing: [] }
      }
    },
    {
      key: 'HB-24', title: 'Invoice PDF rendering', kind: 'task', column: 'Done', page: 1,
      outcome: 'unverified', confidence: 0, revoked: true,
      reason: 'Its evidence is in acme-synthetic/billing-worker, and access to that repository was revoked.',
      recovery: ['Reconnect repository'],
      criteria: [
        C('unknown', 'Invoices render as PDF with line items.', 'Evidence from this repository is hidden because access was revoked.', []),
        C('unknown', 'Totals match the invoice record.', 'Evidence from this repository is hidden because access was revoked.', [])
      ],
      missing: [{ text: 'No current access to acme-synthetic/billing-worker.', prevents: 'Done is left unchanged; missing access does not mean the work is missing.' }]
    }
  );
})();
