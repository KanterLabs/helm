# Unreleased (on `beta`)

User-visible changes after v0.2.1 belong here, newest first. Latest release:
[Helm v0.2.1](v0.2.1.md).

## Features

- **Bugs intake connected to ticket triage:** classify an existing ticket as a bug,
  keep its comments and history, and file it into a project's Bugs column.
  Review severity and priority, then use **Triage & move to Ready** to accept it.
  Bug reports and reopened bugs default to Bugs; ordinary tasks stay in Backlog.
  The additive upgrade adds the column without moving existing work.

- **Guided releases:** `make prepare-release VERSION=vX.Y.Z` prepares a
  production release PR from the verified, deployed beta. After the main
  deployment passes, `make finish-release VERSION=vX.Y.Z` publishes matching
  version tags and release notes on both repository hosts.
- **New Helm identity:** A circular H mark, shaped like board columns,
  accompanies the redesigned desktop sidebar and phone navigation. The
  favicon and home-screen icons use the same identity, with a lighter
  favicon color for dark browser themes.
- **Redesigned navigation:** The sidebar now nests the current project's
  **Board**, **Timeline**, **Focus**, **Progress** and **Audits** under the
  project, so project views no longer sit as buttons in the board header.
  Favorites appear as **Pinned**, project order moves into the **⋯** menu,
  and long project lists show **Show more**. Search (⌘K/Ctrl K) is at the
  top of the sidebar. The top bar shows where you are (project ⌄ / view)
  and has one **New** menu for a task, bug report or project. Theme and
  **Sign out** live in the account menu, and the beta build switcher sits
  in the sidebar footer. **Export project** and **Import archive** are under
  the board's **Actions** menu.
- **Collapsible sidebar:** Collapse the sidebar to an icon rail with the
  panel button or `[`; Helm remembers the choice on each device. Rail icons
  show their names on hover or focus.
- **Phone navigation:** The bottom bar has five tabs: **My work**,
  **Issues**, **Board**, **Tickets** and **More**. **More** opens the full
  navigation, including projects, settings, account and the beta switcher.

## Fixes

- Search drafts survive a delayed workspace startup after navigation.
