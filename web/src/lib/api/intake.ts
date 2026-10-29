import type { IntakeCollection, IntakeListParams } from '../types/intake';
import { pathWithQuery, request } from './client';

export const intakeApi = {
  /** Existing ticket membership and bugs, deduplicated by immutable task ID. */
  listIntake: (params: IntakeListParams = {}, signal?: AbortSignal) =>
    request<IntakeCollection>(pathWithQuery('/intake', { ...params }), { signal })
};
