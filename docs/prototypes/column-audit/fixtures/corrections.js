/* Synthetic cards whose committed work supports a different column. */
(function () {
  const F = window.HelmAuditFixtures;
  // status: supported | contradicted | unknown. src: beta | pr | task | check.
  F.criterion = (status, text, note, cites) => ({ status, text, note, cites: cites || [] });
  F.cite = (kind, path, lines, src, excerpt) => ({ kind, path, lines, src, excerpt: excerpt || '' });
  const C = F.criterion;
  const E = F.cite;

  F.cards.push(
    {
      key: 'HB-12', title: 'Add task dependency checks', kind: 'task', column: 'In progress', page: 1,
      outcome: 'correction', to: 'Done', op: 'complete', confidence: 0.91,
      reason: 'The required dependency checks are implemented at the audited commit.',
      tally: '3 of 3 conditions supported',
      criteria: [
        C('supported', 'A task cannot be completed while a dependency is still open.',
          'Completion returns a dependency error before any column change.',
          [E('Implementation', 'internal/store/task_complete.go', '41-49', 'beta',
            'open, err := s.openDependencies(ctx, tx, taskID)\nif err != nil {\n\treturn err\n}\nif len(open) > 0 {\n\treturn ErrOpenDependencies{Keys: open}\n}')]),
        C('supported', 'The error names each blocking card.',
          'The error message lists every open dependency key.',
          [E('Implementation', 'internal/store/task_errors.go', '12-15', 'beta',
            'func (e ErrOpenDependencies) Error() string {\n\treturn "open dependencies: " + strings.Join(e.Keys, ", ")\n}')]),
        C('supported', 'Covered by a test that runs in CI.',
          'Test source exists and the required check passed on this exact commit.',
          [E('Test source', 'internal/store/task_complete_test.go', '88-92', 'beta',
            'err := store.Complete(ctx, blocked.ID)\nvar dep ErrOpenDependencies\nif !errors.As(err, &dep) || dep.Keys[0] != "HB-4" {\n\tt.Fatalf("want HB-4 dependency error, got %v", err)\n}'),
          E('Check result', 'go test ./internal/store (required)', '', 'check', 'Passed on beta @ 5f3c9a1')])
      ],
      missing: []
    },
    {
      key: 'HB-7', title: 'Export board as CSV', kind: 'task', column: 'Done', page: 2,
      outcome: 'correction', to: 'In progress', op: 'reopen', confidence: 0.84,
      reason: 'The archived-card filter required by this card was reverted in 9be41d0; export still works without it.',
      tally: '1 of 3 conditions supported · archived-card filter missing',
      criteria: [
        C('supported', 'Export downloads the visible board as CSV.',
          'The export endpoint streams every visible card as one CSV row.',
          [E('Implementation', 'src/export/csv.ts', '18-24', 'beta',
            'export function toCsv(cards: Card[]): string {\n  const rows = cards.map((c) => [c.key, c.title, c.column]);\n  return [HEADER, ...rows].map(line).join("\\n");\n}')]),
        C('contradicted', 'Archived cards are excluded unless “Include archived” is ticked.',
          'The filter was added in 3a7f2c4 and removed by revert 9be41d0. At the audited commit, export ignores the option.',
          [E('Implementation', 'src/export/csv.ts', '9-12', 'beta',
            'export function exportBoard(board: Board, _opts: ExportOptions) {\n  // includeArchived is accepted but unused since 9be41d0\n  return toCsv(board.cards);\n}')]),
        C('unknown', 'Cell values containing commas or markup are quoted safely.',
          'A test exists, but no check result was recorded for this commit, so it is not verified.',
          [E('Test source', 'src/export/csv.test.ts', '30-33', 'beta',
            "it('quotes unsafe cells', () => {\n  const csv = toCsv([card({ title: 'Fix, then <script>window.__xss = true</script>' })]);\n  expect(csv).toContain('\"Fix, then <script>window.__xss = true</script>\"');\n});")])
      ],
      missing: [{ text: 'No passing check result for src/export/csv.test.ts on 5f3c9a1.', prevents: 'Prevents counting the quoting condition as supported. It does not cause the reopen recommendation.' }]
    },
    {
      key: 'HB-21', title: 'Rate-limit login attempts', kind: 'task', column: 'In progress', page: 1,
      outcome: 'correction', to: 'Done', op: 'complete', confidence: 0.88,
      reason: 'Login attempts are limited per account and per address, with tests passing on the audited commit.',
      tally: '2 of 2 conditions supported',
      blocker: { text: 'Cannot apply while this card is claimed by agent build-runner-2.', detail: 'The claim is not released automatically. Ask the claimant or open the card.' },
      criteria: [
        C('supported', 'Lock out after 5 failed attempts within 10 minutes.',
          'A sliding-window limiter is applied before password verification.',
          [E('Implementation', 'internal/auth/limiter.go', '22-27', 'beta',
            'if l.window(account, 10*time.Minute).Failures() >= 5 {\n\treturn ErrTooManyAttempts\n}')]),
        C('supported', 'Limits also apply per client address.',
          'The same limiter is keyed by client address.',
          [E('Test source', 'internal/auth/limiter_test.go', '40-42', 'beta',
            'for i := 0; i < 5; i++ { _ = login(addr, "wrong") }\nif err := login(addr, "right"); !errors.Is(err, ErrTooManyAttempts) {'),
          E('Check result', 'go test ./internal/auth (required)', '', 'check', 'Passed on beta @ 5f3c9a1')])
      ],
      missing: []
    },
    {
      key: 'HB-31', title: 'Duplicate notifications after reconnect', kind: 'bug', column: 'In progress', page: 1,
      outcome: 'correction', to: 'Done', op: 'resolve', confidence: 0.9,
      reason: 'Reconnect now resumes from the last delivered event, and a regression test covers it.',
      tally: '2 of 2 conditions supported',
      criteria: [
        C('supported', 'After a reconnect, each notification is delivered once.',
          'The stream resumes after the last acknowledged event ID instead of replaying the window.',
          [E('Implementation', 'web/src/lib/liveness.ts', '64-68', 'beta',
            'const resumeFrom = lastAckedEventId ?? undefined;\nsource = openStream({ after: resumeFrom });')]),
        C('supported', 'A regression test reproduces the original report.',
          'The test fails on the parent commit and passes here.',
          [E('Test source', 'web/src/lib/liveness.test.ts', '120-123', 'beta',
            "reconnect();\nexpect(delivered.filter((e) => e.id === 'evt-9')).toHaveLength(1);"),
          E('Check result', 'web unit tests (required)', '', 'check', 'Passed on beta @ 5f3c9a1')])
      ],
      missing: []
    },
    {
      key: 'HB-34', title: 'Avatar crops on narrow Safari windows', kind: 'bug', column: 'Done', page: 2,
      outcome: 'correction', to: 'In progress', op: 'reopenBug', confidence: 0.86,
      reason: 'The fix from 2e8d4f1 was reverted in 7a0c3b9, so the cropping bug is present again.',
      tally: 'Reported behavior is present again',
      reopenReason: 'Fix 2e8d4f1 was reverted in 7a0c3b9; avatars use the cropping rule again.',
      criteria: [
        C('contradicted', 'Avatars keep their aspect ratio at widths below 400px.',
          'The audited commit restores the fixed-height rule that caused the crop.',
          [E('Implementation', 'web/src/styles/sidebar.css', '88-89', 'beta',
            '.avatar { width: 28px; height: 22px; object-fit: cover; }'),
          E('Revert', 'commit 7a0c3b9 (synthetic)', '', 'beta', 'Revert "Keep avatar aspect ratio on narrow screens"')])
      ],
      missing: []
    },
    {
      key: 'HB-15', title: 'Archive filter in board search', kind: 'task', column: 'Ready', page: 1,
      outcome: 'correction', to: 'Done', op: 'complete', confidence: 0.8,
      reason: 'Search filters archived cards and the toggle is implemented.',
      tally: '2 of 2 conditions supported',
      stale: { text: 'Card changed after this audit.', detail: 'Its acceptance criteria were edited after the run captured them, so this recommendation can\u2019t be applied. Re-audit to judge the new criteria.' },
      criteria: [
        C('supported', 'Search hides archived cards by default.', 'Archived cards are filtered before ranking.',
          [E('Implementation', 'web/src/lib/search.ts', '31-32', 'beta', 'const pool = includeArchived ? cards : cards.filter((c) => !c.archivedAt);')]),
        C('supported', 'A toggle shows archived results.', 'The search panel exposes the toggle.',
          [E('Implementation', 'web/src/lib/components/SearchPanel.svelte', '14', 'beta', '<label><input type="checkbox" bind:checked={includeArchived} /> Include archived</label>')])
      ],
      missing: []
    },
    {
      key: 'HB-9', title: 'Parse markdown checklists in imports', kind: 'task', column: 'Backlog', page: 1,
      outcome: 'correction', to: 'In progress', op: 'start', confidence: 0.77,
      reason: 'A checklist parser was committed in 3d9e6f2, but nested items are not handled yet.',
      tally: '1 of 2 conditions supported · nested items outstanding',
      criteria: [
        C('supported', 'Top-level “- [ ]” items become checklist entries.', 'The import parser converts task-list items.',
          [E('Implementation', 'internal/importer/checklist.go', '15-18', 'beta',
            'if m := taskItem.FindStringSubmatch(line); m != nil {\n\titems = append(items, Item{Done: m[1] == "x", Text: m[2]})\n}')]),
        C('contradicted', 'Nested items keep their indentation level.', 'Indentation is trimmed before matching, so nesting is lost.',
          [E('Implementation', 'internal/importer/checklist.go', '12', 'beta', 'line = strings.TrimSpace(line)')])
      ],
      missing: []
    },
    {
      key: 'HB-22', title: 'Keyboard shortcut help sheet', kind: 'task', column: 'Ready', page: 1,
      outcome: 'correction', to: 'Done', op: 'complete', confidence: 0.85,
      reason: 'The ? shortcut opens a help sheet listing every registered shortcut.',
      tally: '2 of 2 conditions supported',
      previewBlock: 'beta advanced to 4d1e0a7 after this audit and touched the shortcut registry. Re-audit to apply against the new commit.',
      criteria: [
        C('supported', 'Pressing ? opens the shortcut sheet.', 'The global key handler opens the sheet.',
          [E('Implementation', 'web/src/lib/shortcuts.ts', '40-41', 'beta', "if (event.key === '?' && !isTyping(event)) openShortcutSheet();")]),
        C('supported', 'The sheet lists every registered shortcut.', 'The sheet renders from the same registry the handler uses.',
          [E('Implementation', 'web/src/lib/components/ShortcutSheet.svelte', '9', 'beta', '{#each registry.all() as s}<li><kbd>{s.keys}</kbd> {s.label}</li>{/each}')])
      ],
      missing: []
    }
  );
})();
