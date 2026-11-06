import { apiRequest } from '@/api/client';
import type { MessageKey } from '@/i18n';
import { getLang, getLocale } from '@/i18n/lang';

export type Qualification = {
  id: string;
  organization_id: string;
  key: string;
  name: string;
  description: string;
  /** null = never expires */
  valid_months: number | null;
  created_at: string;
  updated_at: string;
  courses: number;
  certificates: number;
};

export type QualificationRef = {
  id: string;
  key: string;
  name: string;
  valid_months: number | null;
};

export type CertificateStatus = 'valid' | 'expired' | 'revoked';

export type Certificate = {
  id: string;
  user_id: string;
  display_name: string;
  organization_id: string;
  qualification_id: string;
  qualification_key: string;
  qualification_name: string;
  qualification_description: string;
  course_id: string | null;
  course_title: string;
  cycle: number;
  issued_at: string;
  expires_at: string | null;
  verify_code: string;
  revoked_at: string | null;
  revoked_reason: string;
  status: CertificateStatus;
  expiring_soon: boolean;
  verify_url?: string;
};

export type MyQualification = Certificate & {
  renewable_from: string | null;
  renewable: boolean;
  renewal_in_progress: boolean;
};

export type PublicVerification = {
  display_name: string;
  qualification: string;
  issued_at: string;
  expires_at: string | null;
  status: CertificateStatus;
};

export type QualificationInput = {
  key?: string;
  name?: string;
  description?: string;
  valid_months?: number | null;
};

export function listQualifications(signal?: AbortSignal) {
  return apiRequest<{ qualifications: Qualification[] }>('/api/v1/qualifications', { signal });
}

export function createQualification(input: QualificationInput) {
  return apiRequest<Qualification>('/api/v1/qualifications', { method: 'POST', body: input });
}

export function updateQualification(id: string, input: QualificationInput) {
  return apiRequest<Qualification>(`/api/v1/qualifications/${id}`, { method: 'PATCH', body: input });
}

export function deleteQualification(id: string) {
  return apiRequest<void>(`/api/v1/qualifications/${id}`, { method: 'DELETE' });
}

export function qualificationCertificates(id: string, signal?: AbortSignal) {
  return apiRequest<{ qualification: Qualification; certificates: Certificate[] }>(
    `/api/v1/qualifications/${id}/certificates`,
    { signal }
  );
}

export function setCourseQualification(courseId: string, qualificationId: string | null) {
  return apiRequest<{ course_id: string; awards_qualification: QualificationRef | null }>(
    `/api/v1/courses/${courseId}/qualification`,
    { method: 'PUT', body: { qualification_id: qualificationId } }
  );
}

export function renewCourse(courseId: string) {
  return apiRequest<{ course_id: string; cycle: number; due_at: string | null }>(
    `/api/v1/courses/${courseId}/renew`,
    { method: 'POST' }
  );
}

export function myQualifications(signal?: AbortSignal) {
  return apiRequest<{ qualifications: MyQualification[] }>('/api/v1/me/qualifications', { signal });
}

export function getCertificate(id: string, signal?: AbortSignal) {
  return apiRequest<Certificate>(`/api/v1/certificates/${id}`, { signal });
}

export function revokeCertificate(id: string, reason: string) {
  return apiRequest<Certificate>(`/api/v1/certificates/${id}/revoke`, { method: 'POST', body: { reason } });
}

/**
 * Public certificate check. Plain fetch on purpose: visitors scanning a QR
 * code are not signed in, so no auth headers and no 401 session handling.
 */
export async function verifyCertificate(code: string, signal?: AbortSignal): Promise<PublicVerification | null> {
  const base =
    (typeof process !== 'undefined' ? process.env.LEARN_API_BASE_URL : undefined) ??
    (import.meta.env.VITE_API_BASE_URL as string | undefined) ??
    '';
  const res = await fetch(`${base}/api/public/verify/${encodeURIComponent(code)}`, {
    headers: { Accept: 'application/json', 'Accept-Language': getLang() },
    signal,
  });
  if (res.status === 404) return null;
  if (res.status === 429) throw new Error('Too many checks from this network. Try again in a minute.');
  if (!res.ok) throw new Error(`Could not check this certificate (${res.status}).`);
  return (await res.json()) as PublicVerification;
}

export function formatDate(iso: string | null | undefined) {
  if (!iso) return null;
  return new Date(iso).toLocaleDateString(getLocale(), { day: 'numeric', month: 'long', year: 'numeric' });
}

export function verifyLink(c: Pick<Certificate, 'verify_code' | 'verify_url'>) {
  if (c.verify_url && c.verify_url.startsWith('http')) return c.verify_url;
  return `${window.location.origin}/verify/${c.verify_code}`;
}

/** Message ids for certificate statuses; render with t(). */
export const STATUS_LABEL: Record<CertificateStatus, MessageKey> = {
  valid: 'cert.status.valid',
  expired: 'cert.status.expired',
  revoked: 'cert.status.revoked',
};
