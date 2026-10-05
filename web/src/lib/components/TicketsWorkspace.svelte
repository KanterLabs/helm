<script lang="ts">
  import { onDestroy, onMount, tick } from 'svelte';
  import { api } from '../api';
  import { renderMarkdown } from '../markdown';
  import { actorId } from '../state';
  import { ApiError, type Actor, type Column, type Comment, type Project, type PublicEndpointView, type Task, type TicketCounts, type TicketQueue, type TicketStatus } from '../types';
  import AlertSourcePanel from './AlertSourcePanel.svelte';
  import TicketIntegrations from './TicketIntegrations.svelte';

  export let user: Actor;
  export let projects: Project[] = [];
  export let actorNames: Record<string, string> = {};
  export let onOpenTask: (task: Task) => void | Promise<void> = () => undefined;
  export let onChanged: () => void = () => undefined;

  const queues: { value: TicketQueue; label: string }[] = [
    { value: 'open', label: 'All open' },
    { value: 'mine', label: 'My open' },
    { value: 'needs_triage', label: 'Needs triage' },
    { value: 'ready', label: 'Ready' },
    { value: 'in_progress', label: 'In progress' },
    { value: 'waiting', label: 'Waiting' },
    { value: 'completed', label: 'Completed' }
  ];
  const statusLabels: Record<TicketStatus, string> = {
    needs_triage: 'Needs triage',
    ready: 'Ready',
    in_progress: 'In progress',
    waiting: 'Waiting',
    completed: 'Completed'
  };
  const statusStates: Record<TicketStatus, Column['semantic_state']> = {
    needs_triage: 'backlog',
    ready: 'ready',
    in_progress: 'active',
    waiting: 'blocked',
    completed: 'completed'
  };
  const priorities: Task['priority'][] = ['urgent', 'high', 'normal', 'low'];
  const priorityLabels: Record<string, string> = { urgent: 'Urgent', high: 'High', normal: 'Normal', low: 'Low' };

  let queue: TicketQueue = 'open';
  let projectFilter = '';
  let query = '';
  let selectedKey = '';
  let tickets: Task[] = [];
  let counts: TicketCounts | null = null;
  let nextCursor = '';
  let listLoading = false;
  let listError = '';
  let listRequest = 0;
  let detail: Task | null = null;
  let detailLoading = false;
  let detailError = '';
  let detailRequest = 0;
  let comments: Comment[] = [];
  let noteDraft = '';
  let notePosting = false;
  let actionPending = false;
  let actionStatus: { kind: 'pending' | 'saved' | 'conflict' | 'error'; message: string } | null = null;
  let movedNotice = '';
  let waitOpen = false;
  let waitReason = '';
  let creating = false;
  let connectOpen = false;
  // Admins see at a glance whether this Helm is reachable from the internet.
  let publicAccess: PublicEndpointView | null = null;
  let createTitle = '';
  let createDescription = '';
  let createPriority: Task['priority'] = 'normal';
  let createAssignMe = true;
  let createError = '';
  let createSaving = false;
  let columnsByProject: Record<string, Column[]> = {};
  let searchInput: HTMLInputElement | null = null;
  let listElement: HTMLElement | null = null;
  let detailHeading: HTMLElement | null = null;
  let searchTimer: ReturnType<typeof setTimeout> | undefined;
  let refreshTimer: ReturnType<typeof setInterval> | undefined;

  // The ticket queue (docs/TICKET_QUEUE_PLAN.md): every new ticket lands
  // there; triage files it into a project when it belongs to one.
  let ticketQueue: { project_id: string; key: string } | null = null;
  let fileProject = '';
  $: projectById = new Map(projects.map((project) => [project.id, project]));
  $: workProjects = projects.filter((project) => !project.system_kind && !project.archived_at);
  $: if (detail) fileProject = filedProject(detail)?.id || '';

  function filedProject(task: Task): Project | undefined {
    const project = projectById.get(task.project_id);
    if (!project || project.system_kind || task.project_id === ticketQueue?.project_id) return undefined;
    return project;
  }

  export function searchElement(): HTMLInputElement | null {
    return searchInput;
  }

  function validQueue(value: string | null): TicketQueue {
    return queues.some((item) => item.value === value) ? value as TicketQueue : 'open';
  }

  function onTicketsRoute(): boolean {
    return /^\/tickets\/?$/.test(window.location.pathname);
  }

  function readURL() {
    const params = new URL(window.location.href).searchParams;
    queue = validQueue(params.get('queue'));
    projectFilter = params.get('project') || '';
    query = params.get('q') || '';
    selectedKey = params.get('ticket') || '';
  }

  function writeURL(push = false) {
    if (!onTicketsRoute()) return;
    const url = new URL(window.location.href);
    const values: Record<string, string> = { queue: queue === 'open' ? '' : queue, project: projectFilter, q: query.trim(), ticket: selectedKey };
    for (const [name, value] of Object.entries(values)) {
      if (value) url.searchParams.set(name, value);
      else url.searchParams.delete(name);
    }
    const next = url.pathname + url.search;
    if (next === window.location.pathname + window.location.search) return;
    if (push) window.history.pushState({}, '', next);
    else window.history.replaceState({}, '', next);
  }

  function ticketStatus(task: Task): TicketStatus {
    return task.ticket?.status || 'needs_triage';
  }

  function inQueue(task: Task, target: TicketQueue): boolean {
    const status = ticketStatus(task);
    if (target === 'open') return status !== 'completed';
    if (target === 'mine') return status !== 'completed' && actorId(task.assignee) === user.id;
    return status === target;
  }

  function assigneeLabel(task: Task): string {
    const id = actorId(task.assignee);
    if (!id) return 'Unassigned';
    if (id === user.id) return 'You';
    return actorNames[id] || 'Assigned';
  }

  function age(value: string | undefined): string {
    const timestamp = Date.parse(value || '');
    if (!Number.isFinite(timestamp)) return '';
    const minutes = Math.max(0, Math.round((Date.now() - timestamp) / 60000));
    if (minutes < 60) return `${minutes}m`;
    const hours = Math.round(minutes / 60);
    if (hours < 48) return `${hours}h`;
    return `${Math.round(hours / 24)}d`;
  }

  function errorMessage(error: unknown, fallback: string): string {
    return error instanceof Error && error.message ? error.message : fallback;
  }

  function isConflict(error: unknown): boolean {
    return error instanceof ApiError && (error.status === 409 || error.status === 412);
  }

  async function loadList(options: { append?: boolean; quiet?: boolean } = {}) {
    const request = ++listRequest;
    if (!options.quiet) listLoading = true;
    listError = '';
    try {
      const page = await api.listTickets({
        queue,
        project: projectFilter || undefined,
        q: query.trim() || undefined,
        cursor: options.append ? nextCursor : undefined,
        limit: 50
      });
      if (request !== listRequest) return;
      tickets = options.append ? [...tickets, ...page.data] : page.data;
      counts = page.counts;
      ticketQueue = page.queue || null;
      nextCursor = page.next_cursor || '';
    } catch (error) {
      if (request !== listRequest) return;
      listError = errorMessage(error, 'Tickets could not be loaded.');
    } finally {
      if (request === listRequest) listLoading = false;
    }
  }

  async function columnsFor(projectId: string): Promise<Column[]> {
    if (!columnsByProject[projectId]) {
      const result = await api.listAllColumns(projectId);
      const columns = Array.isArray(result) ? result : result.data;
      columnsByProject = { ...columnsByProject, [projectId]: columns };
    }
    return columnsByProject[projectId];
  }

  async function loadDetail(key: string) {
    const request = ++detailRequest;
    waitOpen = false;
    waitReason = '';
    actionStatus = null;
    if (!key) {
      detail = null;
      comments = [];
      return;
    }
    detailLoading = true;
    detailError = '';
    try {
      const task = await api.getTask(key);
      if (request !== detailRequest) return;
      detail = task;
      const [page] = await Promise.all([api.listComments(task.id, { limit: 100 }), columnsFor(task.project_id)]);
      if (request !== detailRequest) return;
      comments = page.data;
    } catch (error) {
      if (request !== detailRequest) return;
      detail = null;
      detailError = errorMessage(error, 'This ticket could not be loaded.');
    } finally {
      if (request === detailRequest) detailLoading = false;
    }
  }

  async function selectTicket(task: Task, focusDetail = false) {
    movedNotice = '';
    if (selectedKey !== task.key) {
      selectedKey = task.key;
      writeURL(true);
      await loadDetail(task.key);
    }
    if (focusDetail) {
      await tick();
      detailHeading?.focus();
    }
  }

  async function openReference(reference: string) {
    try {
      await selectTicket(await api.getTask(reference), true);
    } catch (error) {
      detailError = errorMessage(error, 'The linked ticket could not be opened.');
    }
  }

  function backToList() {
    if (!selectedKey) return;
    const key = selectedKey;
    selectedKey = '';
    detailRequest += 1;
    detail = null;
    writeURL(true);
    void tick().then(() => listElement?.querySelector<HTMLElement>(`[data-ticket-key="${key}"]`)?.focus());
  }

  function changeFilters() {
    writeURL();
    void loadList();
  }

  function setQueue(value: TicketQueue) {
    queue = value;
    changeFilters();
  }

  function searchChanged() {
    clearTimeout(searchTimer);
    searchTimer = setTimeout(changeFilters, 250);
  }

  async function act(label: string, run: (task: Task) => Promise<Task>) {
    if (!detail || actionPending) return;
    const request = detailRequest;
    const before = detail;
    actionPending = true;
    actionStatus = { kind: 'pending', message: `${label}…` };
    try {
      const updated = await run(before);
      if (request !== detailRequest) return;
      detail = updated;
      tickets = tickets.map((task) => task.id === updated.id ? updated : task);
      actionStatus = { kind: 'saved', message: `${label} saved` };
      if (!inQueue(updated, queue)) {
        movedNotice = `${updated.key} moved to ${statusLabels[ticketStatus(updated)]}.`;
      }
      waitOpen = false;
      waitReason = '';
      onChanged();
      await loadList({ quiet: true });
    } catch (error) {
      if (request !== detailRequest) return;
      if (isConflict(error)) {
        actionStatus = { kind: 'conflict', message: 'This ticket changed elsewhere. The latest version is loaded; review it and try again.' };
        const key = before.key;
        const refreshed = await api.getTask(key).catch(() => null);
        if (refreshed && request === detailRequest) {
          detail = refreshed;
          tickets = tickets.map((task) => task.id === refreshed.id ? refreshed : task);
        }
      } else {
        actionStatus = { kind: 'error', message: errorMessage(error, `${label} failed.`) };
      }
    } finally {
      if (request === detailRequest) actionPending = false;
    }
  }

  async function moveTo(status: TicketStatus, label: string) {
    await act(label, async (task) => {
      const columns = await columnsFor(task.project_id);
      const destination = columns
        .filter((column) => column.semantic_state === statusStates[status] && !column.archived_at)
        .sort((a, b) => a.position - b.position)[0];
      if (!destination) throw new Error(`This project has no ${statusLabels[status]} column.`);
      return api.patchTask(task.id, { column_id: destination.id }, task.version);
    });
  }

  async function fileTo(projectId: string) {
    const project = projectById.get(projectId);
    if (!detail || !project || projectId === detail.project_id) return;
    const before = detail.key;
    await act('Project', (task) => api.fileTicket(task.id, projectId));
    if (detail && detail.key !== before) {
      selectedKey = detail.key;
      writeURL();
      movedNotice = `${before} is now ${detail.key} in ${project.name}.`;
    }
    // A failed filing leaves the ticket where it was; show that again.
    fileProject = detail ? filedProject(detail)?.id || '' : '';
  }

  async function markWaiting() {
    const reason = waitReason.trim();
    if (!reason) return;
    await act('Waiting', (task) => api.blockTask(task.id, task.version, reason));
  }

  async function postNote() {
    if (!detail || !noteDraft.trim() || notePosting) return;
    const request = detailRequest;
    notePosting = true;
    try {
      const comment = await api.postComment(detail.id, noteDraft.trim());
      if (request !== detailRequest) return;
      comments = [...comments, comment];
      noteDraft = '';
    } catch (error) {
      if (request === detailRequest) actionStatus = { kind: 'error', message: errorMessage(error, 'The note could not be saved.') };
    } finally {
      if (request === detailRequest) notePosting = false;
    }
  }

  async function createTicket() {
    if (!createTitle.trim() || createSaving) return;
    createSaving = true;
    createError = '';
    try {
      const task = await api.createTicket({
        title: createTitle.trim(),
        description: createDescription.trim() || undefined,
        priority: createPriority,
        assignee: createAssignMe ? user.id : null
      });
      creating = false;
      createTitle = '';
      createDescription = '';
      createPriority = 'normal';
      onChanged();
      await loadList({ quiet: true });
      await selectTicket(task, true);
    } catch (error) {
      createError = errorMessage(error, 'The ticket could not be created.');
    } finally {
      createSaving = false;
    }
  }

  function listKeydown(event: KeyboardEvent) {
    const rows = Array.from(listElement?.querySelectorAll<HTMLElement>('[data-ticket-key]') || []);
    const index = rows.findIndex((row) => row === document.activeElement);
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      event.preventDefault();
      const next = rows[Math.min(rows.length - 1, Math.max(0, index + (event.key === 'ArrowDown' ? 1 : -1)))];
      next?.focus();
    } else if (event.key === 'Home' || event.key === 'End') {
      event.preventDefault();
      (event.key === 'Home' ? rows[0] : rows[rows.length - 1])?.focus();
    }
  }

  function detailKeydown(event: KeyboardEvent) {
    if (event.key === 'Escape' && !waitOpen) {
      event.preventDefault();
      backToList();
    }
  }

  function handlePopState() {
    if (!onTicketsRoute()) return;
    const previous = JSON.stringify([queue, projectFilter, query]);
    const previousKey = selectedKey;
    readURL();
    if (JSON.stringify([queue, projectFilter, query]) !== previous) void loadList();
    if (selectedKey !== previousKey) void loadDetail(selectedKey);
  }

  async function loadPublicAccess() {
    if (!user.admin) return;
    try {
      publicAccess = await api.getPublicEndpoints();
    } catch {
      publicAccess = null;
    }
  }

  function toggleConnect() {
    connectOpen = !connectOpen;
    if (!connectOpen) void loadPublicAccess();
  }

  onMount(() => {
    readURL();
    // ?connect=1 deep-links straight to Connect apps (e.g. from Admin).
    const url = new URL(window.location.href);
    if (url.searchParams.get('connect') === '1') {
      connectOpen = true;
      url.searchParams.delete('connect');
      window.history.replaceState({}, '', url.pathname + url.search);
    }
    void loadPublicAccess();
    void loadList();
    if (selectedKey) void loadDetail(selectedKey);
    window.addEventListener('popstate', handlePopState);
    refreshTimer = setInterval(() => {
      if (document.visibilityState === 'visible' && !actionPending) void loadList({ quiet: true });
    }, 30000);
  });

  onDestroy(() => {
    clearTimeout(searchTimer);
    clearInterval(refreshTimer);
    if (typeof window !== 'undefined') window.removeEventListener('popstate', handlePopState);
  });
</script>

<section class="tickets-workspace" class:has-selection={Boolean(selectedKey)} aria-labelledby="tickets-heading">
  <header class="tickets-heading">
    <div>
      <div class="breadcrumbs"><span>Workspace</span><span>/</span><span>Triage</span></div>
      <h1 id="tickets-heading">Tickets</h1>
      <p>Alerts and requests that need a person. Everything new lands in Needs triage; file a ticket into a project when you know where it belongs.</p>
    </div>
    <div class="tickets-heading-actions">
      <button class="button quiet-button" type="button" aria-expanded={connectOpen} on:click={toggleConnect}>⇄ Connect apps{#if publicAccess?.active}<span class={`public-pill pill-${publicAccess.connector.state}`} title={`Public URL https://${publicAccess.active.hostname} is ${publicAccess.connector.state === 'connected' ? 'live' : publicAccess.connector.state}`} data-public-pill>● Public</span>{/if}</button>
      <button class="button primary" type="button" on:click={() => { creating = !creating; createError = ''; }} aria-expanded={creating}>＋ New ticket</button>
    </div>
  </header>

  {#if connectOpen}<TicketIntegrations {user} onPublicAccessChanged={loadPublicAccess} />{/if}

  {#if creating}
    <form class="ticket-create" aria-label="New ticket" on:submit|preventDefault={createTicket}>
      <label>Title<input bind:value={createTitle} maxlength="500" required placeholder="What needs attention?" /></label>
      <label>Details <span class="optional">Optional</span><textarea rows="3" bind:value={createDescription} placeholder="Context, links, or the alert text"></textarea></label>
      <div class="ticket-create-row">
        <label>Priority<select bind:value={createPriority}>{#each priorities as value}<option value={value}>{priorityLabels[value]}</option>{/each}</select></label>
        <label class="ticket-check"><input type="checkbox" bind:checked={createAssignMe} /> Assign to me</label>
      </div>
      {#if createError}<div class="inline-alert error" role="alert"><span>!</span>{createError}</div>{/if}
      <div class="ticket-create-actions"><button class="text-button" type="button" on:click={() => creating = false}>Cancel</button><button class="button primary" type="submit" disabled={!createTitle.trim() || createSaving}>{createSaving ? 'Creating…' : 'Create ticket'}</button></div>
    </form>
  {/if}

  <div class="ticket-queues" role="tablist" aria-label="Ticket queues">
    {#each queues as item (item.value)}
      <button class="ticket-queue" class:active={queue === item.value} type="button" role="tab" aria-selected={queue === item.value} on:click={() => setQueue(item.value)}>
        <span>{item.label}</span>{#if counts}<span class="ticket-queue-count" data-queue-count={item.value}>{counts[item.value]}</span>{/if}
      </button>
    {/each}
  </div>

  <div class="ticket-filters">
    <label class="ticket-search"><span class="sr-only">Search tickets</span><input bind:this={searchInput} type="search" bind:value={query} on:input={searchChanged} placeholder="Search tickets or a key (press /)" /></label>
    <label><span class="sr-only">Project</span><select aria-label="Project filter" bind:value={projectFilter} on:change={changeFilters}><option value="">All tickets</option>{#if ticketQueue}<option value={ticketQueue.key}>Not filed</option>{/if}{#each workProjects as project (project.id)}<option value={project.key}>{project.key} · {project.name}</option>{/each}</select></label>
  </div>

  {#if movedNotice}<div class="inline-alert ticket-moved" role="status"><span>✓</span>{movedNotice}</div>{/if}

  <div class="tickets-layout">
    <div class="tickets-list-pane">
      {#if listError}<div class="inline-alert error" role="alert"><span>!</span>{listError}<button class="text-button" type="button" on:click={() => loadList()}>Retry</button></div>{/if}
      {#if listLoading && !tickets.length}
        <div class="list-skeleton" aria-label="Loading tickets"><div></div><div></div><div></div></div>
      {:else if !tickets.length}
        <div class="empty-state"><div class="empty-icon">✓</div><h2>Nothing in {queues.find((item) => item.value === queue)?.label}</h2><p>New alerts and requests land in Needs triage.</p></div>
      {:else}
        <ul class="ticket-list" bind:this={listElement} aria-label="Tickets">
          {#each tickets as task (task.id)}
            <li>
              <button class="ticket-row" class:selected={selectedKey === task.key} type="button" data-ticket-key={task.key} aria-current={selectedKey === task.key ? 'true' : undefined} on:keydown={listKeydown} on:click={() => selectTicket(task, true)}>
                <span class="ticket-row-top">
                  <span class="task-key">{task.key}</span>
                  <span class={`priority-pill priority-${task.priority}`}>{priorityLabels[task.priority]}</span>
                  <span class={`ticket-status status-${ticketStatus(task)}`}>{statusLabels[ticketStatus(task)]}</span>
                  {#if (task.alert_source?.occurrence_count || 0) > 1}<span class="ticket-repeat" title="Times this alert was received">×{task.alert_source?.occurrence_count}</span>{/if}
                </span>
                <strong>{task.title}</strong>
                <span class="ticket-row-meta">
                  <span data-ticket-project>{filedProject(task)?.key || 'Not filed'}{#if task.alert_source} · {task.alert_source.resource_name}{/if}</span>
                  <span>{assigneeLabel(task)}</span>
                  <span>{age(task.ticket?.created_at || task.created_at)}</span>
                </span>
              </button>
            </li>
          {/each}
        </ul>
        {#if nextCursor}<button class="button quiet-button ticket-more" type="button" on:click={() => loadList({ append: true })}>Load more tickets</button>{/if}
      {/if}
    </div>

    <div class="ticket-detail-pane" on:keydown={detailKeydown} role="presentation">
      {#if !selectedKey}
        <div class="ticket-detail-empty"><p>Select a ticket to triage it.</p><p class="optional">↑ ↓ move · Enter opens · / searches</p></div>
      {:else if detailLoading && !detail}
        <div class="drawer-loading"><span class="spinner"></span><span>Loading ticket…</span></div>
      {:else if detailError}
        <div class="inline-alert error" role="alert"><span>!</span>{detailError}</div>
      {:else if detail}
        <article class="ticket-detail" aria-labelledby="ticket-detail-title">
          <button class="text-button ticket-back" type="button" on:click={backToList}>← All tickets</button>
          <div class="ticket-detail-header">
            <div class="ticket-row-top"><span class="task-key">{detail.key}</span><span class={`ticket-status status-${ticketStatus(detail)}`} data-ticket-status>{statusLabels[ticketStatus(detail)]}</span>{#if detail.ticket?.origin === 'alert'}<span class="ticket-origin">Alert</span>{/if}</div>
            <h2 id="ticket-detail-title" tabindex="-1" bind:this={detailHeading}>{detail.title}</h2>
            <div class="ticket-controls">
              <label>Priority<select aria-label="Ticket priority" value={detail.priority} disabled={actionPending} on:change={(event) => { const priority = event.currentTarget.value as Task['priority']; void act('Priority', (task) => api.patchTask(task.id, { priority }, task.version)); }}>{#each priorities as value}<option value={value}>{priorityLabels[value]}</option>{/each}</select></label>
              <label>Project<select aria-label="File to project" bind:value={fileProject} disabled={actionPending} on:change={() => fileTo(fileProject)}>{#if !filedProject(detail)}<option value="">Not filed</option>{/if}{#each workProjects as project (project.id)}<option value={project.id}>{project.key} · {project.name}</option>{/each}</select></label>
              <span class="ticket-assignee">Assignee: <strong>{assigneeLabel(detail)}</strong></span>
              {#if actorId(detail.assignee) !== user.id}<button class="button quiet-button compact" type="button" disabled={actionPending} on:click={() => act('Assignment', (task) => api.patchTask(task.id, { assignee: user.id }, task.version))}>Assign to me</button>{/if}
              {#if actorId(detail.assignee)}<button class="text-button" type="button" disabled={actionPending} on:click={() => act('Assignment', (task) => api.patchTask(task.id, { assignee: null }, task.version))}>Unassign</button>{/if}
            </div>
          </div>

          <div class="ticket-actions" role="group" aria-label="Ticket actions">
            {#if ticketStatus(detail) === 'needs_triage'}<button class="button primary" type="button" disabled={actionPending} on:click={() => moveTo('ready', 'Accept')}>Accept to Ready</button>{/if}
            {#if ticketStatus(detail) === 'needs_triage' || ticketStatus(detail) === 'ready'}<button class="button quiet-button" type="button" disabled={actionPending} on:click={() => moveTo('in_progress', 'Start')}>Start</button>{/if}
            {#if ticketStatus(detail) === 'waiting'}<button class="button primary" type="button" disabled={actionPending} on:click={() => moveTo('ready', 'Resume')}>Resume</button>{/if}
            {#if ticketStatus(detail) !== 'completed' && ticketStatus(detail) !== 'waiting'}<button class="button quiet-button" type="button" disabled={actionPending} aria-expanded={waitOpen} on:click={() => { waitOpen = !waitOpen; }}>Waiting…</button>{/if}
            {#if ticketStatus(detail) !== 'completed'}<button class="button complete-button" type="button" disabled={actionPending} on:click={() => act('Complete', (task) => api.completeTask(task.id, task.version))}>✓ Complete</button>
            {:else}<button class="button quiet-button" type="button" disabled={actionPending} on:click={() => moveTo('needs_triage', 'Reopen')}>Reopen</button>{/if}
            <button class="text-button ticket-open-board" type="button" on:click={() => detail && onOpenTask(detail)}>Open full task ↗</button>
          </div>
          {#if waitOpen}
            <form class="ticket-wait" on:submit|preventDefault={markWaiting}>
              <label>What is this waiting on?<textarea rows="2" bind:value={waitReason} required placeholder="e.g. Maintenance window on Saturday"></textarea></label>
              <div class="ticket-create-actions"><button class="text-button" type="button" on:click={() => { waitOpen = false; waitReason = ''; }}>Cancel</button><button class="button primary" type="submit" disabled={!waitReason.trim() || actionPending}>Mark waiting</button></div>
            </form>
          {/if}
          <div class="ticket-action-status" class:conflict={actionStatus?.kind === 'conflict'} class:error={actionStatus?.kind === 'error'} role="status" aria-live="polite">{actionStatus?.message || ''}</div>

          <section class="drawer-section ticket-attention" aria-labelledby="ticket-attention-heading">
            <h3 id="ticket-attention-heading">What needs attention</h3>
            {#if detail.description}<div class="ticket-description">{@html renderMarkdown(detail.description)}</div>{:else}<p class="optional">No details yet.</p>{/if}
          </section>
          {#if detail.alert_source}<AlertSourcePanel source={detail.alert_source} onOpenPrevious={openReference} />{/if}

          <section class="drawer-section ticket-notes" aria-labelledby="ticket-notes-heading">
            <h3 id="ticket-notes-heading">Notes</h3>
            {#if comments.length}
              <ul class="ticket-note-list">
                {#each comments as comment (comment.id)}
                  <li><span class="ticket-note-author">{comment.actor_id === user.id ? 'You' : actorNames[comment.actor_id] || 'Teammate'} · {age(comment.created_at)}</span><div>{@html renderMarkdown(comment.body)}</div></li>
                {/each}
              </ul>
            {:else}<p class="optional">No notes yet.</p>{/if}
            <form class="ticket-note-form" on:submit|preventDefault={postNote}>
              <label><span class="sr-only">Add internal note</span><textarea rows="2" bind:value={noteDraft} placeholder="Add an internal note (visible to everyone on this project)"></textarea></label>
              <button class="button quiet-button" type="submit" disabled={!noteDraft.trim() || notePosting}>{notePosting ? 'Saving…' : 'Add note'}</button>
            </form>
          </section>
        </article>
      {/if}
    </div>
  </div>
</section>

<style>
  .tickets-workspace { display: grid; gap: 14px; }
  .tickets-heading { display: flex; align-items: flex-end; justify-content: space-between; gap: 16px; }
  .tickets-heading h1 { margin: 0; font: 800 28px var(--font-display); letter-spacing: -.045em; }
  .tickets-heading p { max-width: 650px; margin: 8px 0 0; color: var(--muted); font-size: 12px; line-height: 1.5; }
  .tickets-heading-actions { display: flex; flex-wrap: wrap; gap: 8px; }
  .public-pill { margin-left: 6px; padding: 1px 6px; border-radius: 999px; font-size: 10px; font-weight: 800; color: var(--semantic-amber); background: var(--amber-soft); }
  .public-pill.pill-connected { color: var(--semantic-green); background: var(--green-soft); }
  .public-pill.pill-restarting, .public-pill.pill-error { color: var(--semantic-red); background: var(--red-soft); }
  .breadcrumbs { display: flex; gap: 6px; color: var(--muted); font-size: 11px; }
  .ticket-create, .ticket-wait { display: grid; gap: 10px; padding: 14px; border: 1px solid var(--border); border-radius: 10px; background: var(--surface); }
  .ticket-create label, .ticket-wait label { display: grid; gap: 5px; font-size: 12px; font-weight: 700; }
  .ticket-create-row { display: flex; flex-wrap: wrap; align-items: end; gap: 14px; }
  .ticket-check { display: flex !important; align-items: center; gap: 6px !important; }
  .ticket-create-actions { display: flex; justify-content: flex-end; align-items: center; gap: 12px; }
  .ticket-queues { display: flex; flex-wrap: wrap; gap: 6px; }
  .ticket-queue { display: inline-flex; align-items: center; gap: 6px; min-height: 32px; padding: 0 11px; border: 1px solid var(--border); border-radius: 999px; color: var(--ink-soft); background: var(--surface); font-size: 12px; font-weight: 700; }
  .ticket-queue.active { color: var(--purple); border-color: var(--purple); background: var(--purple-soft); }
  .ticket-queue-count { min-width: 18px; padding: 0 5px; border-radius: 999px; background: var(--surface-muted); font-size: 11px; text-align: center; }
  .ticket-queue:focus-visible, .ticket-row:focus-visible, .ticket-detail h2:focus-visible { outline: 2px solid var(--purple); outline-offset: 2px; }
  .ticket-filters { display: grid; grid-template-columns: minmax(0, 1fr) minmax(140px, 240px); gap: 8px; }
  .ticket-filters input, .ticket-filters select { width: 100%; }
  .tickets-layout { display: grid; grid-template-columns: minmax(280px, 400px) minmax(0, 1fr); gap: 16px; align-items: start; }
  .ticket-list { display: grid; gap: 6px; margin: 0; padding: 0; list-style: none; }
  .ticket-row { width: 100%; display: grid; gap: 5px; padding: 11px 12px; border: 1px solid var(--border); border-radius: 10px; color: var(--ink); background: var(--surface); text-align: left; }
  .ticket-row:hover { background: var(--surface-hover); }
  .ticket-row.selected { border-color: var(--purple); box-shadow: inset 3px 0 0 var(--purple); }
  .ticket-row strong { font-size: 13px; line-height: 1.35; }
  .ticket-row-top { display: flex; flex-wrap: wrap; align-items: center; gap: 6px; }
  .ticket-row-meta { display: flex; flex-wrap: wrap; gap: 4px 12px; color: var(--muted); font-size: 11px; }
  .ticket-status { padding: 2px 7px; border: 1px solid var(--border-strong); border-radius: 5px; font-size: 11px; font-weight: 800; }
  .ticket-status.status-needs_triage { color: var(--semantic-amber); background: var(--amber-soft); }
  .ticket-status.status-waiting { color: var(--semantic-red); background: var(--red-soft); }
  .ticket-status.status-in_progress { color: var(--purple); background: var(--purple-soft); }
  .ticket-status.status-completed { color: var(--semantic-green); background: var(--green-soft); }
  .ticket-repeat, .ticket-origin { padding: 2px 6px; border-radius: 5px; color: var(--ink-soft); background: var(--surface-muted); font-size: 11px; font-weight: 700; }
  .ticket-more { margin-top: 8px; width: 100%; }
  .ticket-moved { border-color: color-mix(in srgb, var(--semantic-green), var(--border) 72%); background: var(--green-soft); }
  .ticket-moved > span:first-child { background: var(--semantic-green); }
  .ticket-detail-pane { min-width: 0; padding: 16px; border: 1px solid var(--border); border-radius: 12px; background: var(--surface-raised, var(--surface)); }
  .ticket-detail-empty { color: var(--muted); font-size: 13px; text-align: center; }
  .ticket-detail { display: grid; gap: 12px; }
  .ticket-detail h2 { margin: 6px 0 0; font: 800 20px var(--font-display); letter-spacing: -.03em; }
  .ticket-detail h3 { margin: 0 0 8px; font-size: 13px; }
  .ticket-back { display: none; justify-self: start; }
  .ticket-controls { display: flex; flex-wrap: wrap; align-items: center; gap: 10px; margin-top: 10px; font-size: 12px; }
  .ticket-controls label { display: inline-flex; align-items: center; gap: 6px; font-weight: 700; }
  .compact { min-height: 30px; }
  .ticket-actions { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; }
  .ticket-open-board { margin-left: auto; }
  .ticket-action-status { min-height: 16px; color: var(--muted); font-size: 12px; }
  .ticket-action-status:empty { min-height: 0; margin: -6px 0; }
  .ticket-detail :global(.drawer-section) { margin: 0; }
  .ticket-action-status.conflict, .ticket-action-status.error { color: var(--semantic-red); font-weight: 700; }
  .ticket-description { font-size: 13px; line-height: 1.55; overflow-wrap: anywhere; }
  .ticket-note-list { display: grid; gap: 10px; margin: 0 0 10px; padding: 0; list-style: none; font-size: 13px; }
  .ticket-note-author { color: var(--muted); font-size: 11px; font-weight: 700; }
  .ticket-note-form { display: grid; gap: 8px; justify-items: end; }
  .ticket-note-form label, .ticket-note-form textarea { width: 100%; }
  .optional { color: var(--muted); font-size: 12px; }
  @media (max-width: 760px) {
    .tickets-heading { align-items: flex-start; flex-direction: column; }
    .tickets-layout { grid-template-columns: minmax(0, 1fr); }
    .ticket-detail-pane { display: none; padding: 12px; }
    .has-selection .ticket-detail-pane { display: block; }
    .has-selection .tickets-list-pane, .has-selection .ticket-queues, .has-selection .ticket-filters, .has-selection .tickets-heading p { display: none; }
    .ticket-back { display: inline-block; }
    .ticket-filters { grid-template-columns: minmax(0, 1fr); }
    .ticket-open-board { margin-left: 0; }
  }
</style>
