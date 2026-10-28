/* Prototype state, derived results and the simulated apply endpoint.
   State is kept in sessionStorage so a run deep link survives reloads. */
(function () {
  const F = window.HelmAuditFixtures;
  const KEY = 'helm-column-audit-prototype';
  const STAGES = [
    { name: 'Reading committed work', ms: 1000 },
    { name: 'Checking card criteria', ms: 1000 },
    { name: 'Preparing corrections', ms: 700 }
  ];
  const OPS = {
    complete: { verb: 'Mark done', detail: 'Completes the card through the normal completion rules: checklist and dependencies are rechecked.' },
    reopen: { verb: 'Reopen', detail: 'Reopens the card. Its completion history, comments and links are kept.' },
    start: { verb: 'Move to In progress', detail: 'A normal state change by you. No agent claim is created.' },
    resolve: { verb: 'Resolve as fixed', detail: 'Uses the normal bug resolution. The resolution below is recorded on the bug.' },
    reopenBug: { verb: 'Reopen bug', detail: 'Reopens the bug through its normal workflow. The reason below is recorded.' }
  };

  const fresh = () => ({ scope: { mode: 'all', columns: [], ref: F.project.savedRef, qaMeaning: '' }, runs: [], runId: null,
    selected: [], resolution: {}, reopenReason: {}, receipts: {}, previewKeys: [], receiptKeys: [], lastApplyResponse: null, tab: null });

  let S = load();
  function load() {
    try { return Object.assign(fresh(), JSON.parse(sessionStorage.getItem(KEY) || 'null') || {}); }
    catch { return fresh(); }
  }
  function save() { try { sessionStorage.setItem(KEY, JSON.stringify(S)); } catch { /* storage disabled */ } }

  const columnCount = (name) => F.cards.filter((c) => c.column === name).length;
  const scopeCards = (scope) => F.cards.filter((c) => scope.mode === 'all' || scope.columns.includes(c.column));
  const run = (id) => S.runs.find((r) => r.id === (id || S.runId)) || null;

  // One result per card in scope; the custom-column meaning picks a variant.
  function results(r) {
    r = r || run();
    if (!r) return [];
    return scopeCards(r.scope).map((c) => {
      const v = c.variants && r.scope.qaMeaning ? c.variants[r.scope.qaMeaning] : null;
      return Object.assign({}, c, v || {});
    });
  }
  function groups(r) {
    const all = results(r);
    return {
      all,
      corrections: all.filter((c) => c.outcome === 'correction'),
      correct: all.filter((c) => c.outcome === 'correct'),
      unverified: all.filter((c) => c.outcome === 'unverified')
    };
  }
  const find = (key) => results().find((c) => c.key === key) || null;
  const receipt = (key) => (S.receipts[S.runId] || {})[key] || null;

  function selectable(c) {
    if (c.outcome !== 'correction' || c.blocker || c.stale) return false;
    const rc = receipt(c.key);
    return !rc || rc.status === 'failed';
  }
  function toggle(key, on) {
    const has = S.selected.includes(key);
    if (on && !has) S.selected.push(key);
    if (!on && has) S.selected = S.selected.filter((k) => k !== key);
    save();
  }

  function startRun() {
    const id = 'run-' + String(412 + S.runs.length).padStart(4, '0');
    S.runs.push({ id, status: 'running', startedAt: Date.now(), scope: JSON.parse(JSON.stringify(S.scope)) });
    S.runId = id; S.selected = []; S.tab = null; save();
    return id;
  }
  function progress(r) {
    const elapsed = Date.now() - r.startedAt;
    let t = 0;
    for (let i = 0; i < STAGES.length; i++) {
      t += STAGES[i].ms;
      if (elapsed < t) return { stage: i, frac: 1 - (t - elapsed) / STAGES[i].ms, done: false };
    }
    return { stage: STAGES.length - 1, frac: 1, done: true };
  }
  function finish(r) { r.status = 'done'; save(); }
  function cancel(r) { r.status = 'canceled'; r.canceledStage = STAGES[progress(r).stage].name; save(); }

  function stats(r) {
    const cards = scopeCards(r.scope);
    const paths = new Set();
    let criteria = 0;
    cards.forEach((c) => { criteria += c.criteria.length; c.criteria.forEach((k) => k.cites.forEach((e) => { if (e.src !== 'check' && e.src !== 'task') paths.add(e.path); })); });
    return { cards: cards.length, done: cards.filter((c) => c.column === 'Done').length, criteria, files: paths.size };
  }

  // Preview revalidation: the exact selected corrections, rechecked now.
  function preview() {
    S.previewKeys = S.selected.slice(); save();
    return S.previewKeys.map(find).filter(Boolean).map((c) => ({ card: c, op: OPS[c.op], blocked: c.previewBlock || null }));
  }

  function attempt(key, c) {
    const plan = F.applyScript[key] || 'applied';
    const prev = receipt(key);
    if (plan === 'failedOnce') return prev && prev.status === 'failed' ? 'applied' : 'failed';
    return plan;
  }
  function record(c, status, attempts) {
    const res = { status, from: c.column, to: c.to, verb: OPS[c.op].verb, attempts, message: status === 'applied' ? null : F.applyMessages[status] };
    if (c.kind === 'bug' && c.op === 'resolve') res.resolution = S.resolution[c.key] || 'fixed';
    if (c.op === 'reopenBug') res.reason = S.reopenReason[c.key] || c.reopenReason;
    S.receipts[S.runId] = S.receipts[S.runId] || {};
    S.receipts[S.runId][c.key] = res;
    return res;
  }
  function receiptJSON(c, res) {
    return { card: c.key, operation: res.verb, from: res.from, to: res.to, status: res.status,
      receipt_id: 'rcpt-' + S.runId + '-' + c.key, idempotency_key: S.runId + ':' + c.key + ':' + F.baseline.short,
      attempt: res.attempts, resolution: res.resolution, reason: res.reason, message: res.message || undefined };
  }

  // Simulated server response; deterministic so it can be saved as an artifact.
  function apply(keys) {
    const out = keys.map((k) => {
      const c = find(k);
      const prev = receipt(k);
      const res = record(c, attempt(k, c), prev ? prev.attempts + 1 : 1);
      return receiptJSON(c, res);
    });
    S.receiptKeys = keys.slice();
    S.selected = S.selected.filter((k) => !keys.includes(k) || (receipt(k) || {}).status === 'failed');
    S.lastApplyResponse = { prototype: true, run: S.runId, baseline: F.project.repo + '@' + F.baseline.ref + ':' + F.baseline.short, receipts: out };
    save();
    return S.lastApplyResponse;
  }
  function retry(key) {
    const c = find(key);
    const res = record(c, attempt(key, c), receipt(key).attempts + 1);
    if (res.status !== 'failed') S.selected = S.selected.filter((k) => k !== key);
    save();
    return receiptJSON(c, res);
  }

  window.HelmAuditModel = {
    STAGES, OPS, get state() { return S; }, save, reset() { S = fresh(); save(); },
    columnCount, scopeCards, run, results, groups, find, receipt, selectable, toggle,
    startRun, progress, finish, cancel, stats, preview, apply, retry
  };
})();
