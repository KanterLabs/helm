<script lang="ts">
  import { onDestroy, onMount, tick } from 'svelte';
  import { api } from '../api';
  import { renderMarkdown } from '../markdown';
  import type { Actor, Column, Comment, Project, Task, TicketCounts, TicketQueue, TicketStatus } from '../types';
  import {
    isTicketConflict as isConflict,
    onTicketsRoute,
    readTicketRoute,
    ticketErrorMessage as errorMessage,
    ticketInQueue,
    ticketQueues as queues,
    ticketStatus,
    ticketStatusLabels as statusLabels,
    ticketStatusStates as statusStates,
    writeTicketRoute
  } from '../tickets';
  import AlertSourcePanel from './AlertSourcePanel.svelte';
  import '../../styles/tickets-workspace.css';
  import TicketBugTriage from './TicketBugTriage.svelte';
  import TicketActions from './TicketActions.svelte';
  import TicketCreateForm, { emptyTicketDraft } from './TicketCreateForm.svelte';
  import TicketDetailHeader from './TicketDetailHeader.svelte';
  import TicketList from './TicketList.svelte';
  import TicketQueueTabs from './TicketQueueTabs.svelte';
  import TicketNotes from './TicketNotes.svelte';
  import TicketsHeading from './TicketsHeading.svelte';

  export let user: Actor;
  export let projects: Project[] = [];
  export let actorNames: Record<string, string> = {};
  export let onOpenTask: (task: Task) => void | Promise<void> = () => undefined;
  export let onChanged: () => void = () => undefined;
  /** Unsaved ticket text/options exposed to the host's update guard. */
  export let dirty = false;

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
  let bugDraftDirty = false;
  let noteDraft = '';
  let actionPending = false;
  let actionStatus: { kind: 'pending' | 'saved' | 'conflict' | 'error'; message: string } | null = null;
  let movedNotice = '';
  let movedKey = '';
  let waitOpen = false;
  let waitReason = '';
  let creating = false;
  let createDraft = emptyTicketDraft();
  let columnsByProject: Record<string, Column[]> = {};
  let searchInput: HTMLInputElement | null = null;
  let listElement: HTMLElement | null = null;
  let detailHeading: HTMLElement | null = null;
  let searchTimer: ReturnType<typeof setTimeout> | undefined;
  let refreshTimer: ReturnType<typeof setInterval> | undefined;

  $: dirty = Boolean(bugDraftDirty || noteDraft
    || (waitOpen && waitReason)
    || (creating && (createDraft.title || createDraft.description || createDraft.priority !== 'normal' || !createDraft.assignMe)));

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

  function readURL() {
    ({ queue, project: projectFilter, q: query, ticket: selectedKey } = readTicketRoute());
  }

  function writeURL(push = false) {
    writeTicketRoute({ queue, project: projectFilter, q: query, ticket: selectedKey }, push);
  }

  export function searchElement(): HTMLInputElement | null {
    return searchInput;
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
    movedKey = '';
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
      const position = tickets.findIndex((task) => task.id === updated.id);
      tickets = tickets.map((task) => task.id === updated.id ? updated : task);
      actionStatus = { kind: 'saved', message: `${label} saved` };
      const left = !ticketInQueue(updated, queue, user.id);
      if (left) {
        movedNotice = `${updated.key} moved to ${statusLabels[ticketStatus(updated)]}.`;
        movedKey = updated.key;
      }
      waitOpen = false;
      waitReason = '';
      onChanged();
      await loadList({ quiet: true });
      if (left && request === detailRequest) await advancePast(updated, position);
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
      actionPending = false;
    }
  }

  /** A ticket that left the queue gives way to its neighbour, or the empty pane. */
  async function advancePast(moved: Task, position: number) {
    const remaining = tickets.filter((task) => task.id !== moved.id);
    const next = remaining[Math.min(Math.max(position, 0), remaining.length - 1)];
    selectedKey = next?.key || '';
    writeURL();
    await loadDetail(selectedKey);
    await tick();
    if (next) detailHeading?.focus();
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
      movedKey = '';
    }
    // A failed filing leaves the ticket where it was; show that again.
    fileProject = detail ? filedProject(detail)?.id || '' : '';
  }

  async function ticketCreated(task: Task) {
    creating = false;
    onChanged();
    await loadList({ quiet: true });
    await selectTicket(task, true);
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

  onMount(() => {
    readURL();
    void loadList();
    if (selectedKey) void loadDetail(selectedKey);
    window.addEventListener('popstate', handlePopState);
    refreshTimer = setInterval(() => {
      if (document.visibilityState === 'visible' && !actionPending) void loadList({ quiet: true });
    }, 30000);
  });

  onDestroy(() => {
    dirty = false;
    clearTimeout(searchTimer);
    clearInterval(refreshTimer);
    if (typeof window !== 'undefined') window.removeEventListener('popstate', handlePopState);
  });
</script>

<section class="tickets-workspace" class:has-selection={Boolean(selectedKey)} aria-labelledby="tickets-heading">
  <TicketsHeading {user} bind:creating />

  {#if creating}
    <TicketCreateForm {user} bind:draft={createDraft} onCreated={ticketCreated} onCancel={() => { creating = false; }} />
  {/if}

  <TicketQueueTabs {queue} {counts} onSelect={setQueue} />

  <div class="ticket-filters">
    <label class="ticket-search"><span class="sr-only">Search tickets</span><input bind:this={searchInput} type="search" bind:value={query} on:input={searchChanged} placeholder="Search tickets or a key (press /)" /></label>
    <label><span class="sr-only">Project</span><select aria-label="Project filter" bind:value={projectFilter} on:change={changeFilters}><option value="">All tickets</option>{#if ticketQueue}<option value={ticketQueue.key}>Not filed</option>{/if}{#each workProjects as project (project.id)}<option value={project.key}>{project.key} · {project.name}</option>{/each}</select></label>
  </div>

  {#if movedNotice}<div class="inline-alert ticket-moved" role="status"><span>✓</span>{movedNotice}{#if movedKey && movedKey !== selectedKey}<button class="text-button" type="button" on:click={() => openReference(movedKey)}>View</button>{/if}</div>{/if}

  <div class="tickets-layout">
    <TicketList
      {tickets}
      {selectedKey}
      loading={listLoading}
      error={listError}
      {nextCursor}
      queueLabel={queues.find((item) => item.value === queue)?.label || ''}
      userId={user.id}
      {actorNames}
      projectKey={(task) => filedProject(task)?.key || ''}
      onSelect={(task) => selectTicket(task, true)}
      onRetry={() => loadList()}
      onMore={() => loadList({ append: true })}
      bind:listElement
    />

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
          <TicketDetailHeader {detail} {user} {actorNames} {workProjects} filed={Boolean(filedProject(detail))} bind:fileProject {actionPending} bind:heading={detailHeading} onAct={act} onFile={fileTo} />

          {#key detail.id}<TicketBugTriage {detail} {actionPending} onAct={act} bind:dirty={bugDraftDirty} />{/key}

          <TicketActions {detail} {actionPending} bind:waitOpen bind:waitReason onAct={act} onMove={moveTo} {onOpenTask} />
          <div class="ticket-action-status" class:conflict={actionStatus?.kind === 'conflict'} class:error={actionStatus?.kind === 'error'} role="status" aria-live="polite">{actionStatus?.message || ''}</div>

          <section class="drawer-section ticket-attention" aria-labelledby="ticket-attention-heading">
            <h3 id="ticket-attention-heading">What needs attention</h3>
            {#if detail.description}<div class="ticket-description">{@html renderMarkdown(detail.description)}</div>{:else}<p class="optional">No details yet.</p>{/if}
          </section>
          {#if detail.alert_source}<AlertSourcePanel source={detail.alert_source} onOpenPrevious={openReference} />{/if}

          <TicketNotes taskId={detail.id} bind:comments bind:noteDraft userId={user.id} {actorNames} onError={(message) => { actionStatus = { kind: 'error', message }; }} />
        </article>
      {/if}
    </div>
  </div>
</section>
