/** Versioned archive returned by the portable export endpoint. */
export interface PortableArchive {
  format: 'helm.portable' | string;
  version: number;
  exported_at?: string;
  source?: { product?: string; api?: string };
  projects: Array<Record<string, unknown>>;
  columns?: Array<Record<string, unknown>>;
  tasks?: Array<Record<string, unknown>>;
  labels?: Array<Record<string, unknown>>;
  actors?: Array<Record<string, unknown>>;
  relationships?: Record<string, unknown>;
  activity?: Record<string, unknown>;
  comments?: Array<Record<string, unknown>>;
  [key: string]: unknown;
}

export interface PortableImportCounts {
  [key: string]: number;
}

export interface PortableImportRemap {
  entity: string;
  source: string;
  target: string;
  field?: string;
  reason: string;
}

export interface PortableImportIssue {
  entity: string;
  id?: string;
  field?: string;
  message: string;
}

export interface PortableImportReport {
  format: string;
  version: number;
  dry_run: boolean;
  conflict: string;
  counts: PortableImportCounts;
  remaps: PortableImportRemap[];
  warnings: string[];
  errors: PortableImportIssue[];
}
