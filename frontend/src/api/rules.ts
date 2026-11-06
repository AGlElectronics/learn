import { apiRequest } from '@/api/client';
import type { Enrolment } from '@/api/courses';
import type { TFunction } from '@/i18n';
import { getLocale } from '@/i18n/lang';

/** Replace what must be complete before an activity opens (same course, no cycles). */
export function setPrerequisites(activityId: string, requires: string[]) {
  return apiRequest<{ activity_id: string; prerequisites: string[] }>(
    `/api/v1/activities/${activityId}/prerequisites`,
    { method: 'PUT', body: { requires } }
  );
}

/** Override one learner's due date; null removes it. */
export function setEnrolmentDue(enrolmentId: string, dueAt: string | null) {
  return apiRequest<Enrolment>(`/api/v1/enrolments/${enrolmentId}`, { method: 'PATCH', body: { due_at: dueAt } });
}

/** "Due 12 Nov" / "Overdue since 12 Nov" for badges. */
export function dueLabel(t: TFunction, dueAt: string | null | undefined, overdue: boolean) {
  if (!dueAt) return null;
  const when = new Date(dueAt).toLocaleDateString(getLocale(), { day: 'numeric', month: 'short', year: 'numeric' });
  return t(overdue ? 'due.overdueSince' : 'due.on', { date: when });
}
