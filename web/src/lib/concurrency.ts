/**
 * Maps items with at most `limit` calls in flight. Views that load one
 * request per project use it: firing them all at once makes Chromium fail
 * requests with ERR_INSUFFICIENT_RESOURCES once a workspace has a few dozen
 * projects, which the app then reads as a lost connection. Results keep the
 * input order; the first rejection rejects the whole map, like Promise.all.
 */
export async function mapWithConcurrency<T, R>(items: readonly T[], limit: number, run: (item: T, index: number) => Promise<R>): Promise<R[]> {
  const results = new Array<R>(items.length);
  let next = 0;
  const worker = async () => {
    while (next < items.length) {
      const index = next++;
      results[index] = await run(items[index], index);
    }
  };
  await Promise.all(Array.from({ length: Math.max(1, Math.min(limit, items.length)) }, worker));
  return results;
}

/** Per-project fan-out limit, well under the browser's per-host queue. */
export const projectFanOut = 6;
