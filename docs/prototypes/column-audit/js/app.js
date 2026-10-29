/* Controller: hash routing, actions, focus management and theme.
   Routes: "" home · #scope · #run=ID[&view=preview|receipt][&evidence=KEY] */
(function () {
  const { h, icon, announce, focusables } = window.HelmDom;
  const F = window.HelmAuditFixtures;
  const M = window.HelmAuditModel;
  const V = window.HelmAuditViews;
  const main = document.getElementById('view');
  const panelHost = document.getElementById('panel-host');

  let current = { view: null, run: null };
  let ticker = 0;
  let opener = null;       // element or focus key that opened the evidence panel
  let pushedEvidence = false;
  let pendingFocus = null;

  function parse() {
    const raw = location.hash.replace(/^#/, '');
    if (raw === 'scope') return { view: 'scope' };
    const p = new URLSearchParams(raw);
    if (!p.get('run')) return { view: 'home' };
    return { run: p.get('run'), view: p.get('view') || 'results', evidence: p.get('evidence') };
  }
  const runHash = (id, view) => '#run=' + id + (view && view !== 'results' ? '&view=' + view : '');

  function go(hash, replace) {
    if (replace) { history.replaceState(null, '', hash || location.pathname + location.search); route(); }
    else if (location.hash === hash || (!hash && !location.hash)) route();
    else location.hash = hash;
  }

  function focusKey(el) {
    if (!el || el === document.body) return null;
    return el.dataset.fk || (el.id ? '#' + el.id : el.name ? el.name + ':' + el.value : null);
  }
  function findKey(key) {
    if (!key) return null;
    if (key.startsWith('#')) return document.getElementById(key.slice(1));
    return document.querySelector('[data-fk="' + key + '"]') ||
      [...document.querySelectorAll('input[name]')].find((el) => el.name + ':' + el.value === key) || null;
  }

  function build(view, r) {
    if (view === 'scope') return V.scope(A);
    if (!r) return V.home(A);
    if (r.status === 'running') return V.running(A, r);
    if (r.status === 'canceled') return V.canceled(A, r);
    if (view === 'preview' && M.state.selected.length) return V.preview(A);
    if (view === 'receipt' && M.state.receiptKeys.length) return V.receipt(A);
    return V.results(A, r);
  }

  function render(keepFocus) {
    const key = keepFocus ? focusKey(document.activeElement) : null;
    const r = current.run ? M.run(current.run) : null;
    main.replaceChildren(build(current.view, r));
    V.reviewBar(A);
    if (r && r.status === 'running') startTicker(r);
    const target = (pendingFocus && findKey(pendingFocus)) || findKey(key);
    pendingFocus = null;
    if (target) target.focus();
    else if (!keepFocus) { const h1 = main.querySelector('[data-autofocus]'); if (h1) h1.focus({ preventScroll: true }); }
  }

  function route() {
    const p = parse();
    const r = p.run ? M.run(p.run) : null;
    if (p.run && !r) {
      current = { view: 'home', run: null };
      render();
      announce('That audit isn’t in this browser session. The prototype keeps runs in session storage.', true);
      return;
    }
    if (r) { M.state.runId = r.id; M.save(); }
    const changed = current.view !== p.view || current.run !== (r && r.id);
    current = { view: p.view, run: r ? r.id : null };
    if (changed) { render(); if (p.view !== 'results') window.scrollTo(0, 0); }
    syncPanel(p.evidence && r && r.status === 'done' && p.view === 'results' ? p.evidence : null);
  }

  function startTicker(r) {
    if (ticker) return;
    ticker = setInterval(() => {
      const run = M.run(r.id);
      if (!run || run.status !== 'running') { clearInterval(ticker); ticker = 0; return; }
      const p = M.progress(run);
      const el = document.getElementById('progress');
      if (p.done) {
        clearInterval(ticker); ticker = 0;
        M.finish(run);
        const g = M.groups(run);
        render();
        announce('Audit finished. ' + V.plural(g.corrections.length, 'correction') + ' suggested.');
      } else if (el) {
        el.replaceChildren(V.progressList(run));
      }
    }, 150);
  }

  // Evidence panel ------------------------------------------------------
  function syncPanel(key) {
    const open = panelHost.firstChild;
    if (!key) { if (open) closePanelDom(); return; }
    const c = M.find(key);
    if (!c) return;
    panelHost.replaceChildren(V.evidence(A, c));
    document.body.classList.add('panel-open');
    const dialog = panelHost.querySelector('[role="dialog"]');
    dialog.querySelector('h2').focus();
  }
  function closePanelDom() {
    panelHost.replaceChildren();
    document.body.classList.remove('panel-open');
    let target = opener && opener.isConnected ? opener : findKey(opener && opener.dataset ? opener.dataset.fk : opener);
    if (target) target.focus();
    opener = null;
  }
  function closeEvidence() {
    if (!panelHost.firstChild) return;
    if (pushedEvidence) { pushedEvidence = false; closePanelDom(); history.back(); return; }
    history.replaceState(null, '', runHash(M.state.runId));
    closePanelDom();
  }

  document.addEventListener('keydown', (e) => {
    const dialog = panelHost.querySelector('[role="dialog"]');
    if (!dialog) return;
    if (e.key === 'Escape') { e.preventDefault(); closeEvidence(); return; }
    if (e.key !== 'Tab') return;
    const f = focusables(dialog);
    if (!f.length) return;
    const first = f[0]; const last = f[f.length - 1];
    if (e.shiftKey && (document.activeElement === first || !dialog.contains(document.activeElement))) { e.preventDefault(); last.focus(); }
    else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus(); }
  });
  window.addEventListener('hashchange', () => {
    if (pushedEvidence && !parse().evidence) pushedEvidence = false;
    route();
  });

  // Actions -------------------------------------------------------------
  const A = {
    go,
    view: () => current.view,
    rerender: () => render(true),
    start() {
      const id = M.startRun();
      announce('Audit started for ' + V.plural(M.scopeCards(M.state.scope).length, 'card') + '.');
      go(runHash(id));
    },
    cancel() {
      const r = M.run();
      if (!r || r.status !== 'running') return;
      M.cancel(r); clearInterval(ticker); ticker = 0;
      render();
      announce('Audit canceled. No cards were changed.');
    },
    retryRun() {
      const prev = M.run();
      if (prev) M.state.scope = JSON.parse(JSON.stringify(prev.scope));
      A.start();
    },
    setTab(id) {
      M.state.tab = id; M.save();
      pendingFocus = 'tab-' + id;
      render(true);
    },
    select(key, on) {
      M.toggle(key, on);
      document.querySelectorAll('[data-select="' + key + '"]').forEach((el) => { el.checked = on; });
      V.reviewBar(A);
      announce((on ? 'Selected ' : 'Removed ') + key + '. ' + V.plural(M.state.selected.length, 'correction') + ' selected.');
    },
    clearSelection() {
      M.state.selected.slice().forEach((k) => A.select(k, false));
      const h1 = main.querySelector('h1'); if (h1) h1.focus();
    },
    openPreview() { go(runHash(M.state.runId, 'preview')); },
    apply(keys) {
      const res = M.apply(keys);
      go(runHash(M.state.runId, 'receipt'), true);
      const applied = res.receipts.filter((x) => x.status === 'applied').length;
      announce(applied + ' of ' + V.plural(keys.length, 'correction') + ' applied. Review each card’s result.');
    },
    retryApply(key) {
      const out = M.retry(key);
      render();
      announce(key + ': ' + (out.status === 'applied' ? 'applied on retry.' : 'still failing.'), true);
    },
    openEvidence(key, el) {
      opener = el;
      pushedEvidence = true;
      location.hash = runHash(M.state.runId) + '&evidence=' + encodeURIComponent(key);
    },
    closeEvidence,
    openCard(c) { A.prototypeOnly('Would open ' + c.key + ' in the card drawer. The audit stays open behind it.'); },
    reaudit(c) { A.prototypeOnly('Would start a new audit of ' + c.key + ' only, with a fresh source capture. Nothing re-runs automatically.'); },
    recover(c, label, el) {
      if (label === 'Review inspected evidence') return A.openEvidence(c.key, el);
      if (label === 'Choose column meaning') { closeEvidence(); pendingFocus = '#qa-meaning'; return go('#scope'); }
      if (label === 'Re-audit this card') return A.reaudit(c);
      const msg = {
        'Reconnect repository': 'Would open the GitHub connection for acme-synthetic/billing-worker.',
        'Refine source': 'Would let you attach a path, route or PR to ' + c.key + ' before re-auditing it.',
        'Add acceptance criteria': 'Would open ' + c.key + ' with its acceptance criteria field focused.'
      }[label];
      A.prototypeOnly(msg || label);
    },
    prototypeOnly(msg) { announce('Prototype: ' + msg, true); }
  };

  // Theme ---------------------------------------------------------------
  const q = new URLSearchParams(location.search);
  const preferDark = window.matchMedia && window.matchMedia('(prefers-color-scheme: dark)').matches;
  let theme = q.get('theme') || (preferDark ? 'dark' : 'light');
  const themeBtn = document.getElementById('theme-toggle');
  function applyTheme() {
    document.documentElement.dataset.theme = theme;
    const next = theme === 'dark' ? 'light' : 'dark';
    themeBtn.replaceChildren(icon(theme === 'dark' ? 'sun' : 'moon'), h('span.sr-only', { text: 'Switch to ' + next + ' theme' }));
    themeBtn.setAttribute('aria-pressed', String(theme === 'dark'));
  }
  themeBtn.addEventListener('click', () => { theme = theme === 'dark' ? 'light' : 'dark'; applyTheme(); });
  applyTheme();

  window.helmAuditPrototype = {
    get lastApplyResponse() { return M.state.lastApplyResponse; },
    reset() { M.reset(); go('', true); }
  };
  route();
})();
