import { useQuery } from '@tanstack/react-query';
import { apiRequest } from '@/api/client';

export type OptionStat = { option_id: string; label: string; correct: boolean; picks: number };
export type RegionStat = { region_id: string; correct: boolean; hits: number; hit_rate: number | null };

/** One bank question over every submitted attempt (all cycles). */
export type QuestionStat = {
  question_id: string;
  category_id: string;
  kind: 'single' | 'multiple' | 'hotspot';
  attempts: number;
  correct: number;
  /** null while nobody has answered it. */
  percent_correct: number | null;
  options: OptionStat[];
  regions: RegionStat[];
  taps: number;
  miss_taps: number;
};

export function getQuestionStats(courseId: string, signal?: AbortSignal) {
  return apiRequest<{ questions: QuestionStat[] }>(`/api/v1/courses/${courseId}/question-stats`, { signal });
}

/** Stats keyed by question id. Staff only. */
export function useQuestionStats(courseId: string) {
  return useQuery({
    queryKey: ['question-stats', courseId],
    queryFn: ({ signal }) => getQuestionStats(courseId, signal),
    select: (d) => new Map(d.questions.map((q) => [q.question_id, q])),
    retry: false,
  });
}
