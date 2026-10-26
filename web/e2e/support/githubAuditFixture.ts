import type { Route } from '@playwright/test';
import { API, error, json, project, SHA_API, SHA_WEB, stamp, WEB, type Json } from './githubShared';

// Synthetic server-run GitHub audit routes: runs, durable jobs, evidence and apply.

export const auditRun = (id: string, status: string, extra: Json = {}) => ({
  id, project_id: project.id, actor_id: 'human-gh', scope: 'github', status,
  started_at: stamp, created_at: stamp, updated_at: stamp, finding_count: 0, ...extra
});

const stages = ['queued', 'fetching', 'analyzing', 'validating', 'finished'];

export function evidenceManifest(auditId: string): Json {
  return {
    audit_id: auditId, captured_at: stamp, partial: true, omitted_files: 1, omissions: ['assets/logo.png: binary file omitted'],
    // Captured identity: titles and keys survive board pagination and later edits.
    tasks: [
      { id: 'task-1', key: 'GHB-1', title: 'Link PRs to tasks', description: '', version: 3, column_id: 'col-active', semantic_state: 'active', column_name: 'In progress', criteria: ['Drawer keeps focus'] },
      { id: 'task-2', key: 'GHB-2', title: 'Show verified PR status', description: '', version: 2, column_id: 'col-backlog', semantic_state: 'backlog', column_name: 'Backlog', criteria: [] },
      { id: 'task-77', key: 'GHB-77', title: 'Off-page task from page 51', description: '', version: 1, column_id: 'col-backlog', semantic_state: 'backlog', column_name: 'Backlog', criteria: [] }
    ],
    repositories: [{ repository: WEB, ref: 'main', sha: SHA_WEB }, { repository: API, ref: 'main', sha: SHA_API }],
    items: [
      { id: 'ev-check-1', kind: 'check', title: 'web-e2e failed', url: 'https://github.com/demo-org/helm-web/runs/9001', repository: WEB, sha: SHA_WEB, text: 'keeps focus after linking a PR\nexpected: PR row\nreceived: body' },
      { id: 'ev-file-1', kind: 'file', title: 'Drawer section', url: `https://github.com/demo-org/helm-web/blob/${SHA_WEB}/src/Drawer.svelte#L10-L12`, repository: WEB, sha: SHA_WEB, path: 'src/Drawer.svelte', start_line: 10, end_line: 12, text: '<section aria-labelledby="pr">\n  <h3 id="pr">Pull requests</h3>\n</section>' },
      { id: 'ev-pr-1', kind: 'pull_request', title: 'helm-api #212 merged', url: 'javascript:alert(1)', repository: API, sha: SHA_API, text: 'Store task to PR links' }
    ]
  };
}

export function serverFindings(auditId: string): Json[] {
  const base = { audit_id: auditId, review_state: 'pending', version: 1, changed_since_audit: false, created_at: stamp, updated_at: stamp };
  return [
    { ...base, id: 'gf-move', task_id: 'task-2', captured_version: 2, source_column: 'col-backlog', verdict: 'move_proposed', proposed_semantic_destination: 'ready', confidence: 0.86, reason: 'Its storage prerequisite merged; criteria are ready.', evidence_refs: ['ev-pr-1'] },
    { ...base, id: 'gf-attention', task_id: 'task-1', captured_version: 3, source_column: 'col-active', verdict: 'needs_attention', confidence: 0.7, reason: 'Notes say done, but web-e2e fails on the captured head.', evidence_refs: ['ev-check-1', 'ev-file-1'] },
    { ...base, id: 'gf-offpage', task_id: 'task-77', captured_version: 1, source_column: 'col-backlog', verdict: 'move_proposed', proposed_semantic_destination: 'ready', confidence: 0.55, reason: 'Off-page task captured by the server run.', evidence_refs: ['legacy:note'] }
  ];
}

type AuditMockContext = {
  findTask: (id: string) => Json | undefined;
  options: { evidenceError?: boolean };
};

export function createAuditMock({ findTask, options }: AuditMockContext) {
  const runs: Json[] = [auditRun('audit-external', 'complete', { scope: 'board', finalized_at: stamp, started_at: '2026-10-20T10:00:00Z' })];
  const jobs = new Map<string, Json>();
  const findings = new Map<string, Json[]>([['audit-external', []]]);
  let nextAudit = 1;

  /** Returns null when the request is not an audit route. */
  function handle(route: Route, method: string, path: string, parts: string[], body: any): Promise<void> | null {
    if (path === `/projects/${project.id}/audits`) return json(route, { data: [...runs].reverse(), next_cursor: null });
    if (path === `/projects/${project.id}/github-audits` && method === 'POST') {
      const active = [...jobs.values()].find((job) => job.stage !== 'finished');
      if (active) return error(route, 409, 'audit_in_progress', 'An audit is already running for this project.', { audit_id: active.audit_id });
      return json(route, createRun(), 202);
    }
    if (parts[0] === 'audits' && parts[1]) {
      const auditId = parts[1];
      const run = runs.find((item) => item.id === auditId);
      if (!run) return error(route, 404, 'not_found', 'Audit not found');
      const job = jobs.get(auditId);
      if (parts.length === 2) return json(route, run);
      if (parts[2] === 'findings') return json(route, { data: findings.get(auditId) || [], next_cursor: null });
      if (!job) return error(route, 404, 'not_found', 'This audit has no server job.');
      if (parts[2] === 'job') {
        if (job.stage !== 'finished') advance(job, run);
        return json(route, job);
      }
      if (parts[2] === 'cancel') {
        Object.assign(job, { stage: 'finished', outcome: 'cancelled', message: 'Cancelled by Synthetic reviewer.', finished_at: new Date().toISOString() });
        run.status = 'failed';
        return json(route, job);
      }
      if (parts[2] === 'retry') return json(route, createRun(auditId));
      if (parts[2] === 'evidence') {
        return options.evidenceError ? error(route, 403, 'evidence_unavailable', 'Evidence access was revoked for this repository.') : json(route, evidenceManifest(auditId));
      }
      if (parts[2] === 'apply') {
        const ids: string[] = body?.finding_ids || [];
        const data = ids.map((id) => {
          const finding = (findings.get(auditId) || []).find((item) => item.id === id);
          if (!finding || finding.verdict !== 'move_proposed') return { finding_id: id, task_id: finding?.task_id || '', outcome: 'skipped', message: 'No safe move for this finding.' };
          if (finding.task_id === 'task-77') return { finding_id: id, task_id: finding.task_id, outcome: 'needs_follow_up', message: 'Task changed after capture; review it manually.' };
          if (body.preview) return { finding_id: id, task_id: finding.task_id, outcome: 'eligible', message: 'Backlog → Ready is safe.' };
          const moved = findTask(finding.task_id)!;
          Object.assign(moved, { column_id: 'col-ready', version: moved.version + 1 });
          return { finding_id: id, task_id: finding.task_id, outcome: 'applied', message: 'Moved to Ready.', task: moved };
        });
        return json(route, { data });
      }
    }
    if (parts[0] === 'audit-findings' && method === 'PATCH') {
      for (const list of findings.values()) {
        const finding = list.find((item) => item.id === parts[1]);
        if (finding) {
          Object.assign(finding, body, { version: finding.version + 1 });
          return json(route, finding);
        }
      }
      return error(route, 404, 'not_found', 'Finding not found');
    }
    return null;
  }

  function createRun(retryOf = ''): Json {
    const id = `audit-gh-${nextAudit++}`;
    const run = auditRun(id, 'queued');
    const job = {
      audit_id: id, project_id: project.id, stage: 'queued', outcome: '', created_at: stamp, updated_at: stamp,
      task_count: 0, captured_files: 0, omitted_files: 0, message: 'Waiting for a worker.', ...(retryOf ? { retry_of: retryOf } : {})
    };
    runs.push(run);
    jobs.set(id, job);
    findings.set(id, []);
    return { audit: run, job };
  }

  function advance(job: Json, run: Json): void {
    // Each job read moves one real stage forward so the UI can observe each one.
    job.polls = (job.polls || 0) + 1;
    if (job.paused) return;
    const next = stages[Math.min(stages.indexOf(job.stage) + 1, stages.length - 1)];
    Object.assign(job, { stage: next, updated_at: new Date().toISOString(), task_count: 4, captured_files: next === 'queued' ? 0 : 6, omitted_files: next === 'queued' ? 0 : 1 });
    run.status = next === 'finished' ? 'partial' : 'running';
    if (next === 'finished') {
      Object.assign(job, { outcome: 'partial', message: 'One binary file was omitted.', finished_at: new Date().toISOString() });
      findings.set(job.audit_id, serverFindings(job.audit_id));
      run.finding_count = 3;
      run.finalized_at = job.finished_at;
    }
  }

  return { jobs, handle };
}
