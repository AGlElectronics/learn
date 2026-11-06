import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from '@tanstack/react-router';
import { Award, CheckCircle2, Circle, Lock, Route } from 'lucide-react';
import { listMyPaths, selfEnrolPath, type MyPath } from '@/api/paths';
import { Button } from '@/components/ui/Button';
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/Card';
import { useT } from '@/i18n';
import { cn } from '@/lib/utils';

function PathCard({ path }: { path: MyPath }) {
  const t = useT();
  const pct = path.courses_total > 0 ? Math.round((path.courses_done / path.courses_total) * 100) : 0;
  return (
    <Card>
      <CardHeader>
        <div className="flex items-center justify-between gap-2">
          <CardTitle className="flex items-center gap-2">
            <Route className="w-4 h-4 text-primary" />
            {path.name}
          </CardTitle>
          {path.complete ? (
            <span className="text-[11px] font-semibold rounded-pill bg-primary-soft text-primary px-2 py-0.5">
              {t('paths.complete')}
            </span>
          ) : null}
        </div>
        {path.description ? <CardDescription>{path.description}</CardDescription> : null}
      </CardHeader>
      <CardContent className="space-y-3">
        <div>
          <div className="h-2 rounded-pill bg-secondary overflow-hidden">
            <div className="h-full rounded-pill bg-primary transition-all" style={{ width: `${pct}%` }} />
          </div>
          <p className="text-xs text-muted-foreground mt-1">
            {t('paths.coursesDone', { done: path.courses_done, count: path.courses_total })}
            {path.sequential ? ` · ${t('paths.inOrder')}` : ''}
          </p>
        </div>
        <ol className="space-y-1">
          {path.courses.map((c) => (
            <li key={c.course_id} className="flex items-center gap-2 text-sm">
              {c.complete ? (
                <CheckCircle2 className="w-4 h-4 shrink-0 text-success" />
              ) : c.locked ? (
                <Lock className="w-4 h-4 shrink-0 text-muted-foreground" />
              ) : (
                <Circle className="w-4 h-4 shrink-0 text-muted-foreground" />
              )}
              {c.locked ? (
                <span className="text-muted-foreground" title={c.needs?.length ? t('locked.finishThese', { items: c.needs.join(', ') }) : undefined}>
                  {c.title}
                </span>
              ) : (
                <Link to="/courses/$courseId" params={{ courseId: c.course_id }} className="hover:underline">
                  {c.title}
                </Link>
              )}
              {!c.complete && !c.locked && c.progress.total > 0 ? (
                <span className="ml-auto text-xs text-muted-foreground">
                  {c.progress.completed}/{c.progress.total}
                </span>
              ) : null}
            </li>
          ))}
        </ol>
        {path.awards_qualification ? (
          <p className="flex items-center gap-1.5 text-xs text-muted-foreground">
            <Award className="w-3.5 h-3.5" />
            {path.certificate_id ? (
              <Link to="/certificates/$certificateId" params={{ certificateId: path.certificate_id }} className="hover:underline">
                {t('paths.awarded', { name: path.awards_qualification.name })}
              </Link>
            ) : (
              <>{t('paths.finishingAwards', { name: path.awards_qualification.name })}</>
            )}
          </p>
        ) : null}
      </CardContent>
    </Card>
  );
}

/** Learner home: learning paths with per-course progress, plus open paths to join. */
export function MyPaths() {
  const qc = useQueryClient();
  const t = useT();
  const q = useQuery({
    queryKey: ['paths', 'mine'],
    queryFn: ({ signal }) => listMyPaths(signal),
  });
  const join = useMutation({
    mutationFn: selfEnrolPath,
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['paths', 'mine'] });
      void qc.invalidateQueries({ queryKey: ['courses'] });
    },
  });
  const paths = q.data?.paths ?? [];
  const available = q.data?.available ?? [];
  if (paths.length === 0 && available.length === 0) return null;

  return (
    <div className="mt-10">
      <h2 className="font-heading text-lg font-bold mb-3">{t('paths.title')}</h2>
      {paths.length > 0 ? (
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
          {paths.map((p) => (
            <PathCard key={p.id} path={p} />
          ))}
        </div>
      ) : null}
      {available.length > 0 ? (
        <div className={cn('grid gap-4 sm:grid-cols-2 lg:grid-cols-3', paths.length > 0 && 'mt-4')}>
          {available.map((p) => (
            <Card key={p.id}>
              <CardHeader>
                <CardTitle className="flex items-center gap-2">
                  <Route className="w-4 h-4 text-muted-foreground" />
                  {p.name}
                </CardTitle>
                {p.description ? <CardDescription>{p.description}</CardDescription> : null}
              </CardHeader>
              <CardContent className="space-y-3">
                <p className="text-xs text-muted-foreground">
                  {t('paths.courseCount', { count: p.courses.length })}
                  {p.awards_qualification ? ` · ${t('paths.awards', { name: p.awards_qualification.name })}` : ''}
                </p>
                <Button size="sm" disabled={join.isPending} onClick={() => join.mutate(p.id)}>
                  <Route className="w-4 h-4" />
                  {t('paths.join')}
                </Button>
              </CardContent>
            </Card>
          ))}
        </div>
      ) : null}
      {join.isError ? <p className="mt-2 text-sm text-destructive">{(join.error as Error).message}</p> : null}
    </div>
  );
}
