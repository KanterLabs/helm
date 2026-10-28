/* Results overview: outcome summary, the three result views and their rows. */
(function () {
  const { h, icon } = window.HelmDom;
  const F = window.HelmAuditFixtures;
  const M = window.HelmAuditModel;
  const V = window.HelmAuditViews;
  const plural = V.plural;

  const TABS = [
    { id: 'corrections', label: 'Corrections' },
    { id: 'correct', label: 'Already correct' },
    { id: 'unverified', label: "Couldn't verify" }
  ];

  function headline(g) {
    if (!g.all.length) return 'No cards in this scope';
    if (g.corrections.length) return plural(g.corrections.length, 'column correction') + ' suggested';
    if (!g.correct.length) return 'No placement judgments could be verified';
    return 'No column corrections found';
  }

  V.defaultTab = function (g) {
    if (g.corrections.length) return 'corrections';
    if (g.unverified.length) return 'unverified';
    return 'correct';
  };

  V.results = function (A, r) {
    const S = M.state;
    const g = M.groups(r);
    const tabs = TABS.filter((t) => g[t.id].length);
    if (!S.tab || !g[S.tab] || !g[S.tab].length) S.tab = V.defaultTab(g);
    const parts = [plural(g.all.length, 'card') + ' audited'];
    if (g.correct.length) parts.push(g.correct.length + ' already correct');
    if (g.unverified.length) parts.push(g.unverified.length + " couldn't be verified");

    return h('section.page.results', { 'aria-labelledby': 'results-title' },
      h('header.page-head.results-head', null,
        h('p.eyebrow', { text: 'Column audit · ' + r.id + ' · finished' }),
        h('h1#results-title', { tabindex: '-1', 'data-autofocus': '', text: headline(g) }),
        h('p.summary', { 'data-testid': 'result-summary', text: parts.join(' · ') }),
        h('div.head-row', null, V.baselineChip(r.scope.ref),
          h('button.btn.ghost.sm', { type: 'button', onclick: () => A.go('#scope') }, 'New audit'))),
      !g.correct.length && !g.corrections.length && g.unverified.length
        ? h('p.note', { text: 'Nothing here is an all-clear: none of these cards could be checked. Each one below explains what is missing.' }) : null,
      tabs.length ? h('div.tabs', { role: 'tablist', 'aria-label': 'Result views', onkeydown: (e) => tabKeys(e, A, tabs) },
        tabs.map((t) => h('button.tab', {
          type: 'button', role: 'tab', id: 'tab-' + t.id, 'aria-controls': 'tabpanel',
          'aria-selected': String(S.tab === t.id), tabindex: S.tab === t.id ? '0' : '-1', 'data-fk': 'tab-' + t.id,
          onclick: () => A.setTab(t.id)
        }, h('span', { text: t.label }), h('span.tab-count', { text: String(g[t.id].length) })))) : null,
      h('div#tabpanel', { role: 'tabpanel', 'aria-labelledby': 'tab-' + S.tab, 'data-testid': 'result-list' },
        h('ul.rows', { class: 'rows-' + S.tab }, g[S.tab].map((c) => V.row(A, c)))),
      V.runDetails(r));
  };

  function tabKeys(e, A, tabs) {
    const i = tabs.findIndex((t) => t.id === M.state.tab);
    let n = -1;
    if (e.key === 'ArrowRight') n = (i + 1) % tabs.length;
    if (e.key === 'ArrowLeft') n = (i - 1 + tabs.length) % tabs.length;
    if (e.key === 'Home') n = 0;
    if (e.key === 'End') n = tabs.length - 1;
    if (n < 0) return;
    e.preventDefault();
    A.setTab(tabs[n].id);
  }

  function head(A, c) {
    return h('div.row-head', null,
      h('span.key', { text: c.key }),
      c.kind === 'bug' ? h('span.kind-tag', { text: 'Bug' }) : null,
      h('button.title-link', { type: 'button', 'aria-label': 'Open ' + c.key + ': ' + c.title, onclick: () => A.openCard(c) }, c.title));
  }

  function transition(c, muted) {
    return h('p.transition', { class: muted ? 'muted' : null },
      h('span.col', { 'data-testid': 'current', text: c.column }),
      icon('arrow', 'arrow'), h('span.sr-only', { text: ' to ' }),
      h('span.col.rec', { 'data-testid': 'recommended', text: c.to }));
  }

  V.selectBox = function (A, c, where) {
    const verb = M.OPS[c.op].verb;
    return h('label.select-box', null,
      h('input', { type: 'checkbox', 'data-select': c.key, 'data-fk': where + '-sel-' + c.key, checked: M.state.selected.includes(c.key), onchange: (e) => A.select(c.key, e.target.checked) }),
      h('span', { text: 'Select: ' + verb }));
  };

  function callout(kind, iconName, title, body, actions) {
    return h('div.callout', { class: 'callout-' + kind },
      icon(iconName),
      h('div.callout-body', null, h('strong', { text: title }), body ? h('p', { text: body }) : null,
        actions && actions.length ? h('div.callout-actions', null, actions) : null));
  }

  function correctionRow(A, c) {
    const rc = M.receipt(c.key);
    const done = rc && (rc.status === 'applied' || rc.status === 'already');
    const evidence = h('button.btn.sm', { type: 'button', 'data-fk': 'ev-' + c.key, onclick: (e) => A.openEvidence(c.key, e.currentTarget) }, 'Review evidence');
    let status = null;
    if (done) {
      status = h('p.applied-badge', { class: rc.status === 'already' ? 'already' : null }, icon('check'),
        h('span', { text: (rc.status === 'already' ? 'Already applied: ' : 'Applied: ') + rc.from + ' → ' + rc.to }));
    } else if (rc && rc.status === 'skipped') {
      status = callout('neutral', 'skip', 'Skipped: not moved', rc.message, [h('button.btn.sm', { type: 'button', onclick: () => A.reaudit(c) }, 'Re-audit this card')]);
    } else if (rc && rc.status === 'failed') {
      status = callout('error', 'alert', 'Apply failed', rc.message);
    }
    const blocker = c.blocker && !done ? callout('warn', 'lock', c.to + ' recommended · ' + c.blocker.text, c.blocker.detail,
      [h('button.btn.sm', { type: 'button', onclick: () => A.openCard(c) }, 'Open card')]) : null;
    const stale = c.stale && !done ? callout('warn', 'clock', c.stale.text, c.stale.detail,
      [h('button.btn.sm', { type: 'button', onclick: () => A.reaudit(c) }, 'Re-audit this card')]) : null;
    return h('li.row.correction', { 'data-card': c.key, class: done ? 'is-applied' : c.stale ? 'is-stale' : null },
      head(A, c),
      done ? null : transition(c, Boolean(c.stale)),
      status,
      h('p.reason', { text: c.reason }),
      h('p.tally', null, icon(c.op === 'complete' || c.op === 'resolve' ? 'check' : 'dash'), h('span', { text: c.tally })),
      blocker, stale,
      h('div.row-actions', null, evidence, M.selectable(c) ? V.selectBox(A, c, 'row') : null));
  }

  function correctRow(A, c) {
    return h('li.row.correct', { 'data-card': c.key },
      head(A, c),
      h('p.state-line', null, h('span.col', { 'data-testid': 'current', text: c.column }),
        h('span.pill.ok', null, icon('check'), 'Correct column')),
      h('p.reason', { text: c.reason }),
      h('div.row-foot', null, h('span.tally-inline', { text: c.tally }),
        h('button.btn.sm.ghost', { type: 'button', 'data-fk': 'ev-' + c.key, onclick: (e) => A.openEvidence(c.key, e.currentTarget) }, 'Review evidence')));
  }

  V.recoveryButtons = function (A, c, opener) {
    const list = c.revoked ? c.recovery.slice() : ['Review inspected evidence'].concat(c.recovery.filter((x) => x !== 'Review inspected evidence'));
    return list.map((label, i) => h('button.btn.sm', {
      type: 'button', class: i === 0 ? null : 'ghost', 'data-fk': 'rec-' + c.key + '-' + i,
      onclick: (e) => A.recover(c, label, opener !== false ? e.currentTarget : null)
    }, label));
  };

  function unverifiedRow(A, c) {
    return h('li.row.unverified', { 'data-card': c.key },
      head(A, c),
      h('p.state-line', null, h('span.col', { 'data-testid': 'current', text: c.column }),
        h('span.pill.unknown', null, icon('question'), "Couldn't verify")),
      h('p.reason', { text: c.reason }),
      h('p.help', { text: 'This is not a column change. The card stays in ' + c.column + '.' }),
      h('div.row-actions.wrap', null, V.recoveryButtons(A, c)));
  }

  V.row = function (A, c) {
    if (c.outcome === 'correction') return correctionRow(A, c);
    if (c.outcome === 'correct') return correctRow(A, c);
    return unverifiedRow(A, c);
  };

  // Sticky selection bar, patched in place so focus is never disturbed.
  V.reviewBar = function (A) {
    const n = M.state.selected.length;
    const bar = document.getElementById('review-bar');
    bar.hidden = !(n > 0 && A.view() === 'results');
    bar.replaceChildren(
      h('p', null, h('strong', { text: plural(n, 'correction') + ' selected' }), h('span.muted', { text: ' · nothing moves until you apply' })),
      h('div.bar-actions', null,
        h('button.btn.ghost.sm', { type: 'button', onclick: A.clearSelection }, 'Clear'),
        h('button.btn.primary', { type: 'button', onclick: A.openPreview }, 'Review ' + plural(n, 'correction'))));
    document.body.classList.toggle('has-bar', !bar.hidden);
  };
})();
