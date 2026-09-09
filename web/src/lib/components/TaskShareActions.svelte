<script context="module" lang="ts">
  export type TaskShareCopyKind = 'link' | 'key';
  export type TaskShareFeedback = {
    kind: 'success' | 'error';
    message: string;
  };

  export function taskShareCopyLabel(kind: TaskShareCopyKind): string {
    return kind === 'link' ? 'task link' : 'task key';
  }

  export function taskShareFeedbackMessage(
    kind: TaskShareCopyKind,
    copied: boolean
  ): string {
    const label = taskShareCopyLabel(kind);
    return copied
      ? `${label[0].toUpperCase()}${label.slice(1)} copied to clipboard.`
      : `Couldn’t copy ${label}. Select the value and copy it manually.`;
  }
</script>

<script lang="ts">
  import { copyText, type ClipboardWriter } from '../taskShare';

  export let taskKey = '';
  export let taskUrl = '';
  /** Optional injection point for tests; omitted uses navigator.clipboard. */
  export let clipboard: ClipboardWriter | null | undefined = undefined;

  let copying: TaskShareCopyKind | '' = '';
  let feedback: TaskShareFeedback | null = null;
  let fallbackKind: TaskShareCopyKind | '' = '';
  let feedbackTaskIdentity = '';
  let taskIdentity = '';
  let copyRequest = 0;

  $: taskIdentity = `${taskKey}\u0000${taskUrl}`;
  $: if (feedbackTaskIdentity !== taskIdentity) {
    feedbackTaskIdentity = taskIdentity;
    copyRequest += 1;
    copying = '';
    feedback = null;
    fallbackKind = '';
  }

  async function copy(kind: TaskShareCopyKind): Promise<void> {
    if (copying) return;
    const value = kind === 'link' ? taskUrl : taskKey;
    const request = ++copyRequest;
    const requestTaskIdentity = taskIdentity;
    copying = kind;
    feedback = null;
    fallbackKind = '';
    const result = await copyText(value, clipboard);
    // A drawer can switch tasks while a permission prompt or clipboard write
    // is pending. Never let that old result clear or announce for the new task.
    if (request !== copyRequest || requestTaskIdentity !== taskIdentity) return;
    copying = '';
    feedback = result.ok
      ? { kind: 'success', message: taskShareFeedbackMessage(kind, true) }
      : { kind: 'error', message: taskShareFeedbackMessage(kind, false) };
    fallbackKind = result.ok ? '' : kind;
  }
</script>

<div
  class="task-share-actions"
  data-task-share
  role="group"
  aria-busy={Boolean(copying)}
  aria-label="Share task"
>
  <div class="task-share-buttons">
    <button
      data-task-share-copy="link"
      class="task-share-copy"
      type="button"
      aria-label="Copy task link"
      disabled={!taskUrl || Boolean(copying)}
      on:click={() => void copy('link')}
    >
      <span aria-hidden="true">↗</span>{copying === 'link' ? 'Copying…' : 'Copy link'}
    </button>
    <button
      data-task-share-copy="key"
      class="task-share-copy"
      type="button"
      aria-label="Copy task key"
      disabled={!taskKey || Boolean(copying)}
      on:click={() => void copy('key')}
    >
      <span aria-hidden="true">#</span>{copying === 'key' ? 'Copying…' : 'Copy key'}
    </button>
  </div>

  {#if feedback}
    <p
      class="task-share-feedback"
      class:error={feedback.kind === 'error'}
      class:success={feedback.kind === 'success'}
      role={feedback.kind === 'error' ? 'alert' : 'status'}
      aria-live={feedback.kind === 'error' ? 'assertive' : 'polite'}
      aria-atomic="true"
    >
      <span aria-hidden="true">{feedback.kind === 'error' ? '!' : '✓'}</span>
      {feedback.message}
    </p>
  {/if}

  {#if fallbackKind}
    {@const fallbackLabel = taskShareCopyLabel(fallbackKind)}
    {@const fallbackValue = fallbackKind === 'link' ? taskUrl : taskKey}
    <div class="task-share-fallback" role="group" aria-labelledby="task-share-fallback-heading">
      <strong id="task-share-fallback-heading">Copy {fallbackLabel} manually</strong>
      <input
        data-task-share-fallback={fallbackKind}
        aria-label={`${fallbackLabel} to copy manually`}
        aria-describedby="task-share-fallback-help"
        readonly
        value={fallbackValue}
      />
      <p id="task-share-fallback-help">Clipboard access was blocked. Select this value and copy it manually.</p>
    </div>
  {/if}
</div>

<style>
  .task-share-actions {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 7px;
    min-width: 0;
    padding: 7px 22px;
    border-bottom: 1px solid var(--border);
    background: var(--surface);
  }

  .task-share-buttons {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    flex: 0 0 auto;
  }

  .task-share-copy {
    min-height: 30px;
    display: inline-flex;
    align-items: center;
    gap: 5px;
    padding: 0 9px;
    border: 1px solid var(--border);
    border-radius: 7px;
    color: var(--muted);
    background: var(--surface-muted);
    font-size: 10px;
    font-weight: 800;
  }

  .task-share-copy:hover:not(:disabled),
  .task-share-copy:focus-visible {
    color: var(--purple);
    border-color: color-mix(in srgb, var(--purple), var(--border) 50%);
    background: var(--surface-hover);
  }

  .task-share-copy:disabled {
    cursor: wait;
    opacity: .65;
  }

  .task-share-feedback {
    display: inline-flex;
    align-items: flex-start;
    gap: 5px;
    min-width: 0;
    max-width: 100%;
    margin: 0;
    padding: 5px 7px;
    border: 1px solid var(--border);
    border-radius: 7px;
    color: var(--ink-soft);
    background: var(--surface-muted);
    font-size: 10px;
    font-weight: 700;
    line-height: 1.35;
  }

  .task-share-feedback > span {
    flex: 0 0 auto;
    font-weight: 900;
  }

  .task-share-feedback.success {
    color: var(--green);
    border-color: color-mix(in srgb, var(--green), var(--border) 60%);
    background: var(--green-soft);
  }

  .task-share-feedback.error {
    color: var(--red);
    border-color: color-mix(in srgb, var(--red), var(--border) 60%);
    background: var(--red-soft);
  }

  .task-share-fallback {
    display: grid;
    gap: 4px;
    flex: 1 1 100%;
    min-width: 0;
    padding-top: 6px;
    border-top: 1px dashed var(--border-strong);
    color: var(--ink-soft);
    font-size: 10px;
  }

  .task-share-fallback input {
    min-width: 0;
    width: 100%;
    min-height: 30px;
    padding: 0 8px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    border: 1px solid var(--border);
    border-radius: 8px;
    color: var(--ink);
    background: var(--surface);
    font: 11px ui-monospace, SFMono-Regular, Menlo, monospace;
    user-select: text;
  }

  .task-share-fallback p {
    margin: 0;
    color: var(--muted);
    font-size: 10px;
    line-height: 1.4;
  }

  @media (max-width: 600px) {
    .task-share-actions { padding-right: 16px; padding-left: 16px; }
    .task-share-copy { min-height: 38px; }
    .task-share-fallback input { min-height: 38px; }
  }
</style>
