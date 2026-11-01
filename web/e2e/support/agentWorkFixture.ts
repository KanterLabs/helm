// Creates the two-project live-work fixture through the public API and makes
// selected tasks look stale or expired only at the browser read boundary.
import { expect, type APIRequestContext, type Page } from '@playwright/test';
import { routeAgentWork } from './agentWorkRoutes';
import {
  ageTaskInPayload,
  collectionData,
  columnFor,
  etagForVersion,
  expireTaskClaimInPayload,
  getJSON,
  postJSON,
  type AgentWork,
  type Collection,
  type Column,
  type Project,
  type Task
} from './agentWorkApi';

export type AgentWorkTasks = {
  working: Task;
  waiting: Task;
  verifying: Task;
  stale: Task;
  staleWaiting: Task;
  completed: Task;
  missing: Task;
};

export type AgentWorkFixture = {
  request: APIRequestContext;
  suffix: string;
  projectA: Project;
  projectB: Project;
  projectAName: string;
  projectBName: string;
  activeA: Column;
  blockedA: Column;
  readyB: Column;
  activeB: Column;
  tasks: AgentWorkTasks;
  remoteSummary: string;
  mutationKey: () => string;
  publish: (task: Task, state: AgentWork['state'], phase: string, summary: string, nextAction: string, refs: string[], completed: number, total: number) => Promise<Task>;
  eventReads: () => number;
};

export async function createAgentWorkFixture(page: Page, request: APIRequestContext): Promise<AgentWorkFixture> {
  const status = await getJSON<{ mode?: string }>(request, '/api/v1/auth/status');
  expect(status.mode, 'The E2E server must run with HELM_AUTH_MODE=disabled').toBe('disabled');

  const runID = `${Date.now().toString(36)}${Math.random().toString(36).slice(2, 7)}`.toUpperCase();
  const suffix = runID.slice(-8);
  const projectAName = `Agent Work Alpha ${suffix}`;
  const projectBName = `Agent Work Beta ${suffix}`;
  const projectAKey = `AWA${suffix}`.slice(0, 16);
  const projectBKey = `AWB${suffix}`.slice(0, 16);
  let mutationNumber = 0;
  const mutationKey = () => `agent-work-e2e-${runID}-${++mutationNumber}`;

  // Create both projects and all tasks through the public HTTP API. Keeping
  // the fixtures in two projects exercises the cross-project My Work view.
  const projectA = await postJSON<Project>(request, '/api/v1/projects', {
    key: projectAKey,
    name: projectAName,
    description: 'Cross-project live work acceptance fixture.'
  }, { 'Idempotency-Key': mutationKey() });
  const projectB = await postJSON<Project>(request, '/api/v1/projects', {
    key: projectBKey,
    name: projectBName,
    description: 'Cross-project live work acceptance fixture.'
  }, { 'Idempotency-Key': mutationKey() });

  const columnsA = collectionData(await getJSON<Collection<Column> | Column[]>(request, `/api/v1/projects/${projectA.id}/columns?limit=20`));
  const columnsB = collectionData(await getJSON<Collection<Column> | Column[]>(request, `/api/v1/projects/${projectB.id}/columns?limit=20`));
  const activeA = columnFor(columnsA, 'active');
  const blockedA = columnFor(columnsA, 'blocked');
  const readyB = columnFor(columnsB, 'ready');
  const activeB = columnFor(columnsB, 'active');

  const createTask = async (project: Project, column: Column, title: string): Promise<Task> =>
    postJSON<Task>(request, `/api/v1/projects/${project.id}/tasks`, {
      title,
      column_id: column.id,
      priority: 'normal'
    }, { 'Idempotency-Key': mutationKey() });

  let workingTask = await createTask(projectA, activeA, `Working pulse ${suffix}`);
  let waitingTask = await createTask(projectA, blockedA, `Waiting pulse ${suffix}`);
  let verifyingTask = await createTask(projectB, readyB, `Verifying pulse ${suffix}`);
  let staleTask = await createTask(projectB, activeB, `Stale pulse ${suffix}`);
  let staleWaitingTask = await createTask(projectB, activeB, `Stale dependency wait ${suffix}`);
  let completedTask = await createTask(projectA, activeA, `Completed pulse ${suffix}`);
  const missingTask = await createTask(projectA, activeA, `Missing pulse ${suffix}`);

  const claimTask = async (task: Task): Promise<Task> =>
    postJSON<Task>(request, `/api/v1/tasks/${task.id}/claim`, { lease_seconds: 3600 }, {
      'If-Match': etagForVersion(task.version),
      'Idempotency-Key': mutationKey()
    });

  workingTask = await claimTask(workingTask);
  waitingTask = await claimTask(waitingTask);
  verifyingTask = await claimTask(verifyingTask);
  staleTask = await claimTask(staleTask);
  staleWaitingTask = await claimTask(staleWaitingTask);
  completedTask = await claimTask(completedTask);

  const publish = async (task: Task, state: AgentWork['state'], phase: string, summary: string, nextAction: string, refs: string[], completed: number, total: number): Promise<Task> =>
    postJSON<Task>(request, `/api/v1/tasks/${task.id}/progress`, {
      operation_id: `agent-work/${runID}/${task.key}`,
      state,
      phase,
      summary,
      next_action: nextAction,
      checkpoint_refs: refs,
      checkpoint_completed: completed,
      checkpoint_total: total
    }, {
      'If-Match': etagForVersion(task.version),
      'Idempotency-Key': mutationKey()
    });

  workingTask = await publish(
    workingTask,
    'working',
    'Implementing the browser workflow',
    'The live working pulse is visible on the board.',
    'Wait for the remote progress event.',
    ['board', 'drawer'],
    1,
    2
  );
  waitingTask = await publish(
    waitingTask,
    'waiting',
    'Dependency review',
    'Waiting for the API fixture to become available.',
    'Confirm the fixture and continue.',
    ['dependency', 'decision'],
    1,
    2
  );
  verifyingTask = await publish(
    verifyingTask,
    'verifying',
    'QA verification',
    'The cross-project rendering is ready for a final check.',
    'Run the acceptance checks.',
    ['rendering', 'acceptance'],
    1,
    2
  );
  staleTask = await publish(
    staleTask,
    'working',
    'Background worker',
    'This pulse will be aged at the read boundary.',
    'Restart the worker after review.',
    ['heartbeat'],
    0,
    1
  );
  staleWaitingTask = await publish(
    staleWaitingTask,
    'waiting',
    'Waiting on a stale dependency',
    'This waiting pulse will also be aged at the read boundary.',
    'Escalate the dependency if it remains unanswered.',
    ['dependency', 'stale'],
    1,
    2
  );
  completedTask = await publish(
    completedTask,
    'waiting',
    'Finished work history',
    'This retained snapshot must not remain live after completion.',
    'No action remains.',
    ['implementation', 'completion'],
    2,
    2
  );
  completedTask = await postJSON<Task>(request, `/api/v1/tasks/${completedTask.id}/complete`, {}, {
    'If-Match': etagForVersion(completedTask.version),
    'Idempotency-Key': mutationKey()
  });

  const staleReadTimestamp = new Date(Date.now() - 16 * 60 * 1000).toISOString();
  const expiredClaimReadTimestamp = new Date(Date.now() - 60 * 1000).toISOString();
  let eventReads = 0;
  await routeAgentWork(page, '**/api/v1/**', async (route) => {
    const url = new URL(route.request().url());
    const pathname = url.pathname;
    const isEventRead = route.request().method() === 'GET' && pathname === '/api/v1/events';
    const isTaskRead = route.request().method() === 'GET' && (
      pathname === '/api/v1/my-work' ||
      /\/api\/v1\/projects\/[^/]+\/tasks$/.test(pathname) ||
      /^\/api\/v1\/tasks\/[^/]+$/.test(pathname)
    );

    if (!isEventRead && !isTaskRead) {
      await route.continue();
      return;
    }

    if (isEventRead) eventReads += 1;
    const response = await route.fetch();
    if (!response.ok()) {
      await route.fulfill({ response });
      return;
    }

    const payload = await response.json() as unknown;
    if (isTaskRead) {
      ageTaskInPayload(payload, staleTask.id, staleReadTimestamp);
      ageTaskInPayload(payload, staleWaitingTask.id, staleReadTimestamp);
      ageTaskInPayload(payload, completedTask.id, staleReadTimestamp);
      // The API has no clock-setting hook, so make this fixture's own claim
      // look expired only at the browser read boundary. This verifies that an
      // expired own claim offers Claim rather than Renew without waiting.
      expireTaskClaimInPayload(payload, staleTask.id, expiredClaimReadTimestamp);
    }
    await route.fulfill({ response, json: payload });
  });

  return {
    request,
    suffix,
    projectA,
    projectB,
    projectAName,
    projectBName,
    activeA,
    blockedA,
    readyB,
    activeB,
    tasks: {
      working: workingTask,
      waiting: waitingTask,
      verifying: verifyingTask,
      stale: staleTask,
      staleWaiting: staleWaitingTask,
      completed: completedTask,
      missing: missingTask
    },
    remoteSummary: `Remote progress survived the poll refresh ${suffix}.`,
    mutationKey,
    publish,
    eventReads: () => eventReads
  };
}
