import { apiRequest } from '@/api/client';

export type Progress = {
  total: number;
  completed: number;
};

export type Course = {
  id: string;
  organization_id: string;
  title: string;
  summary: string;
  published: boolean;
  self_enrol: boolean;
  /** Language the course is written in; translations cover the other languages. */
  default_lang: string;
  languages: string[];
  /** Language this payload's texts are in (learner views). */
  lang?: string;
  created_by?: string;
  created_at: string;
  updated_at: string;
};

export type CourseSummary = Course & {
  enrolment_role?: 'learner' | 'trainer';
  progress: Progress;
  /** Learner's due date (courses/mine). */
  due_at?: string;
  overdue?: boolean;
  /** Learner on a sequential path: an earlier path course is unfinished. */
  locked?: boolean;
  /** Titles of the path courses to finish first. */
  needs?: string[];
};

export type QuizStanding = {
  attempts_used: number;
  attempts_left: number | null;
  best_score: number | null;
  best_max_score: number | null;
  best_percent: number | null;
  passed: boolean;
  open_attempt_id: string | null;
};

export type Activity = {
  id: string;
  section_id: string;
  kind: 'page' | 'quiz' | 'session';
  title: string;
  body: string;
  position: number;
  pass_percent: number;
  max_attempts: number | null;
  shuffle_options: boolean;
  completed: boolean;
  quiz?: QuizStanding;
  /** Activity ids that must be complete first. */
  prerequisites: string[];
  /** Learner only: prerequisites incomplete (page body withheld). */
  locked: boolean;
  /** Titles of the incomplete prerequisites. */
  needs?: string[];
};

export type Section = {
  id: string;
  course_id: string;
  title: string;
  position: number;
  activities: Activity[];
};

export type CourseDetail = Course & {
  enrolment_role?: 'learner' | 'trainer';
  sections: Section[];
  progress: Progress;
  /** Days from enrolment until a learner is due; null = no due date. */
  due_days: number | null;
  due_at?: string;
  overdue: boolean;
  /** Qualification finishing the course awards; null = none. */
  awards_qualification?: { id: string; key: string; name: string; valid_months: number | null } | null;
  /** Current renewal cycle for the caller (1 until they renew). */
  cycle?: number;
};

export type Enrolment = {
  id: string;
  course_id: string;
  user_id: string;
  role: 'learner' | 'trainer';
  display_name: string;
  email: string;
  created_at: string;
  progress: Progress;
  /** group = created by the Keycloak group sync. */
  source: 'manual' | 'group';
  /** inactive = left the synced group: no access, progress kept. */
  status: 'active' | 'inactive';
  left_group_at: string | null;
  due_at: string | null;
  overdue: boolean;
};

export type Person = {
  id: string;
  display_name: string;
  email: string;
};

export function listMyCourses(signal?: AbortSignal) {
  return apiRequest<{ courses: CourseSummary[] }>('/api/v1/courses/mine', { signal });
}

export function listCatalog(signal?: AbortSignal) {
  return apiRequest<{ courses: CourseSummary[] }>('/api/v1/courses/catalog', { signal });
}

export function listTeachCourses(signal?: AbortSignal) {
  return apiRequest<{ courses: CourseSummary[] }>('/api/v1/courses/teach', { signal });
}

export function createCourse(body: { title: string; summary?: string; self_enrol?: boolean }) {
  return apiRequest<Course>('/api/v1/courses', { method: 'POST', body });
}

/** Copy for a new edition: content only, no learner data; starts unpublished. */
export function copyCourse(id: string, body: { title: string; include_group_links: boolean }) {
  return apiRequest<Course>(`/api/v1/courses/${id}/copy`, { method: 'POST', body });
}

/** lang asks for that language's texts (staff otherwise get the default text, for editing). */
export function getCourse(id: string, signal?: AbortSignal, lang?: string) {
  return apiRequest<CourseDetail>(`/api/v1/courses/${id}`, { signal, query: { lang } });
}

export function updateCourse(
  id: string,
  body: {
    title?: string;
    summary?: string;
    self_enrol?: boolean;
    due_days?: number | null;
    languages?: string[];
    default_lang?: string;
  }
) {
  return apiRequest<Course>(`/api/v1/courses/${id}`, { method: 'PATCH', body });
}

export function setCoursePublished(id: string, published: boolean) {
  return apiRequest<Course>(`/api/v1/courses/${id}/${published ? 'publish' : 'unpublish'}`, {
    method: 'POST',
  });
}

export function createSection(courseId: string, title: string) {
  return apiRequest<Section>(`/api/v1/courses/${courseId}/sections`, {
    method: 'POST',
    body: { title },
  });
}

export function updateSection(id: string, body: { title?: string; position?: number }) {
  return apiRequest<Section>(`/api/v1/sections/${id}`, { method: 'PATCH', body });
}

export function deleteSection(id: string) {
  return apiRequest<void>(`/api/v1/sections/${id}`, { method: 'DELETE' });
}

export type QuizSettings = {
  pass_percent?: number;
  max_attempts?: number | null;
  shuffle_options?: boolean;
};

export function createActivity(
  sectionId: string,
  body: { kind?: Activity['kind']; title: string; body: string } & QuizSettings
) {
  return apiRequest<Activity>(`/api/v1/sections/${sectionId}/activities`, {
    method: 'POST',
    body,
  });
}

export function updateActivity(
  id: string,
  body: { title?: string; body?: string; position?: number } & QuizSettings
) {
  return apiRequest<Activity>(`/api/v1/activities/${id}`, { method: 'PATCH', body });
}

export function deleteActivity(id: string) {
  return apiRequest<void>(`/api/v1/activities/${id}`, { method: 'DELETE' });
}

export function setCompletion(activityId: string, completed: boolean) {
  return apiRequest<{ activity_id: string; completed: boolean }>(
    `/api/v1/activities/${activityId}/completion`,
    { method: 'PUT', body: { completed } }
  );
}

export function listEnrolments(courseId: string, signal?: AbortSignal) {
  return apiRequest<{ enrolments: Enrolment[] }>(`/api/v1/courses/${courseId}/enrolments`, { signal });
}

export function enrolPerson(courseId: string, body: { user_id: string; role?: 'learner' | 'trainer' }) {
  return apiRequest<Enrolment>(`/api/v1/courses/${courseId}/enrolments`, { method: 'POST', body });
}

export function selfEnrol(courseId: string) {
  return apiRequest<Enrolment>(`/api/v1/courses/${courseId}/enrol`, { method: 'POST' });
}

export function unenrol(enrolmentId: string) {
  return apiRequest<void>(`/api/v1/enrolments/${enrolmentId}`, { method: 'DELETE' });
}

export function searchPeople(q: string, signal?: AbortSignal) {
  return apiRequest<{ people: Person[] }>('/api/v1/users', { query: { q }, signal });
}
