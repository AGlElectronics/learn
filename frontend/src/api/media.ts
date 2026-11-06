import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { ApiError, apiRequest } from '@/api/client';

export type MediaKind = 'image' | 'audio' | 'video';

/** What content embeds: enough to render a file without the library row. */
export type MediaRef = {
  id: string;
  kind: MediaKind;
  content_type: string;
  alt_text: string;
};

export type Media = MediaRef & {
  course_id: string;
  size_bytes: number;
  status: 'pending' | 'ready';
  name: string;
  created_by?: string;
  created_at: string;
  updated_at?: string;
  usage_count: number;
  unused: boolean;
  /** Pending for over a day: the browser never finished the upload. */
  upload_unfinished: boolean;
};

export type UploadTicket = {
  media: Media;
  url: string;
  fields: Record<string, string>;
  expires_at: string;
};

export type ActivityMedia = {
  id: string;
  activity_id: string;
  media_id: string | null;
  media?: MediaRef;
  external_url: string | null;
  /** youtube-nocookie / player.vimeo.com URL for external videos. */
  embed_url?: string;
  position: number;
  caption: string;
  created_at: string;
};

export const MEDIA_TYPES: Record<string, MediaKind> = {
  'image/png': 'image',
  'image/jpeg': 'image',
  'image/webp': 'image',
  'image/gif': 'image',
  'audio/mpeg': 'audio',
  'audio/mp3': 'audio',
  'audio/mp4': 'audio',
  'audio/x-m4a': 'audio',
  'audio/m4a': 'audio',
  'audio/ogg': 'audio',
  'video/mp4': 'video',
  'video/webm': 'video',
};

export const MEDIA_MAX_MB: Record<MediaKind, number> = { image: 10, audio: 20, video: 200 };

export function acceptFor(kinds: MediaKind[]) {
  return Object.entries(MEDIA_TYPES)
    .filter(([, k]) => kinds.includes(k))
    .map(([t]) => t)
    .join(',');
}

// ----------------------------------------------------------------------------
// Requests
// ----------------------------------------------------------------------------

export function listMedia(courseId: string, unusedOnly: boolean, signal?: AbortSignal) {
  return apiRequest<{ media: Media[] }>(`/api/v1/courses/${courseId}/media`, {
    signal,
    query: { filter: unusedOnly ? 'unused' : undefined },
  });
}

export function updateMedia(id: string, body: { alt_text?: string; name?: string }) {
  return apiRequest<Media>(`/api/v1/media/${id}`, { method: 'PATCH', body });
}

export function deleteMedia(id: string) {
  return apiRequest<void>(`/api/v1/media/${id}`, { method: 'DELETE' });
}

export function getMediaUrl(id: string, signal?: AbortSignal) {
  return apiRequest<{ url: string; expires_at: string }>(`/api/v1/media/${id}/url`, { signal });
}

export function listActivityMedia(activityId: string, signal?: AbortSignal) {
  return apiRequest<{ attachments: ActivityMedia[] }>(`/api/v1/activities/${activityId}/media`, { signal });
}

export function addActivityMedia(
  activityId: string,
  body: { media_id: string; caption?: string } | { external_url: string; caption?: string }
) {
  return apiRequest<ActivityMedia>(`/api/v1/activities/${activityId}/media`, { method: 'POST', body });
}

export function updateActivityMedia(id: string, body: { caption?: string; position?: number }) {
  return apiRequest<ActivityMedia>(`/api/v1/activity-media/${id}`, { method: 'PATCH', body });
}

export function deleteActivityMedia(id: string) {
  return apiRequest<void>(`/api/v1/activity-media/${id}`, { method: 'DELETE' });
}

/**
 * uploadMedia asks the API for a presigned POST, sends the file straight to
 * storage (reporting progress 0..1), then confirms it. A file whose upload
 * fails stays pending in the library ("upload not finished") for staff to
 * delete by hand.
 */
export async function uploadMedia(
  courseId: string,
  file: File,
  altText: string,
  onProgress?: (fraction: number) => void
): Promise<Media> {
  const ticket = await apiRequest<UploadTicket>(`/api/v1/courses/${courseId}/media/uploads`, {
    method: 'POST',
    body: { filename: file.name, content_type: file.type, size_bytes: file.size, alt_text: altText },
  });
  const form = new FormData();
  for (const [k, v] of Object.entries(ticket.fields)) form.append(k, v);
  form.append('file', file);
  await new Promise<void>((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open('POST', ticket.url);
    xhr.upload.onprogress = (e) => {
      if (e.lengthComputable) onProgress?.(e.loaded / e.total);
    };
    xhr.onload = () => {
      if (xhr.status >= 200 && xhr.status < 300) resolve();
      else reject(new ApiError(xhr.status, { error: { code: 'upload_failed', message: 'The upload was rejected by storage.' } }));
    };
    xhr.onerror = () =>
      reject(new ApiError(0, { error: { code: 'upload_failed', message: 'The upload failed. Check your connection and try again.' } }));
    xhr.send(form);
  });
  onProgress?.(1);
  return apiRequest<Media>(`/api/v1/media/${ticket.media.id}/complete`, { method: 'POST' });
}

// ----------------------------------------------------------------------------
// Hooks
// ----------------------------------------------------------------------------

export const mediaKeys = {
  library: (courseId: string, unusedOnly: boolean) => ['media', courseId, unusedOnly] as const,
  url: (id: string) => ['media-url', id] as const,
  attachments: (activityId: string) => ['activity-media', activityId] as const,
};

export function useMediaLibrary(courseId: string, unusedOnly = false) {
  return useQuery({
    queryKey: mediaKeys.library(courseId, unusedOnly),
    queryFn: ({ signal }) => listMedia(courseId, unusedOnly, signal),
    retry: false,
  });
}

// Viewing links last 5 minutes; refresh a little before they run out.
const URL_REFRESH_MS = 4 * 60 * 1000;

/**
 * useMediaUrl fetches a short-lived viewing link with the bearer header
 * (so no token ends up in a URL) and keeps it fresh. Call `refresh` when an
 * element reports a load error, e.g. a link that expired mid-playback.
 */
export function useMediaUrl(id: string | null | undefined) {
  const q = useQuery({
    queryKey: mediaKeys.url(id ?? ''),
    queryFn: ({ signal }) => getMediaUrl(id!, signal),
    enabled: !!id,
    staleTime: URL_REFRESH_MS,
    refetchInterval: URL_REFRESH_MS,
    refetchOnWindowFocus: false,
    retry: false,
  });
  return { url: q.data?.url ?? null, error: q.error, isLoading: q.isLoading, refresh: () => void q.refetch() };
}

export function useActivityMedia(activityId: string | null) {
  return useQuery({
    queryKey: mediaKeys.attachments(activityId ?? ''),
    queryFn: ({ signal }) => listActivityMedia(activityId!, signal),
    enabled: !!activityId,
    retry: false,
  });
}

/** useMediaMutation refreshes the library and attachment lists afterwards. */
export function useMediaMutation<TArgs, TResult>(fn: (args: TArgs) => Promise<TResult>) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: fn,
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['media'] });
      void qc.invalidateQueries({ queryKey: ['activity-media'] });
    },
  });
}
