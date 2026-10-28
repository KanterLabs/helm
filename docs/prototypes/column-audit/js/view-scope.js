/* Home, scope, running and canceled views. */
(function () {
  const { h, icon } = window.HelmDom;
  const F = window.HelmAuditFixtures;
  const M = window.HelmAuditModel;
  const V = (window.HelmAuditViews = window.HelmAuditViews || {});
  const plural = (n, one, many) => n + ' ' + (n === 1 ? one : many || one + 's');
  V.plural = plural;

  V.baselineChip = function (ref) {
    const pr = ref === 'pr-52';
    return h('span.baseline-chip', { 'data-testid': 'baseline-chip' }, icon(pr ? 'pr' : 'branch'),
      h('span', { text: F.project.repo + ' · ' + (pr ? 'PR #52 head' : ref) }),
      h('span.sha', { text: '@ ' + (pr ? F.baseline.prShort : F.baseline.short) }));
  };

  V.pageHead = function (eyebrow, title, sub) {
    return h('header.page-head', null, h('p.eyebrow', { text: eyebrow }),
      h('h1', { tabindex: '-1', 'data-autofocus': '', text: title }), sub ? h('p.lede', { text: sub }) : null);
  };

  V.home = function (A) {
    const S = M.state;
    const prior = S.runs.slice().reverse();
    return h('section.page.home', { 'aria-labelledby': 'home-title' },
      h('header.page-head', null, h('p.eyebrow', { text: F.project.name }),
        h('h1#home-title', { tabindex: '-1', 'data-autofocus': '', text: 'Audits' }),
        h('p.lede', { text: 'Check whether each card sits in the column that matches the work actually committed.' })),
      h('div.home-actions', null,
        h('button.btn.primary.lg', { type: 'button', onclick: () => A.go('#scope') }, 'Audit columns'),
        h('p.hint', { text: 'Opens the scope first. Nothing runs until you start it.' })),
      h('section.panel-card', { 'aria-label': 'Board snapshot' },
        h('h2.section-title', { text: 'Board snapshot' }),
        h('ul.board-strip', null, F.columns.map((c) =>
          h('li', null, h('span.col-name', { text: c.name }), h('span.col-count', { text: String(M.columnCount(c.name)) }))))),
      prior.length ? h('section.panel-card', { 'aria-label': 'Previous audits' },
        h('h2.section-title', { text: 'Previous audits in this session' }),
        h('ul.history', null, prior.map((r) => h('li', null,
          h('button.btn.link', { type: 'button', onclick: () => A.go('#run=' + r.id) }, r.id),
          h('span.muted', { text: ' · ' + plural(M.scopeCards(r.scope).length, 'card') + ' · ' + r.status }))))) : null);
  };

  function radio(name, value, label, checked, onchange, extra) {
    return h('label.choice', null,
      h('input', { type: 'radio', name, value, checked, disabled: extra && extra.disabled, onchange }),
      h('span.choice-body', null, h('span.choice-label', { text: label }), extra && extra.hint ? h('span.choice-hint', { text: extra.hint }) : null));
  }

  V.scope = function (A) {
    const S = M.state;
    const sc = S.scope;
    const cards = M.scopeCards(sc);
    const done = cards.filter((c) => c.column === 'Done');
    const beyond = done.filter((c) => c.page > 1).length;
    const fixturesOnly = sc.ref !== 'beta';
    const canStart = cards.length > 0 && !fixturesOnly;
    const set = (patch) => { Object.assign(sc, patch); M.save(); A.rerender(); };
    const colToggle = (name, on) => {
      sc.columns = on ? sc.columns.concat(name) : sc.columns.filter((n) => n !== name);
      M.save(); A.rerender();
    };

    const doneNote = done.length
      ? 'Includes ' + plural(done.length, 'Done card') + (beyond ? ', ' + beyond + ' beyond the board’s first page' : '') + '.'
      : 'No Done cards in this scope, so wrong completion claims won’t be found.';

    return h('section.page.scope', { 'aria-label': 'Audit columns' },
      V.pageHead(F.project.name + ' · Audits', 'Audit columns', 'Choose what to compare. The audit reads committed work, checks each card’s conditions and suggests column corrections. Nothing moves until you apply.'),
      h('div.scope-grid', null,
        h('section.panel-card', { 'aria-labelledby': 'baseline-title' },
          h('h2#baseline-title.section-title', { text: 'Committed baseline' }),
          h('p.repo-line', null, icon('branch'), h('strong', { text: F.project.repo })),
          h('label.field', { for: 'ref' }, 'Branch or ref'),
          h('select#ref.input', { onchange: (e) => set({ ref: e.target.value }) },
            F.project.refs.map((r) => h('option', { value: r.value, selected: r.value === sc.ref, text: r.label }))),
          h('p.help', { text: 'Cards are judged against committed work here.' }),
          sc.ref === 'pr-52' ? h('p.note', { text: 'Cards will be judged against this unmerged PR head instead of beta. Results will say so.' }) : null,
          fixturesOnly ? h('p.note.warn', { role: 'note', text: 'Prototype: synthetic results exist for beta only. Choose beta to continue.' }) : null,
          h('p.help', null, icon('pr'), ' ', F.project.linkedPRs)),

        h('section.panel-card', { 'aria-labelledby': 'cards-title' },
          h('h2#cards-title.section-title', { text: 'Cards to audit' }),
          h('div.choices', { role: 'radiogroup', 'aria-labelledby': 'cards-title' },
            radio('mode', 'all', 'All cards', sc.mode === 'all', () => set({ mode: 'all' }), { hint: plural(F.cards.length, 'card') + ' across every column and page' }),
            radio('mode', 'columns', 'Specific columns', sc.mode === 'columns', () => set({ mode: 'columns' })),
            radio('mode', 'selected', 'Selected cards', false, null, { disabled: true, hint: 'Select cards on the board first.' })),
          sc.mode === 'columns' ? h('fieldset.columns-pick', null, h('legend.sr-only', { text: 'Columns' }),
            F.columns.map((c) => {
              const n = M.columnCount(c.name);
              return h('label.check', null,
                h('input', { type: 'checkbox', 'data-fk': 'col-' + c.name, checked: sc.columns.includes(c.name), disabled: n === 0, onchange: (e) => colToggle(c.name, e.target.checked) }),
                h('span', { text: c.name }), h('span.count', { text: String(n) }));
            })) : null,
          h('p.help.done-note', { text: doneNote })),

        h('section.panel-card', { 'aria-labelledby': 'meaning-title' },
          h('h2#meaning-title.section-title', { text: 'Column meanings' }),
          h('p.help', { text: F.columns.filter((c) => c.meaning).length + ' columns use standard meanings. One custom column needs a decision.' }),
          h('label.field', { for: 'qa-meaning' }, 'QA hold means'),
          h('select#qa-meaning.input', { onchange: (e) => set({ qaMeaning: e.target.value }) },
            F.qaMeanings.map((m) => h('option', { value: m.value, selected: m.value === sc.qaMeaning, text: m.label }))),
          h('p.help', { text: 'Columns are never matched by position. You can start without choosing.' })),

        h('section.panel-card', { 'aria-labelledby': 'ready-title' },
          h('h2#ready-title.section-title', { text: 'Connections' }),
          h('ul.readiness', null, F.readiness.map((r) => h('li', { class: r.ok ? 'ok' : 'warn' },
            icon(r.ok ? 'check' : 'alert'),
            h('div', null, h('strong', { text: r.label }), h('p', { text: r.text }),
              r.fix ? h('button.btn.sm', { type: 'button', onclick: () => A.prototypeOnly('Would open the GitHub connection for acme-synthetic/billing-worker. Your scope is kept.') }, r.fix) : null)))))),

      h('div.start-bar', null,
        h('div.start-copy', null,
          h('p', { text: cards.length ? 'Up to ' + F.project.batchLimit + ' cards per run. The audit model runs once for this scope.' : 'Choose at least one column with cards.' })),
        h('div.start-actions', null,
          h('button.btn.ghost', { type: 'button', onclick: () => A.go('') }, 'Back'),
          h('button.btn.primary.lg', { type: 'button', 'data-fk': 'start', disabled: !canStart, onclick: A.start }, 'Audit ' + plural(cards.length, 'card')))));
  };

  V.running = function (A, r) {
    const st = M.stats(r);
    return h('section.page.running', { 'aria-labelledby': 'run-title' },
      h('header.page-head', null, h('p.eyebrow', { text: 'Column audit · ' + r.id }),
        h('h1#run-title', { tabindex: '-1', 'data-autofocus': '', text: 'Auditing ' + plural(st.cards, 'card') }),
        V.baselineChip(r.scope.ref)),
      h('div.panel-card.progress-card', null,
        h('div#progress', { role: 'status', 'aria-label': 'Audit progress' }, V.progressList(r)),
        h('p.help', null, icon('clock'), ' Leaving this page is safe. This link brings you back to the same run.'),
        h('div.row-actions', null, h('button.btn', { type: 'button', onclick: A.cancel }, 'Cancel audit'))),
      V.runDetails(r));
  };

  V.progressList = function (r) {
    const p = M.progress(r);
    const st = M.stats(r);
    const details = [
      (i) => i < p.stage ? 'Pinned beta @ ' + F.baseline.short + ' · ' + plural(st.cards, 'card') + ' captured, ' + st.done + ' in Done' : 'Pinning ' + F.baseline.ref + ' and capturing cards…',
      (i) => i < p.stage ? st.criteria + ' of ' + st.criteria + ' conditions checked · ' + plural(st.files, 'file') + ' read' : i === p.stage ? Math.floor(st.criteria * p.frac) + ' of ' + st.criteria + ' conditions checked' : 'Waiting',
      (i) => i === p.stage ? 'Comparing supported state with each card’s column…' : 'Waiting'
    ];
    return h('ol.stages', null, M.STAGES.map((s, i) => h('li', { class: i < p.stage ? 'done' : i === p.stage ? 'current' : 'pending', 'aria-current': i === p.stage ? 'step' : null },
      h('span.stage-mark', null, i < p.stage ? icon('check') : h('span.dot')),
      h('div', null, h('strong', { text: s.name }), h('p', { text: details[i](i) })))));
  };

  V.canceled = function (A, r) {
    return h('section.page.canceled', { 'aria-labelledby': 'cancel-title' },
      h('header.page-head', null, h('p.eyebrow', { text: 'Column audit · ' + r.id }),
        h('h1#cancel-title', { tabindex: '-1', 'data-autofocus': '', text: 'Audit canceled' }), V.baselineChip(r.scope.ref)),
      h('div.panel-card', null,
        h('p.lede', { text: 'No cards were changed.' }),
        h('p', { text: 'The run stopped while ' + (r.canceledStage || 'running').toLowerCase() + '. Partial results aren’t shown because they wouldn’t cover your whole scope.' }),
        h('p.help', { text: 'Retrying captures the source again as a new attempt. This attempt stays in history as canceled.' }),
        h('div.row-actions', null,
          h('button.btn.primary', { type: 'button', onclick: A.retryRun }, 'Retry with the same scope'),
          h('button.btn.ghost', { type: 'button', onclick: () => A.go('#scope') }, 'Change scope'))));
  };

  V.runDetails = function (r) {
    const st = M.stats(r);
    const attempts = M.state.runs.map((x) => x.id + ': ' + x.status).join('\n');
    const lines = [
      'Repository: ' + F.project.repo,
      'Baseline: ' + r.scope.ref + ' @ ' + F.baseline.full,
      'Linked PR #52 head (separate evidence): ' + F.baseline.prFull,
      'Captured: ' + F.baseline.capturedAt + ' · Finished: ' + F.baseline.finishedAt,
      'Scope: ' + plural(st.cards, 'card') + ', ' + st.criteria + ' conditions, ' + plural(st.files, 'file') + ' read',
      F.runStats.budgets, F.runStats.coverage, 'Attempts:\n' + attempts
    ];
    return h('details.run-details', null, h('summary', { text: 'Run details' }), h('pre', { text: lines.join('\n') }));
  };
})();
