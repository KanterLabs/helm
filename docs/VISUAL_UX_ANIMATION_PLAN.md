# Visual, UX, and animation improvement plan

Status: proposed (2026-10-04). Not started; planned after v0.2.0.

## Goal

Make Roadmap feel alive and trustworthy as an agent-operated board: every state
change should be visible, every interaction should acknowledge input, and motion
should explain *what changed* instead of decorating the page.

Three outcomes:

1. A person can glance at the board and see what moved since they last looked.
2. Drag-and-drop, quick-add, and live agent updates feel physical and
   predictable.
3. Motion follows one shared token system and fully respects
   `prefers-reduced-motion`.

## Current foundation

The app already has a solid styling base in `web/src/app.css`:

- Light and dark themes via CSS custom properties on `.app-shell`.
- Entrance keyframes: `slide-in` (drawer), `command-in`, `modal-in`,
  `toast-in`, plus `spin` and `shimmer` for loading.
- `.15s ease` hover transitions on cards, rows, and buttons; a width
  transition on `.column-progress`.
- A global `prefers-reduced-motion` block that disables all animation.

The gaps, in rough priority order:

- **No motion system.** Durations, easings, and distances are ad hoc
  (`.15s`, `.16s`, `.18s`, `.2s`, `.22s`, all plain `ease`/`ease-out`).
- **No list or layout animation.** Nothing uses `svelte/transition` or
  `svelte/animate`. When a poll or a drag moves a card, it teleports. The
  human eye loses it.
- **Drag-and-drop is bare.** The dragged card gets `.dragging`
  (opacity `.45`, rotate `1deg`), but drop targets give no feedback: no
  column highlight, no insertion indicator, no placeholder gap.
- **No exit animations.** Toasts, popovers, modals, and the drawer appear
  with animation but vanish instantly.
- **Live updates are invisible.** `observeWorkTransitions` announces changes
  to screen readers and toasts fire, but the card itself does not flash or
  pulse when agent state changes underneath it.
- **Skeletons pop.** Shimmer blocks are replaced by real content with no
  crossfade, which reads as a jolt on every view change.
- **Touch targets are small.** `.card-move` is 21px, `.icon-button.tiny` is
  25px, label chips are 8px type. WCAG 2.5.8 minimum is 24px; comfortable
  mobile targets are 40px+.
- **Type runs small everywhere.** Dense 9–11px copy is fine for metadata but
  is used for primary content in lists and rows.

## Motion tokens

Introduce a small token set in `app.css` before adding any new animation, and
migrate existing keyframes/transitions onto it:

```css
.app-shell {
  --dur-instant: 80ms;   /* press feedback, color swaps */
  --dur-fast: 140ms;     /* hovers, focus, chip toggles */
  --dur-base: 200ms;     /* rows, popovers, toasts */
  --dur-slow: 280ms;     /* drawer, modal, FLIP moves */
  --ease-out: cubic-bezier(.2, .8, .3, 1);      /* enter, hover lift */
  --ease-in: cubic-bezier(.5, 0, .75, .4);      /* exit, dismiss */
  --ease-spring: cubic-bezier(.3, 1.35, .45, 1); /* small overshoot: cards, FAB-like */
  --lift-sm: 0 4px 12px rgba(29, 36, 51, .10);
  --lift-md: 0 10px 26px rgba(29, 36, 51, .14);
}
```

Rules of thumb:

- Nothing functional exceeds ~300ms. The app should feel instant, not animated.
- Entrances use `--ease-out`, exits use `--ease-in` and are ~30% shorter.
- Only `transform` and `opacity` animate on the hot path (cards, rows).
  Box-shadow and color transitions stay on hover only.
- The existing `prefers-reduced-motion` block stays the kill switch; any new
  Svelte transitions must check the same media query (see Accessibility).

## Recommended experience, by surface

### 1. Board — drag-and-drop

This is the highest-impact surface.

- **Drop-target feedback.** Track `dragenter`/`dragleave` per column and add a
  `.drop-target` class: column border becomes `--purple` dashed, background
  tints `--purple-soft`, and a 2px insertion line appears between the cards
  nearest the pointer. Remove the global `dragover` handler's silence — the
  user should always know where the drop will land.
- **Placeholder gap.** While dragging over a column, render a dashed
  placeholder card of the dragged card's height at the insertion index so the
  list physically opens up. This replaces guesswork with affordance.
- **Lift on drag start.** `.task-card.dragging` should scale to `1.02`,
  rotate `1.5deg`, and take `--lift-md`, over `--dur-fast` — a "picked up"
  feel — instead of only fading.
- **FLIP on move.** Wrap each column's `{#each}` in a keyed block with
  `animate:flip={{ duration: 280 }}` (from `svelte/animate`) so cards that
  shift to make room, or a card landing in a new column, glide instead of
  teleporting. This also animates poll-driven reordering for free.
- **Scroll the board during drags.** When the pointer nears the left/right
  edge of `.board` (which scrolls horizontally), auto-scroll toward the edge.

### 2. Board — card lifecycle and live updates

- **New card entrance.** `in:fly={{ y: 8, duration: 200 }}` (or a CSS
  equivalent) on task cards so quick-add and poll-created cards fade up
  instead of popping in.
- **Change flash.** When `mergeAuthoritativeTaskList` reports an update to a
  visible card, apply a `.just-updated` class for ~1.2s: a `--purple-soft`
  background sweep from the top edge. This makes the 15s poll and 30s pulse
  visible without a toast for every change. The data hook already exists in
  `observeWorkTransitions`; it currently only calls `announce()`.
- **Agent pulse actually pulses.** The `.agent-pulse` strip on active cards
  gets a slow (2.4s) opacity breathing on `.agent-pulse-icon` while an agent
  is working; stale and action-needed states get a small amber/red dot with a
  single soft ping ring when the state *changes* into them (not a loop).
- **Column count tick.** When `.column-count` changes, briefly scale the
  badge (`--ease-spring`, ~200ms) to draw the eye to what moved.
- **Horizontal scroll affordance.** Add inset shadow fades at the left/right
  edges of `.board` when more columns exist off-screen.

### 3. Task drawer

- Add an exit transition matching the entrance: slide right + fade over
  `~180ms` with `--ease-in`. Svelte's `transition:fly` on the drawer element
  gives both directions for free and replaces the CSS-only `slide-in`.
- **Backdrop fade.** `.drawer-backdrop` fades in/out over `--dur-base`
  (currently instant on both ends).
- **Tab indicator.** The active `.drawer-tab` underline should slide between
  tabs (shared-element style via a positioned pseudo-element) instead of
  snapping.
- **Save feedback.** On successful save, the Save button briefly shows a
  check state before reverting, instead of relying only on a toast.
- When the drawer's task is updated by the 60s liveness refresh, show the
  same `.just-updated` sweep on the title block.

### 4. Modals, popovers, command palette

- **Exit animations** for `.modal` and `.command-menu` (scale to `.98`,
  fade, ~140ms, `--ease-in`). Move entrance/exit to Svelte transitions so
  both directions are symmetric.
- **Popover entrance.** `.popover` currently appears with no animation; give
  it the same `command-in`-style slide+scale over `--dur-fast`.
- **Command palette row highlight.** Animate the selected-row background
  between items when navigating with ↑/↓ (a moving highlight reads better
  than rows snapping). Keep it to `--dur-fast`.
- **Stagger results.** On open, command rows can stagger in at 12–15ms per
  row, capped at the first 6 rows.

### 5. Toasts

- Add a matching exit: fade + slide down ~8px over `--dur-fast` before
  removal. Svelte `transition:fly` on the `{#each toasts}` block handles
  enter, exit, and reflow of remaining toasts (`animate:flip`) in one place.
- Give success toasts a subtle green left border and errors red; the icon
  circle already color-codes, but color-at-edge is readable at a glance in
  the periphery.
- Consider auto-dismissing success/info toasts after ~4s while errors stay
  until dismissed.

### 6. Loading and view transitions

- **Skeleton → content crossfade.** Wrap skeleton and loaded content in a
  keyed `{#key}` block so the shimmer fades out while content fades in over
  `--dur-base`. Apply to board columns, work lists, roadmap panels, and the
  audit list — the pop is currently the most frequent jolt in the app.
- **View switch fade.** When `view` changes (board/timeline/issues/…), fade
  and 6px-rise the `.content` container over `--dur-base`. Cheap, and it
  hides the uneven arrival of async data.
- **Progress ring.** Animate the roadmap `.progress-ring` `--progress`
  custom property on load/change (registered `@property` enables a smooth
  conic-gradient sweep, with a number-count fallback).
- **Metric cards.** Stagger the four roadmap metric cards in at 40ms
  intervals on first load.

### 7. Micro-interactions and controls

- **Press states.** All `.button` and icon buttons get
  `:active { transform: scale(.97) }` over `--dur-instant` — the missing
  half of the current hover lift.
- **Favorite star.** On toggle, a quick pop (`scale 1 → 1.35 → 1`,
  `--ease-spring`, ~250ms). Same treatment for the heading star and sidebar
  stars.
- **Theme toggle.** The existing `.2s` color/background transition on
  `.app-shell` works; extend it to borders and shadows so the whole shell
  crossfades instead of half the chrome snapping.
- **Checkbox chips.** `.check-label` toggles get a `--dur-fast` color/border
  transition.
- **Focus rings.** Keep the existing focus-visible outline but add a 120ms
  `outline-offset` transition so the ring "arrives" rather than appearing.

### 8. Typography, spacing, and touch targets

- Raise the floor for primary content: list/row titles from 11–12px to
  12.5–13px; card titles stay 12px but with `line-height` 1.45. Keep 9–10px
  strictly for metadata (keys, timestamps).
- Bring interactive controls to a 24px minimum: `.card-move` 21px → 24px,
  `.icon-button.tiny` 25px stays for desktop but mobile media queries should
  bump all icon buttons to 40px hit areas (visual size can stay small via
  padding).
- On `max-width: 600px`, quick-add triggers, work rows, and issue rows
  should have min-heights of 44px for thumb comfort.
- Card hover on touch devices: hoist the `translateY(-1px)` hover rules
  behind `@media (hover: hover)` so cards don't stick "lifted" after a tap.

## Accessibility

- Keep the existing global `prefers-reduced-motion: reduce` rule. For Svelte
  transitions, gate with a `$:` derived `reduceMotion` boolean from
  `matchMedia('(prefers-reduced-motion: reduce)')` and pass zero durations —
  Svelte does not honor the CSS media query for its JS-driven transitions
  (`flip`, `fly`) on its own.
- Every animated state change must remain perceivable without motion: the
  change flash has the existing `announce()` live region; drop targets keep
  the dashed border, not just movement.
- Never animate `outline` on focus for keyboard users at a pace slower than
  150ms; focus must feel instant.

## Implementation notes

- All work is in `web/src`: tokens and keyframes in `app.css`, transitions in
  `App.svelte` and the `lib/components/*.svelte` files that own each surface
  (`AgentPulse`, `LiveWorkRow`, `BoardTimeline`, `RoadmapActivity`,
  `AuditReview`).
- Prefer Svelte's built-ins (`transition:fly`, `transition:fade`,
  `animate:flip`) over hand-rolled keyframes for anything that enters,
  leaves, or reorders — they handle exit and interruption correctly, which
  CSS-only entrance animations cannot.
- Keep drag insertion-index logic in `state.ts` (pure, testable); the Svelte
  layer only renders the placeholder.
- The e2e suite (`web/e2e`) should keep passing unchanged; add one
  Playwright check that drop-target styling appears during a simulated drag
  and one that `prefers-reduced-motion` yields zero-duration FLIP.

## Phased rollout

**Phase 1 — motion foundation (low risk, no markup logic changes)**

1. Add motion tokens; migrate existing transitions/keyframes to them.
2. Press states, focus-ring animation, theme crossfade, hover-guard media
   query.
3. Toast exit + FLIP reflow; popover entrance.
4. Touch-target and type-floor adjustments.

**Phase 2 — board feel (the visible win)**

5. Drag: drop-target highlight, placeholder gap, lift-on-grab, edge
   auto-scroll.
6. `animate:flip` on column card lists; new-card entrance; column-count tick.
7. Change flash wired to `observeWorkTransitions`; agent pulse breathing.

**Phase 3 — polish**

8. Skeleton crossfades and view-switch fade.
9. Drawer exit + tab indicator; modal/command exits; command highlight and
   stagger.
10. Progress ring sweep, metric stagger, favorite pop.
