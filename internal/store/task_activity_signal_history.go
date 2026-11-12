package store

import (
	"context"
	"math"
	"sort"
)

// timelineUnbounded is the source limit for whole-history reads. SQLite
// accepts it as LIMIT; slice preallocation is capped by timelineCapacity.
const timelineUnbounded = math.MaxInt32 - 1

// taskTimelineAttachmentBatch keeps attachment hydration well under SQLite's
// bound-parameter limit for tasks with very long histories.
const taskTimelineAttachmentBatch = 400

// eachTaskTimelineItem calls fn for every item of a task's merged timeline,
// newest first: the same three sources, deduplication, ordering and
// attachment hydration the timeline endpoint serves, without pagination.
//
// Unlike ListTaskTimeline it reads soft-deleted tasks (every task gets a
// signal row) and never aborts on one stored row: a progress row that cannot
// be decoded is passed to fn as a bare item (ID, Kind, TaskID, CreatedAt)
// with decodeErr set. Event payloads are passed through raw, as the store
// timeline always has; judging them is the classifier's job. A non-nil error
// from fn stops the iteration and is returned.
func (s *Store) eachTaskTimelineItem(ctx context.Context, taskID string, fn func(item TaskTimelineItem, decodeErr error) error) error {
	undecodable := map[string]error{}
	candidates, err := s.collectTaskTimelineCandidates(ctx, taskID, TaskTimelineFilter{Limit: timelineUnbounded}, timelineSortKey{},
		func(id string, err error) { undecodable[id] = err })
	if err != nil {
		return err
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return compareTimelineKeys(candidates[i].key, candidates[j].key) > 0
	})
	items := make([]TaskTimelineItem, len(candidates))
	for index := range candidates {
		items[index] = candidates[index].item
	}
	for start := 0; start < len(items); start += taskTimelineAttachmentBatch {
		end := min(start+taskTimelineAttachmentBatch, len(items))
		if err := s.hydrateTimelineAttachments(ctx, items[start:end]); err != nil {
			return err
		}
	}
	for _, item := range items {
		var decodeErr error
		if item.Kind == "agent_progress" && item.Progress == nil {
			decodeErr = undecodable[item.ID]
		}
		if err := fn(item, decodeErr); err != nil {
			return err
		}
	}
	return nil
}
