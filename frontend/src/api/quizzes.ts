import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { apiRequest } from '@/api/client';
import type { QuizStanding } from '@/api/courses';
import type { MediaRef } from '@/api/media';

export type QuestionKind = 'single' | 'multiple' | 'hotspot';

/** A tap on a hotspot image, normalized 0..1 (origin top-left). */
export type Point = { x: number; y: number };

/**
 * A hotspot region in normalized 0..1 coordinates: rect {x, y, w, h};
 * circle {cx, cy, rx, ry}, an ellipse in normalized space.
 */
export type RegionShape = 'rect' | 'circle';
export type RegionInput = { shape: RegionShape; coords: Record<string, number>; correct: boolean };
export type QuestionRegion = RegionInput & { id: string; question_id: string; position: number };

export type QuestionCategory = {
  id: string;
  course_id: string;
  name: string;
  position: number;
  question_count: number;
};

export type QuestionOption = {
  id: string;
  question_id: string;
  label: string;
  correct: boolean;
  position: number;
  media_id: string | null;
  media?: MediaRef;
};

export type Question = {
  id: string;
  category_id: string;
  kind: QuestionKind;
  prompt: string;
  feedback: string;
  points: number;
  position: number;
  locked: boolean;
  options: QuestionOption[];
  media_id: string | null;
  media?: MediaRef;
  feedback_media_id: string | null;
  feedback_media?: MediaRef;
  /** Taps allowed; null = one per correct region. */
  hotspot_clicks: number | null;
  regions: QuestionRegion[];
};

export type QuizSlot = {
  id: string;
  activity_id: string;
  position: number;
  question_id: string | null;
  category_id: string | null;
  draw_count: number | null;
  question_prompt?: string;
  category_name?: string;
};

export type AttemptSummary = {
  id: string;
  activity_id: string;
  user_id: string;
  started_at: string;
  submitted_at: string | null;
  score: number | null;
  max_score: number | null;
  percent: number | null;
  passed: boolean | null;
};

export type AttemptQuestion = {
  question_id: string;
  position: number;
  kind: QuestionKind;
  prompt: string;
  points: number;
  options: { id: string; label: string; media?: MediaRef }[];
  chosen_option_ids: string[];
  media?: MediaRef;
  /** Hotspot only: how many taps may be placed. */
  max_taps?: number;
  taps?: Point[];
  // Only present once the attempt is submitted.
  correct?: boolean;
  points_awarded?: number;
  correct_option_ids?: string[];
  feedback?: string;
  feedback_media?: MediaRef;
  regions?: QuestionRegion[];
};

export type Attempt = AttemptSummary & {
  pass_percent: number;
  questions: AttemptQuestion[];
};

export type QuizInfo = {
  activity_id: string;
  title: string;
  body: string;
  pass_percent: number;
  max_attempts: number | null;
  shuffle_options: boolean;
  question_count: number;
  standing: QuizStanding;
  attempts: AttemptSummary[];
};

export type GradebookCell = {
  activity_id: string;
  attempts: number;
  best_percent: number | null;
  passed: boolean;
  in_progress: boolean;
};

export type Gradebook = {
  quizzes: { activity_id: string; title: string; pass_percent: number; max_attempts: number | null }[];
  rows: {
    user_id: string;
    display_name: string;
    email: string;
    cells: GradebookCell[];
    due_at: string | null;
    overdue: boolean;
    progress: { total: number; completed: number };
  }[];
};

// ----------------------------------------------------------------------------
// Requests
// ----------------------------------------------------------------------------

export function listCategories(courseId: string, signal?: AbortSignal) {
  return apiRequest<{ categories: QuestionCategory[] }>(`/api/v1/courses/${courseId}/question-categories`, { signal });
}

export function createCategory(courseId: string, name: string) {
  return apiRequest<QuestionCategory>(`/api/v1/courses/${courseId}/question-categories`, {
    method: 'POST',
    body: { name },
  });
}

export function updateCategory(id: string, body: { name?: string; position?: number }) {
  return apiRequest<QuestionCategory>(`/api/v1/question-categories/${id}`, { method: 'PATCH', body });
}

export function deleteCategory(id: string) {
  return apiRequest<void>(`/api/v1/question-categories/${id}`, { method: 'DELETE' });
}

export function listQuestions(categoryId: string, signal?: AbortSignal) {
  return apiRequest<{ questions: Question[] }>(`/api/v1/question-categories/${categoryId}/questions`, { signal });
}

export function createQuestion(
  categoryId: string,
  body: {
    kind: QuestionKind;
    prompt: string;
    feedback?: string;
    points?: number;
    options: { label: string; correct: boolean; media_id?: string | null }[];
    media_id?: string | null;
    feedback_media_id?: string | null;
    hotspot_clicks?: number | null;
    regions?: RegionInput[];
  }
) {
  return apiRequest<Question>(`/api/v1/question-categories/${categoryId}/questions`, { method: 'POST', body });
}

/** null clears media_id, feedback_media_id or hotspot_clicks. */
export function updateQuestion(
  id: string,
  body: {
    kind?: QuestionKind;
    prompt?: string;
    feedback?: string;
    points?: number;
    position?: number;
    media_id?: string | null;
    feedback_media_id?: string | null;
    hotspot_clicks?: number | null;
  }
) {
  return apiRequest<Question>(`/api/v1/questions/${id}`, { method: 'PATCH', body });
}

export function replaceRegions(questionId: string, regions: RegionInput[]) {
  return apiRequest<Question>(`/api/v1/questions/${questionId}/regions`, { method: 'PUT', body: { regions } });
}

export function deleteQuestion(id: string) {
  return apiRequest<void>(`/api/v1/questions/${id}`, { method: 'DELETE' });
}

export function createOption(
  questionId: string,
  body: { label: string; correct?: boolean; media_id?: string | null }
) {
  return apiRequest<Question>(`/api/v1/questions/${questionId}/options`, { method: 'POST', body });
}

export function updateOption(
  id: string,
  body: { label?: string; correct?: boolean; position?: number; media_id?: string | null }
) {
  return apiRequest<Question>(`/api/v1/options/${id}`, { method: 'PATCH', body });
}

export function deleteOption(id: string) {
  return apiRequest<Question>(`/api/v1/options/${id}`, { method: 'DELETE' });
}

export function listSlots(activityId: string, signal?: AbortSignal) {
  return apiRequest<{ slots: QuizSlot[]; problems: string[] }>(`/api/v1/activities/${activityId}/slots`, { signal });
}

export function createSlot(
  activityId: string,
  body: { question_id: string } | { category_id: string; draw_count: number }
) {
  return apiRequest<QuizSlot>(`/api/v1/activities/${activityId}/slots`, { method: 'POST', body });
}

export function updateSlot(id: string, body: { position?: number; draw_count?: number }) {
  return apiRequest<QuizSlot>(`/api/v1/slots/${id}`, { method: 'PATCH', body });
}

export function deleteSlot(id: string) {
  return apiRequest<void>(`/api/v1/slots/${id}`, { method: 'DELETE' });
}

export function getQuizInfo(activityId: string, signal?: AbortSignal) {
  return apiRequest<QuizInfo>(`/api/v1/activities/${activityId}/quiz`, { signal });
}

export function startAttempt(activityId: string) {
  return apiRequest<Attempt>(`/api/v1/activities/${activityId}/attempts`, { method: 'POST' });
}

export function getAttempt(id: string, signal?: AbortSignal) {
  return apiRequest<Attempt>(`/api/v1/attempts/${id}`, { signal });
}

export function submitAttempt(id: string, answers: Record<string, string[]>, taps: Record<string, Point[]> = {}) {
  return apiRequest<Attempt>(`/api/v1/attempts/${id}/submit`, { method: 'POST', body: { answers, taps } });
}

export function resetAttempts(activityId: string) {
  return apiRequest<{ deleted: number }>(`/api/v1/activities/${activityId}/attempts`, { method: 'DELETE' });
}

export function getGradebook(courseId: string, signal?: AbortSignal) {
  return apiRequest<Gradebook>(`/api/v1/courses/${courseId}/gradebook`, { signal });
}

// ----------------------------------------------------------------------------
// Hooks
// ----------------------------------------------------------------------------

export const quizKeys = {
  categories: (courseId: string) => ['question-categories', courseId] as const,
  questions: (categoryId: string) => ['questions', categoryId] as const,
  slots: (activityId: string) => ['quiz-slots', activityId] as const,
  info: (activityId: string) => ['quiz', activityId] as const,
  gradebook: (courseId: string) => ['gradebook', courseId] as const,
};

export function useCategories(courseId: string) {
  return useQuery({
    queryKey: quizKeys.categories(courseId),
    queryFn: ({ signal }) => listCategories(courseId, signal),
    retry: false,
  });
}

export function useQuestions(categoryId: string | null) {
  return useQuery({
    queryKey: quizKeys.questions(categoryId ?? ''),
    queryFn: ({ signal }) => listQuestions(categoryId!, signal),
    enabled: !!categoryId,
    retry: false,
  });
}

export function useSlots(activityId: string) {
  return useQuery({
    queryKey: quizKeys.slots(activityId),
    queryFn: ({ signal }) => listSlots(activityId, signal),
    retry: false,
  });
}

export function useQuizInfo(activityId: string) {
  return useQuery({
    queryKey: quizKeys.info(activityId),
    queryFn: ({ signal }) => getQuizInfo(activityId, signal),
    retry: false,
  });
}

export function useGradebook(courseId: string) {
  return useQuery({
    queryKey: quizKeys.gradebook(courseId),
    queryFn: ({ signal }) => getGradebook(courseId, signal),
    retry: false,
  });
}

/**
 * useBankMutation wraps a bank change and refreshes everything the bank
 * feeds: categories (counts), questions, and every quiz's slot list.
 */
export function useBankMutation<TArgs, TResult>(fn: (args: TArgs) => Promise<TResult>) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: fn,
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['question-categories'] });
      void qc.invalidateQueries({ queryKey: ['questions'] });
      void qc.invalidateQueries({ queryKey: ['quiz-slots'] });
    },
  });
}
