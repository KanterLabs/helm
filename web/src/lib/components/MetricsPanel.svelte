<script lang="ts">
  // Titled card shared by the admin metrics charts and tables. An optional
  // `legend` slot sits beside the title.
  export let title: string;
  export let subtitle: string;
</script>

<section class="panel">
  <div class="panel-title">
    <div><h2>{title}</h2><p>{subtitle}</p></div>
    <slot name="legend" />
  </div>
  <slot />
</section>

<style>
  .panel { min-width: 0; padding: 16px; border: 1px solid var(--border); border-radius: 11px; background: var(--surface); }
  .panel-title { display: flex; justify-content: space-between; align-items: flex-start; gap: 12px; margin-bottom: 12px; }
  .panel-title h2 { margin: 0; font-size: var(--text-md); }
  .panel-title p { margin: 2px 0 0; color: var(--muted); font-size: var(--text-xs); }
</style>
