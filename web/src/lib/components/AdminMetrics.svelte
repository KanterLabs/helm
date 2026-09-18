<script lang="ts">
  import { onDestroy, onMount } from 'svelte';
  import { api } from '../api';
  import { formatRelative } from '../state';
  import type { AdminMetrics } from '../types';

  const windows = [7, 30, 90];
  const chartHeight = 140;

  let days = 7;
  let metrics: AdminMetrics | null = null;
  let loading = true;
  let error = '';
  let requestId = 0;
  let dailyHover = -1;
  let hourlyHover = -1;
  let refreshTimer: ReturnType<typeof setInterval> | undefined;

  async function load(showSpinner = true) {
    const current = ++requestId;
    if (showSpinner) loading = true;
    error = '';
    try {
      const result = await api.adminMetrics(days);
      if (current === requestId) metrics = result;
    } catch (err) {
      if (current === requestId) error = err instanceof Error ? err.message : 'Could not load metrics';
    } finally {
      if (current === requestId) loading = false;
    }
  }

  function selectWindow(next: number) {
    if (next === days) return;
    days = next;
    dailyHover = -1;
    void load();
  }

  onMount(() => {
    void load();
    refreshTimer = setInterval(() => {
      if (document.visibilityState === 'visible') void load(false);
    }, 30_000);
  });
  onDestroy(() => clearInterval(refreshTimer));

  const number = new Intl.NumberFormat();
  const fmt = (value: number) => number.format(value);

  function niceMax(value: number): number {
    if (value <= 4) return 4;
    const magnitude = 10 ** Math.floor(Math.log10(value));
    for (const step of [1, 2, 2.5, 5, 10]) if (step * magnitude >= value) return step * magnitude;
    return 10 * magnitude;
  }

  function shortDate(date: string): string {
    return new Date(`${date}T00:00:00Z`).toLocaleDateString(undefined, { month: 'short', day: 'numeric', timeZone: 'UTC' });
  }

  function hourLabel(hour: string): string {
    return new Date(hour).toLocaleTimeString(undefined, { hour: 'numeric' });
  }

  $: daily = metrics?.daily ?? [];
  $: dailyMax = niceMax(Math.max(0, ...daily.map((d) => Math.max(d.tasks_created, d.tasks_completed))));
  $: dailySlot = daily.length ? 100 / daily.length : 0;
  $: dailyLabelEvery = Math.max(1, Math.ceil(daily.length / 8));

  $: hourly = metrics?.requests.hourly ?? [];
  $: hourlyMax = niceMax(Math.max(0, ...hourly.map((h) => h.agent + h.human)));
  $: hourlySlot = hourly.length ? 100 / hourly.length : 0;

  $: totals = metrics?.totals;
  $: activity = metrics?.activity;
  $: requests = metrics?.requests;
  $: agentShare = requests && requests.total ? Math.round((requests.agent / requests.total) * 100) : 0;
  $: errorRate = requests && requests.total ? ((requests.errors / requests.total) * 100).toFixed(1) : '0.0';

  function barHeight(value: number, max: number): number {
    return max ? (value / max) * chartHeight : 0;
  }
</script>

<section class="admin-metrics" aria-busy={loading}>
  <section class="page-heading">
    <div>
      <div class="breadcrumbs"><span>Workspace</span><span>/</span><span>Administration</span></div>
      <h1>Metrics <span class="beta-badge">Beta</span></h1>
      <p>Workspace activity, agent usage, and API traffic across every project.</p>
    </div>
    <div class="heading-actions">
      <div class="window-picker" role="group" aria-label="Time window">
        {#each windows as option}
          <button type="button" class:active={days === option} aria-pressed={days === option} on:click={() => selectWindow(option)}>{option}d</button>
        {/each}
      </div>
      <button class="button quiet-button" type="button" on:click={() => load()}>↻ Refresh</button>
    </div>
  </section>

  {#if error}
    <div class="inline-alert error content-alert" role="alert"><span>!</span>{error}<button class="text-button" type="button" on:click={() => load()}>Retry</button></div>
  {/if}

  {#if loading && !metrics}
    <div class="roadmap-skeleton"><div></div><div></div><div></div></div>
  {:else if metrics && totals && activity && requests}
    <div class="stat-grid">
      <div class="stat"><span class="stat-label">Tasks created</span><strong>{fmt(activity.tasks_created)}</strong><span class="stat-note">Last {metrics.window_days} days</span></div>
      <div class="stat"><span class="stat-label">Tasks completed</span><strong>{fmt(activity.tasks_completed)}</strong><span class="stat-note">Last {metrics.window_days} days</span></div>
      <div class="stat"><span class="stat-label">Agent actions</span><strong>{fmt(activity.agent_events)}</strong><span class="stat-note">{fmt(activity.claims)} claims · {fmt(activity.progress_updates)} progress updates</span></div>
      <div class="stat"><span class="stat-label">API requests</span><strong>{fmt(requests.total)}</strong><span class="stat-note">{agentShare}% from agents · last {metrics.window_days} days</span></div>
      <div class="stat"><span class="stat-label">Comments</span><strong>{fmt(activity.comments)}</strong><span class="stat-note">Last {metrics.window_days} days</span></div>
      <div class="stat"><span class="stat-label">Bugs</span><strong>{fmt(activity.bugs_reported)} <small>/ {fmt(activity.bugs_resolved)}</small></strong><span class="stat-note">Reported / resolved</span></div>
      <div class="stat"><span class="stat-label">Error rate</span><strong>{errorRate}%</strong><span class="stat-note">{fmt(requests.errors)} responses ≥ 400 · avg {fmt(requests.avg_ms)} ms</span></div>
      <div class="stat"><span class="stat-label">Active claims</span><strong>{fmt(totals.claims_active)}</strong><span class="stat-note">Agents holding work now</span></div>
    </div>

    <div class="chart-grid">
      <section class="panel">
        <div class="panel-title">
          <div><h2>Tasks per day</h2><p>Created vs. completed, UTC days</p></div>
          <div class="legend"><span><i class="swatch a"></i>Created</span><span><i class="swatch b"></i>Completed</span></div>
        </div>
        <div class="chart" role="img" aria-label={`Tasks created and completed per day over the last ${metrics.window_days} days`}>
          <div class="axis-labels"><span>{fmt(dailyMax)}</span><span>{fmt(dailyMax / 2)}</span><span>0</span></div>
          <svg role="presentation" viewBox={`0 0 100 ${chartHeight}`} preserveAspectRatio="none" on:mouseleave={() => (dailyHover = -1)}>
            <line class="grid" x1="0" x2="100" y1={chartHeight / 2} y2={chartHeight / 2} />
            <line class="baseline" x1="0" x2="100" y1={chartHeight} y2={chartHeight} />
            {#each daily as day, index}
              {@const barWidth = Math.max(0.4, dailySlot * 0.36)}
              {@const x = index * dailySlot + dailySlot / 2}
              <rect class="bar a" class:dim={dailyHover !== -1 && dailyHover !== index} x={x - barWidth - 0.15} width={barWidth} y={chartHeight - barHeight(day.tasks_created, dailyMax)} height={barHeight(day.tasks_created, dailyMax)} />
              <rect class="bar b" class:dim={dailyHover !== -1 && dailyHover !== index} x={x + 0.15} width={barWidth} y={chartHeight - barHeight(day.tasks_completed, dailyMax)} height={barHeight(day.tasks_completed, dailyMax)} />
              <rect class="hit" x={index * dailySlot} width={dailySlot} y="0" height={chartHeight} on:mouseenter={() => (dailyHover = index)} role="presentation" />
            {/each}
          </svg>
          {#if dailyHover >= 0 && daily[dailyHover]}
            <div class="tooltip" style={`left: calc(34px + (100% - 34px) * ${(dailyHover + 0.5) * dailySlot / 100})`}>
              <strong>{shortDate(daily[dailyHover].date)}</strong>
              <span><i class="swatch a"></i>Created <b>{fmt(daily[dailyHover].tasks_created)}</b></span>
              <span><i class="swatch b"></i>Completed <b>{fmt(daily[dailyHover].tasks_completed)}</b></span>
              <span class="muted">{fmt(daily[dailyHover].agent_events)} agent · {fmt(daily[dailyHover].human_events)} human events</span>
            </div>
          {/if}
          <div class="x-labels">
            {#each daily as day, index}<span style={`left: ${(index + 0.5) * dailySlot}%`}>{index % dailyLabelEvery === 0 ? shortDate(day.date) : ''}</span>{/each}
          </div>
        </div>
      </section>

      <section class="panel">
        <div class="panel-title">
          <div><h2>API requests, last 24h</h2><p>Hourly, updated about once a minute</p></div>
          <div class="legend"><span><i class="swatch a"></i>Agents</span><span><i class="swatch b"></i>Humans</span></div>
        </div>
        <div class="chart" role="img" aria-label="API requests per hour over the last 24 hours, split by agents and humans">
          <div class="axis-labels"><span>{fmt(hourlyMax)}</span><span>{fmt(hourlyMax / 2)}</span><span>0</span></div>
          <svg role="presentation" viewBox={`0 0 100 ${chartHeight}`} preserveAspectRatio="none" on:mouseleave={() => (hourlyHover = -1)}>
            <line class="grid" x1="0" x2="100" y1={chartHeight / 2} y2={chartHeight / 2} />
            <line class="baseline" x1="0" x2="100" y1={chartHeight} y2={chartHeight} />
            {#each hourly as hour, index}
              {@const barWidth = hourlySlot * 0.66}
              {@const x = index * hourlySlot + (hourlySlot - barWidth) / 2}
              {@const agentH = barHeight(hour.agent, hourlyMax)}
              {@const humanH = barHeight(hour.human, hourlyMax)}
              <rect class="bar a" class:dim={hourlyHover !== -1 && hourlyHover !== index} {x} width={barWidth} y={chartHeight - agentH} height={agentH} />
              <rect class="bar b" class:dim={hourlyHover !== -1 && hourlyHover !== index} {x} width={barWidth} y={chartHeight - agentH - humanH - (agentH && humanH ? 1.5 : 0)} height={humanH} />
              <rect class="hit" x={index * hourlySlot} width={hourlySlot} y="0" height={chartHeight} on:mouseenter={() => (hourlyHover = index)} role="presentation" />
            {/each}
          </svg>
          {#if hourlyHover >= 0 && hourly[hourlyHover]}
            <div class="tooltip" style={`left: calc(34px + (100% - 34px) * ${(hourlyHover + 0.5) * hourlySlot / 100})`}>
              <strong>{hourLabel(hourly[hourlyHover].hour)}</strong>
              <span><i class="swatch a"></i>Agents <b>{fmt(hourly[hourlyHover].agent)}</b></span>
              <span><i class="swatch b"></i>Humans <b>{fmt(hourly[hourlyHover].human)}</b></span>
              <span class="muted">{fmt(hourly[hourlyHover].errors)} errors · {fmt(hourly[hourlyHover].anonymous)} unauthenticated</span>
            </div>
          {/if}
          <div class="x-labels">
            {#each hourly as hour, index}<span style={`left: ${(index + 0.5) * hourlySlot}%`}>{index % 6 === 0 ? hourLabel(hour.hour) : ''}</span>{/each}
          </div>
        </div>
      </section>
    </div>

    <div class="chart-grid">
      <section class="panel">
        <div class="panel-title"><div><h2>Agents</h2><p>Recorded activity in the last {metrics.window_days} days</p></div></div>
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
      </section>

      <section class="panel">
        <div class="panel-title"><div><h2>Top API callers</h2><p>Requests in the last {metrics.window_days} days</p></div></div>
        {#if requests.top_actors.length}
          <div class="table-wrap">
            <table>
              <thead><tr><th scope="col">Actor</th><th scope="col" class="num">Requests</th><th scope="col" class="num">Errors</th><th scope="col" class="num">Avg</th><th scope="col">Last seen</th></tr></thead>
              <tbody>
                {#each requests.top_actors as actor}
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
      </section>
    </div>

    <p class="footnote">
      {fmt(totals.projects)} projects · {fmt(totals.tasks_open)} open / {fmt(totals.tasks_done)} done tasks · {fmt(totals.bugs_open)} open bugs ·
      {fmt(totals.humans)} people · {fmt(totals.agents)} agents · {fmt(totals.active_tokens)} active tokens.
      Activity comes from the event log; request counts are saved hourly, kept for 90 days, and can lag by up to a minute.
      {#if metrics.release_sha}Release <code>{metrics.release_sha.slice(0, 7)}</code>.{/if}
    </p>
  {/if}
</section>

<style>
  .admin-metrics { --series-a: #6d5efc; --series-b: #23875f; display: grid; gap: 18px; }
  :global(.app-shell.dark-mode) .admin-metrics { --series-a: #7d70ff; --series-b: #3aa982; }
  .beta-badge { display: inline-block; margin-left: 8px; padding: 2px 8px; border-radius: 999px; background: var(--purple-soft); color: var(--purple); font-size: 12px; font-weight: 600; vertical-align: middle; }
  .window-picker { display: inline-flex; padding: 2px; border: 1px solid var(--border); border-radius: 8px; background: var(--surface); }
  .window-picker button { padding: 5px 11px; border: 0; border-radius: 6px; background: transparent; color: var(--muted); font: inherit; font-size: 13px; cursor: pointer; }
  .window-picker button.active { background: var(--purple-soft); color: var(--purple); font-weight: 600; }
  .stat-grid { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 12px; }
  .stat { display: grid; gap: 3px; padding: 14px 16px; border: 1px solid var(--border); border-radius: 11px; background: var(--surface); }
  .stat-label { color: var(--muted); font-size: 12.5px; font-weight: 600; }
  .stat strong { color: var(--ink); font-size: 26px; font-variant-numeric: tabular-nums; }
  .stat strong small { color: var(--muted); font-size: 16px; font-weight: 500; }
  .stat-note { color: var(--faint); font-size: 12px; }
  .chart-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(360px, 1fr)); gap: 12px; }
  .panel { min-width: 0; padding: 16px; border: 1px solid var(--border); border-radius: 11px; background: var(--surface); }
  .panel-title { display: flex; justify-content: space-between; align-items: flex-start; gap: 12px; margin-bottom: 12px; }
  .panel-title h2 { margin: 0; font-size: 15px; }
  .panel-title p { margin: 2px 0 0; color: var(--muted); font-size: 12.5px; }
  .legend { display: flex; gap: 12px; color: var(--ink-soft); font-size: 12px; white-space: nowrap; }
  .legend span, .tooltip span { display: inline-flex; align-items: center; gap: 6px; }
  .swatch { display: inline-block; width: 10px; height: 10px; border-radius: 3px; }
  .swatch.a { background: var(--series-a); }
  .swatch.b { background: var(--series-b); }
  .chart { position: relative; padding-left: 34px; padding-bottom: 22px; }
  .chart svg { display: block; width: 100%; height: 140px; overflow: visible; }
  .axis-labels { position: absolute; top: -6px; left: 0; bottom: 16px; display: flex; flex-direction: column; justify-content: space-between; color: var(--faint); font-size: 11px; font-variant-numeric: tabular-nums; }
  .grid { stroke: var(--border); stroke-width: 1; stroke-dasharray: 2 3; vector-effect: non-scaling-stroke; }
  .baseline { stroke: var(--border-strong); stroke-width: 1; vector-effect: non-scaling-stroke; }
  .bar { transition: opacity 120ms ease; }
  .bar.a { fill: var(--series-a); }
  .bar.b { fill: var(--series-b); }
  .bar.dim { opacity: 0.35; }
  .hit { fill: transparent; }
  .x-labels { position: absolute; left: 34px; right: 0; bottom: 0; height: 16px; }
  .x-labels span { position: absolute; transform: translateX(-50%); color: var(--faint); font-size: 11px; white-space: nowrap; }
  .tooltip { position: absolute; top: -8px; z-index: 2; display: grid; gap: 3px; min-width: 150px; padding: 8px 10px; border: 1px solid var(--border-strong); border-radius: 8px; background: var(--surface-raised); box-shadow: 0 6px 20px rgb(0 0 0 / 0.12); color: var(--ink); font-size: 12px; pointer-events: none; transform: translateX(-50%); }
  .tooltip b { margin-left: auto; padding-left: 12px; font-variant-numeric: tabular-nums; }
  .muted { color: var(--muted); }
  .table-wrap { overflow-x: auto; }
  table { width: 100%; border-collapse: collapse; font-size: 13px; }
  th { padding: 6px 8px; border-bottom: 1px solid var(--border); color: var(--muted); font-size: 12px; font-weight: 600; text-align: left; }
  td { padding: 8px; border-bottom: 1px solid var(--border); color: var(--ink); }
  tr:last-child td { border-bottom: 0; }
  tr.disabled td { opacity: 0.6; }
  .num { text-align: right; font-variant-numeric: tabular-nums; }
  .agent-name { font-weight: 600; }
  .tag { margin-left: 8px; padding: 1px 7px; border-radius: 999px; background: var(--surface-muted); color: var(--muted); font-size: 11px; }
  .tag.agent { background: var(--purple-soft); color: var(--purple); }
  .panel-empty { padding: 18px 0; color: var(--muted); font-size: 13px; }
  .footnote { margin: 0; color: var(--faint); font-size: 12px; line-height: 1.5; }
  @media (max-width: 1100px) { .stat-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
  @media (max-width: 720px) { .chart-grid { grid-template-columns: 1fr; } }
  @media (prefers-reduced-motion: reduce) { .bar { transition: none; } }
</style>
