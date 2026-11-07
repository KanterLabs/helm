# Unreleased (on `beta`)

User-visible changes after v0.2.2 belong here, newest first. Latest release:
[Helm v0.2.2](v0.2.2.md).

## Features

- **Focus guidance for agents:** discover existing goal groups before proposing
  more Backlog work, review meaningful inactivity and blockers, and carry the
  remaining cards into a handoff. New `focus` CLI commands cover discovery,
  bounded read-only review and lifecycle/membership management. Startup hooks
  mention Focus even in fresh sessions. Heartbeats and identical progress do
  not make old work look fresh. See the [full main changelog](../FOCUS_AGENT_CHANGELOG.md).

- **Screenshot evidence on tasks:** paste, drop or select PNG, JPEG and static
  WebP images in task activity. Captions, original downloads, comparison view
  and durable progress history help humans and agents review UI and game work.
  Agent tooling supports upload retries and image updates with project-scoped
  access. Version 5 portable bundles and verified backups preserve image files.
  See [Task images](../TASK_IMAGES.md) for limits and workflows.

- **Roadmap explore mode:** select **Explore** on a roadmap to hide the sidebar,
  top bar and page heading and enlarge the roadmap. Zoom from 100% to 175% with
  the controls or `+`/`-`, optionally go full screen, and leave with **Exit
  explore** or `Esc`. The mode stays on through a reload and after opening a task.
  Roadmaps on phones no longer scroll sideways.

- **Roomier project boards:** wider columns and larger three-line titles improve
  readability. Centred cards, separate bug metadata, and quieter footer controls
  make tasks easier to scan. Expanded details keep their controls within reach.
  A compact heading and toolbar bring the board higher on the page, align mobile
  actions to the left, and keep Add task beside the work in each column.

- **Bugs intake connected to ticket triage:** classify an existing ticket as a bug,
  keep its comments and history, and file it into a project's Bugs column.
  Review severity and priority, then use **Triage & move to Ready** to accept it.
  Bug reports and reopened bugs default to Bugs; ordinary tasks stay in Backlog.
  The additive upgrade adds the column without moving existing work.
