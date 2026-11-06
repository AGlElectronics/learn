import { useEffect, useMemo, useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useParams } from '@tanstack/react-router';
import { ArrowLeft, ArrowRight, CalendarClock, Check, CheckCircle2, Circle, ClipboardCheck, Lock, Pencil } from 'lucide-react';
import { QuizPlayer } from '@/components/QuizPlayer';
import { ActivityMediaView } from '@/components/media/ActivityMedia';
import { DueBadge, LockedNotice } from '@/components/Rules';
import { SessionPlayer } from '@/components/SessionPlayer';
import { FeedbackPrompt } from '@/components/FeedbackPrompt';
import { TeamBoardCard } from '@/components/TeamBoard';
import { Button, LinkButton } from '@/components/ui/Button';
import { PageContainer, PageHeader } from '@/components/ui/PageHeader';
import { EmptyState, ErrorRow, LoadingRow } from '@/components/ui/States';
import { getCourse, setCompletion, type Activity } from '@/api/courses';
import { ApiError } from '@/api/client';
import { useMe, PERMS } from '@/hooks/useMe';
import { useI18n } from '@/i18n';
import { cn } from '@/lib/utils';

/**
 * The course player: sections and activities on the left, the open one on
 * the right. Pages get a "mark as done" toggle; quizzes complete by passing
 * an attempt. Trainers get a shortcut to the editor.
 */
export function CoursePlayerPage() {
  const { courseId } = useParams({ from: '/app/courses/$courseId' });
  const qc = useQueryClient();
  const { canAny } = useMe();
  const { lang, t } = useI18n();
  // Always ask for the UI language: staff otherwise get the untranslated text.
  const courseQ = useQuery({
    queryKey: ['course', courseId, lang],
    queryFn: ({ signal }) => getCourse(courseId, signal, lang),
    retry: false,
  });

  const pages = useMemo(
    () => (courseQ.data?.sections ?? []).flatMap((s) => s.activities),
    [courseQ.data]
  );
  const [openId, setOpenId] = useState<string | null>(null);
  useEffect(() => {
    if (!openId && pages.length > 0) {
      const firstOpen = pages.find((p) => !p.completed) ?? pages[0];
      setOpenId(firstOpen.id);
    }
  }, [openId, pages]);
  const open = pages.find((p) => p.id === openId) ?? null;
  const openIndex = open ? pages.indexOf(open) : -1;

  const complete = useMutation({
    mutationFn: ({ id, completed }: { id: string; completed: boolean }) => setCompletion(id, completed),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['course', courseId] });
      void qc.invalidateQueries({ queryKey: ['courses'] });
    },
  });

  if (courseQ.isLoading) {
    return (
      <PageContainer>
        <LoadingRow message={t('course.loading')} />
      </PageContainer>
    );
  }
  if (courseQ.isError) {
    const err = courseQ.error;
    const message =
      err instanceof ApiError && err.code === 'locked'
        ? t('player.pathLocked', { reason: err.message })
        : err instanceof ApiError && err.status === 403
        ? t('player.notEnrolled')
        : t('player.loadError');
    return (
      <PageContainer>
        <ErrorRow message={message} />
        <div className="text-center">
          <LinkButton to="/courses" variant="secondary" size="sm">
            <ArrowLeft className="w-4 h-4" />
            {t('player.backToCourses')}
          </LinkButton>
        </div>
      </PageContainer>
    );
  }
  const course = courseQ.data!;
  const isStaff = course.enrolment_role === 'trainer' || canAny(PERMS.courseManage);
  const pct =
    course.progress.total > 0
      ? Math.round((course.progress.completed / course.progress.total) * 100)
      : 0;

  return (
    <PageContainer>
      <PageHeader
        title={course.title}
        description={course.summary || undefined}
        actions={
          isStaff ? (
            <LinkButton to={`/teach/${course.id}`} variant="secondary" size="sm">
              <Pencil className="w-4 h-4" />
              {t('player.editCourse')}
            </LinkButton>
          ) : undefined
        }
      />

      <div className="mb-6">
        <div className="h-2 rounded-pill bg-secondary overflow-hidden max-w-md">
          <div className="h-full rounded-pill bg-primary transition-all" style={{ width: `${pct}%` }} />
        </div>
        <p className="text-xs text-muted-foreground mt-1">
          {t('progress.activitiesDone', { completed: course.progress.completed, count: course.progress.total })} · {pct}%
        </p>
        <DueBadge dueAt={course.due_at} overdue={course.overdue} className="mt-2" />
      </div>

      {pages.length === 0 ? (
        <EmptyState
          title={t('player.emptyTitle')}
          description={isStaff ? t('player.emptyStaff') : t('player.emptyLearner')}
        />
      ) : (
        <div className="grid gap-6 lg:grid-cols-[280px_1fr]">
          <nav className="space-y-4">
            {course.sections.map((section) => (
              <div key={section.id}>
                <h3 className="text-xs font-semibold uppercase tracking-wide text-muted-foreground mb-1.5">
                  {section.title}
                </h3>
                <ul className="space-y-0.5">
                  {section.activities.map((a: Activity) => (
                    <li key={a.id}>
                      <button
                        type="button"
                        onClick={() => setOpenId(a.id)}
                        className={cn(
                          'w-full flex items-center gap-2 text-left text-sm px-3 py-1.5 rounded-pill transition-colors',
                          a.id === openId
                            ? 'bg-accent text-accent-foreground font-medium'
                            : 'text-muted-foreground hover:bg-secondary hover:text-foreground'
                        )}
                      >
                        {a.completed ? (
                          <CheckCircle2 className="w-4 h-4 shrink-0 text-success" />
                        ) : a.locked ? (
                          <Lock className="w-4 h-4 shrink-0" />
                        ) : a.kind === 'quiz' ? (
                          <ClipboardCheck className="w-4 h-4 shrink-0" />
                        ) : a.kind === 'session' ? (
                          <CalendarClock className="w-4 h-4 shrink-0" />
                        ) : (
                          <Circle className="w-4 h-4 shrink-0" />
                        )}
                        <span className="truncate">{a.title}</span>
                      </button>
                    </li>
                  ))}
                </ul>
              </div>
            ))}
            <TeamBoardCard courseId={course.id} />
          </nav>

          {open ? (
            <article className="min-w-0">
              {open.locked ? (
                <LockedNotice needs={open.needs} />
              ) : open.kind === 'quiz' ? (
                <QuizPlayer key={open.id} courseId={course.id} activityId={open.id} />
              ) : open.kind === 'session' ? (
                <SessionPlayer key={open.id} courseId={course.id} activityId={open.id} />
              ) : (
                <div className="rounded-card border border-border bg-card p-6">
                  <h2 className="font-heading text-xl font-bold mb-3">{open.title}</h2>
                  <div className="text-sm leading-relaxed whitespace-pre-wrap">{open.body || t('player.pageEmpty')}</div>
                  <ActivityMediaView key={open.id} activityId={open.id} />
                </div>
              )}
              <div className="flex flex-wrap items-center gap-2 mt-4">
                {open.kind === 'page' && !open.locked ? (
                  <Button
                    variant={open.completed ? 'secondary' : 'primary'}
                    size="sm"
                    disabled={complete.isPending}
                    onClick={() => complete.mutate({ id: open.id, completed: !open.completed })}
                  >
                    <Check className="w-4 h-4" />
                    {open.completed ? t('player.markNotDone') : t('player.markDone')}
                  </Button>
                ) : null}
                <div className="flex-1" />
                <Button
                  variant="ghost"
                  size="sm"
                  disabled={openIndex <= 0}
                  onClick={() => setOpenId(pages[openIndex - 1]?.id ?? null)}
                >
                  <ArrowLeft className="w-4 h-4" />
                  {t('common.previous')}
                </Button>
                <Button
                  variant="ghost"
                  size="sm"
                  disabled={openIndex < 0 || openIndex >= pages.length - 1}
                  onClick={() => setOpenId(pages[openIndex + 1]?.id ?? null)}
                >
                  {t('common.next')}
                  <ArrowRight className="w-4 h-4" />
                </Button>
              </div>
              {complete.isError ? <ErrorRow message={(complete.error as Error).message} /> : null}
              {!isStaff ? (
                <FeedbackPrompt
                  courseId={course.id}
                  activityId={open.kind === 'session' ? open.id : null}
                  completed={course.progress.completed}
                />
              ) : null}
            </article>
          ) : null}
        </div>
      )}
    </PageContainer>
  );
}
