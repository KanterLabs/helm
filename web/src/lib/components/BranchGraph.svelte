<svelte:options runes={true} />

<script lang="ts">
  // Branches drawn as lanes forking from the base rail: a lane starts where
  // the branch fell behind and grows by its commits ahead. Labels are real
  // text beside each tip; the SVG carries only geometry.
  import { branchCounts, layoutBranchGraph } from '../branchGraph';
  import type { BranchBucket, BranchVisibility } from '../branchGraph';
  import type { GithubBranchReport } from '../types';

  let { report, show, now, onToggle }: {
    report: GithubBranchReport;
    show: BranchVisibility;
    now: number;
    onToggle: (bucket: BranchBucket) => void;
  } = $props();

  const buckets: ReadonlyArray<{ key: BranchBucket; label: string }> = [
    { key: 'active', label: 'Active' },
    { key: 'stale', label: 'Stale' },
    { key: 'merged', label: 'Merged' }
  ];
  let width = $state(0);
  let hovered = $state('');
  const base = $derived(report.base);
  const staging = $derived(report.staging);
  const counts = $derived(branchCounts(report, now));
  const layout = $derived(layoutBranchGraph(report, show, width || 712, now));
  const uncompared = $derived(report.branches.filter((branch) => !branch.compared));
  const drawn = $derived(layout.rows.filter((row) => row.kind !== 'staging').length);
  const headline = $derived(!base ? '' : staging?.compared
    ? `${staging.name} is ${staging.ahead} ahead, ${staging.behind} behind ${base.name}`
    : staging ? `${staging.name} has not been compared with ${base.name} yet` : `No beta branch to compare with ${base.name}`);

  const branchURL = (name: string) => `https://github.com/${report.repository}/tree/${name.split('/').map(encodeURIComponent).join('/')}`;
  const prefix = (name: string) => name.includes('/') ? name.slice(0, name.indexOf('/') + 1) : '';
</script>

{#if base}
  <section class="branch-panel" aria-label={`Branches of ${report.repository}`}>
    <div class="branch-top">
      <div class="branch-headline" role="heading" aria-level="2" aria-label={headline} data-branch-headline>
        {#if staging?.compared}
          <span class="trunk-pill staging"><i></i>{staging.name}</span><span class="link staging"></span>
          <span class="stat ahead"><span class="arrow">↑</span><b>{staging.ahead}</b><em>ahead</em></span>
          <span class="divider"></span>
          <span class="stat behind"><span class="arrow">↓</span><b>{staging.behind}</b><em>behind</em></span>
          <span class="link main"></span><span class="trunk-pill main"><i></i>{base.name}</span>
        {:else}
          <span class="trunk-pill main"><i></i>{base.name}</span><em class="no-staging">{headline}</em>
        {/if}
      </div>
      <div class="branch-toggles" role="group" aria-label="Branches shown">
        {#each buckets as bucket (bucket.key)}
          <button type="button" class="branch-toggle" class:active={show[bucket.key]} aria-pressed={show[bucket.key]} data-branch-toggle={bucket.key} on:click={() => onToggle(bucket.key)}>{bucket.label}<b>{counts[bucket.key]}</b></button>
        {/each}
      </div>
    </div>

    <div class="branch-graph" bind:clientWidth={width} style:height={`${layout.height}px`} data-branch-graph>
      <svg width={layout.width} height={layout.height} viewBox={`0 0 ${layout.width} ${layout.height}`} aria-hidden="true" focusable="false">
        <defs>
          <linearGradient id="branch-rail-fade" gradientUnits="userSpaceOnUse" x1="0" y1="0" x2={Math.min(60, layout.railEnd / 3)} y2="0">
            <stop offset="0" class="fade-from" /><stop offset="1" class="fade-to" />
          </linearGradient>
        </defs>
        {#each layout.ticks as tick (tick.label)}
          <path class="grid" d={`M${tick.x} ${layout.railY}V${layout.height}`} />
          <text x={tick.x} y={layout.railY - 10} text-anchor="middle">{tick.label}</text>
        {/each}
        {#each layout.rows as row (row.branch.name)}
          <g class={`lane ${row.kind}`} class:in-staging={row.branch.in_staging === true} class:hot={hovered === row.branch.name}>
            <path class="connector" d={`M${row.forkX} ${layout.railY}V${row.y - 8}Q${row.forkX} ${row.y} ${row.forkX + 8} ${row.y}`} />
            <path class="ahead" d={`M${row.forkX + 8} ${row.y}H${row.branch.ahead ? row.tipX : row.tipX - 5}`} />
            {#each row.dots as dot (dot)}<circle class="commit" cx={dot} cy={row.y} r={row.kind === 'staging' ? 4.5 : 2.8} />{/each}
            <circle class="tip" cx={row.tipX} cy={row.y} r={row.kind === 'staging' ? 5.5 : row.branch.ahead ? 4.4 : 3.6} />
          </g>
        {/each}
        <path class="rail" d={`M0 ${layout.railY}H${layout.railEnd}`} stroke="url(#branch-rail-fade)" />
        <circle class="rail-tip" cx={layout.railEnd} cy={layout.railY} r="6" />
      </svg>
      <ul class="branch-labels">
        <li class="branch-label" style={`left:${layout.railEnd + 14}px;top:${layout.railY - 11}px`} data-branch-row={base.name}>
          <span class="trunk-pill main small">{base.name}</span>
          {#if layout.wide}<code>{base.sha.slice(0, 7)}</code>{/if}
        </li>
        {#each layout.rows as row (row.branch.name)}
          <!-- svelte-ignore a11y_no_noninteractive_element_interactions -->
          <li class={`branch-label ${row.kind}`} style={`left:${Math.round(row.tipX + 13)}px;top:${row.y - 11}px`}
            title={`${row.branch.name}: ${row.branch.ahead} ahead, ${row.branch.behind} behind ${base.name}`}
            data-branch-row={row.branch.name} data-ahead={row.branch.ahead} data-behind={row.branch.behind} data-kind={row.kind}
            on:mouseenter={() => hovered = row.branch.name} on:mouseleave={() => hovered = ''}>
            {#if row.kind === 'staging'}
              <span class="trunk-pill staging small">{row.branch.name}</span>
            {:else}
              <a class="name" href={branchURL(row.branch.name)} target="_blank" rel="noreferrer"><span class="prefix">{prefix(row.branch.name)}</span>{row.branch.name.slice(prefix(row.branch.name).length)}</a>
            {/if}
            {#if row.branch.ahead}
              <span class="count up">↑{row.branch.ahead}<span class="sr-only"> commits ahead</span></span>
            {:else}
              <span class="count">merged</span>
            {/if}
            <span class="count">↓{row.branch.behind}<span class="sr-only"> commits behind</span></span>
            {#if row.branch.in_staging && staging}<span class="in-staging-tag"><i></i><span class:sr-only={!layout.wide}>in {staging.name}</span></span>{/if}
            {#if layout.wide && row.age}<span class="age">{row.age}</span>{/if}
          </li>
        {/each}
      </ul>
    </div>

    {#if !drawn}
      <p class="branch-note" data-branch-empty>No branches match the filters that are on. Turn on another filter to see the rest.</p>
    {/if}
    {#if uncompared.length}
      <p class="branch-note" data-branch-uncompared>
        <strong>Not compared yet ({uncompared.length}):</strong>
        {uncompared.slice(0, 12).map((branch) => branch.name).join(', ')}{uncompared.length > 12 ? ` and ${uncompared.length - 12} more` : ''}
      </p>
    {/if}
    <p class="branch-note">Further left means further behind {base.name} (log scale). Each dot is one commit ahead.</p>
  </section>
{/if}

<style>
  .branch-panel { --trunk-main: #6d5efc; --trunk-staging: #23875f; --lane: color-mix(in srgb, var(--ink) 82%, transparent); display: grid; grid-template-columns: minmax(0, 1fr); gap: 14px; min-width: 0; padding: 20px; border: 1px solid var(--border); border-radius: 13px; background: var(--surface); }
  :global(.app-shell.dark-mode) .branch-panel { --trunk-main: #8377ff; --trunk-staging: #3fb58a; }
  .branch-top { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 12px 18px; }
  .branch-headline { display: flex; align-items: center; flex-wrap: wrap; gap: 8px 12px; min-width: 0; }
  .branch-headline > * { flex: none; white-space: nowrap; }
  .trunk-pill { display: inline-flex; align-items: center; gap: 8px; flex: none; padding: 4px 14px 4px 11px; border-radius: 999px; color: var(--ink); font: 600 var(--text-base) var(--font-mono); }
  .trunk-pill.small { padding: 2px 10px; font-size: var(--text-xs); }
  .trunk-pill i { width: 8px; height: 8px; border-radius: 50%; }
  .trunk-pill.main { background: color-mix(in srgb, var(--trunk-main) 20%, transparent); }
  .trunk-pill.staging { background: color-mix(in srgb, var(--trunk-staging) 20%, transparent); }
  .trunk-pill.main i { background: var(--trunk-main); box-shadow: 0 0 8px var(--trunk-main); }
  .trunk-pill.staging i { background: var(--trunk-staging); box-shadow: 0 0 8px var(--trunk-staging); }
  .link { width: 28px; height: 2px; border-radius: 2px; }
  .link.staging { background: linear-gradient(90deg, var(--trunk-staging), transparent); }
  .link.main { background: linear-gradient(90deg, transparent, var(--trunk-main)); }
  .stat { display: inline-flex; align-items: baseline; gap: 5px; }
  .stat b { font: 600 28px/1 var(--font-sans); letter-spacing: -.03em; }
  .stat em, .no-staging { color: var(--muted); font-size: var(--text-sm); font-style: normal; }
  .stat .arrow { position: relative; top: -2px; font-size: var(--text-md); font-weight: 600; }
  .stat.ahead .arrow { color: var(--trunk-staging); }
  .stat.behind .arrow { color: var(--trunk-main); }
  .divider { width: 1px; height: 20px; background: var(--border-strong); }
  .branch-toggles { display: flex; gap: 6px; }
  .branch-toggle { min-height: 30px; padding: 0 12px; border: 1px solid var(--border); border-radius: 999px; color: var(--muted); background: transparent; font-size: var(--text-xs); font-weight: 600; }
  .branch-toggle b { margin-left: 6px; font-variant-numeric: tabular-nums; }
  .branch-toggle:hover { color: var(--ink); border-color: var(--border-strong); }
  .branch-toggle.active { color: var(--purple); border-color: color-mix(in srgb, var(--purple), var(--border) 55%); background: var(--purple-soft); }

  .branch-graph { position: relative; min-width: 0; }
  /* Out of flow: the drawing must never widen the panel it is measured from. */
  svg { position: absolute; top: 0; left: 0; display: block; overflow: visible; }
  text { fill: var(--muted); font: 10px var(--font-mono); }
  .grid { stroke: color-mix(in srgb, var(--ink) 7%, transparent); stroke-width: 1; }
  .fade-from { stop-color: var(--trunk-main); stop-opacity: 0; }
  .fade-to { stop-color: var(--trunk-main); }
  .rail { fill: none; stroke-width: 3; filter: drop-shadow(0 0 6px color-mix(in srgb, var(--trunk-main) 70%, transparent)); }
  .rail-tip { fill: var(--trunk-main); stroke: var(--surface); stroke-width: 2; filter: drop-shadow(0 0 7px var(--trunk-main)); }
  .connector { fill: none; stroke: color-mix(in srgb, var(--trunk-main) 42%, transparent); stroke-width: 1.5; transition: stroke var(--dur-fast) var(--ease-out); }
  .ahead { fill: none; stroke: var(--lane); stroke-width: 2; stroke-linecap: round; }
  .commit { fill: var(--ink); stroke: var(--surface); stroke-width: 1.5; }
  .tip { fill: var(--ink); stroke: var(--surface); stroke-width: 2; filter: drop-shadow(0 0 4px color-mix(in srgb, var(--ink) 45%, transparent)); }
  .lane.hot .connector { stroke: var(--trunk-main); }
  .lane.staging .connector, .lane.staging .ahead { stroke: var(--trunk-staging); stroke-width: 3; filter: drop-shadow(0 0 5px color-mix(in srgb, var(--trunk-staging) 60%, transparent)); }
  .lane.staging .commit, .lane.staging .tip, .lane.in-staging .tip { fill: var(--trunk-staging); }
  .lane.staging .tip, .lane.in-staging .tip { filter: drop-shadow(0 0 6px color-mix(in srgb, var(--trunk-staging) 80%, transparent)); }
  .lane.merged .ahead { stroke: color-mix(in srgb, var(--trunk-main) 42%, transparent); stroke-width: 1.5; }
  .lane.merged .tip { fill: var(--surface); stroke: color-mix(in srgb, var(--trunk-main) 75%, transparent); stroke-width: 1.5; filter: none; }
  .lane.stale { opacity: .5; }
  .lane.stale.hot { opacity: 1; }

  .branch-labels { margin: 0; padding: 0; list-style: none; }
  .branch-label { position: absolute; right: 0; height: 22px; display: flex; align-items: center; gap: 8px; min-width: 0; white-space: nowrap; font-size: var(--text-xs); }
  .branch-label > * { flex: none; }
  .branch-label .name { flex: 0 1 auto; min-width: 0; overflow: hidden; color: var(--ink); font-family: var(--font-mono); text-decoration: none; text-overflow: ellipsis; }
  .branch-label .name:hover { text-decoration: underline; }
  .branch-label .prefix, .branch-label code { color: var(--muted); }
  .branch-label code { font: var(--text-xs) var(--font-mono); }
  .branch-label.merged .name, .branch-label.stale .name { color: var(--muted); }
  .count { color: var(--muted); font-variant-numeric: tabular-nums; }
  .count.up { color: var(--ink); font-weight: 600; }
  .in-staging-tag { display: inline-flex; align-items: center; gap: 5px; color: var(--muted); font-size: var(--text-2xs); }
  .in-staging-tag i { width: 6px; height: 6px; border-radius: 50%; background: var(--trunk-staging); }
  .age { margin-left: auto; padding-left: 8px; color: var(--muted); font-size: var(--text-2xs); font-variant-numeric: tabular-nums; }
  .branch-note { margin: 0; color: var(--muted); font-size: var(--text-2xs); line-height: 1.5; overflow-wrap: anywhere; }
  .branch-note strong { color: var(--ink-soft); font-weight: 600; }

  @media (max-width: 620px) {
    .branch-panel { padding: 14px; }
    .branch-headline { gap: 9px; }
    .link { display: none; }
    .stat b { font-size: 22px; }
    .stat em { font-size: var(--text-xs); }
    .divider { display: none; }
    .trunk-pill { gap: 6px; padding: 3px 10px 3px 8px; font-size: var(--text-sm); }
    .branch-label { gap: 6px; }
  }
</style>
