import { apiRequest } from '@/api/client';
import type { Progress } from '@/api/courses';
import type { GroupLinksResponse, GroupSyncResult } from '@/api/groups';
import type { QualificationRef } from '@/api/qualifications';

export type PathCourse = {
  course_id: string;
  title: string;
  published: boolean;
  position: number;
};

export type LearningPath = {
  id: string;
  organization_id: string;
  name: string;
  description: string;
  /** Each course unlocks when the previous one is complete. */
  sequential: boolean;
  self_enrol: boolean;
  awards_qualification_id: string | null;
  awards_qualification: QualificationRef | null;
  created_at: string;
  updated_at: string;
  courses: PathCourse[];
  active_enrolments: number;
};

export type PathEnrolment = {
  id: string;
  path_id: string;
  user_id: string;
  display_name: string;
  email: string;
  source: 'manual' | 'group';
  status: 'active' | 'inactive';
  left_group_at: string | null;
  cycle: number;
  created_at: string;
  courses_done: number;
  courses_total: number;
  complete: boolean;
};

export type PathGroupLink = {
  path_id: string;
  /** Normalised key; use it to unlink. */
  group_name: string;
  /** Full path as the directory spells it. */
  group_path: string;
  path_resolved: boolean;
  created_at: string;
  last_synced_at: string | null;
  last_sync_error: string;
  members: number;
  active: number;
};

export type MyPathCourse = PathCourse & {
  progress: Progress;
  complete: boolean;
  locked: boolean;
  needs?: string[];
};

export type MyPath = {
  id: string;
  name: string;
  description: string;
  sequential: boolean;
  source: 'manual' | 'group';
  awards_qualification: QualificationRef | null;
  courses: MyPathCourse[];
  courses_done: number;
  courses_total: number;
  complete: boolean;
  certificate_id: string | null;
};

export type PathBody = {
  name?: string;
  description?: string;
  sequential?: boolean;
  self_enrol?: boolean;
  awards_qualification_id?: string | null;
};

export function listPaths(signal?: AbortSignal) {
  return apiRequest<{ paths: LearningPath[] }>('/api/v1/paths', { signal });
}

export function getPath(id: string, signal?: AbortSignal) {
  return apiRequest<LearningPath>(`/api/v1/paths/${id}`, { signal });
}

export function createPath(body: PathBody) {
  return apiRequest<LearningPath>('/api/v1/paths', { method: 'POST', body });
}

export function updatePath(id: string, body: PathBody) {
  return apiRequest<LearningPath>(`/api/v1/paths/${id}`, { method: 'PATCH', body });
}

export function deletePath(id: string) {
  return apiRequest<void>(`/api/v1/paths/${id}`, { method: 'DELETE' });
}

export function setPathCourses(id: string, courseIds: string[]) {
  return apiRequest<LearningPath>(`/api/v1/paths/${id}/courses`, { method: 'PUT', body: { course_ids: courseIds } });
}

export function listPathEnrolments(id: string, signal?: AbortSignal) {
  return apiRequest<{ enrolments: PathEnrolment[] }>(`/api/v1/paths/${id}/enrolments`, { signal });
}

export function enrolInPath(id: string, userId: string) {
  return apiRequest<PathEnrolment>(`/api/v1/paths/${id}/enrolments`, { method: 'POST', body: { user_id: userId } });
}

export function removePathEnrolment(enrolmentId: string) {
  return apiRequest<void>(`/api/v1/path-enrolments/${enrolmentId}`, { method: 'DELETE' });
}

export function selfEnrolPath(id: string) {
  return apiRequest<PathEnrolment>(`/api/v1/paths/${id}/enrol`, { method: 'POST' });
}

export function listPathGroupLinks(id: string, signal?: AbortSignal) {
  return apiRequest<GroupLinksResponse<PathGroupLink>>(`/api/v1/paths/${id}/group-links`, { signal });
}

/** groupPath is the group's full path, e.g. /learn/cohort-a. */
export function addPathGroupLink(id: string, groupPath: string) {
  return apiRequest<{ link: PathGroupLink; sync: GroupSyncResult | null; sync_error?: string }>(
    `/api/v1/paths/${id}/group-links`,
    { method: 'POST', body: { group_path: groupPath } }
  );
}

export function removePathGroupLink(id: string, groupName: string) {
  return apiRequest<{ deactivated: number }>(`/api/v1/paths/${id}/group-links`, {
    method: 'DELETE',
    query: { group: groupName },
  });
}

export function syncPathGroupLinks(id: string) {
  return apiRequest<GroupSyncResult>(`/api/v1/paths/${id}/group-links/sync`, { method: 'POST' });
}

export function listMyPaths(signal?: AbortSignal) {
  return apiRequest<{ paths: MyPath[]; available: LearningPath[] }>('/api/v1/me/paths', { signal });
}
