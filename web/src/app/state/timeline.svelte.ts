// Reactive state: Board-wide recent-work timeline.
import type { TaskTimelineFilter, TaskTimelineItem } from '../../lib/types';

class TimelineState {
  boardTimelineItems = $state.raw<TaskTimelineItem[]>([]);
  boardTimelineNextCursor = $state.raw('');
  boardTimelineLoading = $state.raw(false);
  boardTimelineLoadingOlder = $state.raw(false);
  boardTimelineError = $state.raw('');
  boardTimelineFilter = $state.raw<TaskTimelineFilter>('all');
  boardTimelineProjectId = $state.raw('');
  boardTimelineRequest = $state.raw(0);
  boardTimelineLoadQueue = $state.raw<Promise<boolean>>(Promise.resolve(true));
}

export const timeline = new TimelineState();
