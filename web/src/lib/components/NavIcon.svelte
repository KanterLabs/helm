<script context="module" lang="ts">
  /** Stroke icons shared by the sidebar, topbar, and mobile navigation. */
  export const navIconPaths = {
    'my-work': '<circle cx="12" cy="12" r="9"/><path d="m8 12 3 3 5-6"/>',
    issues: '<path d="M12 3 2.5 20h19z"/><path d="M12 10v4M12 17h.01"/>',
    tickets: '<rect x="3" y="5" width="18" height="14" rx="2"/><path d="m3 7 9 6 9-6"/>',
    roadmap: '<path d="M3 6l6-2 6 2 6-2v14l-6 2-6-2-6 2z"/><path d="M9 4v14M15 6v14"/>',
    audits: '<path d="M12 3l8 3v6c0 5-3.5 8-8 9-4.5-1-8-4-8-9V6z"/><path d="m9 12 2 2 4-4"/>',
    board: '<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M9 4v16M15 4v16"/>',
    timeline: '<path d="M3 12h18"/><rect x="5" y="6" width="8" height="3" rx="1"/><rect x="10" y="15" width="9" height="3" rx="1"/>',
    focus: '<circle cx="12" cy="12" r="8"/><circle cx="12" cy="12" r="4"/><circle cx="12" cy="12" r=".6"/>',
    progress: '<path d="M4 20V11M10 20V5M16 20v-6M2 20h20"/>',
    metrics: '<path d="M3 3v18h18"/><path d="m7 15 4-4 3 3 5-6"/>',
    settings: '<path d="M4 6h9M17 6h3M4 12h3M11 12h9M4 18h11M19 18h1"/><circle cx="15" cy="6" r="2"/><circle cx="9" cy="12" r="2"/><circle cx="17" cy="18" r="2"/>',
    search: '<circle cx="11" cy="11" r="7"/><path d="m20 20-3.5-3.5"/>',
    plus: '<path d="M12 5v14M5 12h14"/>',
    chevron: '<path d="m9 6 6 6-6 6"/>',
    'chevron-down': '<path d="m6 9 6 6 6-6"/>',
    updown: '<path d="m7 9 5-5 5 5M7 15l5 5 5-5"/>',
    panel: '<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M9 4v16"/>',
    more: '<circle cx="5" cy="12" r="1.2"/><circle cx="12" cy="12" r="1.2"/><circle cx="19" cy="12" r="1.2"/>',
    menu: '<path d="M4 7h16M4 12h16M4 17h16"/>',
    bug: '<rect x="7" y="7" width="10" height="13" rx="5"/><path d="M12 11v9M7 13H3M21 13h-4M7 17l-3 2M17 17l3 2M9 7a3 3 0 0 1 6 0"/>',
    'sign-out': '<path d="M15 4h4v16h-4M10 8l-4 4 4 4M6 12h10"/>',
    sun: '<circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M2 12h2M20 12h2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/>',
    moon: '<path d="M20 14.5A8 8 0 1 1 9.5 4a6.5 6.5 0 0 0 10.5 10.5z"/>'
  } as const;

  export type NavIconName = keyof typeof navIconPaths;
</script>

<script lang="ts">
  export let name: NavIconName;
  export let size = 16;
</script>

<svg class="nav-svg" width={size} height={size} viewBox="0 0 24 24" aria-hidden="true" focusable="false">{@html navIconPaths[name]}</svg>
