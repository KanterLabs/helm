import type { AuditFinding, GithubAssessment } from '../../../types';

/** A finding that carries an immutable v2 assessment. */
export type V2Finding = AuditFinding & { assessment: GithubAssessment };
