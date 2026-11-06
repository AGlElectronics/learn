import { apiRequest } from '@/api/client';
import { getLocale } from '@/i18n/lang';

export type SessionSlot = {
  id: string;
  activity_id: string;
  starts_at: string;
  ends_at: string;
  location: string;
  capacity: number;
  /** Booked plus attended seats. */
  booked: number;
  waitlist: number;
  seats_left: number;
};

export type SignupStatus = 'booked' | 'waitlist' | 'attended' | 'no_show';

export type SessionSignup = {
  id: string;
  slot_id: string;
  activity_id: string;
  user_id: string;
  status: SignupStatus;
  walk_in: boolean;
  created_at: string;
  waitlist_position?: number;
};

export type SessionInfo = {
  activity_id: string;
  title: string;
  body: string;
  slots: SessionSlot[];
  my_signup: SessionSignup | null;
};

export type RosterEntry = SessionSignup & { display_name: string; email: string };

export type Roster = {
  slot: SessionSlot;
  course_id: string;
  activity_title: string;
  signups: RosterEntry[];
};

export type CheckinCode = {
  code: string;
  expires_at: string;
  /** Absolute when PUBLIC_BASE_URL is set on the backend, else a path. */
  url: string;
  window_seconds: number;
};

export type CheckinResult = {
  signup: SessionSignup;
  slot: SessionSlot;
  course_id: string;
  activity_title: string;
  already_attended: boolean;
};

export type SlotInput = {
  starts_at: string;
  ends_at: string;
  location: string;
  capacity: number;
};

export function getSessionInfo(activityId: string, signal?: AbortSignal) {
  return apiRequest<SessionInfo>(`/api/v1/activities/${activityId}/session`, { signal });
}

export function createSessionSlot(activityId: string, body: SlotInput) {
  return apiRequest<SessionSlot>(`/api/v1/activities/${activityId}/session-slots`, { method: 'POST', body });
}

export function updateSessionSlot(slotId: string, body: Partial<SlotInput>) {
  return apiRequest<SessionSlot>(`/api/v1/session-slots/${slotId}`, { method: 'PATCH', body });
}

export function deleteSessionSlot(slotId: string) {
  return apiRequest<void>(`/api/v1/session-slots/${slotId}`, { method: 'DELETE' });
}

export function bookSlot(slotId: string) {
  return apiRequest<SessionSignup>(`/api/v1/slots/${slotId}/booking`, { method: 'POST' });
}

export function cancelBooking(slotId: string) {
  return apiRequest<void>(`/api/v1/slots/${slotId}/booking`, { method: 'DELETE' });
}

export function getCheckinCode(slotId: string, signal?: AbortSignal) {
  return apiRequest<CheckinCode>(`/api/v1/slots/${slotId}/checkin-code`, { signal });
}

export function checkIn(slotId: string, code: string) {
  return apiRequest<CheckinResult>(`/api/v1/slots/${slotId}/checkin`, { method: 'POST', body: { code } });
}

export function getRoster(slotId: string, signal?: AbortSignal) {
  return apiRequest<Roster>(`/api/v1/slots/${slotId}/roster`, { signal });
}

export function setSignupStatus(signupId: string, status: 'attended' | 'no_show' | 'booked') {
  return apiRequest<SessionSignup>(`/api/v1/signups/${signupId}`, { method: 'PUT', body: { status } });
}

/** "Sat 7 Nov, 10:00–11:30" in the UI language and the browser's time zone. */
export function formatSlotTime(slot: Pick<SessionSlot, 'starts_at' | 'ends_at'>) {
  const locale = getLocale();
  const start = new Date(slot.starts_at);
  const end = new Date(slot.ends_at);
  const day = (d: Date) => d.toLocaleDateString(locale, { weekday: 'short', day: 'numeric', month: 'short' });
  const time = (d: Date) => d.toLocaleTimeString(locale, { hour: '2-digit', minute: '2-digit' });
  const sameDay = start.toDateString() === end.toDateString();
  return sameDay
    ? `${day(start)}, ${time(start)}–${time(end)}`
    : `${day(start)}, ${time(start)} – ${day(end)}, ${time(end)}`;
}
