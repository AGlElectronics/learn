import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useNavigate } from '@tanstack/react-router';
import { Archive, BookPlus, Copy, Pencil, Route } from 'lucide-react';
import { Button, LinkButton } from '@/components/ui/Button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/Card';
import { Input, Label } from '@/components/ui/Input';
import { PageContainer, PageHeader } from '@/components/ui/PageHeader';
import { EmptyState, ErrorRow, LoadingRow } from '@/components/ui/States';
import { createCourse, listTeachCourses, type CourseSummary } from '@/api/courses';
import { CopyCourseDialog } from '@/components/CopyCourseDialog';
import { useMe, PERMS } from '@/hooks/useMe';
import { useT } from '@/i18n';

/**
 * The trainer home: every course the caller staffs (admins see all), plus a
 * small create form. The backend decides who may create; this page only
 * renders for holders of course.teach / course.manage (see router).
 */
export function TeachPage() {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const t = useT();
  const [showArchived, setShowArchived] = useState(false);
  const coursesQ = useQuery({
    queryKey: ['courses', 'teach', showArchived],
    queryFn: ({ signal }) => listTeachCourses(signal, showArchived),
    retry: false,
  });

  const { can } = useMe();
  const [copying, setCopying] = useState<CourseSummary | null>(null);
  const [title, setTitle] = useState('');
  const [summary, setSummary] = useState('');
  const create = useMutation({
    mutationFn: () => createCourse({ title, summary }),
    onSuccess: (course) => {
      setTitle('');
      setSummary('');
      void qc.invalidateQueries({ queryKey: ['courses'] });
      void navigate({ to: '/teach/$courseId', params: { courseId: course.id } });
    },
  });

  return (
    <PageContainer>
      <PageHeader
        title={t('nav.teach')}
        description={t('teach.homeDescription')}
        actions={
          <>
            <Button variant="ghost" size="sm" aria-pressed={showArchived} onClick={() => setShowArchived(!showArchived)}>
              <Archive className="w-4 h-4" />
              {showArchived ? t('teach.showLive') : t('teach.showArchived')}
            </Button>
            {can(PERMS.courseManage) ? (
              <LinkButton to="/teach/paths" variant="secondary" size="sm">
                <Route className="w-4 h-4" />
                {t('paths.title')}
              </LinkButton>
            ) : null}
          </>
        }
      />
      {copying ? <CopyCourseDialog course={copying} onClose={() => setCopying(null)} /> : null}

      <div className="grid gap-6 lg:grid-cols-[1fr_320px]">
        <div>
          {coursesQ.isLoading ? (
            <LoadingRow />
          ) : coursesQ.isError ? (
            <ErrorRow message={t('courses.loadError')} />
          ) : coursesQ.data && coursesQ.data.courses.length > 0 ? (
            <div className="grid gap-4 sm:grid-cols-2">
              {coursesQ.data.courses.map((c) => (
                <Card key={c.id}>
                  <CardHeader>
                    <div className="flex items-center justify-between gap-2">
                      <CardTitle>{c.title}</CardTitle>
                      <span
                        className={
                          c.published
                            ? 'text-[11px] font-semibold rounded-pill bg-primary-soft text-primary px-2 py-0.5'
                            : 'text-[11px] font-semibold rounded-pill bg-secondary text-muted-foreground px-2 py-0.5'
                        }
                      >
                        {c.archived_at ? t('teach.archived') : c.published ? t('teach.published') : t('teach.draft')}
                      </span>
                    </div>
                    {c.summary ? <CardDescription>{c.summary}</CardDescription> : null}
                  </CardHeader>
                  <CardContent className="flex flex-wrap gap-2">
                    <LinkButton to={`/teach/${c.id}`} variant="secondary" size="sm">
                      <Pencil className="w-4 h-4" />
                      {t('common.edit')}
                    </LinkButton>
                    <Button variant="ghost" size="sm" onClick={() => setCopying(c)}>
                      <Copy className="w-4 h-4" />
                      {t('teach.copyCourse')}
                    </Button>
                  </CardContent>
                </Card>
              ))}
            </div>
          ) : (
            <EmptyState
              icon={BookPlus}
              title={t('courses.emptyTitle')}
              description={t('teach.emptyDescription')}
            />
          )}
        </div>

        <Card className="h-fit">
          <CardHeader>
            <CardTitle>{t('teach.newCourse')}</CardTitle>
            <CardDescription>{t('teach.newCourseHint')}</CardDescription>
          </CardHeader>
          <CardContent>
            <form
              className="space-y-3"
              onSubmit={(e) => {
                e.preventDefault();
                if (title.trim()) create.mutate();
              }}
            >
              <div>
                <Label htmlFor="course-title">{t('teach.title')}</Label>
                <Input
                  id="course-title"
                  value={title}
                  onChange={(e) => setTitle(e.target.value)}
                  placeholder={t('teach.titlePlaceholder')}
                />
              </div>
              <div>
                <Label htmlFor="course-summary">{t('teach.summary')}</Label>
                <Input
                  id="course-summary"
                  value={summary}
                  onChange={(e) => setSummary(e.target.value)}
                  placeholder={t('teach.summaryPlaceholder')}
                />
              </div>
              <Button type="submit" className="w-full" disabled={!title.trim() || create.isPending}>
                <BookPlus className="w-4 h-4" />
                {t('teach.createCourse')}
              </Button>
              {create.isError ? <p className="text-sm text-destructive">{(create.error as Error).message}</p> : null}
            </form>
          </CardContent>
        </Card>
      </div>
    </PageContainer>
  );
}
