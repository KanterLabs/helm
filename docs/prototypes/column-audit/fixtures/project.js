/* Synthetic project, baseline and run metadata. Nothing here describes a real
   repository, PR or commit; every identifier is invented for the prototype. */
(function () {
  const F = (window.HelmAuditFixtures = window.HelmAuditFixtures || { cards: [] });

  F.project = {
    name: 'Harbor (synthetic)',
    repo: 'acme-synthetic/harbor',
    savedRef: 'beta',
    refs: [
      { value: 'beta', label: 'beta (saved project branch)' },
      { value: 'main', label: 'main' },
      { value: 'pr-52', label: 'PR #52 head (synthetic, unmerged)' }
    ],
    linkedPRs: 'PR #52 (synthetic) is linked to 1 card. Its head is shown as separate evidence, never as work on the baseline.',
    batchLimit: 50,
    firstPageSize: 12
  };

  // Column meanings come from project semantics. QA hold is a custom column
  // whose meaning is unset, so it is never mapped by position.
  F.columns = [
    { name: 'Backlog', meaning: 'backlog' },
    { name: 'Ready', meaning: 'ready' },
    { name: 'In progress', meaning: 'in_progress' },
    { name: 'QA hold', meaning: null, custom: true },
    { name: 'Blocked', meaning: 'blocked' },
    { name: 'Done', meaning: 'done' }
  ];

  F.qaMeanings = [
    { value: '', label: 'Not set: its cards are reported as Couldn’t verify' },
    { value: 'in_progress', label: 'Work still in progress (verification underway)' },
    { value: 'done', label: 'Counts as Done (waiting for release)' }
  ];

  F.readiness = [
    { ok: true, label: 'GitHub', text: 'Read access to acme-synthetic/harbor (synthetic installation).' },
    { ok: true, label: 'Audit model', text: 'Ready. Opening results never starts another paid analysis.' },
    {
      ok: false,
      label: 'acme-synthetic/billing-worker',
      text: 'Access was revoked. 1 card cites this repository and will be reported as Couldn’t verify.',
      fix: 'Reconnect repository'
    }
  ];

  F.baseline = {
    ref: 'beta',
    short: '5f3c9a1',
    full: '5f3c9a1d0e4b7c2a9f86e1b3d5c7a9e0f2b4d6c8',
    prShort: 'c71e0b2',
    prFull: 'c71e0b28a4f6d1e3b5c7a9f0e2d4b6a8c0e1f3a5',
    capturedAt: '2026-10-28 14:02 UTC (synthetic)',
    finishedAt: '2026-10-28 14:04 UTC (synthetic)'
  };

  // Card, criterion and file counts are computed from the fixtures in scope.
  F.runStats = {
    budgets: 'Per card: 12 files / 400 KB. Run ceiling: 160 files / 4 MB / 3 refinement rounds.',
    coverage: 'Coverage is measured per criterion. Unrelated unread files do not affect a card.'
  };

  // Deterministic apply outcomes. "failedOnce" fails on the first attempt and
  // succeeds on an idempotent retry.
  F.applyScript = {
    'HB-12': 'applied',
    'HB-7': 'applied',
    'HB-31': 'already',
    'HB-9': 'skipped',
    'HB-34': 'failedOnce'
  };
  F.applyMessages = {
    applied: 'Applied',
    already: 'Already resolved by someone else before this apply. No second transition was recorded.',
    skipped: 'Skipped: the card was edited after this preview (description changed). It was not moved.',
    failed: 'Confirmation was not received. Retry safely with the same request ID to learn whether this correction was applied.'
  };
})();
