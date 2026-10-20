// Reactive state: Roadmap overview, live work and recent activity.
import type { Actor, Column, RoadmapActivityFilter, RoadmapSummary, Task } from '../../lib/types';
import { workspace } from './workspace.svelte';

class RoadmapDataState {
  roadmapRequest = $state.raw(0);
  roadmap = $state.raw<RoadmapSummary | null>(null);
  roadmapProjectId = $state.raw<string | undefined>();
  roadmapLoading = $state.raw(false);
  roadmapError = $state.raw('');
  roadmapLiveTasks = $state.raw<Task[]>([]);
  roadmapLiveColumnsByProject = $state.raw<Record<string, Column[]>>({});
  roadmapLiveLoading = $state.raw(false);
  roadmapLiveError = $state.raw('');
  roadmapActivityTasks = $state.raw<Record<string, Task>>({});
  roadmapActivityLoading = $state.raw(false);
  roadmapActivityError = $state.raw('');
  roadmapActivityFilter = $state.raw<RoadmapActivityFilter>('all');
  roadmapLiveRequest = $state.raw(0);
  roadmapActivityRequest = $state.raw(0);
  roadmapActorsLoaded = $state.raw(false);
  roadmapActorsLoading = $state.raw(false);
  roadmapActors = $state.raw<Record<string, Pick<Actor, 'id' | 'kind' | 'name'>>>({});

  // Derived values
  roadmapTotal = $derived(roadmapData.roadmap?.task_total ?? roadmapData.roadmap?.total_tasks ?? 0);
  roadmapCompletion = $derived(Math.max(0, Math.min(100, roadmapData.roadmap?.completion_percentage ?? roadmapData.roadmap?.completion_percent ?? 0)));
  roadmapCompleted = $derived(roadmapData.roadmap?.completed_count ?? roadmapData.roadmap?.completed ?? 0);
  roadmapOverdue = $derived(roadmapData.roadmap?.overdue_count ?? roadmapData.roadmap?.overdue ?? 0);
  roadmapDueSoon = $derived(roadmapData.roadmap?.due_soon_count ?? roadmapData.roadmap?.due_soon ?? 0);
  roadmapUpcoming = $derived(roadmapData.roadmap?.upcoming_tasks ?? roadmapData.roadmap?.upcoming ?? []);
  roadmapProject = $derived(workspace.projects.find((project) => project.id === roadmapData.roadmapProjectId));
  roadmapActivityEvents = $derived(roadmapData.roadmap?.recent_activity ?? []);
  roadmapProjectRowsUnordered = $derived(roadmapData.roadmap?.projects?.length
    ? roadmapData.roadmap.projects
    : workspace.projects.map((project) => ({
        project,
        total_tasks: project.task_count ?? 0,
        completed_tasks: project.completed_task_count ?? project.completed_count ?? 0,
        completion_percentage: project.task_count ? ((project.completed_task_count ?? project.completed_count ?? 0) / project.task_count) * 100 : 0
      })));
  roadmapProjectOrder = $derived(new Map(workspace.orderedProjects.map((project, index) => [project.id, index])));
  roadmapProjectRows = $derived([...roadmapData.roadmapProjectRowsUnordered].sort((left, right) =>
    (roadmapData.roadmapProjectOrder.get(left.project.id) ?? Number.MAX_SAFE_INTEGER)
      - (roadmapData.roadmapProjectOrder.get(right.project.id) ?? Number.MAX_SAFE_INTEGER)
  ));
}

export const roadmapData = new RoadmapDataState();
