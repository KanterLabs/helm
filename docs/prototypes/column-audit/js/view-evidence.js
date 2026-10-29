/* Review evidence: a side panel on desktop and a full-height sheet on mobile.
   It lists each quoted criterion with its status in words and an icon, then
   the citations that support it. */
(function () {
  const { h, icon } = window.HelmDom;
  const F = window.HelmAuditFixtures;
  const M = window.HelmAuditModel;
  const V = window.HelmAuditViews;

  const STATUS = {
    supported: { word: 'Supported', icon: 'check' },
    contradicted: { word: 'Contradicted', icon: 'cross' },
    unknown: { word: 'Not verified', icon: 'question' }
  };

  function source(e) {
    if (e.src === 'pr') return h('p.cite-src.pr', null, icon('pr'), h('span', { text: 'PR #52 head @ ' + F.baseline.prShort }), h('span.tag.pr-tag', { text: 'PR head · not on beta' }));
    if (e.src === 'check') return h('p.cite-src', null, icon('check'), h('span', { text: 'Commit check' }));
    if (e.src === 'task') return h('p.cite-src', null, icon('file'), h('span', { text: 'Task and dependency facts' }));
    return h('p.cite-src', null, icon('branch'), h('span', { text: 'beta @ ' + F.baseline.short }));
  }

  function excerpt(e) {
    if (!e.excerpt) return null;
    const start = parseInt(String(e.lines).split('-')[0], 10);
    const lines = e.excerpt.split('\n');
    if (e.src === 'check' || Number.isNaN(start)) return h('p.check-result', { text: e.excerpt });
    return h('pre.excerpt', { tabindex: '0', 'aria-label': 'Excerpt from ' + e.path },
      h('code', null, lines.map((l, i) => h('span.line', null, h('span.ln', { 'aria-hidden': 'true', text: String(start + i) }), h('span.lc', { text: l || ' ' })))));
  }

  function citation(A, e) {
    const where = e.lines ? e.path + ':' + e.lines : e.path;
    const openable = e.src === 'beta' || e.src === 'pr';
    return h('li.cite', null,
      h('div.cite-head', null, h('span.tag', { class: 'tag-' + e.kind.toLowerCase().replace(/\s+/g, '-'), text: e.kind }), h('code.path', { text: where })),
      source(e), excerpt(e),
      openable ? h('button.btn.link.sm', { type: 'button', onclick: () => A.prototypeOnly('Would open ' + where + ' at ' + (e.src === 'pr' ? F.baseline.prShort : F.baseline.short) + '. Commit links are not live in this prototype.') }, 'Open at commit') : null);
  }

  function criterion(A, k, i, revoked) {
    const s = STATUS[k.status];
    return h('li.criterion', { class: 'is-' + k.status },
      h('p.crit-status', null, icon(s.icon), h('span', { text: s.word }), h('span.crit-n', { text: 'Condition ' + (i + 1) })),
      h('blockquote', { text: k.text }),
      h('p.crit-note', { text: k.note }),
      !revoked && k.cites.length ? h('ul.cites', null, k.cites.map((e) => citation(A, e))) : null);
  }

  function heading(c) {
    if (c.outcome === 'correction') {
      return h('p.transition', null, h('span.col', { text: c.column }), icon('arrow', 'arrow'), h('span.sr-only', { text: ' to ' }), h('span.col.rec', { text: c.to }));
    }
    if (c.outcome === 'correct') return h('p.state-line', null, h('span.col', { text: c.column }), h('span.pill.ok', null, icon('check'), 'Correct column'));
    return h('p.state-line', null, h('span.col', { text: c.column }), h('span.pill.unknown', null, icon('question'), "Couldn't verify"));
  }

  function selection(A, c) {
    const rc = M.receipt(c.key);
    if (c.outcome !== 'correction') return null;
    if (rc && (rc.status === 'applied' || rc.status === 'already')) return h('p.applied-badge', null, icon('check'), h('span', { text: 'Applied: ' + rc.from + ' → ' + rc.to }));
    if (c.blocker) return h('p.note.warn', { text: c.to + ' recommended · ' + c.blocker.text });
    if (c.stale) return h('p.note.warn', { text: c.stale.text + ' Re-audit before applying.' });
    if (!M.selectable(c)) return null;
    return V.selectBox(A, c, 'panel');
  }

  function technical(c) {
    const ids = c.criteria.map((k, i) => 'ev-' + c.key.toLowerCase() + '-' + (i + 1)).join(', ') || 'none';
    const pr = c.criteria.some((k) => k.cites.some((e) => e.src === 'pr'));
    const lines = ['Baseline commit: ' + F.baseline.full + (pr ? '\nPR #52 head commit: ' + F.baseline.prFull : ''),
      'Evidence IDs: ' + ids, 'Captured card version: v' + (3 + c.key.length), 'Model confidence (diagnostic only): ' + c.confidence.toFixed(2)];
    return h('details.tech', null, h('summary', { text: 'Technical details' }), h('pre', { text: lines.join('\n') }));
  }

  V.evidence = function (A, c) {
    const titleId = 'ev-title';
    const body = [];
    if (c.revoked) {
      body.push(h('div.callout.callout-warn', null, icon('lock'), h('div.callout-body', null,
        h('strong', { text: 'Evidence hidden' }),
        h('p', { text: 'Access to acme-synthetic/billing-worker was revoked, so its private excerpts were cleared. Reconnect to review them.' }))));
    }
    if (!c.criteria.length) {
      body.push(h('p.note', { text: 'This card has no goal or acceptance criteria, so there is nothing to check committed work against.' }));
    } else {
      body.push(h('h3.panel-sub', { text: 'Conditions' }), h('ol.criteria', null, c.criteria.map((k, i) => criterion(A, k, i, c.revoked))));
    }
    if (c.missing && c.missing.length) {
      body.push(h('h3.panel-sub', { text: 'Missing evidence' }), h('ul.missing', null, c.missing.map((m) =>
        h('li', null, h('strong', { text: m.text }), h('p', { text: m.prevents })))));
    }
    return h('div.panel-shell', null,
      h('div.panel-scrim', { onclick: () => A.closeEvidence() }),
      h('aside.evidence', { role: 'dialog', 'aria-modal': 'true', 'aria-labelledby': titleId, tabindex: '-1' },
        h('header.panel-head', null,
          h('div', null,
            h('p.eyebrow', { text: 'Review evidence' + (c.kind === 'bug' ? ' · Bug' : '') }),
            h('h2#' + titleId, { tabindex: '-1', text: c.key + ' · ' + c.title })),
          h('button.icon-btn', { type: 'button', 'aria-label': 'Close evidence', onclick: () => A.closeEvidence() }, icon('cross'))),
        h('div.panel-body', null,
          heading(c),
          h('p.reason', { text: c.reason }),
          c.outcome === 'unverified' ? h('div.row-actions.wrap', null, V.recoveryButtons(A, c, false).filter((b) => b.textContent !== 'Review inspected evidence')) : null,
          body,
          technical(c)),
        h('footer.panel-foot', null, selection(A, c),
          h('button.btn.ghost', { type: 'button', onclick: () => A.closeEvidence() }, 'Close'))));
  };
})();
