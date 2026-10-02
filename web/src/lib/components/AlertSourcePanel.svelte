<script lang="ts">
  import type { AlertSource } from '../types';

  export let source: AlertSource;
  export let onOpenPrevious: (taskId: string) => void | Promise<void> = () => undefined;

  const integrationLabels: Record<string, string> = { coolify: 'Coolify' };
  const alertLabels: Record<string, string> = { traefik_version_outdated: 'Traefik version outdated' };
  // Known evidence keys first, in reading order; unknown keys follow.
  const evidenceLabels: Record<string, string> = {
    current_version: 'Installed',
    latest_version: 'Offered',
    update_type: 'Update type',
    upgrade_target: 'Upgrade target',
    newer_branch_target: 'Newer branch',
    newer_branch_latest: 'Newer branch latest'
  };

  $: evidence = Object.entries(source.evidence || {}).sort(([a], [b]) => evidenceRank(a) - evidenceRank(b) || a.localeCompare(b));

  function evidenceRank(key: string): number {
    const index = Object.keys(evidenceLabels).indexOf(key);
    return index === -1 ? Number.MAX_SAFE_INTEGER : index;
  }

  function humanize(value: string): string {
    return value.replace(/[._-]+/g, ' ').replace(/^\w/, (letter) => letter.toUpperCase());
  }

  function formatDateTime(value: string): string {
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return value;
    return new Intl.DateTimeFormat(undefined, { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' }).format(date);
  }
</script>

<section class="drawer-section alert-source-section" aria-labelledby="alert-source-heading">
  <div class="section-heading-inline">
    <h2 id="alert-source-heading">Alert source</h2>
    <span class="optional">{integrationLabels[source.integration] || humanize(source.integration)} · read-only</span>
  </div>
  <dl class="alert-source-facts">
    <div><dt>Alert</dt><dd>{alertLabels[source.alert_type] || humanize(source.alert_type)}</dd></div>
    <div><dt>Server</dt><dd>{source.resource_name}</dd></div>
    {#each evidence as [key, value] (key)}
      <div><dt>{evidenceLabels[key] || humanize(key)}</dt><dd>{#if key === 'update_type'}{humanize(value)}{:else}<code>{value}</code>{/if}</dd></div>
    {/each}
    <div><dt>Received</dt><dd>{source.occurrence_count === 1 ? 'Once' : `${source.occurrence_count} times`}</dd></div>
    <div><dt>First</dt><dd><time datetime={source.first_received_at}>{formatDateTime(source.first_received_at)}</time></dd></div>
    {#if source.occurrence_count > 1}
      <div><dt>Last</dt><dd><time datetime={source.last_received_at}>{formatDateTime(source.last_received_at)}</time></dd></div>
    {/if}
    {#if source.previous_task_id && source.previous_task_key}
      <div><dt>Previous</dt><dd><button class="text-button" type="button" on:click={() => onOpenPrevious(source.previous_task_id || '')}>{source.previous_task_key}</button></dd></div>
    {/if}
  </dl>
</section>

<style>
  .alert-source-facts { display: grid; grid-template-columns: repeat(auto-fill, minmax(150px, 1fr)); gap: 8px 16px; margin: 0; }
  .alert-source-facts div { min-width: 0; }
  .alert-source-facts dt { color: var(--muted); font-size: 11px; font-weight: 700; text-transform: uppercase; letter-spacing: 0.04em; }
  .alert-source-facts dd { margin: 2px 0 0; overflow-wrap: anywhere; }
  .alert-source-facts code { font-size: 12px; }
</style>
