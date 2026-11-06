import { apiRequest } from '@/api/client';

export type KeycloakGroup = { id: string; name: string; path: string };

export type GroupLink = {
  course_id: string;
  group_name: string;
  created_at: string;
  last_synced_at: string | null;
  last_sync_error: string;
  /** People Learn knows to be in the group. */
  members: number;
  /** Their active group enrolments on this course. */
  active: number;
};

export type GroupSyncResult = {
  courses: number;
  groups: number;
  members: number;
  users_created: number;
  enrolled: number;
  reactivated: number;
  deactivated: number;
  errors: string[];
};

export function listKeycloakGroups(signal?: AbortSignal) {
  return apiRequest<{ groups: KeycloakGroup[] }>('/api/v1/groups', { signal });
}

export function listGroupLinks(courseId: string, signal?: AbortSignal) {
  return apiRequest<{ links: GroupLink[]; sync_available: boolean }>(`/api/v1/courses/${courseId}/group-links`, {
    signal,
  });
}

export function addGroupLink(courseId: string, groupName: string) {
  return apiRequest<{ link: GroupLink; sync: GroupSyncResult | null; sync_error?: string }>(
    `/api/v1/courses/${courseId}/group-links`,
    { method: 'POST', body: { group_name: groupName } }
  );
}

export function removeGroupLink(courseId: string, groupName: string) {
  return apiRequest<{ deactivated: number }>(`/api/v1/courses/${courseId}/group-links`, {
    method: 'DELETE',
    query: { group: groupName },
  });
}

export function syncGroupLinks(courseId: string) {
  return apiRequest<GroupSyncResult>(`/api/v1/courses/${courseId}/group-links/sync`, { method: 'POST' });
}
