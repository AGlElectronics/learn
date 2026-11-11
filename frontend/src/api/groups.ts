import { apiRequest } from '@/api/client';

export type KeycloakGroup = { id: string; name: string; path: string };

/** GROUP_DIRECTORY: keycloak reads groups and people from Keycloak; off never does. */
export type GroupDirectoryMode = 'keycloak' | 'off';

export type GroupLink = {
  course_id: string;
  /** Normalised key (lower-case, no leading "/"); use it to unlink. */
  group_name: string;
  /** Full path as the directory spells it, e.g. /learn/cohort-a. */
  group_path: string;
  /** False for an old link made by plain name whose path was not found yet. */
  path_resolved: boolean;
  created_at: string;
  last_synced_at: string | null;
  last_sync_error: string;
  /** People Learn knows to be in the group. */
  members: number;
  /** Their active group enrolments on this course. */
  active: number;
};

export type GroupLinksResponse<L> = {
  links: L[];
  sync_available: boolean;
  directory: GroupDirectoryMode;
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

/** limited: only the groups LEARN_LINKABLE_GROUPS lets trainers link. */
export function listKeycloakGroups(signal?: AbortSignal) {
  return apiRequest<{ groups: KeycloakGroup[]; limited: boolean; linkable: string[] | null }>('/api/v1/groups', {
    signal,
  });
}

export function listGroupLinks(courseId: string, signal?: AbortSignal) {
  return apiRequest<GroupLinksResponse<GroupLink>>(`/api/v1/courses/${courseId}/group-links`, { signal });
}

/** groupPath is the group's full path, e.g. /learn/cohort-a. */
export function addGroupLink(courseId: string, groupPath: string) {
  return apiRequest<{ link: GroupLink; sync: GroupSyncResult | null; sync_error?: string }>(
    `/api/v1/courses/${courseId}/group-links`,
    { method: 'POST', body: { group_path: groupPath } }
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
