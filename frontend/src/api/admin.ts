import { apiRequest } from '@/api/client';
import type { GroupDirectoryMode } from '@/api/groups';

export type Retention = {
  /** People inactive this long are anonymised; 0 = never. */
  inactive_user_months: number;
  /** Audit entries older than this are deleted; 0 = never. */
  audit_log_months: number;
};

export type ModuleSettings = {
  telegram: { configured: boolean; enabled: boolean };
  retention: Retention & {
    /** den: set in Den's module settings while paired. */
    source: 'den' | 'learn' | 'default';
    editable: boolean;
    pending_legal_review: boolean;
  };
  group_directory: { mode: GroupDirectoryMode; available: boolean; linkable: string[] };
};

export function getModuleSettings(signal?: AbortSignal) {
  return apiRequest<ModuleSettings>('/api/v1/settings', { signal });
}

export function saveModuleSettings(body: { telegram_enabled?: boolean; retention?: Retention }) {
  return apiRequest<ModuleSettings>('/api/v1/settings', { method: 'PUT', body });
}

/** GDPR erasure: anonymises the person. confirm must be "ERASE". */
export function erasePerson(userId: string, confirm: string) {
  return apiRequest<{ user_id: string; erased_at: string }>(`/api/v1/users/${userId}/erase`, {
    method: 'POST',
    body: { confirm },
  });
}

export type AuditEntry = {
  id: number;
  at: string;
  actor_user_id: string | null;
  actor_name: string;
  action: string;
  target_type: string;
  target_id: string;
  organization_id: string | null;
  course_id: string | null;
  details: Record<string, unknown>;
};

export type AuditFilter = { action?: string; course?: string; before?: number; limit?: number };

export function listAudit(filter: AuditFilter, signal?: AbortSignal) {
  return apiRequest<{ entries: AuditEntry[]; next_before: number | null }>('/api/v1/audit', {
    query: filter,
    signal,
  });
}
