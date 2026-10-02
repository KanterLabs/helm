# Bulk selection stability

The bulk review flow must keep the last loaded snapshot for every selected task
while board filters and paginated task responses change. The selection count is
the user's intent; a filtered board page is only the current rendering of that
selection.

## Observed baseline failures

Before the production change, the release candidate showed these failures:

| Scenario | Observable failure |
| --- | --- |
| Select one task under a title filter, clear the filter, select a second task, then review | The existing bulk E2E reaches `2 selected`, but the Review button opens no dialog (`web/e2e/bulk.spec.ts:57`). `selectedTaskIds` still contains both IDs while `selectedTasks` is derived only from the currently loaded `tasks` array. |
| Reapply the first title filter while the real board responses are still loading | `loadBoard` clears the current task pages before the replacement responses arrive. The selected IDs remain, but `selectedTasks` becomes empty, so `openBulkModal` returns without opening Review. |
| Allow the replacement page to finish before opening Review | Only the task matching the active filter is present in `tasks`; the filtered-out selection is omitted from the review snapshot. |

These are data and delivery races across the real board GET boundary. The
regression workflow therefore fetches the filtered task responses from Helm,
holds their fulfillment, opens Review while the rendered board is empty, and
then verifies that both selected task keys remain in the frozen review set.

## Regression workflow

`web/e2e/bulk.spec.ts` adds one real-server scenario that:

1. Selects the first fixture task under its title filter.
2. Clears the filter and selects the second task.
3. Reapplies the first filter while `route.fetch()` obtains the real filtered
   responses and holds their delivery.
4. Opens Review during the replacement window and checks both task keys.
5. Releases the responses, confirms the review snapshot still contains two
   tasks, applies the priority change, and checks the two-task result summary.

The test attaches a sanitized filtered-task response summary and a full-page
review screenshot to the Playwright evidence bundle.

Run the focused workflow from `web/` with:

```sh
npm run e2e -- e2e/bulk.spec.ts
```
