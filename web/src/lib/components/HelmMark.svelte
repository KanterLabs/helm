<script context="module" lang="ts">
  /** A CSS length or a pixel value used for the rendered mark. */
  export type HelmMarkSize = number | string;

  export type HelmMarkAccessibility = {
    role: 'presentation' | 'img';
    ariaHidden?: 'true';
    label?: string;
  };

  /**
   * Keep the standalone mark named even when a caller passes an empty label.
   * Adjacent wordmarks should use `decorative` so the text remains the one
   * accessible name exposed to assistive technology.
   */
  export function helmMarkAccessibleLabel(label?: string): string {
    return label?.trim() || 'Helm';
  }

  /** Keep decorative and standalone semantics explicit and testable. */
  export function helmMarkAccessibility(
    decorative = false,
    label?: string
  ): HelmMarkAccessibility {
    if (decorative) return { role: 'presentation', ariaHidden: 'true' };
    return { role: 'img', label: helmMarkAccessibleLabel(label) };
  }

  /** Translate numeric sizes into a CSS length while retaining CSS sizes. */
  export function helmMarkCssSize(size: HelmMarkSize): string {
    if (typeof size === 'number') return Number.isFinite(size) && size > 0 ? `${size}px` : '32px';
    return size.trim() || '32px';
  }
</script>

<script lang="ts">
  /** Render at 20, 32, or 46px by default, while allowing a CSS length. */
  export let size: HelmMarkSize = 32;
  /** Hide the mark from assistive technology when nearby text names it. */
  export let decorative = false;
  /** Accessible name used when the mark is standalone. */
  export let label = 'Helm';
  /** Optional class hook for a consuming layout. */
  export let className = '';

  $: dimension = helmMarkCssSize(size);
  $: accessibility = helmMarkAccessibility(decorative, label);
  $: svgClass = ['helm-mark', className].filter(Boolean).join(' ');
</script>

<svg
  class={svgClass}
  style={`--helm-mark-size: ${dimension};`}
  width={size}
  height={size}
  viewBox="0 0 48 48"
  fill="none"
  xmlns="http://www.w3.org/2000/svg"
  role={accessibility.role}
  aria-hidden={accessibility.ariaHidden}
  aria-label={accessibility.label}
  focusable="false"
  shape-rendering="geometricPrecision"
>
  {#if accessibility.label}<title>{accessibility.label}</title>{/if}

  <!--
    A helm's hub seen head-on: a 34-unit disc cut by an H-shaped channel into
    three equal 9-unit board columns, with the centre column split into two
    stacked cards. Every channel is 3.5 units wide so the H stays open at 20px.
  -->
  <path
    fill="currentColor"
    d="M16 9A17 17 0 0 0 16 39ZM32 9A17 17 0 0 1 32 39ZM19.5 7.606V20.75A1.5 1.5 0 0 0 21 22.25H27A1.5 1.5 0 0 0 28.5 20.75V7.606A17 17 0 0 0 19.5 7.606ZM19.5 40.394V27.25A1.5 1.5 0 0 1 21 25.75H27A1.5 1.5 0 0 1 28.5 27.25V40.394A17 17 0 0 1 19.5 40.394Z"
  />
</svg>

<style>
  /*
   * The mark inherits its surrounding color by default. Consumers can set
   * --helm-mark-color when the SVG needs a color independent of its context.
   */
  .helm-mark {
    display: inline-block;
    width: var(--helm-mark-size, 2rem);
    height: var(--helm-mark-size, 2rem);
    flex: 0 0 auto;
    color: var(--helm-mark-color, currentColor);
    vertical-align: middle;
  }
</style>
