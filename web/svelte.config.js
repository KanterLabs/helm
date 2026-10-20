import { vitePreprocess } from '@sveltejs/vite-plugin-svelte';

export default {
  preprocess: vitePreprocess(),
  compilerOptions: {
    // Runes-mode components keep legacy `on:` element directives on purpose:
    // Svelte 5 delegates event attributes to the root, which would run them
    // after native listeners such as focusTrap and change keyboard ordering.
    warningFilter: (warning) => warning.code !== 'event_directive_deprecated'
  }
};
