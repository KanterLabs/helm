# Frontend structure

The web client (`web/src`) is a Svelte 5 app in runes mode. `App.svelte` is a
thin shell: it mounts listeners, runs a few app-wide effects and composes the
views. Everything else lives in `web/src/app/`:

| Path | Holds |
| --- | --- |
| `app/state/<domain>.svelte.ts` | One reactive state object per domain (for example `board`, `drawer`, `nav`). Fields use `$state.raw`, so assign a new value instead of mutating in place; derived values use `$derived`. |
| `app/*.ts`, `app/board/`, `app/drawer/`, `app/releases/`, `app/live/`, `app/nav/` | Behavior grouped by domain: loading, routing, mutations, polling and dialogs. Modules import state objects and each other's functions. |
| `app/views/` | Markup components (`shell/`, `board/`, `pages/`, `drawer/`, `settings/`, `overlays/`). They read state objects directly and declare `<svelte:options runes={true} />` so those reads stay reactive. |
| `app/appHelpers.ts` | Pure helpers covered by `App.test.ts`. |
| `lib/` | Shared API client, types, components and pure utilities. |
| `styles/` | Stylesheets, imported by `app.css` in cascade order. |

Views keep legacy `on:` element directives. Svelte 5 delegates `onclick`-style
attributes to the root, so they would run after native listeners such as the
dialog focus trap and change keyboard behavior; `svelte.config.js` silences
only that deprecation warning.

Keep each file focused and under 20,000 bytes; split by responsibility rather
than by line count. Browser tests live in `web/e2e/`, with shared fixtures and
journey phases in `web/e2e/support/`.
