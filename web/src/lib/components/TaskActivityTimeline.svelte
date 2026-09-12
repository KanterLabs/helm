<script lang="ts">
  import { renderMarkdown } from '../markdown';
  import type { Comment, TaskTimelineFilter, TaskTimelineItem } from '../types';

  export let items: TaskTimelineItem[] = [];
  export let filter: TaskTimelineFilter = 'all';
  export let loading = false;
  export let loadingOlder = false;
  export let error = '';
  export let hasOlder = false;
  export let onFilterChange: (next: TaskTimelineFilter) => void = () => undefined;
  export let onLoadOlder: () => void | Promise<void> = () => undefined;
  export let onRetry: () => void | Promise<void> = () => undefined;
  export let currentActorId = '';
  export let canManageComments = false;
  /** Stable owner identity used to keep comment drafts isolated between tasks. */
  export let taskId = '';
  export let onEditComment: (comment: Comment, body: string) => void | Promise<void> = () => undefined;
  export let onConfirmDelete: (comment: Comment) => boolean | Promise<boolean> = () => true;
  export let onDeleteComment: (comment: Comment) => void | Promise<void> = () => undefined;

  interface CommentDraft {
    comment: Comment;
    body: string;
    originalBody: string;
  }

  interface PendingCommentMutation {
    taskId: string;
    commentId: string;
    body: string;
  }

  let editingCommentId: string | null = null;
  let editingBody = '';
  let editingOriginalBody = '';
  let editingCommentSnapshot: Comment | null = null;
  let activeTaskId = '';
  let draftsByTask = new Map<string, Map<string, CommentDraft>>();
  let pendingCommentMutations = new Map<string, PendingCommentMutation>();
  let commentMutationErrors = new Map<string, string>();
  let editingGuardMessage = '';

  const filterOptions: Array<{ value: TaskTimelineFilter; label: string }> = [
    { value: 'all', label: 'All' },
    { value: 'agent_progress', label: 'Agent' },
    { value: 'comment', label: 'Comments' },
    { value: 'task_change', label: 'Changes' }
  ];

  // Prefer the explicit task identity because the host clears rows before a
  // task refresh completes. The fallback keeps the component safe for small
  // embedders/tests that only provide timeline items.
  $: inferredTaskId = items.find((item) => item.task_id)?.task_id || '';
  $: scopedTaskId = taskId || inferredTaskId || activeTaskId;
  $: if (scopedTaskId !== activeTaskId) switchTaskScope(scopedTaskId);
  $: visibleItems = filter === 'all' ? items : items.filter((item) => item.kind === filter);
  $: activeCommentItem = editingCommentId
    ? items.find((item) => item.kind === 'comment' && item.comment?.id === editingCommentId) || null
    : null;
  $: activeDraftComment = activeCommentItem?.comment || editingCommentSnapshot;
  $: activeDraftHidden = Boolean(editingCommentId && (!activeCommentItem || !visibleItems.some((item) => item.id === activeCommentItem?.id)));

  function mutationKey(ownerTaskId: string, commentId: string): string {
    return `${ownerTaskId}\u0000${commentId}`;
  }

  function taskForComment(comment: Comment): string {
    return comment.task_id || activeTaskId;
  }

  function isCommentSaving(comment: Comment, pending = pendingCommentMutations): boolean {
    return pending.has(mutationKey(taskForComment(comment), comment.id));
  }

  function activeCommentIsSaving(pending = pendingCommentMutations): boolean {
    return Boolean(editingCommentId && activeTaskId && pending.has(mutationKey(activeTaskId, editingCommentId)));
  }

  function commentError(comment: Comment, errors = commentMutationErrors): string {
    return errors.get(mutationKey(taskForComment(comment), comment.id)) || '';
  }

  function activeCommentError(errors = commentMutationErrors): string {
    return editingCommentId && activeTaskId
      ? errors.get(mutationKey(activeTaskId, editingCommentId)) || ''
      : '';
  }

  function setPendingMutation(key: string, mutation: PendingCommentMutation | null): void {
    const next = new Map(pendingCommentMutations);
    if (mutation) next.set(key, mutation);
    else next.delete(key);
    pendingCommentMutations = next;
  }

  function setCommentError(key: string, message: string): void {
    const next = new Map(commentMutationErrors);
    if (message) next.set(key, message);
    else next.delete(key);
    commentMutationErrors = next;
  }

  function removeDraft(ownerTaskId: string, commentId: string): void {
    if (!ownerTaskId) return;
    const existing = draftsByTask.get(ownerTaskId);
    if (!existing?.has(commentId)) return;
    const nextByTask = new Map(draftsByTask);
    const next = new Map(existing);
    next.delete(commentId);
    if (next.size) nextByTask.set(ownerTaskId, next);
    else nextByTask.delete(ownerTaskId);
    draftsByTask = nextByTask;
  }

  function persistActiveDraft(): void {
    if (!activeTaskId || !editingCommentId || !editingCommentSnapshot) return;
    const nextByTask = new Map(draftsByTask);
    const taskDrafts = new Map(nextByTask.get(activeTaskId) || []);
    taskDrafts.set(editingCommentId, {
      comment: { ...editingCommentSnapshot },
      body: editingBody,
      originalBody: editingOriginalBody
    });
    nextByTask.set(activeTaskId, taskDrafts);
    draftsByTask = nextByTask;
  }

  function resetActiveEditing(): void {
    editingCommentId = null;
    editingBody = '';
    editingOriginalBody = '';
    editingCommentSnapshot = null;
    editingGuardMessage = '';
  }

  function restoreTaskDraft(ownerTaskId: string): void {
    if (!ownerTaskId) return;
    const draft = draftsByTask.get(ownerTaskId)?.values().next().value as CommentDraft | undefined;
    if (!draft) return;
    editingCommentId = draft.comment.id;
    editingBody = draft.body;
    editingOriginalBody = draft.originalBody;
    editingCommentSnapshot = { ...draft.comment };
  }

  function switchTaskScope(nextTaskId: string): void {
    persistActiveDraft();
    activeTaskId = nextTaskId;
    resetActiveEditing();
    restoreTaskDraft(nextTaskId);
  }

  function discardActiveDraft(): void {
    if (!editingCommentId || activeCommentIsSaving()) return;
    const ownerTaskId = activeTaskId;
    const commentId = editingCommentId;
    removeDraft(ownerTaskId, commentId);
    setCommentError(mutationKey(ownerTaskId, commentId), '');
    resetActiveEditing();
  }

  function updateEditingBody(event: Event): void {
    if (activeCommentIsSaving()) return;
    editingBody = (event.currentTarget as HTMLTextAreaElement).value;
    // Keep the task-scoped copy current if the host switches tasks in the same
    // event loop turn as an input update.
    persistActiveDraft();
  }

  function actorName(item: TaskTimelineItem): string {
    return item.actor?.name || item.progress?.actor_id || item.comment?.actor_id || 'Unknown actor';
  }

  function actorKind(item: TaskTimelineItem): string {
    return item.actor?.kind === 'agent' ? 'Agent' : item.actor?.kind === 'human' ? 'Human' : 'Actor';
  }

  function actorInitial(item: TaskTimelineItem): string {
    return (actorName(item).trim().slice(0, 1) || '?').toUpperCase();
  }

  function kindLabel(kind: TaskTimelineItem['kind']): string {
    if (kind === 'agent_progress') return 'Agent update';
    if (kind === 'comment') return 'Comment';
    return 'Task change';
  }

  function changeLabel(item: TaskTimelineItem): string {
    const type = item.change?.event_type || 'task.updated';
    const labels: Record<string, string> = {
      'task.created': 'created the task',
      'task.updated': 'updated the task',
      'task.moved': 'moved the task',
      'task.completed': 'completed the task',
      'task.blocked': 'blocked the task',
      'task.claimed': 'claimed the task',
      'task.released': 'released the task',
      'task.renewed': 'renewed the claim',
      'task.claim_renewed': 'renewed the claim',
      'bug.triaged': 'triaged the bug',
      'bug.resolved': 'resolved the bug',
      'bug.reopened': 'reopened the bug',
      'comment.updated': 'edited a comment',
      'comment.deleted': 'deleted a comment'
    };
    return labels[type] || type.replace(/[._-]+/g, ' ').replace(/\b\w/g, (letter) => letter.toUpperCase());
  }

  function changeContext(item: TaskTimelineItem): string {
    const payload = item.change?.payload || {};
    const values = ['summary', 'reason', 'note', 'column', 'column_name', 'state', 'resolution', 'phase']
      .map((key) => payload[key])
      .filter((value): value is string | number => typeof value === 'string' || typeof value === 'number')
      .map(String);
    return values.join(' · ');
  }

  function formatRelative(value: string): string {
    const timestamp = Date.parse(value);
    if (!Number.isFinite(timestamp)) return 'Unknown time';
    const minutes = Math.round((timestamp - Date.now()) / 60000);
    const absolute = Math.abs(minutes);
    if (absolute < 1) return 'just now';
    if (absolute < 60) return `${absolute}m ${minutes < 0 ? 'ago' : 'from now'}`;
    const hours = Math.round(absolute / 60);
    if (hours < 24) return `${hours}h ${minutes < 0 ? 'ago' : 'from now'}`;
    const days = Math.round(hours / 24);
    return `${days}d ${minutes < 0 ? 'ago' : 'from now'}`;
  }

  function formatDateTime(value: string): string {
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return value;
    return new Intl.DateTimeFormat(undefined, {
      year: 'numeric',
      month: 'short',
      day: 'numeric',
      hour: 'numeric',
      minute: '2-digit',
      timeZoneName: 'short'
    }).format(date);
  }

  function canEditComment(comment: Comment): boolean {
    return Boolean(comment.actor_id && (comment.actor_id === currentActorId || canManageComments));
  }

  function startEditing(comment: Comment): void {
    if (editingCommentId && editingCommentId !== comment.id) {
      editingGuardMessage = 'Finish or discard the current comment draft before editing another comment.';
      return;
    }
    if (isCommentSaving(comment)) return;
    const ownerTaskId = taskForComment(comment);
    editingCommentId = comment.id;
    editingBody = comment.body;
    editingOriginalBody = comment.body;
    editingCommentSnapshot = { ...comment };
    editingGuardMessage = '';
    setCommentError(mutationKey(ownerTaskId, comment.id), '');
    persistActiveDraft();
  }

  function cancelEditing(): void {
    discardActiveDraft();
  }

  async function saveComment(comment: Comment): Promise<void> {
    if (!editingCommentId || editingCommentId !== comment.id || !editingBody.trim()) return;
    const ownerTaskId = taskForComment(comment);
    const key = mutationKey(ownerTaskId, comment.id);
    if (!ownerTaskId || pendingCommentMutations.has(key)) return;
    const submittedBody = editingBody;
    // Use the version captured when editing started. A newer live row is
    // useful context, but must not silently turn this request into an edit of
    // someone else's newer comment.
    const submittedComment = editingCommentSnapshot?.id === comment.id
      && editingCommentSnapshot.task_id === ownerTaskId
      ? { ...editingCommentSnapshot }
      : { ...comment };
    persistActiveDraft();
    setPendingMutation(key, { taskId: ownerTaskId, commentId: comment.id, body: submittedBody });
    setCommentError(key, '');
    try {
      // Capture both arguments before awaiting: a live refresh must not alter
      // the body that this request submits.
      await onEditComment(submittedComment, submittedBody);
      const storedDraft = draftsByTask.get(ownerTaskId)?.get(comment.id);
      if (storedDraft?.body === submittedBody) removeDraft(ownerTaskId, comment.id);
      setCommentError(key, '');
      if (activeTaskId === ownerTaskId && editingCommentId === comment.id && editingBody === submittedBody) {
        resetActiveEditing();
      }
    } catch (error) {
      // Keep the task-scoped draft untouched so retrying cannot lose text.
      setCommentError(key, error instanceof Error ? error.message : 'The comment could not be saved.');
    } finally {
      setPendingMutation(key, null);
    }
  }

  async function deleteComment(comment: Comment): Promise<void> {
    if (editingCommentId && editingCommentId !== comment.id) return;
    if (isCommentSaving(comment) || [...pendingCommentMutations.values()].some((mutation) => mutation.taskId === taskForComment(comment))) return;
    if (!(await onConfirmDelete(comment))) return;
    const ownerTaskId = taskForComment(comment);
    const key = mutationKey(ownerTaskId, comment.id);
    setPendingMutation(key, { taskId: ownerTaskId, commentId: comment.id, body: '' });
    setCommentError(key, '');
    try {
      await onDeleteComment(comment);
      if (editingCommentId === comment.id) discardActiveDraft();
    } catch (error) {
      setCommentError(key, error instanceof Error ? error.message : 'The comment could not be deleted.');
    } finally {
      setPendingMutation(key, null);
    }
  }

  function showActiveDraft(): void {
    if (activeCommentItem) onFilterChange('all');
  }

  function filterLabel(value: TaskTimelineFilter): string {
    return filterOptions.find((option) => option.value === value)?.label || value;
  }
</script>

<section class="task-timeline" aria-labelledby="task-activity-heading">
  <div class="task-timeline-heading">
    <div>
      <span class="eyebrow">Durable history</span>
      <h2 id="task-activity-heading">Activity</h2>
      <p>Agent updates, comments, and task changes in newest-first order.</p>
    </div>
    <div class="task-timeline-filters" role="group" aria-label="Filter task activity">
      {#each filterOptions as option}
        <button
          class:active={filter === option.value}
          class="task-timeline-filter"
          type="button"
          aria-pressed={filter === option.value}
          on:click={() => onFilterChange(option.value)}
        >{option.label}</button>
      {/each}
    </div>
  </div>

  {#if error}
    <div class="inline-alert error" role="alert"><span>!</span>{error}<button class="text-button" type="button" on:click={onRetry}>Retry</button></div>
  {/if}

  {#if loading && !items.length && !activeDraftHidden}
    <div class="task-timeline-loading" role="status" aria-live="polite"><span class="spinner"></span><span>Loading activity…</span></div>
  {:else}
    {#if activeDraftHidden && activeDraftComment}
      <div class="task-timeline-draft-recovery" role="status" aria-live="polite">
        <div class="task-timeline-draft-recovery-heading">
          <div>
            <strong>Unsaved comment draft</strong>
            {#if activeCommentItem}
              <p>This comment is hidden by the “{filterLabel(filter)}” filter. Your draft is still safe in this task.</p>
            {:else}
              <p>The comment is not in the current activity results. Your draft will stay here until you save or discard it.</p>
            {/if}
          </div>
          {#if activeCommentItem}<button class="text-button" type="button" on:click={showActiveDraft}>Show comment</button>{/if}
        </div>
        <div class="task-timeline-comment-edit">
          <textarea rows="4" value={editingBody} aria-label="Edit comment draft" disabled={activeCommentIsSaving(pendingCommentMutations)} on:input={updateEditingBody}></textarea>
          <div class="task-timeline-comment-actions">
            <button class="button primary" type="button" disabled={!editingBody.trim() || activeCommentIsSaving(pendingCommentMutations)} on:click={() => void saveComment(activeDraftComment as Comment)}>
              {#if activeCommentIsSaving(pendingCommentMutations)}<span class="button-spinner"></span>{/if}Save
            </button>
            <button class="text-button" type="button" disabled={activeCommentIsSaving(pendingCommentMutations)} on:click={cancelEditing}>Discard draft</button>
          </div>
        </div>
        {#if activeCommentError(commentMutationErrors)}<div class="task-timeline-comment-error" role="alert">{activeCommentError(commentMutationErrors)}</div>{/if}
        {#if editingGuardMessage}<div class="task-timeline-comment-error" role="alert">{editingGuardMessage}</div>{/if}
      </div>
    {/if}
    {#if editingCommentId && !activeDraftHidden}
      <div class="task-timeline-draft-lock" role="status">Finish or discard this draft before editing another comment.</div>
    {/if}

    {#if visibleItems.length}
      <div class="task-timeline-list" aria-live="polite">
        {#each visibleItems as item (item.id)}
          <article class="task-timeline-item" data-kind={item.kind}>
            <span class:agent={item.actor?.kind === 'agent'} class="task-timeline-avatar" aria-hidden="true">{actorInitial(item)}</span>
            <div class="task-timeline-content">
              <div class="task-timeline-item-heading">
                <strong>{actorName(item)}</strong>
                <span class="task-timeline-actor-kind">{actorKind(item)}</span>
                <span class={`task-timeline-kind ${item.kind}`}>{kindLabel(item.kind)}</span>
              </div>
              {#if item.kind === 'agent_progress' && item.progress}
                <p class="task-timeline-summary">{item.progress.summary}</p>
                <dl class="task-timeline-details">
                  {#if item.progress.phase}<div><dt>Phase</dt><dd>{item.progress.phase}</dd></div>{/if}
                  {#if item.progress.next_action}<div><dt>Next</dt><dd>{item.progress.next_action}</dd></div>{/if}
                  {#if item.progress.checkpoint_total !== null && item.progress.checkpoint_total !== undefined}<div><dt>Checkpoints</dt><dd>{item.progress.checkpoint_completed ?? 0} of {item.progress.checkpoint_total}</dd></div>{/if}
                </dl>
              {:else if item.kind === 'comment' && item.comment}
                {@const comment = item.comment}
                {#if editingCommentId === comment.id}
                  <div class="task-timeline-comment-edit">
                    {#if comment.body !== editingOriginalBody}<div class="task-timeline-comment-notice" role="status">This comment changed remotely. Your draft is preserved; saving will check the original version.</div>{/if}
                    <textarea rows="4" value={editingBody} aria-label="Edit comment" disabled={isCommentSaving(comment, pendingCommentMutations)} on:input={updateEditingBody}></textarea>
                    <div class="task-timeline-comment-actions">
                      <button class="button primary" type="button" disabled={!editingBody.trim() || isCommentSaving(comment, pendingCommentMutations)} on:click={() => void saveComment(comment)}>
                        {#if isCommentSaving(comment, pendingCommentMutations)}<span class="button-spinner"></span>{/if}Save
                      </button>
                      <button class="text-button" type="button" disabled={isCommentSaving(comment, pendingCommentMutations)} on:click={cancelEditing}>Cancel</button>
                    </div>
                  </div>
                {:else}
                  <div class="task-timeline-comment">{@html renderMarkdown(comment.body)}</div>
                  {#if canEditComment(comment)}
                    <div class="task-timeline-comment-actions">
                      <button class="text-button" type="button" disabled={Boolean(editingCommentId && editingCommentId !== comment.id)} title={editingCommentId && editingCommentId !== comment.id ? 'Finish or discard the current comment draft first.' : undefined} on:click={() => startEditing(comment)}>Edit</button>
                      <button class="text-button danger-text-button" type="button" disabled={isCommentSaving(comment, pendingCommentMutations) || Boolean(editingCommentId && editingCommentId !== comment.id)} on:click={() => void deleteComment(comment)}>Delete</button>
                    </div>
                  {/if}
                {/if}
                {#if commentError(comment, commentMutationErrors)}<div class="task-timeline-comment-error" role="alert">{commentError(comment, commentMutationErrors)}</div>{/if}
                {#if editingCommentId === comment.id && editingGuardMessage}<div class="task-timeline-comment-error" role="alert">{editingGuardMessage}</div>{/if}
              {:else if item.kind === 'task_change' && item.change}
                <p class="task-timeline-summary">{changeLabel(item)}{#if changeContext(item)}<span> · {changeContext(item)}</span>{/if}</p>
              {/if}
              <time datetime={item.created_at} title={formatDateTime(item.created_at)}>{formatRelative(item.created_at)}</time>
            </div>
          </article>
        {/each}
      </div>
    {:else if !activeDraftHidden}
      <div class="task-timeline-empty"><span class="empty-icon" aria-hidden="true">◌</span><p>No activity matches this filter yet.</p></div>
    {/if}
  {/if}

  {#if hasOlder}
    <button class="button quiet-button task-timeline-load-older" type="button" disabled={loadingOlder} on:click={onLoadOlder}>
      {#if loadingOlder}<span class="button-spinner"></span>{/if}Load older activity
    </button>
  {/if}
</section>

<style>
  .task-timeline-draft-recovery {
    display: grid;
    gap: 9px;
    padding: 11px;
    border: 1px solid color-mix(in srgb, var(--amber), var(--border) 45%);
    border-radius: 9px;
    background: color-mix(in srgb, var(--amber-soft), var(--surface) 55%);
  }

  .task-timeline-draft-recovery-heading {
    display: flex;
    align-items: flex-start;
    justify-content: space-between;
    gap: 10px;
  }

  .task-timeline-draft-recovery-heading strong {
    color: var(--ink-soft);
    font-size: 11px;
  }

  .task-timeline-draft-recovery-heading p {
    margin: 3px 0 0;
    color: var(--muted);
    font-size: 10px;
    line-height: 1.4;
  }

  .task-timeline-comment-notice {
    padding: 7px 9px;
    border-radius: 6px;
    color: var(--amber, #986e19);
    background: var(--amber-soft);
    font-size: 10px;
    line-height: 1.4;
  }

  .task-timeline-draft-lock {
    color: var(--muted);
    font-size: 10px;
    line-height: 1.4;
  }
</style>
