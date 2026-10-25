// The command palette.
import { tick } from 'svelte';
import { api, listAllIssues } from '../lib/api';
import { nextCommandIndex } from '../lib/commandPalette';
import type { CommandChoice } from '../lib/commandPalette';
import { projectInitials } from '../lib/state';
import { openBugModalFromCommand } from './bugModal';
import { focusableElements, rememberDialogFocus, restoreDialogFocus } from './dialogs';
import { openWorkTask } from './drawer/open';
import { friendlyError } from './feedback';
import { selectProject, setView } from './routing';
import { openSavedView } from './search';
import { dialogs } from './state/dialogs.svelte';
import { issues } from './state/issues.svelte';
import { nav } from './state/nav.svelte';
import { palette } from './state/palette.svelte';
import { openTaskModalFromCommand } from './taskModal';
import { toggleTheme } from './theme';

export function openCommandPalette() {
  rememberDialogFocus('[data-command-trigger]');
  palette.commandOpen = true;
  nav.projectSwitcherOpen = false;
  palette.commandQuery = '';
  palette.commandIndex = 0;
  palette.commandIssuesError = '';
  if (!issues.issueTasks.length) {
    void loadCommandIssues();
  }
  palette.commandSearchTasks = [];
  palette.commandSearchProjects = [];
  palette.commandSearchViews = [];
  void searchCommand('');
  void tick().then(() => palette.commandInput?.focus());
}

export async function loadCommandIssues() {
  if (palette.commandIssuesLoading) return;
  const requestId = ++palette.commandIssuesRequest;
  palette.commandIssuesLoading = true;
  palette.commandIssuesError = '';
  try {
    const result = await listAllIssues({ limit: 200 });
    if (requestId !== palette.commandIssuesRequest) return;
    issues.issueTasks = result.data;
  } catch (error) {
    if (requestId === palette.commandIssuesRequest) {
      palette.commandIssuesError = friendlyError(error, 'Issue results could not be loaded.');
    }
  } finally {
    if (requestId === palette.commandIssuesRequest) palette.commandIssuesLoading = false;
  }
}

export async function searchCommand(query: string) {
  const requestId = ++palette.commandSearchRequest;
  palette.commandSearchLoading = true;
  try {
    const result = await api.search({ q: query.trim() || undefined, limit: 50 });
    if (requestId !== palette.commandSearchRequest || !palette.commandOpen) return;
    palette.commandSearchTasks = result.data || [];
    palette.commandSearchProjects = result.projects || [];
    palette.commandSearchViews = result.views || [];
  } catch {
    // The local project/view choices remain useful during a transient search
    // failure; command search retries on the next query change.
  } finally {
    if (requestId === palette.commandSearchRequest) palette.commandSearchLoading = false;
  }
}

export function commandInputChanged() {
  void searchCommand(palette.commandQuery);
}

export function closeCommandPalette() {
  closeCommandPaletteWithFocus(true);
}

export function closeCommandPaletteWithoutFocus() {
  closeCommandPaletteWithFocus(false);
}

export function closeCommandPaletteWithFocus(restoreFocus: boolean) {
  if (!palette.commandOpen) return;
  palette.commandOpen = false;
  if (restoreFocus) restoreDialogFocus();
}

export function commandKeydown(event: KeyboardEvent) {
  if (event.key === 'Tab') {
    const source = event.currentTarget instanceof HTMLElement ? event.currentTarget : null;
    const menu = source?.closest<HTMLElement>('.command-menu') || document.querySelector<HTMLElement>('.command-menu');
    const focusable = menu ? focusableElements(menu) : [];
    if (!focusable.length) return;
    const active = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    const index = active ? focusable.indexOf(active) : -1;
    const nextIndex = event.shiftKey
      ? index <= 0 ? focusable.length - 1 : index - 1
      : index < 0 || index === focusable.length - 1 ? 0 : index + 1;
    // Result buttons remain tabindex=-1 for the aria-activedescendant
    // combobox pattern, so the command menu owns both Tab directions.
    event.preventDefault();
    event.stopPropagation();
    focusable[nextIndex].focus();
  } else if (event.key === 'ArrowDown') {
    event.preventDefault();
    palette.commandIndex = nextCommandIndex(palette.commandIndex, palette.commandChoices.length, 1);
  } else if (event.key === 'ArrowUp') {
    event.preventDefault();
    palette.commandIndex = nextCommandIndex(palette.commandIndex, palette.commandChoices.length, -1);
  } else if (event.key === 'Home') {
    event.preventDefault();
    palette.commandIndex = palette.commandChoices.length ? 0 : 0;
  } else if (event.key === 'End') {
    event.preventDefault();
    palette.commandIndex = Math.max(0, palette.commandChoices.length - 1);
  } else if (event.key === 'Enter') {
    event.preventDefault();
    const choice = palette.commandChoices[palette.commandIndex];
    if (choice) void selectCommand(choice);
  }
}

export function commandOptionKeydown(event: KeyboardEvent, choice: CommandChoice) {
  if (['Tab', 'ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) {
    commandKeydown(event);
    return;
  }
  if (event.key === 'Enter' || event.key === ' ') {
    event.preventDefault();
    void selectCommand(choice);
  }
}

export function commandChoiceIcon(choice: CommandChoice): string {
  if (choice.kind === 'project') return projectInitials(choice.project);
  if (choice.kind === 'saved-view') return '☆';
  if (choice.kind === 'issue') return '⚠';
  if (choice.kind === 'task') return '□';
  if (choice.kind === 'action') {
    return choice.action === 'new-task' ? '+' : choice.action === 'report-bug' ? '⚠' : '◐';
  }
  return choice.view === 'search' ? '⌕' : choice.view === 'issues' ? '⚠' : choice.view === 'my-work' ? '◌' : choice.view === 'roadmap' ? '◒' : '⚙';
}

export async function selectCommand(choice: CommandChoice) {
  if (choice.kind === 'action') {
    const returnFocus = dialogs.dialogReturnFocus;
    if (choice.action === 'toggle-theme') {
      closeCommandPalette();
      toggleTheme();
      return;
    }
    closeCommandPaletteWithoutFocus();
    await tick();
    if (choice.action === 'new-task') await openTaskModalFromCommand(returnFocus);
    else await openBugModalFromCommand(returnFocus);
  } else if (choice.kind === 'project') {
    await selectProject(choice.project);
  } else if (choice.kind === 'task') {
    const returnFocus = dialogs.dialogReturnFocus;
    closeCommandPaletteWithoutFocus();
    await tick();
    await openWorkTask(choice.task, returnFocus);
  } else if (choice.kind === 'issue') {
    const returnFocus = dialogs.dialogReturnFocus;
    closeCommandPaletteWithoutFocus();
    await tick();
    if (!await setView('issues')) return;
    await openWorkTask(issues.issueTasks.find((task) => task.id === choice.task.id) || choice.task, returnFocus);
  } else if (choice.kind === 'saved-view') {
    closeCommandPaletteWithoutFocus();
    await openSavedView(choice.savedView);
  } else if (choice.kind === 'view') await setView(choice.view);
}
