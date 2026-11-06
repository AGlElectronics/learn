import { useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { CheckCircle2, ClipboardCheck, Play, RotateCcw, Send, XCircle } from 'lucide-react';
import { Button } from '@/components/ui/Button';
import { ErrorRow, LoadingRow } from '@/components/ui/States';
import { HotspotAnswer } from '@/components/Hotspot';
import { MediaView } from '@/components/media/MediaView';
import {
  getAttempt,
  quizKeys,
  startAttempt,
  submitAttempt,
  useQuizInfo,
  type Attempt,
  type Point,
} from '@/api/quizzes';
import { useT } from '@/i18n';
import { cn } from '@/lib/utils';

/**
 * The learner's side of a quiz: overview (best score, attempts left),
 * taking an attempt (radio buttons for one answer, checkboxes for several),
 * and the result with right/wrong, correct answers and feedback. Questions
 * and option order are frozen server-side, so resuming shows the same quiz.
 */
export function QuizPlayer({ courseId, activityId }: { courseId: string; activityId: string }) {
  const qc = useQueryClient();
  const t = useT();
  const infoQ = useQuizInfo(activityId);
  const [attempt, setAttempt] = useState<Attempt | null>(null);
  const [answers, setAnswers] = useState<Record<string, string[]>>({});
  const [taps, setTaps] = useState<Record<string, Point[]>>({});

  const refresh = () => {
    void qc.invalidateQueries({ queryKey: quizKeys.info(activityId) });
    void qc.invalidateQueries({ queryKey: ['course', courseId] });
    void qc.invalidateQueries({ queryKey: ['courses'] });
  };
  const open = (a: Attempt) => {
    setAttempt(a);
    setAnswers(Object.fromEntries(a.questions.map((q) => [q.question_id, q.chosen_option_ids])));
    setTaps(Object.fromEntries(a.questions.map((q) => [q.question_id, q.taps ?? []])));
  };

  const start = useMutation({
    mutationFn: () => startAttempt(activityId),
    onSuccess: (a) => {
      open(a);
      refresh();
    },
  });
  const review = useMutation({ mutationFn: (id: string) => getAttempt(id), onSuccess: open });
  const submit = useMutation({
    mutationFn: () => submitAttempt(attempt!.id, answers, taps),
    onSuccess: (a) => {
      open(a);
      refresh();
    },
  });
  const error = (start.error ?? review.error ?? submit.error) as Error | null;

  if (infoQ.isLoading) return <LoadingRow message={t('quiz.loading')} />;
  if (infoQ.isError || !infoQ.data) return <ErrorRow message={t('quiz.loadError')} />;
  const info = infoQ.data;
  const st = info.standing;
  const canRetry = st.attempts_left === null || st.attempts_left > 0;

  if (attempt && !attempt.submitted_at) {
    const unanswered = attempt.questions.filter((q) =>
      q.kind === 'hotspot' ? (taps[q.question_id] ?? []).length === 0 : (answers[q.question_id] ?? []).length === 0
    ).length;
    return (
      <div className="space-y-4">
        {attempt.questions.map((q, i) => (
          <fieldset key={q.question_id} className="rounded-card border border-border bg-card p-4">
            <legend className="sr-only">{t('quiz.questionN', { n: i + 1 })}</legend>
            <p className="text-sm font-medium whitespace-pre-wrap mb-1">
              <span className="text-muted-foreground mr-1">{i + 1}.</span>
              {q.prompt}
            </p>
            <p className="text-xs text-muted-foreground mb-3">
              {q.kind === 'hotspot' ? t('quiz.tapPicture') : q.kind === 'multiple' ? t('quiz.selectAll') : t('quiz.selectOne')} ·{' '}
              {t('quiz.points', { count: q.points })}
            </p>
            {q.kind === 'hotspot' && q.media ? (
              <HotspotAnswer
                media={q.media}
                maxTaps={q.max_taps ?? 1}
                taps={taps[q.question_id] ?? []}
                onChange={(pts) => setTaps((prev) => ({ ...prev, [q.question_id]: pts }))}
              />
            ) : q.media ? (
              <MediaView media={q.media} className="mb-3 max-h-80" />
            ) : null}
            <div
              className={
                q.options.some((o) => o.media) ? 'grid grid-cols-2 sm:grid-cols-3 gap-2' : 'space-y-1.5'
              }
            >
              {q.options.map((o) => {
                const chosen = (answers[q.question_id] ?? []).includes(o.id);
                return (
                  <label
                    key={o.id}
                    className={cn(
                      'flex items-center gap-3 rounded-md border px-3 py-2 text-sm cursor-pointer transition-colors',
                      o.media && 'flex-col items-stretch gap-1.5 p-2',
                      chosen ? 'border-primary bg-primary-soft' : 'border-border hover:bg-secondary'
                    )}
                  >
                    {o.media ? <MediaView media={o.media} className="w-full max-h-40 object-contain" /> : null}
                    <span className="flex items-center gap-3">
                      <input
                        type={q.kind === 'multiple' ? 'checkbox' : 'radio'}
                        name={q.question_id}
                        checked={chosen}
                        onChange={(e) =>
                          setAnswers((prev) => {
                            const cur = prev[q.question_id] ?? [];
                            const next =
                              q.kind === 'multiple'
                                ? e.target.checked
                                  ? [...cur, o.id]
                                  : cur.filter((id) => id !== o.id)
                                : [o.id];
                            return { ...prev, [q.question_id]: next };
                          })
                        }
                      />
                      {o.label}
                    </span>
                  </label>
                );
              })}
            </div>
          </fieldset>
        ))}
        <div className="flex flex-wrap items-center gap-2">
          <Button
            disabled={submit.isPending}
            onClick={() => {
              if (unanswered > 0 && !confirm(t('quiz.confirmUnanswered', { count: unanswered }))) return;
              submit.mutate();
            }}
          >
            <Send className="w-4 h-4" />
            {t('quiz.submit')}
          </Button>
          <Button variant="ghost" onClick={() => setAttempt(null)}>
            {t('quiz.saveLeave')}
          </Button>
        </div>
        {error ? <ErrorRow message={error.message} /> : null}
      </div>
    );
  }

  if (attempt && attempt.submitted_at) {
    return (
      <div className="space-y-4">
        <div
          className={cn(
            'rounded-card border p-4 flex items-center gap-3',
            attempt.passed ? 'border-success/40 bg-success/10' : 'border-destructive/40 bg-destructive/10'
          )}
        >
          {attempt.passed ? (
            <CheckCircle2 className="w-6 h-6 text-success shrink-0" />
          ) : (
            <XCircle className="w-6 h-6 text-destructive shrink-0" />
          )}
          <div>
            <p className="font-heading font-bold">
              {attempt.passed ? t('quiz.passed') : t('quiz.notPassed')} · {attempt.percent ?? 0}%
            </p>
            <p className="text-sm text-muted-foreground">
              {t('quiz.scoreLine', { score: attempt.score ?? 0, max: attempt.max_score ?? 0, pass: attempt.pass_percent })}
            </p>
          </div>
        </div>
        {attempt.questions.map((q, i) => {
          const correctIds = new Set(q.correct_option_ids ?? []);
          return (
            <div key={q.question_id} className="rounded-card border border-border bg-card p-4">
              <div className="flex items-start gap-2 mb-2">
                {q.correct ? (
                  <CheckCircle2 className="w-4 h-4 text-success shrink-0 mt-0.5" />
                ) : (
                  <XCircle className="w-4 h-4 text-destructive shrink-0 mt-0.5" />
                )}
                <p className="text-sm font-medium whitespace-pre-wrap flex-1">
                  {i + 1}. {q.prompt}
                </p>
                <span className="text-xs text-muted-foreground shrink-0">
                  {q.points_awarded ?? 0}/{q.points}
                </span>
              </div>
              {q.kind === 'hotspot' && q.media ? (
                <>
                  <HotspotAnswer media={q.media} maxTaps={q.max_taps ?? 1} taps={q.taps ?? []} regions={q.regions ?? []} />
                  <p className="text-xs text-muted-foreground mt-1">
                    {t('quiz.hotspotLegend')}
                  </p>
                </>
              ) : q.media ? (
                <MediaView media={q.media} className="mb-2 max-h-64" />
              ) : null}
              <ul className="space-y-1">
                {q.options.map((o) => {
                  const chosen = q.chosen_option_ids.includes(o.id);
                  const right = correctIds.has(o.id);
                  return (
                    <li
                      key={o.id}
                      className={cn(
                        'flex items-center gap-2 rounded-md px-3 py-1.5 text-sm border',
                        right ? 'border-success/50 bg-success/10' : chosen ? 'border-destructive/50 bg-destructive/10' : 'border-transparent'
                      )}
                    >
                      {o.media ? <MediaView media={o.media} className="h-14 w-20 object-contain" /> : null}
                      <span className="flex-1">{o.label}</span>
                      {chosen ? <span className="text-xs text-muted-foreground">{t('quiz.yourAnswer')}</span> : null}
                      {right ? <span className="text-xs font-semibold text-success">{t('quiz.correct')}</span> : null}
                    </li>
                  );
                })}
              </ul>
              {q.feedback ? <p className="text-sm text-muted-foreground mt-2 whitespace-pre-wrap">{q.feedback}</p> : null}
              {q.feedback_media ? <MediaView media={q.feedback_media} className="mt-2 max-h-64" /> : null}
            </div>
          );
        })}
        <div className="flex flex-wrap gap-2">
          <Button variant="secondary" onClick={() => setAttempt(null)}>
            {t('quiz.back')}
          </Button>
          {canRetry ? (
            <Button onClick={() => start.mutate()} disabled={start.isPending}>
              <RotateCcw className="w-4 h-4" />
              {t('quiz.tryAgain')}
            </Button>
          ) : null}
        </div>
        {error ? <ErrorRow message={error.message} /> : null}
      </div>
    );
  }

  const submitted = info.attempts.filter((a) => a.submitted_at);
  return (
    <div className="space-y-4">
      <div className="rounded-card border border-border bg-card p-6">
        <h2 className="font-heading text-xl font-bold mb-2 flex items-center gap-2">
          <ClipboardCheck className="w-5 h-5 text-primary" />
          {info.title}
        </h2>
        {info.body ? <p className="text-sm leading-relaxed whitespace-pre-wrap mb-3">{info.body}</p> : null}
        <dl className="grid grid-cols-2 sm:grid-cols-4 gap-3 text-sm">
          <div>
            <dt className="text-xs text-muted-foreground">{t('quiz.questions')}</dt>
            <dd className="font-semibold">{info.question_count}</dd>
          </div>
          <div>
            <dt className="text-xs text-muted-foreground">{t('quiz.passMark')}</dt>
            <dd className="font-semibold">{info.pass_percent}%</dd>
          </div>
          <div>
            <dt className="text-xs text-muted-foreground">{t('quiz.bestScore')}</dt>
            <dd className="font-semibold">
              {st.best_percent === null ? '—' : `${st.best_percent}%`}
              {st.passed ? <span className="text-success"> · {t('quiz.passedLower')}</span> : null}
            </dd>
          </div>
          <div>
            <dt className="text-xs text-muted-foreground">{t('quiz.attempts')}</dt>
            <dd className="font-semibold">
              {st.attempts_used}
              {info.max_attempts ? ` ${t('quiz.ofMax', { max: info.max_attempts })}` : ''}
              {st.attempts_left !== null ? ` · ${t('quiz.left', { count: st.attempts_left })}` : ''}
            </dd>
          </div>
        </dl>
      </div>
      <div className="flex flex-wrap items-center gap-2">
        {st.open_attempt_id ? (
          <Button onClick={() => start.mutate()} disabled={start.isPending}>
            <Play className="w-4 h-4" />
            {t('quiz.resume')}
          </Button>
        ) : canRetry ? (
          <Button onClick={() => start.mutate()} disabled={start.isPending}>
            <Play className="w-4 h-4" />
            {st.attempts_used > 0 ? t('quiz.tryAgain') : t('quiz.start')}
          </Button>
        ) : (
          <p className="text-sm text-muted-foreground">{t('quiz.noAttemptsLeft')}</p>
        )}
      </div>
      {submitted.length > 0 ? (
        <div>
          <h3 className="text-xs font-semibold uppercase tracking-wide text-muted-foreground mb-1.5">{t('quiz.yourAttempts')}</h3>
          <ul className="space-y-1">
            {submitted.map((a, i) => (
              <li key={a.id} className="flex items-center gap-2 text-sm">
                {a.passed ? (
                  <CheckCircle2 className="w-4 h-4 text-success" />
                ) : (
                  <XCircle className="w-4 h-4 text-destructive" />
                )}
                <span className="flex-1">
                  {t('quiz.attemptN', { n: i + 1 })}: {a.percent ?? 0}% ({a.score}/{a.max_score})
                </span>
                <Button variant="ghost" size="sm" disabled={review.isPending} onClick={() => review.mutate(a.id)}>
                  {t('quiz.review')}
                </Button>
              </li>
            ))}
          </ul>
        </div>
      ) : null}
      {error ? <ErrorRow message={error.message} /> : null}
    </div>
  );
}
