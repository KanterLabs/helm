// Pure classification and geometry for the branch graph. Every branch forks
// from the base rail where it fell behind and extends by its commits ahead.
import type { GithubBranch, GithubBranchReport } from './types';

export type BranchBucket = 'active' | 'stale' | 'merged';
export type BranchVisibility = Record<BranchBucket, boolean>;

export const staleBranchDays = 30;
const dayMs = 86_400_000;

export interface BranchRow {
  branch: GithubBranch;
  kind: BranchBucket | 'staging';
  y: number;
  forkX: number;
  tipX: number;
  /** Centres of the commits before the tip, drawn only for short lanes. */
  dots: number[];
  age: string;
}

export interface BranchLayout {
  width: number;
  height: number;
  railY: number;
  railEnd: number;
  wide: boolean;
  ticks: { x: number; label: string }[];
  rows: BranchRow[];
}

/** An uncompared branch has no bucket: it must never read as merged. */
export function branchBucket(branch: GithubBranch, now: number): BranchBucket | null {
  if (!branch.compared) return null;
  if (branch.ahead === 0) return 'merged';
  const committed = branch.committed_at ? Date.parse(branch.committed_at) : Number.NaN;
  return Number.isFinite(committed) && now - committed > staleBranchDays * dayMs ? 'stale' : 'active';
}

export function branchCounts(report: GithubBranchReport, now: number): Record<BranchBucket | 'uncompared', number> {
  const counts = { active: 0, stale: 0, merged: 0, uncompared: 0 };
  for (const branch of report.branches) counts[branchBucket(branch, now) || 'uncompared'] += 1;
  return counts;
}

export function branchAge(committedAt: string | undefined, now: number): string {
  const committed = committedAt ? Date.parse(committedAt) : Number.NaN;
  if (!Number.isFinite(committed)) return '';
  const days = Math.max(0, (now - committed) / dayMs);
  if (days < 1) return 'today';
  return days < 14 ? `${Math.round(days)}d` : `${Math.round(days / 7)}w`;
}

export function layoutBranchGraph(report: GithubBranchReport, show: BranchVisibility, width: number, now: number): BranchLayout {
  const wide = width >= 520;
  // Labels sit to the right of each tip, so a narrow graph gives them more room.
  const axis = wide ? Math.min(440, Math.round(width * 0.42)) : Math.max(80, Math.round(width * 0.34));
  const railEnd = 6 + axis;
  const railY = 30;
  const compared = [...report.branches, ...(report.staging ? [report.staging] : [])].filter((branch) => branch.compared);
  const scale = Math.log1p(Math.max(10, ...compared.map((branch) => branch.behind)));
  const forkAt = (behind: number) => Math.round((railEnd - (Math.log1p(behind) / scale) * axis) * 10) / 10;
  const visible = report.branches
    .map((branch) => ({ branch, kind: branchBucket(branch, now) }))
    .filter((item): item is { branch: GithubBranch; kind: BranchBucket } => item.kind !== null && show[item.kind])
    .sort((a, b) => a.branch.behind - b.branch.behind || b.branch.ahead - a.branch.ahead || a.branch.name.localeCompare(b.branch.name));
  const items: { branch: GithubBranch; kind: BranchRow['kind'] }[] = [
    ...(report.staging?.compared ? [{ branch: report.staging, kind: 'staging' as const }] : []),
    ...visible
  ];
  let y = railY + 12;
  const rows = items.map(({ branch, kind }, index): BranchRow => {
    y += kind === 'staging' ? 28 : index === 1 && items[0].kind === 'staging' ? 32 : 26;
    const forkX = forkAt(branch.behind);
    const gap = kind === 'staging' ? 17 : 11;
    const first = forkX + (kind === 'staging' ? 18 : 16);
    const dotted = branch.ahead > 0 && (branch.ahead <= 5 || kind === 'staging' && branch.ahead <= 8);
    const tipX = branch.ahead === 0 ? forkX + 15
      : dotted ? first + gap * (branch.ahead - 1)
        : Math.round((forkX + 60 + Math.min(120, 16 * Math.log2(branch.ahead / 5))) * 10) / 10;
    const dots = dotted ? Array.from({ length: branch.ahead - 1 }, (_, dot) => first + gap * dot) : [];
    return { branch, kind, y, forkX, tipX, dots, age: branchAge(branch.committed_at, now) };
  });
  const ticks = [1, 10, 100, 1000, 10_000]
    .filter((behind) => Math.log1p(behind) <= scale)
    .map((behind) => ({ x: forkAt(behind), label: `−${behind}` }));
  return { width, height: y + 18, railY, railEnd, wide, ticks, rows };
}
