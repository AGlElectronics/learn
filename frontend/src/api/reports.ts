import { apiFetch, apiRequest } from '@/api/client';
import type { Qualification } from '@/api/qualifications';

export type CompletionStatus = 'done' | 'in_progress' | 'not_started' | 'overdue';
export type QualificationReportStatus = 'valid' | 'expiring' | 'expired' | 'missing';

export type ReportScope = {
  /** report.view: every course, plus the qualification report. */
  all: boolean;
  qualifications: Qualification[];
  courses: { id: string; title: string; published: boolean }[];
};

export type CompletionRow = {
  course_id: string;
  course_title: string;
  user_id: string;
  display_name: string;
  email: string;
  source: 'manual' | 'group';
  enrolled_at: string;
  due_at: string | null;
  completed: number;
  total: number;
  last_activity_at: string | null;
  status: CompletionStatus;
};

export type QualificationRow = {
  qualification_id: string;
  qualification_key: string;
  qualification_name: string;
  user_id: string;
  display_name: string;
  email: string;
  /** In a group linked to a course or path that awards it. */
  required: boolean;
  certificate_id: string | null;
  issued_at: string | null;
  expires_at: string | null;
  status: QualificationReportStatus;
};

export type CompletionFilters = { course?: string; group?: string; status?: CompletionStatus | '' };
export type QualificationFilters = { qualification?: string; group?: string; status?: QualificationReportStatus | '' };

function clean(filters: Record<string, string | undefined>): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [k, v] of Object.entries(filters)) if (v) out[k] = v;
  return out;
}

export function getReportScope(signal?: AbortSignal) {
  return apiRequest<ReportScope>('/api/v1/reports/scope', { signal });
}

export function getCompletionReport(filters: CompletionFilters, signal?: AbortSignal) {
  return apiRequest<{ rows: CompletionRow[] }>('/api/v1/reports/completion', { query: clean(filters), signal });
}

export function getQualificationReport(filters: QualificationFilters, signal?: AbortSignal) {
  return apiRequest<{ rows: QualificationRow[] }>('/api/v1/reports/qualifications', { query: clean(filters), signal });
}

/** Fetches the report as CSV with the caller's auth and saves it. */
export async function downloadReportCsv(
  kind: 'completion' | 'qualifications',
  filters: CompletionFilters | QualificationFilters
) {
  const res = await apiFetch(`/api/v1/reports/${kind}`, { query: { ...clean(filters), format: 'csv' } });
  const blob = await res.blob();
  const match = /filename="([^"]+)"/.exec(res.headers.get('Content-Disposition') ?? '');
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = match?.[1] ?? `${kind}-report.csv`;
  document.body.appendChild(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(url);
}
