/* Read-only preview (the only confirmation) and the per-card apply receipt. */
(function () {
  const { h, icon } = window.HelmDom;
  const F = window.HelmAuditFixtures;
  const M = window.HelmAuditModel;
  const V = window.HelmAuditViews;
  const plural = V.plural;

  const RESOLUTIONS = [
    { value: 'fixed', label: 'Fixed' },
    { value: 'duplicate', label: 'Duplicate' },
    { value: 'wont_fix', label: 'Won’t fix' },
    { value: 'cannot_reproduce', label: 'Can’t reproduce' }
  ];

  function fromTo(c) {
    return h('p.transition.compact', null, h('span.col', { text: c.column }), icon('arrow', 'arrow'), h('span.sr-only', { text: ' to ' }), h('span.col.rec', { text: c.to }));
  }

  function policyInput(c) {
    const S = M.state;
    if (c.op === 'resolve') {
      const id = 'res-' + c.key;
      return h('div.policy', null, h('label.field', { for: id }, 'Resolution'),
        h('select.input', { id, onchange: (e) => { S.resolution[c.key] = e.target.value; M.save(); } },
          RESOLUTIONS.map((r) => h('option', { value: r.value, selected: (S.resolution[c.key] || 'fixed') === r.value, text: r.label }))));
    }
    if (c.op === 'reopenBug') {
      const id = 'reason-' + c.key;
      return h('div.policy', null, h('label.field', { for: id }, 'Reason recorded on the bug'),
        h('textarea.input', { id, rows: '2', oninput: (e) => { S.reopenReason[c.key] = e.target.value; M.save(); } }, S.reopenReason[c.key] || c.reopenReason));
    }
    if (c.op === 'start') return h('p.help', { text: 'Reason recorded: ' + c.reason });
    return null;
  }

  V.preview = function (A) {
    const items = M.preview();
    const ok = items.filter((x) => !x.blocked);
    const blocked = items.length - ok.length;
    const check = 'Rechecked just now against ' + F.project.repo + ' · beta @ ' + F.baseline.short + ': ' +
      ok.length + ' can be applied' + (blocked ? ', ' + blocked + ' can’t' : '') + '.';
    return h('section.page.preview', { 'aria-label': 'Review corrections' },
      V.pageHead('Column audit · ' + M.state.runId, 'Review ' + plural(items.length, 'selected correction'),
        'Nothing has moved yet. This preview is read-only; Apply is the only confirmation.'),
      h('p.recheck', { class: blocked ? 'has-blocked' : null }, icon(blocked ? 'alert' : 'check'), h('span', { text: check })),
      h('ol.preview-list', null, items.map(({ card: c, op, blocked: why }) => h('li.preview-row', {
        'data-testid': 'preview-row', 'data-preview': c.key, class: why ? 'ineligible' : null
      },
      h('div.row-head', null, h('span.key', { text: c.key }), c.kind === 'bug' ? h('span.kind-tag', { text: 'Bug' }) : null, h('span.title', { text: c.title })),
      fromTo(c),
      why
        ? h('div.callout.callout-warn', null, icon('alert'), h('div.callout-body', null,
          h('strong', { text: "Can't apply · not counted below" }), h('p', { text: why }),
          h('div.callout-actions', null, h('button.btn.sm', { type: 'button', onclick: () => A.reaudit(c) }, 'Re-audit this card'))))
        : h('div.op', null, h('p.op-verb', null, h('strong', { text: op.verb }), h('span.muted', { text: ' · ' + c.tally })),
          h('p.help', { text: op.detail }), policyInput(c))))),
      h('div.apply-bar', null,
        h('p.help', { text: 'Apply runs each card’s normal lifecycle action and records this exact review. A card that changed since this preview is skipped, never forced.' }),
        h('div.bar-actions', null,
          h('button.btn.ghost', { type: 'button', onclick: () => A.go('#run=' + M.state.runId) }, 'Back to results'),
          h('button.btn.primary.lg', { type: 'button', disabled: !ok.length, onclick: () => A.apply(ok.map((x) => x.card.key)) },
            'Apply ' + plural(ok.length, 'correction')))));
  };

  const RECEIPT = {
    applied: { label: 'Applied', icon: 'check', cls: 'ok' },
    already: { label: 'Already applied', icon: 'check', cls: 'already' },
    skipped: { label: 'Skipped', icon: 'skip', cls: 'skipped' },
    failed: { label: 'Failed', icon: 'alert', cls: 'failed' }
  };

  V.receipt = function (A) {
    const S = M.state;
    const keys = S.receiptKeys;
    const rows = keys.map((k) => ({ c: M.find(k), rc: M.receipt(k) })).filter((x) => x.c && x.rc);
    const count = (s) => rows.filter((x) => x.rc.status === s).length;
    const rest = [];
    if (count('already')) rest.push(count('already') + ' already applied');
    if (count('skipped')) rest.push(count('skipped') + ' skipped');
    if (count('failed')) rest.push(count('failed') + ' failed');
    return h('section.page.receipt', { 'aria-label': 'Apply receipt' },
      h('header.page-head', null,
        h('p.eyebrow', { text: 'Column audit · ' + S.runId + ' · receipt' }),
        h('h1', { tabindex: '-1', 'data-autofocus': '', text: count('applied') + ' of ' + plural(rows.length, 'correction') + ' applied' }),
        h('p.lede', { text: (rest.length ? rest.join(' · ') + '. ' : '') + 'The board shows the server’s result for each card.' })),
      h('ul.receipt-list', null, rows.map(({ c, rc }) => {
        const meta = RECEIPT[rc.status];
        return h('li.receipt-row', { 'data-receipt': c.key, class: 'is-' + meta.cls },
          h('p.receipt-status', null, icon(meta.icon), h('strong', { text: meta.label })),
          h('div.receipt-main', null,
            h('div.row-head', null, h('span.key', { text: c.key }), h('span.title', { text: c.title })),
            h('p', { text: rc.verb + ' · ' + rc.from + ' → ' + rc.to + (rc.resolution ? ' · resolution: ' + rc.resolution : '') }),
            rc.message ? h('p.help', { text: rc.message }) : null),
          h('div.receipt-actions', null,
            rc.status === 'failed' ? h('button.btn.sm.primary', { type: 'button', onclick: () => A.retryApply(c.key) }, icon('retry'), 'Retry') : null,
            h('button.btn.sm.ghost', { type: 'button', onclick: () => A.openCard(c) }, 'Open card')));
      })),
      h('div.row-actions', null, h('button.btn', { type: 'button', onclick: () => A.go('#run=' + S.runId) }, 'Back to results')));
  };
})();
