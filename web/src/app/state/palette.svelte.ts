// Reactive state: Command palette query, results and search.
import { buildCommandChoices, commandChoiceId, filterCommandChoices } from '../../lib/commandPalette';
import type { Project, SavedView, Task } from '../../lib/types';
import { platformShortcut } from '../../lib/ui';
import { board } from './board.svelte';
import { issues } from './issues.svelte';
import { workspace } from './workspace.svelte';

class PaletteState {
  commandOpen = $state.raw(false);
  commandQuery = $state.raw('');
  commandIndex = $state.raw(0);
  commandInput = $state.raw(undefined as unknown as HTMLInputElement);
  commandShortcut = $state.raw(platformShortcut(''));
  commandIssuesLoading = $state.raw(false);
  commandIssuesError = $state.raw('');
  commandIssuesRequest = $state.raw(0);
  commandSearchTasks = $state.raw<Task[]>([]);
  commandSearchProjects = $state.raw<Project[]>([]);
  commandSearchViews = $state.raw<SavedView[]>([]);
  commandSearchLoading = $state.raw(false);
  commandSearchRequest = $state.raw(0);

  // Derived values
  commandChoices = $derived(filterCommandChoices(buildCommandChoices({
    projects: workspace.orderedProjects,
    tasks: board.tasks,
    issueTasks: issues.issueTasks,
    searchProjects: palette.commandSearchProjects,
    searchTasks: palette.commandSearchTasks,
    savedViews: palette.commandSearchViews,
    theme: workspace.theme
  }), palette.commandQuery));
  activeCommandOptionId = $derived(palette.commandChoices[palette.commandIndex] ? commandChoiceId(palette.commandChoices[palette.commandIndex]) : '');
}

export const palette = new PaletteState();
