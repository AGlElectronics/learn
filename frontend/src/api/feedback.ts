import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiRequest } from '@/api/client';

export type FeedbackSettings = { enabled: boolean; anonymous: boolean };

/** Something a learner can rate: the course (activity_id null) or a session. */
export type FeedbackTarget = {
  activity_id: string | null;
  title: string;
  eligible: boolean;
  submitted: boolean;
};

export type MyFeedback = FeedbackSettings & { course: FeedbackTarget; sessions: FeedbackTarget[] };

export type FeedbackComment = { rating: number; comment: string; display_name?: string; created_at: string };

export type FeedbackGroup = {
  activity_id: string | null;
  title: string;
  count: number;
  average: number | null;
  comments: FeedbackComment[];
};

export type FeedbackSummary = FeedbackSettings & { course: FeedbackGroup; sessions: FeedbackGroup[] };

export function getMyFeedback(courseId: string, signal?: AbortSignal) {
  return apiRequest<MyFeedback>(`/api/v1/courses/${courseId}/feedback/mine`, { signal });
}

export function submitFeedback(
  courseId: string,
  body: { activity_id?: string | null; rating: number; comment: string }
) {
  return apiRequest<{ ok: boolean }>(`/api/v1/courses/${courseId}/feedback`, { method: 'POST', body });
}

export function getFeedbackSummary(courseId: string, signal?: AbortSignal) {
  return apiRequest<FeedbackSummary>(`/api/v1/courses/${courseId}/feedback`, { signal });
}

export function setFeedbackSettings(courseId: string, body: Partial<FeedbackSettings>) {
  return apiRequest<FeedbackSettings>(`/api/v1/courses/${courseId}/feedback-settings`, { method: 'PUT', body });
}

export const feedbackKeys = {
  mine: (courseId: string) => ['feedback', 'mine', courseId] as const,
  summary: (courseId: string) => ['feedback', 'summary', courseId] as const,
};

export function useMyFeedback(courseId: string) {
  return useQuery({
    queryKey: feedbackKeys.mine(courseId),
    queryFn: ({ signal }) => getMyFeedback(courseId, signal),
    retry: false,
  });
}

export function useFeedbackSummary(courseId: string) {
  return useQuery({
    queryKey: feedbackKeys.summary(courseId),
    queryFn: ({ signal }) => getFeedbackSummary(courseId, signal),
    retry: false,
  });
}

export function useFeedbackMutation<TArgs, TResult>(fn: (args: TArgs) => Promise<TResult>) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: fn,
    onSuccess: () => void qc.invalidateQueries({ queryKey: ['feedback'] }),
  });
}
