import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { BookOpen, GraduationCap, Lock } from 'lucide-react';
import { LinkButton, Button } from '@/components/ui/Button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/Card';
import { PageContainer, PageHeader } from '@/components/ui/PageHeader';
import { EmptyState, ErrorRow, LoadingRow } from '@/components/ui/States';
import { DueBadge } from '@/components/Rules';
import { MyQualifications } from '@/components/MyQualifications';
import { MyPaths } from '@/components/MyPaths';
import { MyTeamsSummary } from '@/components/TeamBoard';
import { NotificationsCard } from '@/components/NotificationsCard';
import { listCatalog, listMyCourses, selfEnrol, type CourseSummary } from '@/api/courses';
import { useMe } from '@/hooks/useMe';
import { useT } from '@/i18n';

function ProgressBar({ total, completed }: { total: number; completed: number }) {
  const t = useT();
  const pct = total > 0 ? Math.round((completed / total) * 100) : 0;
  return (
    <div>
      <div className="h-2 rounded-pill bg-secondary overflow-hidden">
        <div className="h-full rounded-pill bg-primary transition-all" style={{ width: `${pct}%` }} />
      </div>
      <p className="text-xs text-muted-foreground mt-1">
        {t('progress.pagesDone', { completed, count: total })}
        {total > 0 ? ` · ${pct}%` : ''}
      </p>
    </div>
  );
}

function CourseCard({ course }: { course: CourseSummary }) {
  const t = useT();
  return (
    <Card>
      <CardHeader>
        <div className="flex items-center justify-between gap-2">
          <CardTitle>{course.title}</CardTitle>
          {course.enrolment_role === 'trainer' ? (
            <span className="text-[11px] font-semibold rounded-pill bg-primary-soft text-primary px-2 py-0.5">
              {t('role.trainer')}
            </span>
          ) : null}
        </div>
        {course.summary ? <CardDescription>{course.summary}</CardDescription> : null}
      </CardHeader>
      <CardContent className="space-y-4">
        <ProgressBar total={course.progress.total} completed={course.progress.completed} />
        <DueBadge dueAt={course.due_at} overdue={course.overdue} />
        {course.locked ? (
          <p className="flex items-center gap-1.5 text-sm text-muted-foreground">
            <Lock className="w-4 h-4 shrink-0" />
            {course.needs?.length ? t('locked.finishThese', { items: course.needs.join(', ') }) : t('locked.short')}
          </p>
        ) : (
          <LinkButton to={`/courses/${course.id}`} variant="secondary" size="sm">
            <BookOpen className="w-4 h-4" />
            {t('courses.open')}
          </LinkButton>
        )}
      </CardContent>
    </Card>
  );
}

/**
 * The learner home: enrolled courses with progress, plus the self-enrolment
 * catalog (published courses that allow it).
 */
export function MyCoursesPage() {
  const { me } = useMe();
  const t = useT();
  const qc = useQueryClient();
  const mineQ = useQuery({
    queryKey: ['courses', 'mine'],
    queryFn: ({ signal }) => listMyCourses(signal),
  });
  const catalogQ = useQuery({
    queryKey: ['courses', 'catalog'],
    queryFn: ({ signal }) => listCatalog(signal),
  });
  const enrol = useMutation({
    mutationFn: (courseId: string) => selfEnrol(courseId),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['courses'] });
    },
  });

  const openCatalog = (catalogQ.data?.courses ?? []).filter((c) => !c.enrolment_role);

  return (
    <PageContainer>
      <PageHeader
        title={me?.display_name ? t('home.helloName', { name: me.display_name }) : t('home.hello')}
        description={t('home.description')}
      />

      {mineQ.isLoading ? (
        <LoadingRow />
      ) : mineQ.isError ? (
        <ErrorRow message={t('courses.loadError')} />
      ) : mineQ.data && mineQ.data.courses.length > 0 ? (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {mineQ.data.courses.map((c) => (
            <CourseCard key={c.id} course={c} />
          ))}
        </div>
      ) : (
        <EmptyState
          icon={GraduationCap}
          title={t('courses.emptyTitle')}
          description={t('home.emptyDescription')}
        />
      )}

      <MyPaths />

      <MyQualifications />

      <MyTeamsSummary />

      <div className="mt-10">
        <h2 className="font-heading text-lg font-bold mb-3">{t('home.catalog')}</h2>
        {catalogQ.isLoading ? (
          <LoadingRow />
        ) : catalogQ.isError ? (
          <ErrorRow message={t('home.catalogError')} />
        ) : openCatalog.length > 0 ? (
          <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
            {openCatalog.map((c) => (
              <Card key={c.id}>
                <CardHeader>
                  <CardTitle>{c.title}</CardTitle>
                  {c.summary ? <CardDescription>{c.summary}</CardDescription> : null}
                </CardHeader>
                <CardContent>
                  <Button
                    size="sm"
                    disabled={enrol.isPending}
                    onClick={() => enrol.mutate(c.id)}
                  >
                    <GraduationCap className="w-4 h-4" />
                    {t('home.enrol')}
                  </Button>
                </CardContent>
              </Card>
            ))}
          </div>
        ) : (
          <p className="text-sm text-muted-foreground">
            {t('home.catalogEmpty')}
          </p>
        )}
        {enrol.isError ? <ErrorRow message={(enrol.error as Error).message} /> : null}
      </div>

      <NotificationsCard />
    </PageContainer>
  );
}
