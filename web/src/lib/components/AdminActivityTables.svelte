<script lang="ts">
  import { formatRelative } from '../state';
  import type { AdminMetrics } from '../types';
  import MetricsPanel from './MetricsPanel.svelte';

  // Per-agent activity and the busiest API callers for the selected window.
  export let metrics: AdminMetrics;
  export let fmt: (value: number) => string;
</script>

<MetricsPanel title="Agents" subtitle={`Recorded activity in the last ${metrics.window_days} days`}>
  {#if metrics.agents.length}
    <div class="table-wrap">
      <table>
        <thead><tr><th scope="col">Agent</th><th scope="col" class="num">Actions</th><th scope="col" class="num">Claims</th><th scope="col" class="num">Completed</th><th scope="col">Last active</th></tr></thead>
        <tbody>
          {#each metrics.agents as agent}
            <tr class:disabled={agent.disabled}>
              <td><span class="agent-name">{agent.name}</span>{#if agent.disabled}<span class="tag">disabled</span>{/if}</td>
              <td class="num">{fmt(agent.events)}</td>
              <td class="num">{fmt(agent.claims)}</td>
              <td class="num">{fmt(agent.completions)}</td>
              <td class="muted">{agent.last_event_at ? formatRelative(agent.last_event_at) : agent.token_last_used_at ? `token used ${formatRelative(agent.token_last_used_at)}` : 'Never'}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {:else}
    <div class="panel-empty">No agents yet. Create one in Settings to start tracking agent activity.</div>
  {/if}
</MetricsPanel>

<MetricsPanel title="Top API callers" subtitle={`Requests in the last ${metrics.window_days} days`}>
  {#if metrics.requests.top_actors.length}
    <div class="table-wrap">
      <table>
        <thead><tr><th scope="col">Actor</th><th scope="col" class="num">Requests</th><th scope="col" class="num">Errors</th><th scope="col" class="num">Avg</th><th scope="col">Last seen</th></tr></thead>
        <tbody>
          {#each metrics.requests.top_actors as actor}
            <tr>
              <td><span class="agent-name">{actor.name || 'Unknown actor'}</span><span class="tag" class:agent={actor.kind === 'agent'}>{actor.kind}</span></td>
              <td class="num">{fmt(actor.requests)}</td>
              <td class="num">{fmt(actor.errors)}</td>
              <td class="num">{fmt(actor.avg_ms)} ms</td>
              <td class="muted">{formatRelative(actor.last_seen_at)}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {:else}
    <div class="panel-empty">No authenticated requests in this window.</div>
  {/if}
</MetricsPanel>

<style>
  .muted { color: var(--muted); }
  .table-wrap { overflow-x: auto; }
  table { width: 100%; border-collapse: collapse; font-size: var(--text-sm); }
  th { padding: 6px 8px; border-bottom: 1px solid var(--border); color: var(--muted); font-size: var(--text-xs); font-weight: 600; text-align: left; }
  td { padding: 8px; border-bottom: 1px solid var(--border); color: var(--ink); }
  tr:last-child td { border-bottom: 0; }
  tr.disabled td { opacity: 0.6; }
  .num { text-align: right; font-variant-numeric: tabular-nums; }
  .agent-name { font-weight: 600; }
  .tag { margin-left: 8px; padding: 1px 7px; border-radius: 999px; background: var(--surface-muted); color: var(--muted); font-size: var(--text-xs); }
  .tag.agent { background: var(--purple-soft); color: var(--purple); }
  .panel-empty { padding: 18px 0; color: var(--muted); font-size: var(--text-sm); }
</style>
